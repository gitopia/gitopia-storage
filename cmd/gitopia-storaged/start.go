package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
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
)

const (
	InvokeMergePullRequestQuery    = "tm.event='Tx' AND message.action='InvokeMergePullRequest'"
	InvokeDaoMergePullRequestQuery = "tm.event='Tx' AND message.action='InvokeDaoMergePullRequest'"
	ChallengeCreatedQuery          = "tm.event='NewBlock' AND gitopia.gitopia.storage.EventChallengeCreated.challenge_id EXISTS"
	PackfileUpdatedQuery           = "tm.event='Tx' AND gitopia.gitopia.storage.EventPackfileUpdated.repository_id EXISTS"
	PackfileDeletedQuery           = "tm.event='Tx' AND gitopia.gitopia.storage.EventPackfileDeleted.repository_id EXISTS"
	ReleaseAssetUpdatedQuery       = "tm.event='Tx' AND gitopia.gitopia.storage.EventReleaseAssetUpdated.repository_id EXISTS"
	ReleaseAssetDeletedQuery       = "tm.event='Tx' AND gitopia.gitopia.storage.EventReleaseAssetDeleted.repository_id EXISTS"
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

func validateConfig() error {
	requiredConfigs := []string{
		"IPFS_CLUSTER_PEER_HOST",
		"IPFS_CLUSTER_PEER_PORT",
		"WEB_SERVER_PORT",
		"GIT_REPOS_DIR",
	}

	for _, config := range requiredConfigs {
		if !viper.IsSet(config) {
			return fmt.Errorf("required configuration %s is not set", config)
		}
	}

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

	// Ensure cache directory exists
	if err := os.MkdirAll(viper.GetString("GIT_REPOS_DIR"), os.ModePerm); err != nil {
		return nil, err
	}

	// Configure git server
	if err = server.Setup(); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	serverWrapper := internalapp.ServerWrapper{Server: server}

	mux.Handle("/", &serverWrapper)
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

func start(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	if err := validateConfig(); err != nil {
		return errors.Wrap(err, "configuration validation failed")
	}

	// Start event processor in a goroutine
	eventErrChan := make(chan error, 1)
	go func() {
		defer close(eventErrChan)

		clientCtx := client.GetClientContextFromCmd(cmd)
		txf, err := tx.NewFactoryCLI(clientCtx, cmd.Flags())
		if err != nil {
			eventErrChan <- errors.Wrap(err, "error initializing tx factory")
			return
		}
		txf = txf.WithGasAdjustment(app.GAS_ADJUSTMENT)

		gc, err := gitopia.NewClient(ctx, clientCtx, txf)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "gitopia client error")
			return
		}
		defer gc.Close()

		gp := app.NewGitopiaProxy(gc)

		// Initialize IPFS cluster client
		ipfsCfg := &ipfsclusterclient.Config{
			Host:    viper.GetString("IPFS_CLUSTER_PEER_HOST"),
			Port:    viper.GetString("IPFS_CLUSTER_PEER_PORT"),
			Timeout: time.Minute * 5, // Reasonable timeout for pinning
		}
		cl, err := ipfsclusterclient.NewDefaultClient(ipfsCfg)
		if err != nil {
			eventErrChan <- errors.Wrap(err, "failed to create IPFS cluster client")
			return
		}

		logger.FromContext(ctx).WithFields(logrus.Fields{
			"provider": gp.ClientAddress(),
			"env":      viper.GetString("ENV"),
			"port":     viper.GetString("WEB_SERVER_PORT"),
		}).Info("starting storage provider")

		mtmc, err := gitopia.NewWSEvents(ctx, InvokeMergePullRequestQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

		dmtmc, err := gitopia.NewWSEvents(ctx, InvokeDaoMergePullRequestQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

		ctmc, err := gitopia.NewWSEvents(ctx, ChallengeCreatedQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

		putmc, err := gitopia.NewWSEvents(ctx, PackfileUpdatedQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

		pdmc, err := gitopia.NewWSEvents(ctx, PackfileDeletedQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

		rautmc, err := gitopia.NewWSEvents(ctx, ReleaseAssetUpdatedQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

		radtmc, err := gitopia.NewWSEvents(ctx, ReleaseAssetDeletedQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

		mcc, err := consumer.NewClient("invokeMergePullRequestEvent")
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "error creating consumer client")
			return
		}

		dmcc, err := consumer.NewClient("invokeDaoMergePullRequestEvent")
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "error creating consumer client")
			return
		}

		dcc, err := consumer.NewClient("deleteRepositoryEvent")
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "error creating consumer client")
			return
		}

		mergeHandler := handler.NewInvokeMergePullRequestEventHandler(gp, mcc, cl)
		daoMergeHandler := handler.NewInvokeDaoMergePullRequestEventHandler(gp, dmcc, cl)
		challengeHandler := handler.NewChallengeEventHandler(gp)
		packfileUpdatedHandler := handler.NewPackfileUpdatedEventHandler(gp)
		packfileDeletedHandler := handler.NewPackfileDeletedEventHandler(gp)
		releaseAssetUpdatedHandler := handler.NewReleaseAssetUpdatedEventHandler(gp)
		releaseAssetDeletedHandler := handler.NewReleaseAssetDeletedEventHandler(gp)
		releaseHandler := handler.NewReleaseEventHandler(gp, cl)
		daoReleaseHandler := handler.NewDaoCreateReleaseEventHandler(gp, cl)
		deleteRepoHandler := handler.NewDeleteRepositoryEventHandler(gp, dcc, cl)

		mergeDone, mergeSubscribeErr := mtmc.Subscribe(ctx, mergeHandler.Handle)
		daoMergeDone, daoMergeSubscribeErr := dmtmc.Subscribe(ctx, daoMergeHandler.Handle)
		challengeDone, challengeSubscribeErr := ctmc.Subscribe(ctx, challengeHandler.Handle)

		// Subscribe to release events
		createReleaseTMC, err := gitopia.NewWSEvents(ctx, CreateReleaseQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "create release tm error")
			return
		}
		createReleaseDone, createReleaseSubscribeErr := createReleaseTMC.Subscribe(ctx, func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventCreateReleaseType)
		})

		daoCreateReleaseTMC, err := gitopia.NewWSEvents(ctx, DaoCreateReleaseQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "create dao release tm error")
			return
		}
		daoCreateReleaseDone, daoCreateReleaseSubscribeErr := daoCreateReleaseTMC.Subscribe(ctx, daoReleaseHandler.Handle)

		updateReleaseTMC, err := gitopia.NewWSEvents(ctx, UpdateReleaseQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "update release tm error")
			return
		}
		updateReleaseDone, updateReleaseSubscribeErr := updateReleaseTMC.Subscribe(ctx, func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventUpdateReleaseType)
		})

		deleteReleaseTMC, err := gitopia.NewWSEvents(ctx, DeleteReleaseQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "delete release tm error")
			return
		}
		deleteReleaseDone, deleteReleaseSubscribeErr := deleteReleaseTMC.Subscribe(ctx, func(ctx context.Context, eventBuf []byte) error {
			return releaseHandler.Handle(ctx, eventBuf, handler.EventDeleteReleaseType)
		})

		// Subscribe to repository deletion events
		deleteRepoTMC, err := gitopia.NewWSEvents(ctx, DeleteRepositoryQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "delete repository tm error")
			return
		}
		deleteRepoDone, deleteRepoSubscribeErr := deleteRepoTMC.Subscribe(ctx, deleteRepoHandler.Handle)

		// Only subscribe to packfile and release asset events if external pinning is enabled
		var packfileUpdatedDone, packfileDeletedDone, releaseAssetUpdatedDone, releaseAssetDeletedDone <-chan struct{}
		var packfileUpdatedSubscribeErr, packfileDeletedSubscribeErr, releaseAssetUpdatedSubscribeErr, releaseAssetDeletedSubscribeErr <-chan error
		if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
			packfileUpdatedDone, packfileUpdatedSubscribeErr = putmc.Subscribe(ctx, packfileUpdatedHandler.Handle)
			packfileDeletedDone, packfileDeletedSubscribeErr = pdmc.Subscribe(ctx, packfileDeletedHandler.Handle)
			releaseAssetUpdatedDone, releaseAssetUpdatedSubscribeErr = rautmc.Subscribe(ctx, releaseAssetUpdatedHandler.Handle)
			releaseAssetDeletedDone, releaseAssetDeletedSubscribeErr = radtmc.Subscribe(ctx, releaseAssetDeletedHandler.Handle)
		}

		select {
		case err = <-mergeSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "merge tm subscribe error")
		case <-mergeDone:
			logger.FromContext(ctx).Info("merge done")
		case err = <-daoMergeSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "dao merge tm subscribe error")
		case <-daoMergeDone:
			logger.FromContext(ctx).Info("dao merge done")
		case err = <-challengeSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "challenge tm subscribe error")
		case <-challengeDone:
			logger.FromContext(ctx).Info("challenge done")
		case err = <-packfileUpdatedSubscribeErr:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				eventErrChan <- errors.WithMessage(err, "packfile updated tm subscribe error")
			}
		case <-packfileUpdatedDone:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				logger.FromContext(ctx).Info("packfile updated done")
			}
		case err = <-packfileDeletedSubscribeErr:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				eventErrChan <- errors.WithMessage(err, "packfile deleted tm subscribe error")
			}
		case <-packfileDeletedDone:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				logger.FromContext(ctx).Info("packfile deleted done")
			}
		case err = <-releaseAssetUpdatedSubscribeErr:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				eventErrChan <- errors.WithMessage(err, "release asset updated tm subscribe error")
			}
		case <-releaseAssetUpdatedDone:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				logger.FromContext(ctx).Info("release asset updated done")
			}
		case err = <-releaseAssetDeletedSubscribeErr:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				eventErrChan <- errors.WithMessage(err, "release asset deleted tm subscribe error")
			}
		case <-releaseAssetDeletedDone:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				logger.FromContext(ctx).Info("release asset deleted done")
			}
		case err = <-createReleaseSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "create release tm subscribe error")
		case <-createReleaseDone:
			logger.FromContext(ctx).Info("create release done")
		case err = <-daoCreateReleaseSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "create dao release tm subscribe error")
		case <-daoCreateReleaseDone:
			logger.FromContext(ctx).Info("create dao release done")
		case err = <-updateReleaseSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "update release tm subscribe error")
		case <-updateReleaseDone:
			logger.FromContext(ctx).Info("update release done")
		case err = <-deleteReleaseSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "delete release tm subscribe error")
		case <-deleteReleaseDone:
			logger.FromContext(ctx).Info("delete release done")
		case err = <-deleteRepoSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "delete repository tm subscribe error")
		case <-deleteRepoDone:
			logger.FromContext(ctx).Info("delete repository done")
		case <-ctx.Done():
			eventErrChan <- ctx.Err()
		}
	}()

	// Start web server in a goroutine
	webErrChan := make(chan error, 1)
	go func() {
		server, err := setupWebServer(cmd)
		if err != nil {
			webErrChan <- err
			return
		}

		webErrChan <- server.ListenAndServe()
	}()

	// Wait for either service to error out
	select {
	case err := <-eventErrChan:
		return errors.Wrap(err, "event processor error")
	case err := <-webErrChan:
		return errors.Wrap(err, "web server error")
	}
}
