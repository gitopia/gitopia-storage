package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-storage/app"
	lfsutil "github.com/gitopia/gitopia-storage/lfs"
	"github.com/gitopia/gitopia-storage/route/lfs"
	"github.com/gitopia/gitopia-storage/utils"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// New creates a new Server instance with the given configuration.
func New(cmd *cobra.Command, cfg utils.Config, batchTxManager *app.BatchTxManager) (*Server, error) {
	ctx, cancel := context.WithCancel(cmd.Context())

	s := &Server{
		Config: cfg,
		Ctx:    ctx,
		Cancel: cancel,
	}

	if s.Config.GitPath == "" {
		s.Config.GitPath = "git"
	}

	s.AuthFunc = AuthFunc

	queryClient, err := gitopia.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		return nil, errors.Wrap(err, "failed to get query client")
	}
	s.QueryService = &QueryServiceImpl{Query: &queryClient}

	clientCtx := client.GetClientContextFromCmd(cmd)
	txf, err := tx.NewFactoryCLI(clientCtx, cmd.Flags())
	if err != nil {
		return nil, errors.Wrap(err, "error initializing tx factory")
	}
	txf = txf.WithGasAdjustment(app.GAS_ADJUSTMENT)

	gitopiaClient, err := gitopia.NewClient(s.Ctx, clientCtx, txf)
	if err != nil {
		s.Cancel()
		return nil, errors.Wrap(err, "failed to create gitopia client")
	}
	s.GitopiaProxy = app.NewGitopiaProxy(gitopiaClient, batchTxManager)

	ipfsCfg := &ipfsclusterclient.Config{
		Host:    viper.GetString("IPFS_CLUSTER_PEER_HOST"),
		Port:    viper.GetString("IPFS_CLUSTER_PEER_PORT"),
		Timeout: time.Minute * 5,
	}
	cl, err := ipfsclusterclient.NewDefaultClient(ipfsCfg)
	if err != nil {
		s.Shutdown()
		return nil, errors.Wrap(err, "failed to create IPFS cluster client")
	}
	s.IPFSClusterClient = cl

	basic := &lfs.BasicHandler{
		DefaultStorage: lfsutil.Storage(lfsutil.StorageLocal),
		Storagers: map[lfsutil.Storage]lfsutil.Storager{
			lfsutil.StorageLocal: &lfsutil.LocalStorage{Root: viper.GetString("LFS_OBJECTS_DIR")},
		},
		GitopiaProxy:      s.GitopiaProxy,
		IPFSClusterClient: s.IPFSClusterClient,
	}

	s.Services = []Service{
		{Method: "GET", Suffix: "/info/refs", Handler: s.GetInfoRefs, Rpc: ""},
		{Method: "POST", Suffix: "/git-upload-pack", Handler: s.PostRPC, Rpc: "git-upload-pack"},
		{Method: "POST", Suffix: "/git-receive-pack", Handler: s.PostRPC, Rpc: "git-receive-pack"},
	}

	lockHandler := &lfs.LockHandler{}

	s.LfsServices = []LfsService{
		{Method: "POST", Suffix: "/objects/batch", Handler: basic.ServeBatchHandler},
		{Method: "GET", Suffix: "/objects/basic", Handler: basic.ServeDownloadHandler},
		{Method: "PUT", Suffix: "/objects/basic", Handler: lfs.Authenticate(basic.ServeUploadHandler)},
		{Method: "POST", Suffix: "/objects/basic/verify", Handler: basic.ServeVerifyHandler},
		// LFS Locks endpoints
		{Method: "POST", Suffix: "/locks", Handler: lfs.Authenticate(lockHandler.ServeCreateLockHandler)},
		{Method: "GET", Suffix: "/locks", Handler: lockHandler.ServeListLocksHandler},
		{Method: "POST", Suffix: "/locks/verify", Handler: lockHandler.ServeVerifyLocksHandler},
		{Method: "POST", Suffix: "/locks/*/unlock", Handler: lfs.Authenticate(lockHandler.ServeUnlockHandler)},
	}

	s.CacheManager = utils.NewCacheManager()
	if err := s.CacheManager.Start(); err != nil {
		return nil, errors.Wrap(err, "failed to start cache manager")
	}

	return s, nil
}

// ServerWrapper wraps the Server to provide HTTP handling.
func NewServerWrapper(server *Server) *ServerWrapper {
	return &ServerWrapper{Server: server}
}

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

	svc, repoURLPath := s.findService(r)
	if svc == nil {
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

	repoNamespace, repoName := getNamespaceAndRepo(repoURLPath)
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

	if s.Server.Config.Auth && r.Method == "POST" && strings.HasSuffix(r.RequestURI, "git-receive-pack") {
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

	if !repoExists(req.RepoPath) && s.Server.Config.AutoCreate {
		if err := initRepo(req.RepoName, &s.Server.Config); err != nil {
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

// Setup initializes the server configuration.
func (s *Server) Setup() error {
	if s.Ctx == nil {
		s.Ctx, s.Cancel = context.WithCancel(context.Background())
	}

	if err := s.Config.Setup(); err != nil {
		return err
	}

	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	if s.Cancel != nil {
		s.Cancel()
	}

	if s.GitopiaProxy != nil {
		s.GitopiaProxy.Stop()
	}

	return nil
}

func repoExists(p string) bool {
	_, err := os.Stat(path.Join(p, "objects"))
	return err == nil
}
