package main

import (
	"context"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/handler"
	"github.com/gitopia/gitopia-ipfs-bridge/app/consumer"
	"github.com/gitopia/gitopia-ipfs-bridge/app/tm"
	"github.com/gitopia/gitopia-ipfs-bridge/logger"
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
	return cmdRun
}

func run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	gc, err := app.NewGitopiaClient(context.Background(), viper.GetString("key_name"))
	if err != nil {
		return errors.WithMessage(err, "gitopia client error")
	}

	ftmc, err := tm.NewTmClient()
	if err != nil {
		return errors.WithMessage(err, "tm error")
	}

	mtmc, err := tm.NewTmClient()
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

	forkHandler := handler.NewInvokeForkRepositoryEventHandler(gc, ftmc, fcc)
	mergeHandler := handler.NewInvokeMergePullRequestEventHandler(gc, mtmc, mcc)

	_, forkBackfillErr := forkHandler.BackfillMissedEvents(ctx)
	_, mergeBackfillErr := mergeHandler.BackfillMissedEvents(ctx)

	invokeForkRepositoryQuery := "tm.event='Tx' AND message.action='InvokeForkRepository'"
	InvokeMergePullRequestQuery := "tm.event='Tx' AND message.action='InvokeMergePullRequest'"

	forkDone, forkSubscribeErr := ftmc.Subscribe(ctx, invokeForkRepositoryQuery, forkHandler.Handle)
	mergeDone, mergeSubscribeErr := mtmc.Subscribe(ctx, InvokeMergePullRequestQuery, mergeHandler.Handle)

	// wait for error from all the concurrent event processors
	select {
	case err = <-forkBackfillErr:
		return errors.WithMessage(err, "fork backfill error")
	case err = <-forkSubscribeErr:
		return errors.WithMessage(err, "fork tm subscribe error")
	case <-forkDone:
		logger.FromContext(ctx).Info("fork done")
	case err = <-mergeBackfillErr:
		return errors.WithMessage(err, "merge backfill error")
	case err = <-mergeSubscribeErr:
		return errors.WithMessage(err, "merge tm subscribe error")
	case <-mergeDone:
		logger.FromContext(ctx).Info("merge done")
	}

	return nil
}
