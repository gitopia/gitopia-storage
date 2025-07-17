package app

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/gitopia/gitopia-storage/utils"
)

var reSlashDedup = regexp.MustCompile(`//+`)

func fail500(w http.ResponseWriter, context string, err error) {
	http.Error(w, "Internal server error", http.StatusInternalServerError)
	utils.LogError(context, err)
}

func packLine(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(s)+4, s)
	return err
}

func packFlush(w io.Writer) error {
	_, err := fmt.Fprint(w, "0000")
	return err
}

func subCommand(rpc string) string {
	return strings.TrimPrefix(rpc, "git-")
}

func getNamespaceAndRepo(input string) (string, string) {
	if input == "" || input == "/" {
		return "", ""
	}

	input = reSlashDedup.ReplaceAllString(input, "/")
	if input[0] == '/' {
		input = input[1:]
	}

	blocks := strings.Split(input, "/")
	num := len(blocks)

	if num < 2 {
		return "", blocks[0]
	}

	return strings.Join(blocks[:num-1], "/"), blocks[num-1]
}

func initRepo(name string, config *utils.Config) error {
	fullPath := path.Join(config.Dir, name)

	if err := exec.Command(config.GitPath, "init", "--bare", fullPath).Run(); err != nil {
		return err
	}

	if config.AutoHooks && config.Hooks != nil {
		return config.Hooks.SetupInDir(fullPath)
	}

	return nil
}

func newWriteFlusher(w http.ResponseWriter) io.Writer {
	return &writeFlusher{w.(http.Flusher), w}
}

type writeFlusher struct {
	flusher http.Flusher
	writer  io.Writer
}

func (w *writeFlusher) Write(p []byte) (int, error) {
	defer w.flusher.Flush()
	return w.writer.Write(p)
}
