package main

import (
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
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
			registry := codectypes.NewInterfaceRegistry()
			cryptocodec.RegisterInterfaces(registry)
			kb, err := cmd.Flags().GetString(flags.FlagKeyringBackend)
			if err != nil {
				return err
			}
			kd, err := cmd.Flags().GetString(flags.FlagKeyringDir)
			if err != nil {
				return err
			}
			k, err := sdkkeyring.New(AppName, kb, kd, os.Stdin, codec.NewProtoCodec(registry))
			if err != nil {
				return err
			}

			initClientCtx := client.Context{}.WithInput(os.Stdin).WithKeyring(k)

			return client.SetCmdClientContext(cmd, initClientCtx)
		},
	}
	rootCmd.AddCommand(NewRunCmd())
	rootCmd.AddCommand(keys.Commands("."))
	return rootCmd
}
