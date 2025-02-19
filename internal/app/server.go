package app

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/gitopia/git-server/app"
	lfsutil "github.com/gitopia/git-server/lfs"
	"github.com/gitopia/git-server/pkg/merkleproof"
	"github.com/gitopia/git-server/route/lfs"
	"github.com/gitopia/git-server/utils"
	gc "github.com/gitopia/gitopia-go"
	gitopia "github.com/gitopia/gitopia/v5/app"
	offchaintypes "github.com/gitopia/gitopia/v5/x/offchain/types"
	storagetypes "github.com/gitopia/gitopia/v5/x/storage/types"
	"github.com/ipfs-cluster/ipfs-cluster/api"
	"github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	branchPrefix                 = "refs/heads/"
	tagPrefix                    = "refs/tags/"
	LARGE_OBJECT_THRESHOLD int64 = 1024 * 1024
)

var (
	CacheMutex sync.RWMutex // Mutex to synchronize cache access
)

type QueryService interface {
	GitopiaRepositoryPackfile(ctx context.Context, req *storagetypes.QueryRepositoryPackfileRequest) (*storagetypes.QueryRepositoryPackfileResponse, error)
}

type QueryServiceImpl struct {
	Query *gc.Query
}

func (qs *QueryServiceImpl) GitopiaRepositoryPackfile(ctx context.Context, req *storagetypes.QueryRepositoryPackfileRequest) (*storagetypes.QueryRepositoryPackfileResponse, error) {
	return qs.Query.Storage.RepositoryPackfile(ctx, req)
}

type SaveToArweavePostBody struct {
	RepositoryID     uint64 `json:"repository_id"`
	RemoteRefName    string `json:"remote_ref_name"`
	NewRemoteRefSha  string `json:"new_remote_ref_sha"`
	PrevRemoteRefSha string `json:"prev_remote_ref_sha"`
}

type QueryClient interface{}

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

func New(cmd *cobra.Command, cfg utils.Config) (*Server, error) {
	s := Server{Config: cfg}
	basic := &lfs.BasicHandler{
		DefaultStorage: lfsutil.Storage(lfsutil.StorageLocal),
		Storagers: map[lfsutil.Storage]lfsutil.Storager{
			lfsutil.StorageLocal: &lfsutil.LocalStorage{Root: viper.GetString("LFS_OBJECTS_DIR")},
		},
	}
	s.Services = []Service{
		{"GET", "/info/refs", s.GetInfoRefs, ""},
		{"POST", "/git-upload-pack", s.PostRPC, "git-upload-pack"},
		{"POST", "/git-receive-pack", s.PostRPC, "git-receive-pack"},
	}

	s.LfsServices = []LfsService{
		{"POST", "/objects/batch", lfs.Authenticate(basic.ServeBatchHandler)},
		{"GET", "/objects/basic", basic.ServeDownloadHandler},
		{"PUT", "/objects/basic", lfs.Authenticate(basic.ServeUploadHandler)},
		{"POST", "/objects/basic/verify", basic.ServeVerifyHandler},
	}

	// Use PATH if full path is not specified
	if s.Config.GitPath == "" {
		s.Config.GitPath = "git"
	}

	s.AuthFunc = AuthFunc

	queryClient, err := gc.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		return nil, err
	}

	s.QueryService = &QueryServiceImpl{&queryClient}

	// Initialize GitopiaProxy
	ctx := cmd.Context()
	clientCtx, err := gc.GetClientContext("git-server")
	if err != nil {
		return nil, errors.Wrap(err, "error initializing client context")
	}
	txf, err := tx.NewFactoryCLI(clientCtx, cmd.Flags())
	if err != nil {
		return nil, errors.Wrap(err, "error initializing tx factory")
	}
	txf = txf.WithGasAdjustment(app.GAS_ADJUSTMENT)

	gitopiaClient, err := gc.NewClient(ctx, clientCtx, txf)
	if err != nil {
		return nil, err
	}
	s.GitopiaProxy = app.NewGitopiaProxy(gitopiaClient)

	return &s, nil
}

