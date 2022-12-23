package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/gitopia/git-server/logger"
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

	rc := NewRootCmd()
	err = rc.ExecuteContext(ctx)
	if err != nil {
		logger.FromContext(ctx).WithError(err).Error("app error")
	}
}
