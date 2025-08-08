package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	_ "net/http/pprof" // Add pprof handlers to default HTTP mux
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
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

	clientCtx := client.GetClientContextFromCmd(cmd)
	txf, err := tx.NewFactoryCLI(clientCtx, cmd.Flags())
	if err != nil {
		return errors.Wrap(err, "error initializing tx factory")
	}
	txf = txf.WithGasAdjustment(app.GAS_ADJUSTMENT)

	gitopiaClient, err := gitopia.NewClient(ctx, clientCtx, txf)
	if err != nil {
		return errors.WithMessage(err, "gitopia client error")
	}
	defer gitopiaClient.Close()

	// Check client account balance before starting services
	if err := checkClientBalance(ctx, gitopiaClient); err != nil {
		return errors.WithMessage(err, "client balance check failed")
	}

	batchTxManager := app.NewBatchTxManager(gitopiaClient, app.BLOCK_TIME)
	batchTxManager.Start()
	defer batchTxManager.Stop()

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
		return startWebServer(ctx, cmd, batchTxManager)
	})

	// Start the event processor to handle blockchain events.
	g.Go(func() error {
		return startEventProcessor(ctx, cmd, gitopiaClient, batchTxManager)
	})

	logrus.Info("application started")
	return g.Wait()
}

func startWebServer(ctx context.Context, cmd *cobra.Command, batchTxManager *app.BatchTxManager) error {
	server, err := setupWebServer(cmd, batchTxManager)
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

func setupWebServer(cmd *cobra.Command, batchTxManager *app.BatchTxManager) (*http.Server, error) {
	server, err := internalapp.New(cmd, utils.Config{
		Dir:        viper.GetString("GIT_REPOS_DIR"),
		AutoCreate: true,
		Auth:       true,
		AutoHooks:  true,
		Hooks: &utils.HookScripts{
			PreReceive:  "gitopia-pre-receive",
			PostReceive: "gitopia-post-receive",
		},
	}, batchTxManager)
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

func startEventProcessor(ctx context.Context, cmd *cobra.Command, gitopiaClient gitopia.Client, batchTxManager *app.BatchTxManager) error {
	gp := app.NewGitopiaProxy(gitopiaClient, batchTxManager)

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
	daoMergeHandler := handler.NewInvokeMergePullRequestEventHandler(gp, dmcc, cl)
	challengeHandler := handler.NewChallengeEventHandler(gp)
	packfileUpdatedHandler := handler.NewPackfileUpdatedEventHandler(gp)
	packfileDeletedHandler := handler.NewPackfileDeletedEventHandler(gp)
	releaseAssetUpdatedHandler := handler.NewReleaseAssetUpdatedEventHandler(gp)
	releaseAssetDeletedHandler := handler.NewReleaseAssetDeletedEventHandler(gp)
	lfsObjectUpdatedHandler := handler.NewLfsObjectUpdatedEventHandler(gp)
	lfsObjectDeletedHandler := handler.NewLfsObjectDeletedEventHandler(gp)
	releaseHandler := handler.NewReleaseEventHandler(gp, cl)
	daoReleaseHandler := handler.NewReleaseEventHandler(gp, cl)
	deleteRepoHandler := handler.NewDeleteRepositoryEventHandler(gp, dcc, cl)

	// Create multiple WebSocket clients to distribute subscriptions and avoid hitting the 5 subscription limit

	// Client 1: Merge and Challenge events (3 subscriptions)
	client1, err := gitopia.NewWSEvents(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create WebSocket client 1")
	}
	defer client1.Close()

	client1Queries := []string{
		InvokeMergePullRequestQuery,
		InvokeDaoMergePullRequestQuery,
		ChallengeCreatedQuery,
	}

	if err := client1.SubscribeQueries(ctx, client1Queries...); err != nil {
		return errors.Wrap(err, "failed to subscribe to client 1 events")
	}

	client1EventHandlers := map[string]func(context.Context, []byte) error{
		InvokeMergePullRequestQuery:    mergeHandler.Handle,
		InvokeDaoMergePullRequestQuery: daoMergeHandler.Handle,
		ChallengeCreatedQuery:          challengeHandler.Handle,
	}

	// Client 2: Release events (5 subscriptions)
	client2, err := gitopia.NewWSEvents(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create WebSocket client 2")
	}
	defer client2.Close()

	client2Queries := []string{
		CreateReleaseQuery,
		UpdateReleaseQuery,
		DeleteReleaseQuery,
		DaoCreateReleaseQuery,
		DeleteRepositoryQuery,
	}

	if err := client2.SubscribeQueries(ctx, client2Queries...); err != nil {
		return errors.Wrap(err, "failed to subscribe to client 2 events")
	}

	client2EventHandlers := map[string]func(context.Context, []byte) error{
		CreateReleaseQuery: func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventCreateReleaseType)
		},
		UpdateReleaseQuery: func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventUpdateReleaseType)
		},
		DeleteReleaseQuery: func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventDeleteReleaseType)
		},
		DaoCreateReleaseQuery: daoReleaseHandler.Handle,
		DeleteRepositoryQuery: deleteRepoHandler.Handle,
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Start client 1 event processor
	g.Go(func() error {
		done, errChan := client1.ProcessEvents(gCtx, func(ctx context.Context, eventBuf []byte) error {
			return routeEventToHandler(ctx, eventBuf, client1EventHandlers)
		})
		select {
		case err := <-errChan:
			return errors.Wrap(err, "client 1 event processing error")
		case <-done:
			logger.FromContext(ctx).Info("client 1 event processing completed")
			return nil
		}
	})

	// Start client 2 event processor
	g.Go(func() error {
		done, errChan := client2.ProcessEvents(gCtx, func(ctx context.Context, eventBuf []byte) error {
			return routeEventToHandler(ctx, eventBuf, client2EventHandlers)
		})
		select {
		case err := <-errChan:
			return errors.Wrap(err, "client 2 event processing error")
		case <-done:
			logger.FromContext(ctx).Info("client 2 event processing completed")
			return nil
		}
	})

	// Handle external pinning events if enabled
	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		logger.FromContext(ctx).Info("external pinning enabled, starting external pinning event processor")

		// Client 3: Packfile and Release Asset events (4 subscriptions)
		client3, err := gitopia.NewWSEvents(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to create WebSocket client 3")
		}
		defer client3.Close()

		client3Queries := []string{
			PackfileUpdatedQuery,
			PackfileDeletedQuery,
			ReleaseAssetUpdatedQuery,
			ReleaseAssetDeletedQuery,
		}

		if err := client3.SubscribeQueries(ctx, client3Queries...); err != nil {
			return errors.Wrap(err, "failed to subscribe to client 3 events")
		}

		client3EventHandlers := map[string]func(context.Context, []byte) error{
			PackfileUpdatedQuery:     packfileUpdatedHandler.Handle,
			PackfileDeletedQuery:     packfileDeletedHandler.Handle,
			ReleaseAssetUpdatedQuery: releaseAssetUpdatedHandler.Handle,
			ReleaseAssetDeletedQuery: releaseAssetDeletedHandler.Handle,
		}

		// Client 4: LFS Object events (2 subscriptions)
		client4, err := gitopia.NewWSEvents(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to create WebSocket client 4")
		}
		defer client4.Close()

		client4Queries := []string{
			LfsObjectUpdatedQuery,
			LfsObjectDeletedQuery,
		}

		if err := client4.SubscribeQueries(ctx, client4Queries...); err != nil {
			return errors.Wrap(err, "failed to subscribe to client 4 events")
		}

		client4EventHandlers := map[string]func(context.Context, []byte) error{
			LfsObjectUpdatedQuery: lfsObjectUpdatedHandler.Handle,
			LfsObjectDeletedQuery: lfsObjectDeletedHandler.Handle,
		}

		// Start client 3 event processor
		g.Go(func() error {
			done, errChan := client3.ProcessEvents(gCtx, func(ctx context.Context, eventBuf []byte) error {
				return routeEventToHandler(ctx, eventBuf, client3EventHandlers)
			})
			select {
			case err := <-errChan:
				return errors.Wrap(err, "client 3 event processing error")
			case <-done:
				logger.FromContext(ctx).Info("client 3 event processing completed")
				return nil
			}
		})

		// Start client 4 event processor
		g.Go(func() error {
			done, errChan := client4.ProcessEvents(gCtx, func(ctx context.Context, eventBuf []byte) error {
				return routeEventToHandler(ctx, eventBuf, client4EventHandlers)
			})
			select {
			case err := <-errChan:
				return errors.Wrap(err, "client 4 event processing error")
			case <-done:
				logger.FromContext(ctx).Info("client 4 event processing completed")
				return nil
			}
		})
	}

	return g.Wait()
}

