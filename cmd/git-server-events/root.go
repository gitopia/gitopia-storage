package main

import (

	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/gitopia/gitopia-go"
	"github.com/spf13/cobra"
)

const (
	AppName = "git-server-events"
)

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:               AppName,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return gitopia.CommandInit(cmd, AppName)
		},
	}
	rootCmd.AddCommand(NewRunCmd())
	rootCmd.AddCommand(keys.Commands("."))
	return rootCmd
}