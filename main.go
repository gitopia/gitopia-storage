package main

import (
	"bytes"
	"compress/gzip"
	c "context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	lfsutil "github.com/gitopia/git-server/lfs"
	"github.com/gitopia/git-server/route"
	"github.com/gitopia/git-server/route/lfs"
	"github.com/gitopia/git-server/route/pr"
	"github.com/gitopia/git-server/utils"
	gc "github.com/gitopia/gitopia-go"
	gitopia "github.com/gitopia/gitopia/v4/app"
	gitopiatypes "github.com/gitopia/gitopia/v4/x/gitopia/types"
	offchaintypes "github.com/gitopia/gitopia/v4/x/offchain/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/cors"
	"github.com/spf13/viper"
)

const (
	branchPrefix         = "refs/heads/"
	tagPrefix            = "refs/tags/"
	AccountAddressPrefix = "gitopia"
	AccountPubKeyPrefix  = AccountAddressPrefix + sdk.PrefixPublic
)

const LARGE_OBJECT_THRESHOLD int64 = 1024 * 1024

type SaveToArweavePostBody struct {
	RepositoryID     uint64 `json:"repository_id"`
	RemoteRefName    string `json:"remote_ref_name"`
	NewRemoteRefSha  string `json:"new_remote_ref_sha"`
	PrevRemoteRefSha string `json:"prev_remote_ref_sha"`
}

var (
	env        []string
	db         *sql.DB
	cacheMutex sync.RWMutex // Mutex to synchronize cache access
)

func newWriteFlusher(w http.ResponseWriter) io.Writer {
	return writeFlusher{w.(interface {
		io.Writer
		http.Flusher
	})}
}

type writeFlusher struct {
	wf interface {
		io.Writer
		http.Flusher
	}
}

func (w writeFlusher) Write(p []byte) (int, error) {
	defer w.wf.Flush()
	return w.wf.Write(p)
}

func AuthFunc(token string, req *Request) (bool, error) {
	repoId, err := utils.ParseRepositoryIdfromURI(req.URL.Path)
	if err != nil {
		return false, err
	}

	encConf := gitopia.MakeEncodingConfig()
	offchaintypes.RegisterInterfaces(encConf.InterfaceRegistry)
	offchaintypes.RegisterLegacyAminoCodec(encConf.Amino)

	verifier := offchaintypes.NewVerifier(encConf.TxConfig.SignModeHandler())
	txDecoder := encConf.TxConfig.TxJSONDecoder()

	tx, err := txDecoder([]byte(token))
	if err != nil {
		return false, fmt.Errorf("error decoding")
	}

	// Verify push permission
	msgs := tx.GetMsgs()
	if len(msgs) != 1 || len(msgs[0].GetSigners()) != 1 {
		return false, fmt.Errorf("invalid signature")
	}

	address := msgs[0].GetSigners()[0].String()
	havePushPermission, err := utils.HavePushPermission(repoId, address)
	if err != nil {
		return false, fmt.Errorf("error checking push permission: %s", err.Error())
	}

	if !havePushPermission {
		return false, fmt.Errorf("user does not have push permission")
	}

	// Verify signature
	err = verifier.Verify(tx)
	if err != nil {
		return false, err
	}

	return true, nil
}

var reSlashDedup = regexp.MustCompile(`\/{2,}`)

func fail500(w http.ResponseWriter, context string, err error) {
	http.Error(w, "Internal server error", 500)
	logError(context, err)
}

func logError(context string, err error) {
	log.Printf("%s: %v\n", context, err)
}

func logInfo(context string, message string) {
	log.Printf("%s: %s\n", context, message)
}

func cleanUpProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	process := cmd.Process
	if process != nil && process.Pid > 0 {
		syscall.Kill(-process.Pid, syscall.SIGTERM)
	}

	go cmd.Wait()
}

func packLine(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(s)+4, s)
	return err
}

func packFlush(w io.Writer) error {
	_, err := fmt.Fprint(w, "0000")
	return err
}

