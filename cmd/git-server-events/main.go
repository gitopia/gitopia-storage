package main

import (
	"context"
	"log"
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/gitopia-ipfs-bridge/logger"
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

	ctx := logger.InitLogger(context.Background())
	app.InitGitopiaClientConfig()
	ctx = context.WithValue(ctx, client.ClientContextKey, &client.Context{})

	rc := NewRootCmd()
	err = rc.ExecuteContext(ctx)
	if err != nil {
		logger.FromContext(ctx).WithError(err).Error("app error")
	}
}
