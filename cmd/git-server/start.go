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
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/app/consumer"
	"github.com/gitopia/git-server/handler"
	internalapp "github.com/gitopia/git-server/internal/app"
	internalhandler "github.com/gitopia/git-server/internal/app/handler"
	"github.com/gitopia/git-server/internal/app/handler/pr"
	"github.com/gitopia/git-server/utils"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/rs/cors"
	"github.com/sirupsen/logrus"
)

const (
	InvokeForkRepositoryQuery      = "tm.event='Tx' AND message.action='InvokeForkRepository'"
	InvokeMergePullRequestQuery    = "tm.event='Tx' AND message.action='InvokeMergePullRequest'"
	InvokeDaoMergePullRequestQuery = "tm.event='Tx' AND message.action='InvokeDaoMergePullRequest'"
	ChallengeCreatedQuery          = "tm.event='NewBlock' AND gitopia.gitopia.storage.EventChallengeCreated.challenge_id EXISTS"
	PackfileUpdatedQuery           = "tm.event='Tx' AND gitopia.gitopia.storage.EventPackfileUpdated.repository_id EXISTS"
	ReleaseAssetUpdatedQuery       = "tm.event='Tx' AND gitopia.gitopia.storage.EventReleaseAssetUpdated.repository_id EXISTS"
	CreateReleaseQuery             = "tm.event='Tx' AND message.action='CreateRelease'"
	UpdateReleaseQuery             = "tm.event='Tx' AND message.action='UpdateRelease'"
	DeleteReleaseQuery             = "tm.event='Tx' AND message.action='DeleteRelease'"
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
	ctx := cmd.Context()

	// Start event processor in a goroutine
	eventErrChan := make(chan error, 1)
	go func() {
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

		ftmc, err := gitopia.NewWSEvents(ctx, InvokeForkRepositoryQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

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

		rautmc, err := gitopia.NewWSEvents(ctx, ReleaseAssetUpdatedQuery)
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "tm error")
			return
		}

		fcc, err := consumer.NewClient("invokeForkRepositoryEvent")
		if err != nil {
			eventErrChan <- errors.WithMessage(err, "error creating consumer client")
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

		forkHandler := handler.NewInvokeForkRepositoryEventHandler(gp, fcc)
		mergeHandler := handler.NewInvokeMergePullRequestEventHandler(gp, mcc, cl)
		daoMergeHandler := handler.NewInvokeDaoMergePullRequestEventHandler(gp, dmcc, cl)
		challengeHandler := handler.NewChallengeEventHandler(gp)
		packfileUpdatedHandler := handler.NewPackfileUpdatedEventHandler(gp)
		releaseAssetUpdatedHandler := handler.NewReleaseAssetUpdatedEventHandler(gp)
		releaseHandler := handler.NewReleaseEventHandler(gp, cl)

		forkDone, forkSubscribeErr := ftmc.Subscribe(ctx, forkHandler.Handle)
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

		// Only subscribe to packfile and release asset events if external pinning is enabled
		var packfileUpdatedDone, releaseAssetUpdatedDone <-chan struct{}
		var packfileUpdatedSubscribeErr, releaseAssetUpdatedSubscribeErr <-chan error
		if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
			packfileUpdatedDone, packfileUpdatedSubscribeErr = putmc.Subscribe(ctx, packfileUpdatedHandler.Handle)
			releaseAssetUpdatedDone, releaseAssetUpdatedSubscribeErr = rautmc.Subscribe(ctx, releaseAssetUpdatedHandler.Handle)
		}

		select {
		case err = <-forkSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "fork tm subscribe error")
		case <-forkDone:
			logger.FromContext(ctx).Info("fork done")
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
		case err = <-releaseAssetUpdatedSubscribeErr:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				eventErrChan <- errors.WithMessage(err, "release asset updated tm subscribe error")
			}
		case <-releaseAssetUpdatedDone:
			if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
				logger.FromContext(ctx).Info("release asset updated done")
			}
		case err = <-createReleaseSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "create release tm subscribe error")
		case <-createReleaseDone:
			logger.FromContext(ctx).Info("create release done")
		case err = <-updateReleaseSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "update release tm subscribe error")
		case <-updateReleaseDone:
			logger.FromContext(ctx).Info("update release done")
		case err = <-deleteReleaseSubscribeErr:
			eventErrChan <- errors.WithMessage(err, "delete release tm subscribe error")
		case <-deleteReleaseDone:
			logger.FromContext(ctx).Info("delete release done")
		}
	}()

	// Start web server in a goroutine
	webErrChan := make(chan error, 1)
	go func() {
		// Configure git service
		server, err := internalapp.New(cmd, utils.Config{
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
			webErrChan <- err
			return
		}

		// Ensure cache directory exists
		os.MkdirAll(viper.GetString("GIT_DIR"), os.ModePerm)

		// Configure git server
		if err = server.Setup(); err != nil {
			webErrChan <- err
			return
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

		webErrChan <- http.ListenAndServe(fmt.Sprintf(":%v", viper.GetString("WEB_SERVER_PORT")), handler)
	}()

	// Wait for either service to error out
	select {
	case err := <-eventErrChan:
		return errors.Wrap(err, "event processor error")
	case err := <-webErrChan:
		return errors.Wrap(err, "web server error")
	}
}