func subCommand(rpc string) string {
	return strings.TrimPrefix(rpc, "git-")
}

// Parse out namespace and repository name from the path.
// Examples:
// repo -> "", "repo"
// org/repo -> "org", "repo"
// org/suborg/rpeo -> "org/suborg", "repo"
func getNamespaceAndRepo(input string) (string, string) {
	if input == "" || input == "/" {
		return "", ""
	}

	// Remove duplicate slashes
	input = reSlashDedup.ReplaceAllString(input, "/")

	// Remove leading slash
	if input[0] == '/' && input != "/" {
		input = input[1:]
	}

	blocks := strings.Split(input, "/")
	num := len(blocks)

	if num < 2 {
		return "", blocks[0]
	}

	return strings.Join(blocks[0:num-1], "/"), blocks[num-1]
}

type Config struct {
	KeyDir     string       // Directory for server ssh keys. Only used in SSH strategy.
	Dir        string       // Directory that contains repositories
	GitPath    string       // Path to git binary
	GitUser    string       // User for ssh connections
	AutoCreate bool         // Automatically create repostories
	AutoHooks  bool         // Automatically setup git hooks
	Hooks      *HookScripts // Scripts for hooks/* directory
	Auth       bool         // Require authentication
}

// HookScripts represents all repository server-size git hooks
type HookScripts struct {
	PreReceive  string
	Update      string
	PostReceive string
}

// Configure hook scripts in the repo base directory
func (c *HookScripts) setupInDir(path string) error {
	basePath := filepath.Join(path, "hooks")
	scripts := map[string]string{
		"pre-receive":  c.PreReceive,
		"update":       c.Update,
		"post-receive": c.PostReceive,
	}

	// Cleanup any existing hooks first
	hookFiles, err := ioutil.ReadDir(basePath)
	if err == nil {
		for _, file := range hookFiles {
			if err := os.Remove(filepath.Join(basePath, file.Name())); err != nil {
				return err
			}
		}
	}

	// Write new hook files
	for name, script := range scripts {
		fullPath := filepath.Join(basePath, name)

		// Dont create hook if there's no script content
		if script == "" {
			continue
		}

		if err := ioutil.WriteFile(fullPath, []byte(script), 0755); err != nil {
			logError("hook-update", err)
			return err
		}
	}

	return nil
}

func (c *Config) KeyPath() string {
	return filepath.Join(c.KeyDir, "gitkit.rsa")
}

func (c *Config) Setup() error {
	if _, err := os.Stat(c.Dir); err != nil {
		if err = os.Mkdir(c.Dir, 0755); err != nil {
			return err
		}
	}

	// NOTE: do not rewrite/create hooks for all existing repos
	// if c.AutoHooks == true {
	// 	return c.setupHooks()
	// }

	return nil
}

func (c *Config) setupHooks() error {
	files, err := ioutil.ReadDir(c.Dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if !file.IsDir() {
			continue
		}

		path := filepath.Join(c.Dir, file.Name())

		if err := c.Hooks.setupInDir(path); err != nil {
			return err
		}
	}

	return nil
}

type service struct {
	method  string
	suffix  string
	handler func(string, http.ResponseWriter, *Request)
	rpc     string
}

type lfsService struct {
	method  string
	suffix  string
	handler http.HandlerFunc
}

type Server struct {
	config      Config
	services    []service
	lfsServices []lfsService
	AuthFunc    func(string, *Request) (bool, error)
}

type Request struct {
	*http.Request
	RepoName string
	RepoPath string
}

