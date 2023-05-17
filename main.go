package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	sdk "github.com/cosmos/cosmos-sdk/types"
	lfsutil "github.com/gitopia/git-server/lfs"
	"github.com/gitopia/git-server/route"
	"github.com/gitopia/git-server/route/lfs"
	"github.com/gitopia/git-server/route/pr"
	"github.com/gitopia/git-server/utils"
	gitopia "github.com/gitopia/gitopia/app"
	offchaintypes "github.com/gitopia/gitopia/x/offchain/types"
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

var env []string

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
	//defer pipe.Close()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		fail500(w, context, err)
		return
	}
	defer stdin.Close()
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		fail500(w, context, err)
		return
	}

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

	if _, err := io.Copy(log.Writer(), errPipe); err != nil {
		logError(context, err)
		return
	}

	if _, err := io.Copy(newWriteFlusher(w), outPipe); err != nil {
		logError(context, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		logError(context, err)
		return
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
	cmd.Env = append(cmd.Env, env...)

	r, _ := cmd.StdoutPipe()
	//cmd.Stderr = cmd.Stdout

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
