package main

import (
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/gitopia/gitopia-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:               AppName,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return gitopia.CommandInit(cmd, AppName)
		},
	}
	rootCmd.AddCommand(NewStartCmd())
	rootCmd.AddCommand(keys.Commands(viper.GetString("WORKING_DIR")))
	return rootCmd
}
