package route

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/gitopia/git-server/utils"
	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/object"
	"github.com/spf13/viper"
)

func PullDiffHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/pull/diff") {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body utils.PullDiffRequestBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		headRepositoryPath := path.Join(viper.GetString("GIT_DIR"), fmt.Sprintf("%d.git", body.HeadRepositoryID))
		headRepository, err := git.PlainOpen(headRepositoryPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		baseRepositoryPath := path.Join(viper.GetString("GIT_DIR"), fmt.Sprintf("%d.git", body.BaseRepositoryID))
		baseRepository, err := git.PlainOpen(baseRepositoryPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if body.HeadCommitSha == "" || body.BaseCommitSha == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		headCommitHash := plumbing.NewHash(body.HeadCommitSha)
		baseCommitHash := plumbing.NewHash(body.BaseCommitSha)

		var headCommit, baseCommit *object.Commit

		headCommit, err = object.GetCommit(headRepository.Storer, headCommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		baseCommit, err = object.GetCommit(baseRepository.Storer, baseCommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var headTree, baseTree *object.Tree

		headTree, err = object.GetTree(headRepository.Storer, headCommit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		baseTree, err = object.GetTree(baseRepository.Storer, baseCommit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var changes object.Changes
		changes, err = baseTree.Diff(headTree)
		if err != nil {
			log.Printf("commit-diff: can't generate diff")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if body.OnlyStat {
			patch, err := changes.Patch()
			if err != nil {
				log.Printf("commit-diff: can't generate diff stats")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			addition := 0
			deletion := 0
			stats := patch.Stats()
			for _, l := range stats {
				addition += l.Addition
				deletion += l.Deletion
			}
			diffStat := utils.DiffStat{
				Addition: uint64(addition),
				Deletion: uint64(deletion),
			}
			DiffStatResponse := utils.DiffStatResponse{
				Stat:         diffStat,
				FilesChanged: uint64(changes.Len()),
			}
			DiffStatResponseJson, err := json.Marshal(DiffStatResponse)
			w.Write(DiffStatResponseJson)
			return
		}

		var diffs []*utils.Diff
		pageRes, err := utils.PaginateDiffResponse(changes, body.Pagination, 10, func(diff utils.Diff) error {
			diffs = append(diffs, &diff)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}

		diffResponse := utils.DiffResponse{
			Diff:       diffs,
			Pagination: pageRes,
		}
		diffResponseJson, err := json.Marshal(diffResponse)
		w.Write(diffResponseJson)
		return
	}
}
