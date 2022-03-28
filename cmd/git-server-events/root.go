package main

import (
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/spf13/cobra"
)

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:               "git-server-events",
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			initClientCtx := client.Context{}.
				WithInput(os.Stdin)
			return client.SetCmdClientContext(cmd, initClientCtx)
		},
	}
	rootCmd.AddCommand(NewRunCmd())
	rootCmd.AddCommand(keys.Commands("."))
	return rootCmd
}
