package app

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	gc "github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-storage/app"
	lfsutil "github.com/gitopia/gitopia-storage/lfs"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	"github.com/gitopia/gitopia-storage/route/lfs"
	"github.com/gitopia/gitopia-storage/utils"
	gitopia "github.com/gitopia/gitopia/v6/app"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	offchaintypes "github.com/gitopia/gitopia/v6/x/offchain/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/ipfs/boxo/files"
	ipfspath "github.com/ipfs/boxo/path"
	"github.com/ipfs/kubo/client/rpc"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	branchPrefix                 = "refs/heads/"
	tagPrefix                    = "refs/tags/"
	LARGE_OBJECT_THRESHOLD int64 = 1024 * 1024
)

type QueryService interface {
	GitopiaRepositoryPackfile(ctx context.Context, req *storagetypes.QueryRepositoryPackfileRequest) (*storagetypes.QueryRepositoryPackfileResponse, error)
	GitopiaRepositoryBranches(ctx context.Context, req *gitopiatypes.QueryAllRepositoryBranchRequest) (*gitopiatypes.QueryAllRepositoryBranchResponse, error)
	GitopiaRepositoryTags(ctx context.Context, req *gitopiatypes.QueryAllRepositoryTagRequest) (*gitopiatypes.QueryAllRepositoryTagResponse, error)
	GitopiaRepository(ctx context.Context, req *gitopiatypes.QueryGetRepositoryRequest) (*gitopiatypes.QueryGetRepositoryResponse, error)
}

type QueryServiceImpl struct {
	Query *gc.Query
}

func (qs *QueryServiceImpl) GitopiaRepositoryPackfile(ctx context.Context, req *storagetypes.QueryRepositoryPackfileRequest) (*storagetypes.QueryRepositoryPackfileResponse, error) {
	return qs.Query.Storage.RepositoryPackfile(ctx, req)
}

func (qs *QueryServiceImpl) GitopiaRepositoryBranches(ctx context.Context, req *gitopiatypes.QueryAllRepositoryBranchRequest) (*gitopiatypes.QueryAllRepositoryBranchResponse, error) {
	return qs.Query.Gitopia.RepositoryBranchAll(ctx, req)
}

func (qs *QueryServiceImpl) GitopiaRepositoryTags(ctx context.Context, req *gitopiatypes.QueryAllRepositoryTagRequest) (*gitopiatypes.QueryAllRepositoryTagResponse, error) {
	return qs.Query.Gitopia.RepositoryTagAll(ctx, req)
}

func (qs *QueryServiceImpl) GitopiaRepository(ctx context.Context, req *gitopiatypes.QueryGetRepositoryRequest) (*gitopiatypes.QueryGetRepositoryResponse, error) {
	return qs.Query.Gitopia.Repository(ctx, req)
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
	clientCtx := client.GetClientContextFromCmd(cmd)
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

	// Initialize IPFS cluster client
	ipfsCfg := &ipfsclusterclient.Config{
		Host:    viper.GetString("IPFS_CLUSTER_PEER_HOST"),
		Port:    viper.GetString("IPFS_CLUSTER_PEER_PORT"),
		Timeout: time.Minute * 5, // Reasonable timeout for pinning
	}
	cl, err := ipfsclusterclient.NewDefaultClient(ipfsCfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create IPFS cluster client")
	}
	s.IPFSClusterClient = cl

	// Initialize cache manager
	s.CacheManager = utils.NewCacheManager()
	if err := s.CacheManager.Start(); err != nil {
		return nil, errors.Wrap(err, "failed to start cache manager")
	}

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

	utils.LockRepository(repoId)
	defer utils.UnlockRepository(repoId)

	if err := s.CacheRepository(repoId); err != nil {
		utils.LogError(context, err)
		return
	}

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
	Config            utils.Config
	Services          []Service
	LfsServices       []LfsService
	AuthFunc          func(string, *Request) (bool, error)
	QueryService      QueryService
	GitopiaProxy      app.GitopiaProxy
	IPFSClusterClient ipfsclusterclient.Client
	CacheManager      *utils.CacheManager
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

	// Acquire global lock for repository operations
	utils.LockRepository(repoId)
	defer utils.UnlockRepository(repoId)

	if err := s.CacheRepository(repoId); err != nil {
		fail500(w, logContext, fmt.Errorf("failed to cache repository: %v", err))
		return
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

		packfileName, err := utils.GetPackfileName(r.RepoPath)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to get packfile name: %w", err))
			return
		}

		cid, err := utils.PinFile(s.IPFSClusterClient, packfileName)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to pin packfile to IPFS cluster: %w", err))
			return
		}

		// Get packfile from IPFS cluster
		ipfsHttpApi, err := rpc.NewURLApiWithClient(fmt.Sprintf("http://%s:%s", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT")), &http.Client{})
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to create IPFS API: %w", err))
			return
		}

		p, err := ipfspath.NewPath("/ipfs/" + cid)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to create path: %w", err))
			return
		}

		f, err := ipfsHttpApi.Unixfs().Get(context.Background(), p)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to get packfile from IPFS: %w", err))
			return
		}

		file, ok := f.(files.File)
		if !ok {
			fail500(w, logContext, errors.New("invalid packfile format"))
			return
		}

		rootHash, err := merkleproof.ComputePackfileMerkleRoot(file, 256*1024)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to compute packfile merkle root: %w", err))
			return
		}

		// Get packfile size
		packfileInfo, err := os.Stat(packfileName)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to get packfile size: %w", err))
			return
		}

		// fetch older packfile details from gitopia
		packfileResp, err := s.QueryService.GitopiaRepositoryPackfile(context.Background(), &storagetypes.QueryRepositoryPackfileRequest{
			RepositoryId: repoId,
		})
		if err != nil {
			if !strings.Contains(err.Error(), "packfile not found") {
				fail500(w, logContext, fmt.Errorf("failed to get packfile details: %w", err))
				return
			}
		}

		if packfileResp != nil && packfileResp.Packfile.Cid != "" {
			err = utils.UnpinFile(s.IPFSClusterClient, packfileResp.Packfile.Cid)
			if err != nil {
				fail500(w, logContext, fmt.Errorf("failed to unpin packfile from IPFS cluster: %w", err))
				return
			}
		}

		// After successfully pinning to IPFS
		err = s.GitopiaProxy.UpdateRepositoryPackfile(
			context.Background(),
			repoId,
			filepath.Base(packfileName),
			cid,
			rootHash,
			packfileInfo.Size(),
		)
		if err != nil {
			fail500(w, logContext, fmt.Errorf("failed to update repository packfile: %w", err))
			return
		}

		log.WithFields(log.Fields{
			"operation":     "git push",
			"repository_id": repoId,
			"packfile_name": filepath.Base(packfileName),
			"cid":           cid,
			"root_hash":     rootHash,
		}).Info("successfully updated packfile")
	}
}

func (s *Server) Setup() error {
	return s.Config.Setup()
}
