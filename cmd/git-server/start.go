package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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
	"github.com/rs/cors"
)

const (
	invokeForkRepositoryQuery      = "tm.event='Tx' AND message.action='InvokeForkRepository'"
	InvokeMergePullRequestQuery    = "tm.event='Tx' AND message.action='InvokeMergePullRequest'"
	InvokeDaoMergePullRequestQuery = "tm.event='Tx' AND message.action='InvokeDaoMergePullRequest'"
	challengeCreatedQuery          = "tm.event='EventChallengeCreated'"
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

	// Initialize Gitopia client configuration
	gitopia.WithAppName(viper.GetString("APP_NAME"))
	gitopia.WithChainId(viper.GetString("CHAIN_ID"))
	gitopia.WithFeeGranter(viper.GetString("FEE_GRANTER"))
	gitopia.WithGasPrices(viper.GetString("GAS_PRICES"))
	gitopia.WithGitopiaAddr(viper.GetString("GITOPIA_ADDR"))
	gitopia.WithTmAddr(viper.GetString("TM_ADDR"))
	gitopia.WithWorkingDir(viper.GetString("WORKING_DIR"))

	// Start event processor in a goroutine
	eventErrChan := make(chan error, 1)
	go func() {
		clientCtx, err := gitopia.GetClientContext(AppName)
		if err != nil {
			eventErrChan <- errors.Wrap(err, "error initializing client context")
			return
		}
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

		ftmc, err := gitopia.NewWSEvents(ctx, invokeForkRepositoryQuery)
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

		ctmc, err := gitopia.NewWSEvents(ctx, challengeCreatedQuery)
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
		mergeHandler := handler.NewInvokeMergePullRequestEventHandler(gp, mcc)
		daoMergeHandler := handler.NewInvokeDaoMergePullRequestEventHandler(gp, dmcc)
		challengeHandler := handler.NewChallengeEventHandler(gp)

		forkDone, forkSubscribeErr := ftmc.Subscribe(ctx, forkHandler.Handle)
		mergeDone, mergeSubscribeErr := mtmc.Subscribe(ctx, mergeHandler.Handle)
		daoMergeDone, daoMergeSubscribeErr := dmtmc.Subscribe(ctx, daoMergeHandler.Handle)
		challengeDone, challengeSubscribeErr := ctmc.Subscribe(ctx, challengeHandler.Handle)

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
