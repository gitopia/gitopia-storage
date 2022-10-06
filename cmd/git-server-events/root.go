package main

import (
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/spf13/cobra"
)

const (
	AppName = "git-server-events"
)

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:               "git-server-events",
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			version.Name = "git-server-events"
			registry := codectypes.NewInterfaceRegistry()
			cryptocodec.RegisterInterfaces(registry)
			marshaler := codec.NewProtoCodec(registry)

			initClientCtx := client.GetClientContextFromCmd(cmd).
				WithCodec(marshaler).
				WithInterfaceRegistry(registry).
				WithInput(os.Stdin)

			return client.SetCmdClientContext(cmd, initClientCtx)
		},
	}
	rootCmd.AddCommand(NewRunCmd())
	rootCmd.AddCommand(keys.Commands("."))
	return rootCmd
}
