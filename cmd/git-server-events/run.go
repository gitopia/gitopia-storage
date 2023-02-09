package main

import (
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/app/consumer"
	"github.com/gitopia/git-server/handler"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
)

const (
	invokeForkRepositoryQuery   = "tm.event='Tx' AND message.action='InvokeForkRepository'"
	InvokeMergePullRequestQuery = "tm.event='Tx' AND message.action='InvokeMergePullRequest'"
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

	clientCtx, err := gitopia.GetClientContext(cmd)
	if err != nil {
		return errors.Wrap(err, "error initializing client context")
	}
	txf := tx.NewFactoryCLI(clientCtx, cmd.Flags())

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

	fcc, err := consumer.NewClient("invokeForkRepositoryEvent")
	if err != nil {
		return errors.WithMessage(err, "error creating consumer client")
	}

	mcc, err := consumer.NewClient("invokeMergePullRequestEvent")
	if err != nil {
		return errors.WithMessage(err, "error creating consumer client")
	}

	forkHandler := handler.NewInvokeForkRepositoryEventHandler(gp, fcc)
	mergeHandler := handler.NewInvokeMergePullRequestEventHandler(gp, mcc)

	// _, forkBackfillErr := forkHandler.BackfillMissedEvents(ctx)
	// _, mergeBackfillErr := mergeHandler.BackfillMissedEvents(ctx)

	forkDone, forkSubscribeErr := ftmc.Subscribe(ctx, forkHandler.Handle)
	mergeDone, mergeSubscribeErr := mtmc.Subscribe(ctx, mergeHandler.Handle)

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
	}

	return nil
}
