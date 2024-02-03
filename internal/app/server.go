package app

import (
	"bytes"
	"compress/gzip"
	c "context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gitopia/git-server/internal/db"
	"github.com/gitopia/git-server/utils"
	gc "github.com/gitopia/gitopia-go"
	gitopia "github.com/gitopia/gitopia/v4/app"
	gitopiatypes "github.com/gitopia/gitopia/v4/x/gitopia/types"
	offchaintypes "github.com/gitopia/gitopia/v4/x/offchain/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/viper"
)

const (
	branchPrefix = "refs/heads/"
	tagPrefix    = "refs/tags/"
)

const LARGE_OBJECT_THRESHOLD int64 = 1024 * 1024

type SaveToArweavePostBody struct {
	RepositoryID     uint64 `json:"repository_id"`
	RemoteRefName    string `json:"remote_ref_name"`
	NewRemoteRefSha  string `json:"new_remote_ref_sha"`
	PrevRemoteRefSha string `json:"prev_remote_ref_sha"`
}

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
	utils.LogError(context, err)
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

// findService returns a matching git subservice and parsed repository name
func (s *Server) findService(req *http.Request) (*Service, string) {
	for _, svc := range s.Services {
		if svc.Method == req.Method && strings.HasSuffix(req.URL.Path, svc.Suffix) {
			path := strings.Replace(req.URL.Path, svc.Suffix, "", 1)
			return &svc, path
		}
	}
	return nil, ""
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	utils.LogInfo("request", r.Method+" "+r.Host+r.URL.String())

	// Find the git subservice to handle the request
	svc, repoUrlPath := s.findService(r)
	if svc == nil {
		// Find git lfs service
		for _, lfsService := range s.LfsServices {
			if lfsService.Method == r.Method &&
				(strings.HasSuffix(r.URL.Path, lfsService.Suffix) ||
					(len(r.URL.Path) > 65 && strings.HasSuffix(r.URL.Path[:len(r.URL.Path)-65], lfsService.Suffix))) {
				lfsService.Handler(w, r)
				return
			}
		}

		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Determine namespace and repo name from request path
	repoNamespace, repoName := getNamespaceAndRepo(repoUrlPath)
	if repoName == "" {
		utils.LogError("auth", fmt.Errorf("no repo name provided"))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	req := &Request{
		Request:  r,
		RepoName: path.Join(repoNamespace, repoName),
		RepoPath: path.Join(s.Config.Dir, repoNamespace, repoName),
	}

	if s.Config.Auth && r.Method == "POST" && strings.HasSuffix(r.RequestURI, "git-receive-pack") { // auth only for git push
		if s.AuthFunc == nil {
			utils.LogError("auth", fmt.Errorf("no auth backend provided"))
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
			utils.LogError("auth", errors.New("basic auth credentials missing"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		allow, err := s.AuthFunc(token, req)
		if !allow || err != nil {
			if err != nil {
				utils.LogError("auth", err)
			}

			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	// TODO
	// create empty bare repo only if it's an empty repo
	if !repoExists(req.RepoPath) && s.Config.AutoCreate == true {
		err := initRepo(req.RepoName, &s.Config)
		if err != nil {
			utils.LogError("repo-init", err)
		}
	}

	if !repoExists(req.RepoPath) {
		utils.LogError("repo-init", fmt.Errorf("%s does not exist", req.RepoPath))
		http.NotFound(w, r)
		return
	}

	svc.Handler(svc.Rpc, w, req)
}

func (s *Server) GetInfoRefs(_ string, w http.ResponseWriter, r *Request) {
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

	res, err := queryClient.Gitopia.RepositoryStorage(c.Background(), &gitopiatypes.QueryGetRepositoryStorageRequest{
		RepositoryId: repoId,
	})
	if err != nil {
		fail500(w, context, err)
		return
	}

	// check mutex before accessing cache
	// cacheMutex.Lock()

	if !utils.IsCached(db.CacheDb, repoId, res.Storage.Latest.Id, res.Storage.Latest.Name) {
		if err := utils.DownloadRepo(db.CacheDb, repoId, r.RepoPath, &s.Config); err != nil {
			fail500(w, context, err)
			return
		}
	}

	cmd, pipe := utils.GitCommand(s.Config.GitPath, subCommand(rpc), "--stateless-rpc", "--advertise-refs", r.RepoPath)
	if err := cmd.Start(); err != nil {
		fail500(w, context, err)
		return
	}
	defer utils.CleanUpProcessGroup(cmd)

	if err := packLine(w, fmt.Sprintf("# service=%s\n", rpc)); err != nil {
		utils.LogError(context, err)
		return
	}

	if err := packFlush(w); err != nil {
		utils.LogError(context, err)
		return
	}

	if _, err := io.Copy(w, pipe); err != nil {
		utils.LogError(context, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		utils.LogError(context, err)
		return
	}

	// blocking
	// ideally just make sure no cleanup is scheduled during
	// cacheMutex.Unlock()
}

func initRepo(name string, config *utils.Config) error {
	fullPath := path.Join(config.Dir, name)

	if err := exec.Command(config.GitPath, "init", "--bare", fullPath).Run(); err != nil {
		return err
	}

	if config.AutoHooks && config.Hooks != nil {
		return config.Hooks.SetupInDir(fullPath)
	}

	return nil
}

func repoExists(p string) bool {
	_, err := os.Stat(path.Join(p, "objects"))
	return err == nil
}

type Service struct {
	Method  string
	Suffix  string
	Handler func(string, http.ResponseWriter, *Request)
	Rpc     string
}

type LfsService struct {
	Method  string
	Suffix  string
	Handler http.HandlerFunc
}

type Server struct {
	Config      utils.Config
	Services    []Service
	LfsServices []LfsService
	AuthFunc    func(string, *Request) (bool, error)
}

type Request struct {
	*http.Request
	RepoName string
	RepoPath string
}

func (s *Server) PostRPC(rpc string, w http.ResponseWriter, r *Request) {
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

	cmd, outPipe := utils.GitCommand(s.Config.GitPath, subCommand(rpc), "--stateless-rpc", r.RepoPath)
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
	defer utils.CleanUpProcessGroup(cmd)

	if _, err := io.Copy(stdin, body); err != nil {
		fail500(w, context, err)
		return
	}
	stdin.Close()

	w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-result", rpc))
	w.Header().Add("Cache-Control", "no-cache")
	w.WriteHeader(200)

	if _, err := io.Copy(newWriteFlusher(w), outPipe); err != nil {
		utils.LogError(context, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		utils.LogError(context, err)
		return
	}

	// if receive pack
	// upload to ipfs
	// non-blocking
	// make single packfile
	// some lock to prevent concurrent
	if rpc == "git-receive-pack" {
		cmd, outPipe := utils.GitCommand(s.Config.GitPath, "gc")
		cmd.Dir = r.RepoPath
		if err := cmd.Start(); err != nil {
			fail500(w, context, err)
			return
		}
		defer utils.CleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			fail500(w, context, err)
			return
		}

		if err := cmd.Wait(); err != nil {
			utils.LogError(context, err)
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
	return s.Config.Setup()
}
