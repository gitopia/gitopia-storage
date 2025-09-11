package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/viper"
)

const (
	AccountAddressPrefix = "gitopia"
	AccountPubKeyPrefix  = AccountAddressPrefix + sdk.PrefixPublic
	AppName              = "gitopia-storage"
)

var (
	env []string
)

func initConfig() {
	// Set config paths - system config first, then current directory
	viper.AddConfigPath("/etc/gitopia-storage")
	viper.AddConfigPath(".")
	
	// Always use config.toml as the base configuration
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	
	// Enable automatic environment variable binding
	viper.AutomaticEnv()
	
	// Read the base config file
	err := viper.ReadInConfig()
	if err != nil {
		log.Fatalf("Error reading base config file: %v", err)
	}
	
	// Try to read local override config (config.local.toml) if it exists
	// This allows developers to override settings without modifying the base config
	viper.SetConfigName("config.local")
	localConfigErr := viper.MergeInConfig()
	if localConfigErr == nil {
		log.Println("Loaded local configuration overrides from config.local.toml")
	}
	
	// Build environment variables for child processes
	for _, key := range viper.AllKeys() {
		env = append(env, strings.ToUpper(key)+"="+viper.GetString(key))
	}
}

func main() {
	initConfig()

	conf := sdk.GetConfig()
	conf.SetBech32PrefixForAccount(AccountAddressPrefix, AccountPubKeyPrefix)

	// Initialize context with logger
	ctx := logger.InitLogger(context.Background(), AppName)
	ctx = context.WithValue(ctx, client.ClientContextKey, &client.Context{})

	logger.FromContext(ctx).SetOutput(os.Stdout)

	// Initialize Gitopia client configuration
	gitopia.WithAppName(viper.GetString("APP_NAME"))
	gitopia.WithChainId(viper.GetString("CHAIN_ID"))
	gitopia.WithGasPrices(viper.GetString("GAS_PRICES"))
	gitopia.WithGitopiaAddr(viper.GetString("GITOPIA_ADDR"))
	gitopia.WithTmAddr(viper.GetString("TM_ADDR"))
	gitopia.WithWorkingDir(viper.GetString("WORKING_DIR"))

	// Execute root command
	rc := NewRootCmd()
	if err := rc.ExecuteContext(ctx); err != nil {
		log.Fatalf("Error executing root command: %v", err)
	}
}
