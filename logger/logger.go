package logger

import (
	"context"
	"log"
	"os"

	"github.com/sirupsen/logrus"
)

const LOG_FILE = "./gitopia-git-server-events.log"
const LOG_FILE_PERM = 0777

type logctx struct{}

func InitLogger(ctx context.Context) context.Context {
	logger := logrus.New()
	/* err := os.MkdirAll(LOG_FILE, LOG_FILE_PERM)
	if err != nil {
		log.Fatalln("error creating log dir: ", err.Error())
	} */

	file, err := os.OpenFile(LOG_FILE, os.O_CREATE|os.O_WRONLY|os.O_APPEND, LOG_FILE_PERM)
	if err != nil {
		log.Fatalln("error opening log file: ", err.Error())
	}
	logger.SetOutput(file)
	return context.WithValue(ctx, logctx{}, logger)
}

func FromContext(ctx context.Context) *logrus.Logger {
	return ctx.Value(logctx{}).(*logrus.Logger)
}