func New(cfg Config) *Server {
	s := Server{config: cfg}
	basic := &lfs.BasicHandler{
		DefaultStorage: lfsutil.Storage(lfsutil.StorageLocal),
		Storagers: map[lfsutil.Storage]lfsutil.Storager{
			lfsutil.StorageLocal: &lfsutil.LocalStorage{Root: viper.GetString("LFS_OBJECTS_DIR")},
		},
	}
	s.services = []service{
		{"GET", "/info/refs", s.getInfoRefs, ""},
		{"POST", "/git-upload-pack", s.postRPC, "git-upload-pack"},
		{"POST", "/git-receive-pack", s.postRPC, "git-receive-pack"},
	}

	s.lfsServices = []lfsService{
		{"POST", "/objects/batch", lfs.Authenticate(basic.ServeBatchHandler)},
		{"GET", "/objects/basic", basic.ServeDownloadHandler},
		{"PUT", "/objects/basic", lfs.Authenticate(basic.ServeUploadHandler)},
		{"POST", "/objects/basic/verify", basic.ServeVerifyHandler},
	}

	// Use PATH if full path is not specified
	if s.config.GitPath == "" {
		s.config.GitPath = "git"
	}

	s.AuthFunc = AuthFunc

	return &s
}

// findService returns a matching git subservice and parsed repository name
func (s *Server) findService(req *http.Request) (*service, string) {
	for _, svc := range s.services {
		if svc.method == req.Method && strings.HasSuffix(req.URL.Path, svc.suffix) {
			path := strings.Replace(req.URL.Path, svc.suffix, "", 1)
			return &svc, path
		}
	}
	return nil, ""
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logInfo("request", r.Method+" "+r.Host+r.URL.String())

	// Find the git subservice to handle the request
	svc, repoUrlPath := s.findService(r)
	if svc == nil {
		// Find git lfs service
		for _, lfsService := range s.lfsServices {
			if lfsService.method == r.Method &&
				(strings.HasSuffix(r.URL.Path, lfsService.suffix) ||
					(len(r.URL.Path) > 65 && strings.HasSuffix(r.URL.Path[:len(r.URL.Path)-65], lfsService.suffix))) {
				lfsService.handler(w, r)
				return
			}
		}

		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Determine namespace and repo name from request path
	repoNamespace, repoName := getNamespaceAndRepo(repoUrlPath)
	if repoName == "" {
		logError("auth", fmt.Errorf("no repo name provided"))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	req := &Request{
		Request:  r,
		RepoName: path.Join(repoNamespace, repoName),
		RepoPath: path.Join(s.config.Dir, repoNamespace, repoName),
	}

	if s.config.Auth && r.Method == "POST" && strings.HasSuffix(r.RequestURI, "git-receive-pack") { // auth only for git push
		if s.AuthFunc == nil {
			logError("auth", fmt.Errorf("no auth backend provided"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header()["WWW-Authenticate"] = []string{`Basic realm=""`}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		_, token := utils.DecodeBasic(r.Header)
		if token == "" {
			logError("auth", errors.New("basic auth credentials missing"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		allow, err := s.AuthFunc(token, req)
		if !allow || err != nil {
			if err != nil {
				logError("auth", err)
			}

			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	// TODO
	// create empty bare repo only if it's an empty repo
	if !repoExists(req.RepoPath) && s.config.AutoCreate == true {
		err := initRepo(req.RepoName, &s.config)
		if err != nil {
			logError("repo-init", err)
		}
	}

	if !repoExists(req.RepoPath) {
		logError("repo-init", fmt.Errorf("%s does not exist", req.RepoPath))
		http.NotFound(w, r)
		return
	}

	svc.handler(svc.rpc, w, req)
}

func (s *Server) getInfoRefs(_ string, w http.ResponseWriter, r *Request) {
	context := "get-info-refs"
	rpc := r.URL.Query().Get("service")
	defer r.Body.Close()

	w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-advertisement", rpc))
	w.Header().Add("Cache-Control", "no-cache")
	w.WriteHeader(200)

	if !(rpc == "git-upload-pack" || rpc == "git-receive-pack") {
		http.Error(w, "Not Found", 404)
		return
	}

	// check cache
	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		fail500(w, context, err)
		return
	}

	queryClient, err := gc.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		fail500(w, context, err)
		return
	}

	res, err := queryClient.Gitopia.Repository(c.Background(), &gitopiatypes.QueryGetRepositoryRequest{
		Id: repoId,
	})
	if err != nil {
		fail500(w, context, err)
		return
	}

	var backup *gitopiatypes.RepositoryBackup
	for i := range res.Repository.Backups {
		if res.Repository.Backups[i].Store == gitopiatypes.RepositoryBackup_IPFS {
			backup = res.Repository.Backups[i]
			break
		}
	}

	// check mutex before accessing cache
	cacheMutex.Lock()

	if backup != nil {
		if !utils.IsCached(db, backup.Refs[1], backup.Refs[2]) {
			if err := utils.DownloadRepo(db, repoId, r.RepoPath); err != nil {
				fail500(w, context, err)
				return
			}
		}
	}

	// TODO
	// download single repo packfile
	if err := utils.DownloadPackfile("bafybeifb26lywpeuvjudt3q3wah3botyhjlf4uigc3apgmrvo7unfqxxjq", "pack-3450c3bc1d1f1bc9a8002ea41b56ff87ec1a099b.pack", r.RepoPath); err != nil {
		fail500(w, context, err)
		return
	}

	// TODO
	// run index-pack
	packfilePath := fmt.Sprintf("objects/pack/%s", "pack-3450c3bc1d1f1bc9a8002ea41b56ff87ec1a099b.pack")
	cmd, outPipe := gitCommand(s.config.GitPath, "index-pack", packfilePath)
	cmd.Dir = r.RepoPath
	if err := cmd.Start(); err != nil {
		fail500(w, context, err)
		return
	}
	defer cleanUpProcessGroup(cmd)

	_, err = io.Copy(io.Discard, outPipe)
	if err != nil {
		fail500(w, context, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		logError(context, err)
		return
	}

	// create git branches and tags
	// query branches and tags
	// handle pagination
	branchAllRes, err := queryClient.Gitopia.RepositoryBranchAll(c.Background(), &gitopiatypes.QueryAllRepositoryBranchRequest{
		Id:             res.Repository.Owner.Id,
		RepositoryName: res.Repository.Name,
		Pagination: &query.PageRequest{
			Limit: math.MaxUint64,
		},
	})
	if err != nil {
		fail500(w, context, err)
		return
	}
	for _, branch := range branchAllRes.Branch {
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd, outPipe := gitCommand(s.config.GitPath, "branch", branch.Name, branch.Sha)
		cmd.Dir = r.RepoPath
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
		if err := cmd.Start(); err != nil {
			fail500(w, context, err)
			return
		}
		defer cleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			fail500(w, context, err)
			return
		}

		if err := cmd.Wait(); err != nil {
			logError(context, err)
			return
		}
	}

	tagAllRes, err := queryClient.Gitopia.RepositoryTagAll(c.Background(), &gitopiatypes.QueryAllRepositoryTagRequest{
		Id:             res.Repository.Owner.Id,
		RepositoryName: res.Repository.Name,
		Pagination: &query.PageRequest{
			Limit: math.MaxUint64,
		},
	})
	if err != nil {
		fail500(w, context, err)
		return
	}
	for _, tag := range tagAllRes.Tag {
		cmd, outPipe := gitCommand(s.config.GitPath, "tag", tag.Name, tag.Sha)
		cmd.Dir = r.RepoPath
		if err := cmd.Start(); err != nil {
			fail500(w, context, err)
			return
		}
		defer cleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			fail500(w, context, err)
			return
		}

		if err := cmd.Wait(); err != nil {
			logError(context, err)
			return
		}
	}

	cmd, pipe := gitCommand(s.config.GitPath, subCommand(rpc), "--stateless-rpc", "--advertise-refs", r.RepoPath)
	if err := cmd.Start(); err != nil {
		fail500(w, context, err)
		return
	}
	defer cleanUpProcessGroup(cmd)

	if err := packLine(w, fmt.Sprintf("# service=%s\n", rpc)); err != nil {
		logError(context, err)
		return
	}

	if err := packFlush(w); err != nil {
		logError(context, err)
		return
	}

	if _, err := io.Copy(w, pipe); err != nil {
		logError(context, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		logError(context, err)
		return
	}

	// blocking
	// ideally just make sure no cleanup is scheduled during
	cacheMutex.Unlock()
}

func (s *Server) postRPC(rpc string, w http.ResponseWriter, r *Request) {
	context := "post-rpc"
	body := r.Body

	if r.Header.Get("Content-Encoding") == "gzip" {
		var err error
		body, err = gzip.NewReader(r.Body)
		if err != nil {
			fail500(w, context, err)
			return
		}
	}

	cmd, outPipe := gitCommand(s.config.GitPath, subCommand(rpc), "--stateless-rpc", r.RepoPath)
	defer outPipe.Close()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		fail500(w, context, err)
		return
	}
	defer stdin.Close()

	if err := cmd.Start(); err != nil {
		fail500(w, context, err)
		return
	}
	defer cleanUpProcessGroup(cmd)

	if _, err := io.Copy(stdin, body); err != nil {
		fail500(w, context, err)
		return
	}
	stdin.Close()

	w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-result", rpc))
	w.Header().Add("Cache-Control", "no-cache")
	w.WriteHeader(200)

	if _, err := io.Copy(newWriteFlusher(w), outPipe); err != nil {
		logError(context, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		logError(context, err)
		return
	}

	// if receive pack
	// upload to ipfs
	// non-blocking
	// make single packfile
	// some lock to prevent concurrent
	if rpc == "git-receive-pack" {
		cmd, outPipe := gitCommand(s.config.GitPath, "gc")
		cmd.Dir = r.RepoPath
		if err := cmd.Start(); err != nil {
			fail500(w, context, err)
			return
		}
		defer cleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			fail500(w, context, err)
			return
		}

		if err := cmd.Wait(); err != nil {
			logError(context, err)
			return
		}

		// Walk the directory and get the packfile name
		var packfileName string
		packfileDir := path.Join(r.RepoPath, "objects", "pack")
		err := filepath.Walk(packfileDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Check if the file has a .pack extension
			if !info.IsDir() && strings.HasSuffix(info.Name(), ".pack") {
				packfileName = path
				return nil // Found the file, no need to continue walking
			}

			return nil
		})

		if err != nil {
			fail500(w, context, err)
			return
		}

		// Check if a .pack file was found
		if packfileName == "" {
			err := errors.New("No .pack file found")
			fail500(w, context, err)
			return
		}

		file, err := os.Open(packfileName)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		// Create a buffer to store the multipart form data
		var requestBody bytes.Buffer

		// Create a multipart writer for the buffer
		writer := multipart.NewWriter(&requestBody)

		// Create a form field writer for the file
		basePackFileName := filepath.Base(packfileName)
		part, err := writer.CreateFormFile("filekey", basePackFileName)
		if err != nil {
			log.Fatal(err)
		}

		// Copy the file into the form field writer
		_, err = io.Copy(part, file)
		if err != nil {
			log.Fatal(err)
		}

		// Close the writer to finalize the multipart form data
		err = writer.Close()
		if err != nil {
			log.Fatal(err)
		}

		// Prepare the base URL
		baseURL := "https://api-v2.spheron.network/v1/upload"

		// Parse the base URL
		parsedURL, err := url.Parse(baseURL)
		if err != nil {
			log.Fatal(err)
		}

		// Add query parameters
		params := url.Values{}
		params.Add("organization", "6527ac3e2a996c00124fc2f4")
		params.Add("bucket", "git-server-test")
		params.Add("protocol", "ipfs")
		parsedURL.RawQuery = params.Encode()

		// Create a POST request with the multipart form data
		request, err := http.NewRequest("POST", parsedURL.String(), &requestBody)
		if err != nil {
			log.Fatal(err)
		}

		// Set the content type, this must be done after writer.Close()
		request.Header.Set("Content-Type", writer.FormDataContentType())

		// Set the Authorization header for Bearer token
		request.Header.Set("Authorization", "Bearer "+viper.GetString("SPHERON_API_TOKEN"))

		// Send the request
		client := &http.Client{}
		resp, err := client.Do(request)
		if err != nil {
			log.Fatal(err)
		}
		defer resp.Body.Close()

		// Read the response body
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}

		// Unmarshal the JSON data into the struct
		var apiResponse utils.ApiResponse
		if err := json.Unmarshal(body, &apiResponse); err != nil {
			panic(err)
		}

		// update cid and packname on gitopia

	}
}

func (s *Server) Setup() error {
	return s.config.Setup()
}

func initRepo(name string, config *Config) error {
	fullPath := path.Join(config.Dir, name)

	if err := exec.Command(config.GitPath, "init", "--bare", fullPath).Run(); err != nil {
		return err
	}

	if config.AutoHooks && config.Hooks != nil {
		return config.Hooks.setupInDir(fullPath)
	}

	return nil
}

func repoExists(p string) bool {
	_, err := os.Stat(path.Join(p, "objects"))
	return err == nil
}

func gitCommand(name string, args ...string) (*exec.Cmd, io.ReadCloser) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()
	// cmd.Env = append(cmd.Env, env...)

	r, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout

	return cmd, r
}

func main() {

	viper.AddConfigPath(".")
	if os.Getenv("ENV") == "PRODUCTION" {
		viper.SetConfigName("config_prod")
	} else if os.Getenv("ENV") == "DEVELOPMENT" {
		viper.SetConfigName("config_dev")
	} else {
		viper.SetConfigName("config_local")
	}
	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal(err)
	}

	logInfo("ENV", os.Getenv("ENV"))
	fmt.Println(viper.AllSettings())
	for _, key := range viper.AllKeys() {
		env = append(env, strings.ToUpper(key)+"="+viper.GetString(key))
	}

	// load cache information
	dbPath := "./cache.db"

	if !utils.DbExists(dbPath) {
		db = utils.InitializeDB(dbPath)
		log.Println("Cache database initialized")
	} else {
		var err error
		db, err = sql.Open("sqlite3", dbPath)
		if err != nil {
			log.Fatal(err)
		}
		log.Println("Cache database opened")
	}
	defer db.Close()

	conf := sdk.GetConfig()
	conf.SetBech32PrefixForAccount(AccountAddressPrefix, AccountPubKeyPrefix)
	// cannot seal the config
	// cosmos client sets address prefix for each broadcasttx API call. probably a bug
	// conf.Seal()

	// Configure git service
	service := New(Config{
		Dir:        viper.GetString("GIT_DIR"),
		AutoCreate: true,
		Auth:       true,
		AutoHooks:  true,
		Hooks: &HookScripts{
			PreReceive:  "gitopia-pre-receive",
			PostReceive: "gitopia-post-receive",
		},
	})

	// Configure git server. Will create git repos path if it does not exist.
	// If hooks are set, it will also update all repos with new version of hook scripts.
	if err = service.Setup(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	mux.Handle("/", service)
	mux.Handle("/objects/", http.HandlerFunc(route.ObjectsHandler))
	mux.Handle("/commits", http.HandlerFunc(route.CommitsHandler))
	mux.Handle("/commits/", http.HandlerFunc(route.CommitsHandler))
	mux.Handle("/content", http.HandlerFunc(route.ContentHandler))
	mux.Handle("/diff", http.HandlerFunc(route.CommitDiffHandler))
	mux.Handle("/pull/diff", http.HandlerFunc(pr.PullDiffHandler))
	mux.Handle("/upload", http.HandlerFunc(route.UploadAttachmentHandler))
	mux.Handle("/releases/", http.HandlerFunc(route.GetAttachmentHandler))
	mux.Handle("/pull/commits", http.HandlerFunc(pr.PullRequestCommitsHandler))
	mux.Handle("/pull/check", http.HandlerFunc(pr.PullRequestCheckHandler))
	mux.Handle("/raw/", http.HandlerFunc(route.GetRawFileHandler))

	handler := cors.Default().Handler(mux)

	// Start HTTP server
	if err := http.ListenAndServe(fmt.Sprintf(":%v", viper.GetString("WEB_SERVER_PORT")), handler); err != nil {
		log.Fatal(err)
	}
}
