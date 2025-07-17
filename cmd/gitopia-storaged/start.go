package main

import (
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof" // Add pprof handlers to default HTTP mux
	"os"
	"runtime"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/app/consumer"
	"github.com/gitopia/gitopia-storage/handler"
	internalapp "github.com/gitopia/gitopia-storage/internal/app"
	internalhandler "github.com/gitopia/gitopia-storage/internal/app/handler"
	"github.com/gitopia/gitopia-storage/internal/app/handler/pr"
	"github.com/gitopia/gitopia-storage/utils"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/rs/cors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const (
	InvokeMergePullRequestQuery    = "tm.event='Tx' AND message.action='InvokeMergePullRequest'"
	InvokeDaoMergePullRequestQuery = "tm.event='Tx' AND message.action='InvokeDaoMergePullRequest'"
	ChallengeCreatedQuery          = "tm.event='NewBlock' AND gitopia.gitopia.storage.EventChallengeCreated.challenge_id EXISTS"
	PackfileUpdatedQuery           = "tm.event='Tx' AND gitopia.gitopia.storage.EventPackfileUpdated.repository_id EXISTS"
	PackfileDeletedQuery           = "tm.event='Tx' AND gitopia.gitopia.storage.EventPackfileDeleted.repository_id EXISTS"
	ReleaseAssetUpdatedQuery       = "tm.event='Tx' AND gitopia.gitopia.storage.EventReleaseAssetUpdated.repository_id EXISTS"
	ReleaseAssetDeletedQuery       = "tm.event='Tx' AND gitopia.gitopia.storage.EventReleaseAssetDeleted.repository_id EXISTS"
	LfsObjectUpdatedQuery          = "tm.event='Tx' AND gitopia.gitopia.storage.EventLFSObjectUpdated.repository_id EXISTS"
	LfsObjectDeletedQuery          = "tm.event='Tx' AND gitopia.gitopia.storage.EventLFSObjectDeleted.repository_id EXISTS"
	CreateReleaseQuery             = "tm.event='Tx' AND message.action='CreateRelease'"
	UpdateReleaseQuery             = "tm.event='Tx' AND message.action='UpdateRelease'"
	DeleteReleaseQuery             = "tm.event='Tx' AND message.action='DeleteRelease'"
	DaoCreateReleaseQuery          = "tm.event='Tx' AND message.action='DaoCreateRelease'"
	DeleteRepositoryQuery          = "tm.event='Tx' AND message.action='DeleteRepository'"
)

func NewStartCmd() *cobra.Command {
	var cmdStart = &cobra.Command{
		Use:           "start",
		Short:         "start git server",
		Long:          `starts the git server`,
		Args:          cobra.MinimumNArgs(0),
		RunE:          start,
		SilenceErrors: true,
	}
	AddGitopiaFlags(cmdStart)

	return cmdStart
}

func start(cmd *cobra.Command, args []string) error {
	g, ctx := errgroup.WithContext(cmd.Context())

	// Start pprof server for performance monitoring.
	g.Go(func() error {
		return startPprofServer(ctx, 6060)
	})

	// Start memory monitor to log memory usage periodically.
	g.Go(func() error {
		startMemoryMonitor(ctx)
		return nil
	})

	// Start the main web server.
	g.Go(func() error {
		return startWebServer(ctx, cmd)
	})

	// Start the event processor to handle blockchain events.
	g.Go(func() error {
		return startEventProcessor(ctx, cmd)
	})

	logrus.Info("application started")
	return g.Wait()
}

func startPprofServer(ctx context.Context, port int) error {
	addr := fmt.Sprintf("localhost:%d", port)
	logrus.Infof("starting pprof server on http://%s/debug/pprof/", addr)

	server := &http.Server{Addr: addr, Handler: nil} // Use default mux

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return errors.Wrap(err, "pprof server error")
	}
	logrus.Info("pprof server stopped")
	return nil
}

func startMemoryMonitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			logrus.Infof("Memory Usage: Alloc = %v MiB, TotalAlloc = %v MiB, Sys = %v MiB, NumGC = %v",
				m.Alloc/1024/1024, m.TotalAlloc/1024/1024, m.Sys/1024/1024, m.NumGC)
		case <-ctx.Done():
			logrus.Info("memory monitor stopped")
			return
		}
	}
}

func startWebServer(ctx context.Context, cmd *cobra.Command) error {
	server, err := setupWebServer(cmd)
	if err != nil {
		return errors.Wrap(err, "failed to setup web server")
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	logrus.Infof("starting web server on %s", server.Addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return errors.Wrap(err, "web server error")
	}
	logrus.Info("web server stopped")
	return nil
}

func setupWebServer(cmd *cobra.Command) (*http.Server, error) {
	server, err := internalapp.New(cmd, utils.Config{
		Dir:        viper.GetString("GIT_REPOS_DIR"),
		AutoCreate: true,
		Auth:       true,
		AutoHooks:  true,
		Hooks: &utils.HookScripts{
			PreReceive:  "gitopia-pre-receive",
			PostReceive: "gitopia-post-receive",
		},
	})
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(viper.GetString("GIT_REPOS_DIR"), os.ModePerm); err != nil {
		return nil, err
	}

	server.Ctx, server.Cancel = context.WithCancel(cmd.Context())

	if err = server.Setup(); err != nil {
		return nil, fmt.Errorf("failed to setup server: %w", err)
	}

	mux := http.NewServeMux()
	serverWrapper := internalapp.ServerWrapper{Server: server}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			serverWrapper.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.Handle("/objects/", http.HandlerFunc(serverWrapper.Server.ObjectsHandler))
	mux.Handle("/commits", http.HandlerFunc(internalhandler.CommitsHandler))
	mux.Handle("/commits/", http.HandlerFunc(internalhandler.CommitsHandler))
	mux.Handle("/content", http.HandlerFunc(internalhandler.ContentHandler))
	mux.Handle("/diff", http.HandlerFunc(internalhandler.CommitDiffHandler))
	mux.Handle("/pull/diff", http.HandlerFunc(pr.PullDiffHandler))
	mux.Handle("/upload", http.HandlerFunc(internalhandler.UploadAttachmentHandler))
	mux.Handle("/releases/", http.HandlerFunc(internalhandler.GetAttachmentHandler))
	mux.Handle("/pull/commits", http.HandlerFunc(pr.PullRequestCommitsHandler))
	mux.Handle("/pull/check", http.HandlerFunc(pr.PullRequestCheckHandler))
	mux.Handle("/raw/", http.HandlerFunc(internalhandler.GetRawFileHandler))

	handler := cors.Default().Handler(mux)

	return &http.Server{
		Addr:    fmt.Sprintf(":%v", viper.GetString("WEB_SERVER_PORT")),
		Handler: handler,
	}, nil
}

type eventSubscription struct {
	query   string
	handler func(context.Context, []byte) error
}

func startEventProcessor(ctx context.Context, cmd *cobra.Command) error {
	clientCtx := client.GetClientContextFromCmd(cmd)
	txf, err := tx.NewFactoryCLI(clientCtx, cmd.Flags())
	if err != nil {
		return errors.Wrap(err, "error initializing tx factory")
	}
	txf = txf.WithGasAdjustment(app.GAS_ADJUSTMENT)

	gc, err := gitopia.NewClient(ctx, clientCtx, txf)
	if err != nil {
		return errors.WithMessage(err, "gitopia client error")
	}
	defer gc.Close()

	gp := app.NewGitopiaProxy(gc)

	ipfsCfg := &ipfsclusterclient.Config{
		Host:    viper.GetString("IPFS_CLUSTER_PEER_HOST"),
		Port:    viper.GetString("IPFS_CLUSTER_PEER_PORT"),
		Timeout: time.Minute * 5,
	}
	cl, err := ipfsclusterclient.NewDefaultClient(ipfsCfg)
	if err != nil {
		return errors.Wrap(err, "failed to create IPFS cluster client")
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"provider": gp.ClientAddress(),
		"env":      viper.GetString("ENV"),
	}).Info("starting event processor")

	mcc, err := consumer.NewClient("invokeMergePullRequestEvent")
	if err != nil {
		return errors.WithMessage(err, "error creating consumer client")
	}

	dmcc, err := consumer.NewClient("invokeDaoMergePullRequestEvent")
	if err != nil {
		return errors.WithMessage(err, "error creating consumer client")
	}

	dcc, err := consumer.NewClient("deleteRepositoryEvent")
	if err != nil {
		return errors.WithMessage(err, "error creating consumer client")
	}

	mergeHandler := handler.NewInvokeMergePullRequestEventHandler(gp, mcc, cl)
	daoMergeHandler := handler.NewInvokeDaoMergePullRequestEventHandler(gp, dmcc, cl)
	challengeHandler := handler.NewChallengeEventHandler(gp)
	packfileUpdatedHandler := handler.NewPackfileUpdatedEventHandler(gp)
	packfileDeletedHandler := handler.NewPackfileDeletedEventHandler(gp)
	releaseAssetUpdatedHandler := handler.NewReleaseAssetUpdatedEventHandler(gp)
	releaseAssetDeletedHandler := handler.NewReleaseAssetDeletedEventHandler(gp)
	lfsObjectUpdatedHandler := handler.NewLfsObjectUpdatedEventHandler(gp)
	lfsObjectDeletedHandler := handler.NewLfsObjectDeletedEventHandler(gp)
	releaseHandler := handler.NewReleaseEventHandler(gp, cl)
	daoReleaseHandler := handler.NewDaoCreateReleaseEventHandler(gp, cl)
	deleteRepoHandler := handler.NewDeleteRepositoryEventHandler(gp, dcc, cl)

	subscriptions := []eventSubscription{
		{query: InvokeMergePullRequestQuery, handler: mergeHandler.Handle},
		{query: InvokeDaoMergePullRequestQuery, handler: daoMergeHandler.Handle},
		{query: ChallengeCreatedQuery, handler: challengeHandler.Handle},
		{query: CreateReleaseQuery, handler: func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventCreateReleaseType)
		}},
		{query: DaoCreateReleaseQuery, handler: daoReleaseHandler.Handle},
		{query: UpdateReleaseQuery, handler: func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventUpdateReleaseType)
		}},
		{query: DeleteReleaseQuery, handler: func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventDeleteReleaseType)
		}},
		{query: DeleteRepositoryQuery, handler: deleteRepoHandler.Handle},
	}

	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		subscriptions = append(subscriptions,
			eventSubscription{query: PackfileUpdatedQuery, handler: packfileUpdatedHandler.Handle},
			eventSubscription{query: PackfileDeletedQuery, handler: packfileDeletedHandler.Handle},
			eventSubscription{query: ReleaseAssetUpdatedQuery, handler: releaseAssetUpdatedHandler.Handle},
			eventSubscription{query: ReleaseAssetDeletedQuery, handler: releaseAssetDeletedHandler.Handle},
			eventSubscription{query: LfsObjectUpdatedQuery, handler: lfsObjectUpdatedHandler.Handle},
			eventSubscription{query: LfsObjectDeletedQuery, handler: lfsObjectDeletedHandler.Handle},
		)
	}

	g, gCtx := errgroup.WithContext(ctx)
	for _, sub := range subscriptions {
		s := sub // capture loop variable
		g.Go(func() error {
			return subscribeToEvent(gCtx, s.query, s.handler)
		})
	}

	return g.Wait()
}

func subscribeToEvent(ctx context.Context, query string, handler func(context.Context, []byte) error) error {
	tmc, err := gitopia.NewWSEvents(ctx, query)
	if err != nil {
		return errors.Wrapf(err, "failed to create ws event client for query: %s", query)
	}

	done, errChan := tmc.Subscribe(ctx, handler)
	select {
	case err := <-errChan:
		return errors.Wrapf(err, "subscription error for query: %s", query)
	case <-done:
		logrus.Infof("subscription done for query: %s", query)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}