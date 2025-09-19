package main

import (
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"
)

func AddGitopiaFlags(cmd *cobra.Command) {
	cmd.Flags().String(flags.FlagFrom, "", "Name or address of private key with which to sign")
	cmd.Flags().String(flags.FlagKeyringBackend, flags.DefaultKeyringBackend, "Select keyring's backend (os|file|kwallet|pass|test|memory)")
	cmd.Flags().String(flags.FlagFees, "", "Fees to pay along with transaction; eg: 10ulore")
	
	// Optional monitoring flags
	cmd.Flags().Bool("enable-pprof", true, "Enable pprof server for performance monitoring")
	cmd.Flags().Int("pprof-port", 6060, "Port for pprof server")
	cmd.Flags().Bool("enable-memory-monitor", true, "Enable periodic memory usage logging")
	cmd.Flags().Duration("memory-monitor-interval", 300000000000, "Interval for memory monitoring (default: 5m)")
}
