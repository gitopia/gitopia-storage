package pr

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gitopia/git-server/utils"
	"github.com/gitopia/gitopia/v5/x/gitopia/types"
	"github.com/spf13/viper"
)

type pullRequestCheckResponseData struct {
	IsMergeable bool `json:"is_mergeable"`
}

type pullRequestCheckResponse struct {
	Data  pullRequestCheckResponseData `json:"data"`
	Error string                       `json:"error"`
}

func PullRequestCheckHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" {
		defer r.Body.Close()

		var body utils.PullRequestMergePostBody
		var resp pullRequestCheckResponse

		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&body)
		if err != nil {
			resp.Error = http.StatusText(http.StatusBadRequest)
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusBadRequest)
			return
		}

		// check if  base repository is cached
		cacheDir := viper.GetString("GIT_DIR")
		isCached, err := utils.IsRepoCached(body.BaseRepositoryID, cacheDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !isCached {
			err = utils.DownloadRepo(body.BaseRepositoryID, cacheDir)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		if body.HeadRepositoryID != body.BaseRepositoryID {
			// check if head repository is cached
			isCached, err := utils.IsRepoCached(body.HeadRepositoryID, cacheDir)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !isCached {
				err = utils.DownloadRepo(body.HeadRepositoryID, cacheDir)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}

		quarantineRepoPath, err := utils.CreateQuarantineRepo(body.BaseRepositoryID, body.HeadRepositoryID, body.BaseBranch, body.HeadBranch)
		if err != nil {
			resp.Error = http.StatusText(http.StatusInternalServerError)
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(quarantineRepoPath)

		trackingBranch := "tracking"

		// Read base branch index
		cmd := exec.Command("git", "read-tree", "HEAD")
		cmd.Dir = quarantineRepoPath
		out, err := cmd.Output()
		if err != nil {
			log.Printf("git read-tree HEAD: %v\n%s\n", err, string(out))
			resp.Error = fmt.Sprintf("Unable to read base branch in to the index: %v\n%s\n", err, string(out))
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}

		commitTimeStr := time.Now().Format(time.RFC3339)

		// Because this may call hooks we should pass in the environment
		env := append(os.Environ(),
			"GIT_AUTHOR_NAME="+body.UserName,
			"GIT_AUTHOR_EMAIL="+body.UserEmail,
			"GIT_AUTHOR_DATE="+commitTimeStr,
			"GIT_COMMITTER_NAME="+body.UserName,
			"GIT_COMMITTER_EMAIL="+body.UserEmail,
			"GIT_COMMITTER_DATE="+commitTimeStr,
		)

		cmd = exec.Command("git", "merge", "--no-ff", "--no-commit", trackingBranch)
		cmd.Env = env
		prHead := types.PullRequestHead{
			RepositoryId: body.HeadRepositoryID,
			Branch:       body.HeadBranch,
		}
		prBase := types.PullRequestBase{
			RepositoryId: body.BaseRepositoryID,
			Branch:       body.BaseBranch,
		}
		if err := utils.RunMergeCommand(prHead, prBase, cmd, quarantineRepoPath); err != nil {
			log.Printf("Unable to merge tracking into base: %v\n", err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}

		resp.Data.IsMergeable = true
		json.NewEncoder(w).Encode(resp)
	}
	return
}
