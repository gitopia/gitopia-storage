package app_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"

	"github.com/gitopia/gitopia-storage/internal/app"
	mocks "github.com/gitopia/gitopia-storage/mocks/github.com/gitopia/gitopia-storage/internal_/app"
	"github.com/gitopia/gitopia-storage/utils"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func SetupViper() {
	viper.AddConfigPath("../..")
	viper.SetConfigName("config_local")

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal(err)
	}
}

func TestGETInfoRefs(t *testing.T) {
	t.Run("test git server's get info-ref", func(t *testing.T) {
		mockService := mocks.NewMockQueryService(t)

		mockService.On("")

		SetupViper()
		request, _ := http.NewRequest(http.MethodGet, "/0.git/info/refs?service=git-upload-pack", bytes.NewBuffer([]byte{}))
		response := httptest.NewRecorder()

		s, err := app.New(nil, utils.Config{
			Dir:        "test",
			AutoCreate: true,
			Auth:       true,
			AutoHooks:  true,
			Hooks: &utils.HookScripts{
				PreReceive:  "gitopia-pre-receive",
				PostReceive: "gitopia-post-receive",
			},
		}, nil)
		require.NoError(t, err)

		req := &app.Request{
			Request:  request,
			RepoName: "0.git",
			RepoPath: path.Join(s.Config.Dir, "0.git"),
		}

		s.GetInfoRefs("", response, req)

		got := response.Body.String()
		want := "20"

		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
