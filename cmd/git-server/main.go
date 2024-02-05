package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/git-server/internal/app"
	"github.com/gitopia/git-server/internal/db"
	lfsutil "github.com/gitopia/git-server/lfs"
	"github.com/gitopia/git-server/route"
	"github.com/gitopia/git-server/route/lfs"
	"github.com/gitopia/git-server/route/pr"
	"github.com/gitopia/git-server/utils"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/cors"
	"github.com/spf13/viper"
)

const (
	AccountAddressPrefix = "gitopia"
	AccountPubKeyPrefix  = AccountAddressPrefix + sdk.PrefixPublic
	cacheCleanupPeriod   = 6 * time.Hour
)

var (
	env []string
)

func New(cfg utils.Config) *app.Server {
	s := app.Server{Config: cfg}
	basic := &lfs.BasicHandler{
		DefaultStorage: lfsutil.Storage(lfsutil.StorageLocal),
		Storagers: map[lfsutil.Storage]lfsutil.Storager{
			lfsutil.StorageLocal: &lfsutil.LocalStorage{Root: viper.GetString("LFS_OBJECTS_DIR")},
		},
	}
	s.Services = []app.Service{
		{"GET", "/info/refs", s.GetInfoRefs, ""},
		{"POST", "/git-upload-pack", s.PostRPC, "git-upload-pack"},
		{"POST", "/git-receive-pack", s.PostRPC, "git-receive-pack"},
	}

	s.LfsServices = []app.LfsService{
		{"POST", "/objects/batch", lfs.Authenticate(basic.ServeBatchHandler)},
		{"GET", "/objects/basic", basic.ServeDownloadHandler},
		{"PUT", "/objects/basic", lfs.Authenticate(basic.ServeUploadHandler)},
		{"POST", "/objects/basic/verify", basic.ServeVerifyHandler},
	}

	// Use PATH if full path is not specified
	if s.Config.GitPath == "" {
		s.Config.GitPath = "git"
	}

	s.AuthFunc = app.AuthFunc

	return &s
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

	utils.LogInfo("ENV", os.Getenv("ENV"))
	fmt.Println(viper.AllSettings())
	for _, key := range viper.AllKeys() {
		env = append(env, strings.ToUpper(key)+"="+viper.GetString(key))
	}

	// load cache information
	dbPath := "./cache.db"
	db.OpenDb(dbPath)
	defer db.CacheDb.Close()

	conf := sdk.GetConfig()
	conf.SetBech32PrefixForAccount(AccountAddressPrefix, AccountPubKeyPrefix)
	// cannot seal the config
	// cosmos client sets address prefix for each broadcasttx API call. probably a bug
	// conf.Seal()

	// Configure git service
	service := New(utils.Config{
		Dir:        viper.GetString("GIT_DIR"),
		AutoCreate: true,
		Auth:       true,
		AutoHooks:  true,
		Hooks: &utils.HookScripts{
			PreReceive:  "gitopia-pre-receive",
			PostReceive: "gitopia-post-receive",
		},
	})

	// Ensure cache directory exists
	os.MkdirAll(viper.GetString("GIT_DIR"), os.ModePerm)

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

	go func() {
		ticker := time.NewTicker(cacheCleanupPeriod)
		defer ticker.Stop()

		// clear old cached repos every time the ticker ticks
		for range ticker.C {
			if err := utils.CleanupExpiredRepoCache(db.CacheDb, viper.GetString("GIT_DIR")); err != nil {
				log.Printf("Error cleaning up cache entry: %s", err)
			} else {
				log.Printf("Cleaned up older cached repos")
			}
		}
	}()

	// Start HTTP server
	if err := http.ListenAndServe(fmt.Sprintf(":%v", viper.GetString("WEB_SERVER_PORT")), handler); err != nil {
		log.Fatal(err)
	}
}
