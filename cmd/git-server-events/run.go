package main

import (
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/app/consumer"
	"github.com/gitopia/git-server/handler"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
)

const (
	invokeForkRepositoryQuery      = "tm.event='Tx' AND message.action='InvokeForkRepository'"
	InvokeMergePullRequestQuery    = "tm.event='Tx' AND message.action='InvokeMergePullRequest'"
	InvokeDaoMergePullRequestQuery = "tm.event='Tx' AND message.action='InvokeDaoMergePullRequest'"
)

func NewRunCmd() *cobra.Command {
	var cmdRun = &cobra.Command{
		Use:           "run",
		Short:         "run git server event tasks",
		Long:          `executes git server events like fork repository, merge pull request, etc`,
		Args:          cobra.MinimumNArgs(0),
		RunE:          run,
		SilenceErrors: true,
	}
	AddGitopiaFlags(cmdRun)

	return cmdRun
}

func run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

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
	defer func() {
		gc.Close()
	}()
	gp := app.NewGitopiaProxy(gc)

	ftmc, err := gitopia.NewWSEvents(ctx, invokeForkRepositoryQuery)
	if err != nil {
		return errors.WithMessage(err, "tm error")
	}

	mtmc, err := gitopia.NewWSEvents(ctx, InvokeMergePullRequestQuery)
	if err != nil {
		return errors.WithMessage(err, "tm error")
	}

	dmtmc, err := gitopia.NewWSEvents(ctx, InvokeDaoMergePullRequestQuery)
	if err != nil {
		return errors.WithMessage(err, "tm error")
	}

	fcc, err := consumer.NewClient("invokeForkRepositoryEvent")
	if err != nil {
		return errors.WithMessage(err, "error creating consumer client")
	}

	mcc, err := consumer.NewClient("invokeMergePullRequestEvent")
	if err != nil {
		return errors.WithMessage(err, "error creating consumer client")
	}

	dmcc, err := consumer.NewClient("invokeDaoMergePullRequestEvent")
	if err != nil {
		return errors.WithMessage(err, "error creating consumer client")
	}

	forkHandler := handler.NewInvokeForkRepositoryEventHandler(gp, fcc)
	mergeHandler := handler.NewInvokeMergePullRequestEventHandler(gp, mcc)
	daoMergeHandler := handler.NewInvokeDaoMergePullRequestEventHandler(gp, dmcc)

	// _, forkBackfillErr := forkHandler.BackfillMissedEvents(ctx)
	// _, mergeBackfillErr := mergeHandler.BackfillMissedEvents(ctx)

	forkDone, forkSubscribeErr := ftmc.Subscribe(ctx, forkHandler.Handle)
	mergeDone, mergeSubscribeErr := mtmc.Subscribe(ctx, mergeHandler.Handle)
	daoMergeDone, daoMergeSubscribeErr := dmtmc.Subscribe(ctx, daoMergeHandler.Handle)

	// wait for error from all the concurrent event processors
	select {
	// case err = <-forkBackfillErr:
	// 	return errors.WithMessage(err, "fork backfill error")
	case err = <-forkSubscribeErr:
		return errors.WithMessage(err, "fork tm subscribe error")
	case <-forkDone:
		logger.FromContext(ctx).Info("fork done")
	// case err = <-mergeBackfillErr:
	// 	return errors.WithMessage(err, "merge backfill error")
	case err = <-mergeSubscribeErr:
		return errors.WithMessage(err, "merge tm subscribe error")
	case <-mergeDone:
		logger.FromContext(ctx).Info("merge done")
	case err = <-daoMergeSubscribeErr:
		return errors.WithMessage(err, "dao merge tm subscribe error")
	case <-daoMergeDone:
		logger.FromContext(ctx).Info("dao merge done")
	}

	return nil
}
