package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/spf13/cobra"
)

func NewGetIpfsClusterPeerAddressesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-ipfs-cluster-peer-addresses",
		Short: "Get IPFS cluster peer addresses of all active storage providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			clientCtx := client.GetClientContextFromCmd(cmd)
			txf, err := tx.NewFactoryCLI(clientCtx, cmd.Flags())
			if err != nil {
				return fmt.Errorf("error initializing tx factory: %w", err)
			}
			txf = txf.WithGasAdjustment(app.GAS_ADJUSTMENT)

			gc, err := gitopia.NewClient(ctx, clientCtx, txf)
			if err != nil {
				return fmt.Errorf("gitopia client error: %w", err)
			}
			defer gc.Close()

			gp := app.NewGitopiaProxy(gc)

			providers, err := gp.ActiveProviders(ctx)
			if err != nil {
				return fmt.Errorf("failed to get active providers: %w", err)
			}

			if len(providers) == 0 {
				fmt.Fprintln(os.Stdout, "no active providers")
				return nil
			}

			var addresses []string
			for _, p := range providers {
				addresses = append(addresses, p.IpfsClusterPeerMultiaddr)
			}
			fmt.Fprintln(os.Stdout, strings.Join(addresses, ","))
			return nil
		},
	}
}
