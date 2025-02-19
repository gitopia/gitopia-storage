package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia-go/logger"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/viper"
)

const (
	AccountAddressPrefix = "gitopia"
	AccountPubKeyPrefix  = AccountAddressPrefix + sdk.PrefixPublic
	AppName              = "git-server"
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

	log.Printf("ENV: %s\n", os.Getenv("ENV"))
	fmt.Println(viper.AllSettings())
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

	// Execute root command
	rc := NewRootCmd()
	if err := rc.ExecuteContext(ctx); err != nil {
		log.Fatalf("Error executing root command: %v", err)
	}
}
