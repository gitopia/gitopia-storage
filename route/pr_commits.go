package route

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gitopia/git-server/utils"
)

func PullRequestCommitsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/pull/commits") {
		defer r.Body.Close()

		var body utils.PullRequestCommitsPostBody

		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		quarantineRepoPath, err := utils.CreateQuarantineRepo(body.BaseRepositoryID, body.HeadRepositoryID, body.BaseBranch, body.HeadBranch)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(quarantineRepoPath)

		var cmd *exec.Cmd
		if len(body.BaseCommitSha) == 0 {
			baseBranch := fmt.Sprintf("origin/%s", body.BaseBranch)
			headBranch := fmt.Sprintf("head_repo/%s", body.HeadBranch)
			cmd = exec.Command("git", "rev-list", headBranch, fmt.Sprintf("^%s", baseBranch))
		} else {
			cmd = exec.Command("git", "rev-list", body.HeadCommitSha, fmt.Sprintf("^%s", body.BaseCommitSha))
		}

		cmd.Dir = quarantineRepoPath
		out, err := cmd.Output()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		logs := string(out)
		logs = strings.TrimSpace(logs)
		if len(logs) == 0 {
			json.NewEncoder(w).Encode([]string{})
			return
		}

		commitShas := strings.Split(logs, "\n")
		json.NewEncoder(w).Encode(commitShas)
	}
	return
}
