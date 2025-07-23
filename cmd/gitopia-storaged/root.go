package main

import (
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia/v6/x/storage/client/cli"
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

	registerStorageProviderCmd := cli.CmdRegisterStorageProvider()
	updateStorageProviderCmd := cli.CmdUpdateStorageProvider()
	withdrawRewardsCmd := cli.CmdWithdrawProviderRewards()
	increaseStakeCmd := cli.CmdIncreaseStake()
	decreaseStakeCmd := cli.CmdDecreaseStake()
	completeDecreaseStakeCmd := cli.CmdCompleteDecreaseStake()
	reactivateProviderCmd := cli.CmdReactivateProvider()
	unregisterProviderCmd := cli.CmdUnregisterProvider()
	completeUnstakeCmd := cli.CmdCompleteUnstake()

	AddGitopiaFlags(registerStorageProviderCmd)
	AddGitopiaFlags(updateStorageProviderCmd)
	AddGitopiaFlags(withdrawRewardsCmd)
	AddGitopiaFlags(increaseStakeCmd)
	AddGitopiaFlags(decreaseStakeCmd)
	AddGitopiaFlags(completeDecreaseStakeCmd)
	AddGitopiaFlags(reactivateProviderCmd)
	AddGitopiaFlags(unregisterProviderCmd)
	AddGitopiaFlags(completeUnstakeCmd)

	rootCmd.AddCommand(NewStartCmd())
	rootCmd.AddCommand(keys.Commands(viper.GetString("WORKING_DIR")))
	rootCmd.AddCommand(registerStorageProviderCmd)
	rootCmd.AddCommand(updateStorageProviderCmd)
	rootCmd.AddCommand(withdrawRewardsCmd)
	rootCmd.AddCommand(increaseStakeCmd)
	rootCmd.AddCommand(decreaseStakeCmd)
	rootCmd.AddCommand(completeDecreaseStakeCmd)
	rootCmd.AddCommand(reactivateProviderCmd)
	rootCmd.AddCommand(unregisterProviderCmd)
	rootCmd.AddCommand(completeUnstakeCmd)
	rootCmd.AddCommand(NewGetIpfsClusterPeerAddressesCmd())
	return rootCmd
}
