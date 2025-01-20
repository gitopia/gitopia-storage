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
	"github.com/gitopia/git-server/internal/app/handler"
	"github.com/gitopia/git-server/internal/app/handler/pr"
	"github.com/gitopia/git-server/internal/db"
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
	server, err := app.New(utils.Config{
		Dir:        viper.GetString("GIT_DIR"),
		AutoCreate: true,
		Auth:       true,
		AutoHooks:  true,
		Hooks: &utils.HookScripts{
			PreReceive:  "gitopia-pre-receive",
			PostReceive: "gitopia-post-receive",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Ensure cache directory exists
	os.MkdirAll(viper.GetString("GIT_DIR"), os.ModePerm)

	// Configure git server. Will create git repos path if it does not exist.
	// If hooks are set, it will also update all repos with new version of hook scripts.
	if err = server.Setup(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	serverWrapper := app.ServerWrapper{
		Server: server,
	}

	mux.Handle("/", &serverWrapper)
	mux.Handle("/objects/", http.HandlerFunc(serverWrapper.Server.ObjectsHandler))
	mux.Handle("/commits", http.HandlerFunc(handler.CommitsHandler))
	mux.Handle("/commits/", http.HandlerFunc(handler.CommitsHandler))
	mux.Handle("/content", http.HandlerFunc(handler.ContentHandler))
	mux.Handle("/diff", http.HandlerFunc(handler.CommitDiffHandler))
	mux.Handle("/pull/diff", http.HandlerFunc(pr.PullDiffHandler))
	mux.Handle("/upload", http.HandlerFunc(handler.UploadAttachmentHandler))
	mux.Handle("/releases/", http.HandlerFunc(handler.GetAttachmentHandler))
	mux.Handle("/pull/commits", http.HandlerFunc(pr.PullRequestCommitsHandler))
	mux.Handle("/pull/check", http.HandlerFunc(pr.PullRequestCheckHandler))
	mux.Handle("/raw/", http.HandlerFunc(handler.GetRawFileHandler))

	handler := cors.Default().Handler(mux)

	go func() {
		ticker := time.NewTicker(cacheCleanupPeriod)
		defer ticker.Stop()

		// clear old cached repos every time the ticker ticks
		for range ticker.C {
			app.CacheMutex.Lock()
			if err := utils.CleanupExpiredRepoCache(db.CacheDb, viper.GetString("GIT_DIR")); err != nil {
				log.Printf("Error cleaning up cache entry: %s", err)
			} else {
				log.Printf("Cleaned up older cached repos")
			}
			app.CacheMutex.Unlock()
		}
	}()

	// Start HTTP server
	if err := http.ListenAndServe(fmt.Sprintf(":%v", viper.GetString("WEB_SERVER_PORT")), handler); err != nil {
		log.Fatal(err)
	}
}
