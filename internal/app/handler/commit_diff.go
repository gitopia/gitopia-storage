package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"

	"github.com/gitopia/git-server/utils"
	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/object"
	"github.com/spf13/viper"
)

// Calculate commit diff
func CommitDiffHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body utils.DiffRequestBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		RepoPath := path.Join(viper.GetString("GIT_DIR"), fmt.Sprintf("%d.git", body.RepositoryID))
		repo, err := git.PlainOpen(RepoPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if body.CommitSha == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		PreviousCommitHash := plumbing.NewHash(body.PreviousCommitSha)
		CommitHash := plumbing.NewHash(body.CommitSha)

		var previousCommit, commit *object.Commit

		if body.PreviousCommitSha == "" {
			previousCommit = nil
		} else {
			previousCommit, err = object.GetCommit(repo.Storer, PreviousCommitHash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
		}

		commit, err = object.GetCommit(repo.Storer, CommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var previousTree, tree *object.Tree

		if previousCommit == nil {
			previousCommit, err = commit.Parent(0)
			if err != nil {
				previousTree = nil
			} else {
				previousTree, err = object.GetTree(repo.Storer, previousCommit.TreeHash)
				if err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
			}
		} else {
			previousTree, err = object.GetTree(repo.Storer, previousCommit.TreeHash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
		}

		tree, err = object.GetTree(repo.Storer, commit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var changes object.Changes
		changes, err = previousTree.Diff(tree)
		if err != nil {
			log.Printf("commit-diff: can't generate diff\n")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if body.OnlyStat {
			var addition, deletion int
			var fileNames []string

			patch, err := changes.Patch()
			if err != nil {
				log.Printf("commit-diff: can't generate diff stats")
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			stats := patch.Stats()
			for _, l := range stats {
				addition += l.Addition
				deletion += l.Deletion
				fileNames = append(fileNames, l.Name)

			}
			diffStat := utils.DiffStat{
				Addition: uint64(addition),
				Deletion: uint64(deletion),
			}
			DiffStatResponse := utils.DiffStatResponse{
				Stat:         diffStat,
				FilesChanged: uint64(changes.Len()),
				FileNames:    fileNames,
			}
			DiffStatResponseJson, _ := json.Marshal(DiffStatResponse)
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
