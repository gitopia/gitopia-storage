package app

import (
	"context"
	"net/http"

	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/utils"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
)

const (
	branchPrefix                 = "refs/heads/"
	tagPrefix                    = "refs/tags/"
	LARGE_OBJECT_THRESHOLD int64 = 1024 * 1024
)

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
	AuthFunc          func(string, *Request) (bool, string, error)
	QueryService      QueryService
	GitopiaProxy      *app.GitopiaProxy
	IPFSClusterClient ipfsclusterclient.Client
	CacheManager      *utils.CacheManager
	Ctx               context.Context
	Cancel            context.CancelFunc
}

type ServerWrapper struct {
	Server *Server
}

type Request struct {
	*http.Request
	RepoName string
	RepoPath string
	Address  string
}
