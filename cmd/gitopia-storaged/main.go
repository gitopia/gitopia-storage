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
	viper.AddConfigPath(".")
	if os.Getenv("ENV") == "PRODUCTION" {
		viper.SetConfigName("config_prod")
	} else if os.Getenv("ENV") == "DEVELOPMENT" {
		viper.SetConfigName("config_dev")
	} else {
		viper.SetConfigName("config_local")
	}

	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal(err)
	}

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
