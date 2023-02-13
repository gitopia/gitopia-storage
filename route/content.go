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

// Repository Content
func ContentHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/content") {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body utils.ContentRequestBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		blocks := strings.Split(r.URL.Path, "/")

		if len(blocks) != 2 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		RepoPath := path.Join(viper.GetString("GIT_DIR"), fmt.Sprintf("%d.git", body.RepositoryID))
		repo, err := git.PlainOpen(RepoPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if body.RefId == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		CommitHash := plumbing.NewHash(body.RefId)

		commit, err := object.GetCommit(repo.Storer, CommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		tree, err := object.GetTree(repo.Storer, commit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if body.Path != "" {
			treeEntry, err := tree.FindEntry(body.Path)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if treeEntry.Mode.IsFile() {
				var fileContent []*utils.Content
				blob, err := object.GetBlob(repo.Storer, treeEntry.Hash)
				if err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				fc, err := utils.GrabFileContent(*blob, *treeEntry, body.Path, body.NoRestriction)
				if err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				fileContent = append(fileContent, fc)

				if body.IncludeLastCommit {
					pathCommitId, err := utils.LastCommitForPath(RepoPath, body.RefId, fileContent[0].Path)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					pathCommitHash := plumbing.NewHash(pathCommitId)
					pathCommitObject, err := object.GetCommit(repo.Storer, pathCommitHash)
					if err != nil {
						http.Error(w, err.Error(), http.StatusNotFound)
						return
					}
					fileContent[0].LastCommit, err = utils.GrabCommit(*pathCommitObject)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
				}

				contentResponse := utils.ContentResponse{
					Content: fileContent,
				}
				contentResponseJson, err := json.Marshal(contentResponse)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
				w.Write(contentResponseJson)
				return
			}
			tree, err = object.GetTree(repo.Storer, treeEntry.Hash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
		}

		var treeContents []*utils.Content
		pageRes, err := utils.PaginateTreeContentResponse(tree, body.Pagination, 100, body.Path, func(treeContent utils.Content) error {
			treeContents = append(treeContents, &treeContent)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if body.IncludeLastCommit {
			for i := range treeContents {
				pathCommitId, err := utils.LastCommitForPath(RepoPath, body.RefId, treeContents[i].Path)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				pathCommitHash := plumbing.NewHash(pathCommitId)
				pathCommitObject, err := object.GetCommit(repo.Storer, pathCommitHash)
				if err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				treeContents[i].LastCommit, err = utils.GrabCommit(*pathCommitObject)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
		}

		var sortedTREEContents []*utils.Content
		var sortedBLOBContents []*utils.Content
		for _, tc := range treeContents {
			if tc.Type == "TREE" {
				sortedTREEContents = append(sortedTREEContents, tc)
			} else {
				sortedBLOBContents = append(sortedBLOBContents, tc)
			}
		}
		sortedTreeContents := append(sortedTREEContents, sortedBLOBContents...)

		contentResponse := utils.ContentResponse{
			Content:    sortedTreeContents,
			Pagination: pageRes,
		}
		contentResponseJson, err := json.Marshal(contentResponse)
		w.Write(contentResponseJson)
		return
	}
}
