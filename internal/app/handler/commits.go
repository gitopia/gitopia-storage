package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/gitopia/gitopia-storage/utils"
	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/object"
	"github.com/spf13/viper"
)

// Repository Commits
func CommitsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body utils.CommitsRequestBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		blocks := strings.Split(r.URL.Path, "/")

		if len(blocks) == 3 {
			RepoPath := path.Join(viper.GetString("GIT_REPOS_DIR"), fmt.Sprintf("%d.git", body.RepositoryID))
			repo, err := git.PlainOpen(RepoPath)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}

			commitHash := plumbing.NewHash(blocks[2])
			commitObject, err := object.GetCommit(repo.Storer, commitHash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}

			commit, err := utils.GrabCommit(*commitObject)
			commitResponseJson, err := json.Marshal(commit)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write(commitResponseJson)
			return
		}

		if len(blocks) != 2 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		if body.InitCommitId == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		// cache repo
		cacheDir := viper.GetString("GIT_REPOS_DIR")
		if err := utils.CacheRepository(body.RepositoryID, cacheDir); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		repoPath := path.Join(cacheDir, fmt.Sprintf("%d.git", body.RepositoryID))
		repo, err := git.PlainOpen(repoPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		var commits []*utils.Commit
		var pageRes *utils.PageResponse

		if body.Path != "" {
			commitHash := plumbing.NewHash(body.InitCommitId)
			commit, err := object.GetCommit(repo.Storer, commitHash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			commitTree, err := commit.Tree()
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			_, err = commitTree.FindEntry(body.Path)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
		}

		pageRes, err = utils.PaginateCommitHistoryResponse(repoPath, repo, body.Pagination, 100, &body, func(commit utils.Commit) error {
			commits = append(commits, &commit)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}

		commitsResponse := utils.CommitsResponse{
			Commits:    commits,
			Pagination: pageRes,
		}
		commitsResponseJson, err := json.Marshal(commitsResponse)
		w.Write(commitsResponseJson)
		return
	}
}
