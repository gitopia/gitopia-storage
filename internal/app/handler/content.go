package handler

import (
	"encoding/base64"
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

// Repository Content
func ContentHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body utils.ContentRequestBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		if body.From > body.To {
			log.Println("from > to")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		blocks := strings.Split(r.URL.Path, "/")

		if len(blocks) != 2 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		// cache repo
		utils.LockRepository(body.RepositoryID)
		defer utils.UnlockRepository(body.RepositoryID)

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

				if body.From != 0 && body.To != 0 {
					decodedFc, err := base64.StdEncoding.DecodeString(fc.Content)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}

					splits := strings.Split(string(decodedFc), "\n")
					if body.To > uint64(len(splits)) {
						body.To = (uint64)(len(splits))
					}
					rangeFc := strings.Join(splits[body.From-1:body.To-1], "\n")
					fc.Content = base64.StdEncoding.EncodeToString([]byte(rangeFc))
					fc.Size = (int64)(len(rangeFc))
				}

				fileContent = append(fileContent, fc)

				if body.IncludeLastCommit {
					pathCommitId, err := utils.LastCommitForPath(repoPath, body.RefId, fileContent[0].Path)
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
				pathCommitId, err := utils.LastCommitForPath(repoPath, body.RefId, treeContents[i].Path)
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