func NewServerWrapper(server *Server) *ServerWrapper {
	return &ServerWrapper{
		Server: server,
	}
}

// findService returns a matching git subservice and parsed repository name
func (s *ServerWrapper) findService(req *http.Request) (*Service, string) {
	for _, svc := range s.Server.Services {
		if svc.Method == req.Method && strings.HasSuffix(req.URL.Path, svc.Suffix) {
			path := strings.Replace(req.URL.Path, svc.Suffix, "", 1)
			return &svc, path
		}
	}
	return nil, ""
}

func (s *ServerWrapper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	utils.LogInfo("request", r.Method+" "+r.Host+r.URL.String())

	// Find the git subservice to handle the request
	svc, repoUrlPath := s.findService(r)
	if svc == nil {
		// Find git lfs service
		for _, lfsService := range s.Server.LfsServices {
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
		RepoPath: path.Join(s.Server.Config.Dir, repoNamespace, repoName),
	}

	if s.Server.Config.Auth && r.Method == "POST" && strings.HasSuffix(r.RequestURI, "git-receive-pack") { // auth only for git push
		if s.Server.AuthFunc == nil {
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

		allow, err := s.Server.AuthFunc(token, req)
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
	if !repoExists(req.RepoPath) && s.Server.Config.AutoCreate == true {
		err := initRepo(req.RepoName, &s.Server.Config)
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

	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		utils.LogError(context, err)
		return
	}

	_ = repoId

	// TODO: uncomment this
	// if err := s.CacheRepository(repoId); err != nil {
	// 	utils.LogError(context, err)
	// 	return
	// }

	cmd, pipe := utils.GitCommand(s.Config.GitPath, subCommand(rpc), "--stateless-rpc", "--advertise-refs", r.RepoPath)
	if err := cmd.Start(); err != nil {
		utils.LogError(context, err)
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
	Config       utils.Config
	Services     []Service
	LfsServices  []LfsService
	AuthFunc     func(string, *Request) (bool, error)
	QueryService QueryService
	GitopiaProxy app.GitopiaProxy
}

type ServerWrapper struct {
	Server *Server
}

type Request struct {
	*http.Request
	RepoName string
	RepoPath string
}

func (s *Server) PostRPC(service string, w http.ResponseWriter, r *Request) {
	logContext := "post-rpc"
	body := r.Body

	if r.Header.Get("Content-Encoding") == "gzip" {
		var err error
		body, err = gzip.NewReader(r.Body)
		if err != nil {
			fail500(w, logContext, err)
			return
		}
	}

	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		fail500(w, logContext, fmt.Errorf("failed to parse repository id: %v", err))
		return
	}

	// Get cid from the chain
	packfileResp, err := s.QueryService.GitopiaRepositoryPackfile(context.Background(), &storagetypes.QueryRepositoryPackfileRequest{
		RepositoryId: repoId,
	})
	if err != nil {
		fail500(w, logContext, fmt.Errorf("failed to get cid from chain: %v", err))
		return
	}

	// Check if packfile exists in objects/pack directory
	cached := false
	packfilePath := filepath.Join(r.RepoPath, "objects", "pack", packfileResp.Packfile.Name)
	if _, err := os.Stat(packfilePath); err == nil {
		cached = true
	}

	if !cached {
		// Fetch packfile from IPFS and place in objects/pack directory
		ipfsUrl := fmt.Sprintf("http://127.0.0.1:5001/api/v0/cat?arg=/ipfs/%s&progress=false", packfileResp.Packfile.Cid)
		resp, err := http.Post(ipfsUrl, "application/json", nil)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to fetch packfile from IPFS: %v", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fail500(w, logContext, fmt.Errorf("failed to fetch packfile from IPFS: %v", resp.Status))
			return
		}

		// Create objects/pack directory if it doesn't exist
		packDir := filepath.Join(r.RepoPath, "objects", "pack")
		if err := os.MkdirAll(packDir, 0755); err != nil {
			fail500(w, logContext, fmt.Errorf("failed to create pack directory: %v", err))
			return
		}

		// Create packfile in objects/pack directory
		packfilePath := filepath.Join(packDir, packfileResp.Packfile.Name)
		packfile, err := os.Create(packfilePath)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to create packfile: %v", err))
			return
		}
		defer packfile.Close()

		// Copy packfile contents
		if _, err := io.Copy(packfile, resp.Body); err != nil {
			fail500(w, logContext, fmt.Errorf("failed to write packfile: %v", err))
			return
		}
	}

	cmd, outPipe := utils.GitCommand(s.Config.GitPath, subCommand(service), "--stateless-rpc", r.RepoPath)
	defer outPipe.Close()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		fail500(w, logContext, err)
		return
	}
	defer stdin.Close()

	if err := cmd.Start(); err != nil {
		fail500(w, logContext, err)
		return
	}
	defer utils.CleanUpProcessGroup(cmd)

	if _, err := io.Copy(stdin, body); err != nil {
		fail500(w, logContext, err)
		return
	}
	stdin.Close()

	w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-result", service))
	w.Header().Add("Cache-Control", "no-cache")
	w.WriteHeader(200)

	if _, err := io.Copy(newWriteFlusher(w), outPipe); err != nil {
		utils.LogError(logContext, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		utils.LogError(logContext, err)
		return
	}

	if service == "git-receive-pack" {
		cmd, outPipe := utils.GitCommand(s.Config.GitPath, "gc")
		cmd.Dir = r.RepoPath
		if err := cmd.Start(); err != nil {
			fail500(w, logContext, err)
			return
		}
		defer utils.CleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			fail500(w, logContext, err)
			return
		}

		if err := cmd.Wait(); err != nil {
			utils.LogError(logContext, err)
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
			fail500(w, logContext, err)
			return
		}

		// Check if a .pack file was found
		if packfileName == "" {
			err := errors.New("No .pack file found")
			fail500(w, logContext, err)
			return
		}

		// Chunk the packfile and generate Merkle root
		chunks, err := merkleproof.ChunkPackfile(packfileName, 256*1024) // 256KiB chunks
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to chunk packfile: %w", err))
			return
		}

		// Generate proofs and get root hash
		_, rootHash, err := merkleproof.GeneratePackfileProofs(chunks)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to generate merkle root: %w", err))
			return
		}

		// Initialize IPFS cluster client
		cfg := &client.Config{
			Host:    viper.GetString("IPFS_CLUSTER_PEER_HOST"),
			Port:    viper.GetString("IPFS_CLUSTER_PEER_PORT"),
			Timeout: time.Minute * 5, // Reasonable timeout for pinning
		}

		cl, err := client.NewDefaultClient(cfg)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to create IPFS cluster client: %w", err))
			return
		}

		// Add and pin the packfile to IPFS cluster
		paths := []string{packfileName}
		addParams := api.DefaultAddParams()
		addParams.Recursive = false
		addParams.Layout = "balanced"

		outputChan := make(chan api.AddedOutput)
		var cid api.Cid

		go func() {
			err := cl.Add(context.Background(), paths, addParams, outputChan)
			if err != nil {
				utils.LogError(logContext, fmt.Errorf("failed to add file to IPFS cluster: %w", err))
				close(outputChan)
			}
		}()

		// Get CID from output channel
		for output := range outputChan {
			cid = output.Cid
		}

		// Pin the file with default options
		pinOpts := api.PinOptions{
			ReplicationFactorMin: -1,
			ReplicationFactorMax: -1,
			Name:                 filepath.Base(packfileName),
		}

		_, err = cl.Pin(context.Background(), cid, pinOpts)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to pin file in IPFS cluster: %w", err))
			return
		}

		// After successfully pinning to IPFS
		err = s.GitopiaProxy.UpdateRepositoryPackfile(
			context.Background(),
			repoId,
			filepath.Base(packfileName),
			cid.String(),
			hex.EncodeToString(rootHash), // Pass the hex-encoded root hash
		)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to update repository packfile: %w", err))
			return
		}
	}
}

func (s *Server) Setup() error {
	return s.Config.Setup()
}