// routeEventToHandler routes events to the appropriate handler based on the event content
// It extracts the query information from the event and calls the corresponding handler
func routeEventToHandler(ctx context.Context, eventBuf []byte, handlers map[string]func(context.Context, []byte) error) error {
	// Parse the event to extract query information
	// The event structure typically contains query information that we can use for routing
	var eventData map[string]interface{}
	if err := json.Unmarshal(eventBuf, &eventData); err != nil {
		return errors.Wrap(err, "failed to parse event data")
	}

	for query, handler := range handlers {
		// Check if this event matches the query pattern
		if shouldHandleEvent(eventBuf, query) {
			return handler(ctx, eventBuf)
		}
	}

	// If no handler matches, log and continue
	logger.FromContext(ctx).WithField("event", string(eventBuf)).Debug("no handler found for event")
	return nil
}

// shouldHandleEvent determines if an event should be handled by a specific query handler
// This function examines the event content to match it with the appropriate query
func shouldHandleEvent(eventBuf []byte, query string) bool {
	// Convert event to string for pattern matching
	eventStr := string(eventBuf)

	switch query {
	case InvokeMergePullRequestQuery:
		return strings.Contains(eventStr, "InvokeMergePullRequest")
	case InvokeDaoMergePullRequestQuery:
		return strings.Contains(eventStr, "InvokeDaoMergePullRequest")
	case ChallengeCreatedQuery:
		return strings.Contains(eventStr, "EventChallengeCreated")
	case CreateReleaseQuery:
		return strings.Contains(eventStr, "CreateRelease") && !strings.Contains(eventStr, "DaoCreateRelease")
	case DaoCreateReleaseQuery:
		return strings.Contains(eventStr, "DaoCreateRelease")
	case UpdateReleaseQuery:
		return strings.Contains(eventStr, "UpdateRelease")
	case DeleteReleaseQuery:
		return strings.Contains(eventStr, "DeleteRelease")
	case DeleteRepositoryQuery:
		return strings.Contains(eventStr, "DeleteRepository")
	case PackfileUpdatedQuery:
		return strings.Contains(eventStr, "EventPackfileUpdated")
	case PackfileDeletedQuery:
		return strings.Contains(eventStr, "EventPackfileDeleted")
	case ReleaseAssetUpdatedQuery:
		return strings.Contains(eventStr, "EventReleaseAssetUpdated")
	case ReleaseAssetDeletedQuery:
		return strings.Contains(eventStr, "EventReleaseAssetDeleted")
	case LfsObjectUpdatedQuery:
		return strings.Contains(eventStr, "EventLFSObjectUpdated")
	case LfsObjectDeletedQuery:
		return strings.Contains(eventStr, "EventLFSObjectDeleted")
	default:
		return false
	}
}

func checkClientBalance(ctx context.Context, gitopiaClient gitopia.Client) error {
	// Create a GitopiaProxy to access balance checking functionality
	proxy := app.NewGitopiaProxy(gitopiaClient, nil)

	// Get client address
	clientAddress := proxy.ClientAddress()
	if clientAddress == "" {
		return errors.New("client address is empty")
	}

	// Check client balance
	balance, err := proxy.CosmosBankBalance(ctx, clientAddress, "ulore")
	if err != nil {
		return errors.Wrap(err, "failed to get client balance")
	}

	minRequiredAmount := sdk.NewInt(100_000_000) // 100 lore

	logrus.WithFields(logrus.Fields{
		"client_address": clientAddress,
		"balance":        balance.String(),
		"denom":          "ulore",
		"min_required":   minRequiredAmount.String(),
	}).Info("client balance check")

	// Check if balance is sufficient
	if balance.Amount.LT(minRequiredAmount) {
		return errors.Errorf("insufficient balance: have %s, need at least %s for storage operations",
			balance.String(), minRequiredAmount.String())
	}

	logrus.Info("client balance check passed")
	return nil
}
