package main

import (
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func AddGitopiaFlags(cmd *cobra.Command) {
	cmd.Flags().String(flags.FlagFrom, "", "Name or address of private key with which to sign")
	cmd.Flags().String(flags.FlagKeyringBackend, flags.DefaultKeyringBackend, "Select keyring's backend (os|file|kwallet|pass|test|memory)")
}

func GetClientContext(cmd *cobra.Command) (client.Context, error) {
	clientCtx := client.GetClientContextFromCmd(cmd)
	clientCtx = clientCtx.WithChainID(viper.GetString("chain_id"))
	clientCtx = clientCtx.WithNodeURI(viper.GetString("tm_addr"))
	c, err := client.NewClientFromNode(clientCtx.NodeURI)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error creatig tm client")
	}
	clientCtx = clientCtx.WithClient(c)
	clientCtx = clientCtx.WithBroadcastMode(flags.BroadcastSync)
	clientCtx = clientCtx.WithSkipConfirmation(true)

	backend, err := cmd.Flags().GetString(flags.FlagKeyringBackend)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error parsing keyring backend")
	}
	kr, err := client.NewKeyringFromBackend(clientCtx, backend)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error creating keyring backend")
	}
	clientCtx = clientCtx.WithKeyring(kr)
	clientCtx = clientCtx.WithKeyringDir(viper.GetString("keyring_dir"))

	from, err := cmd.Flags().GetString(flags.FlagFrom)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error parsing from flag")
	}
	fromAddr, fromName, _, err := client.GetFromFields(clientCtx, kr, from)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error parsing from Addr")
	}

	clientCtx = clientCtx.WithFrom(from).WithFromAddress(fromAddr).WithFromName(fromName)
	return clientCtx, nil
}
