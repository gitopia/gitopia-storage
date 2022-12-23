package main

import (
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/std"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/gitopia/git-server/app"
	gtypes "github.com/gitopia/gitopia/x/gitopia/types"
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
			return commandInit(cmd)
		},
	}
	rootCmd.AddCommand(NewRunCmd())
	rootCmd.AddCommand(keys.Commands("."))
	return rootCmd
}

func commandInit(cmd *cobra.Command) error {
	version.Name = AppName // os keyring service name is same as version name
	app.InitGitopiaClientConfig()

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	std.RegisterInterfaces(interfaceRegistry)
	cryptocodec.RegisterInterfaces(interfaceRegistry)
	authtypes.RegisterInterfaces(interfaceRegistry)
	interfaceRegistry.RegisterInterface(
		"cosmos.auth.v1beta1.AccountI",
		(*authtypes.AccountI)(nil),
		&authtypes.BaseAccount{},
		&authtypes.ModuleAccount{},
	)
	gtypes.RegisterInterfaces(interfaceRegistry)

	marshaler := codec.NewProtoCodec(interfaceRegistry)
	txCfg := tx.NewTxConfig(marshaler, tx.DefaultSignModes)
	clientCtx := client.GetClientContextFromCmd(cmd).
		WithCodec(marshaler).
		WithInterfaceRegistry(interfaceRegistry).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithTxConfig(txCfg).
		WithInput(os.Stdin)
	// sets global flags for keys subcommand
	return client.SetCmdClientContext(cmd, clientCtx)
}
