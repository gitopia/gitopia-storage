package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/spf13/viper"
)

func main() {
	viper.AddConfigPath(".")
	if os.Getenv("ENV") == "PRODUCTION" {
		viper.SetConfigName("config_prod")
	} else if os.Getenv("ENV") == "DEVELOPMENT" {
		viper.SetConfigName("config_dev")
	} else {
		viper.SetConfigName("config_local")
	}
	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("ENV: %s\n", os.Getenv("ENV"))
	fmt.Println(viper.AllSettings())

	ctx := logger.InitLogger(context.Background(), AppName)
	ctx = context.WithValue(ctx, client.ClientContextKey, &client.Context{})

	gitopia.WithAppName(viper.GetString("APP_NAME"))
	gitopia.WithChainId(viper.GetString("CHAIN_ID"))
	gitopia.WithFeeGranter(viper.GetString("FEE_GRANTER"))
	gitopia.WithGasPrices(viper.GetString("GAS_PRICES"))
	gitopia.WithGitopiaAddr(viper.GetString("GITOPIA_ADDR"))
	gitopia.WithTmAddr(viper.GetString("TM_ADDR"))
	gitopia.WithWorkingDir(viper.GetString("WORKING_DIR"))

	rc := NewRootCmd()
	err = rc.ExecuteContext(ctx)
	if err != nil {
		logger.FromContext(ctx).WithError(err).Error("app error")
	}
}
