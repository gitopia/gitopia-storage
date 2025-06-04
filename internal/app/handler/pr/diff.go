package pr

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"

	"github.com/gitopia/gitopia-storage/utils"
	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/object"
	"github.com/spf13/viper"
)

func PullDiffHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body utils.PullDiffRequestBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		if body.HeadCommitSha == "" || body.BaseCommitSha == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		// cache repo
		cacheDir := viper.GetString("GIT_DIR")
		if err := utils.CacheRepository(body.BaseRepositoryID, cacheDir); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if body.HeadRepositoryID != body.BaseRepositoryID {
			// cache repo
			if err := utils.CacheRepository(body.HeadRepositoryID, cacheDir); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		qpath, err := utils.CreateReadOnlyQuarantineRepo(body.BaseRepositoryID, body.HeadRepositoryID)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		defer os.RemoveAll(qpath)

		cmd := exec.Command("git", "-C", qpath, "merge-base", "--", body.HeadCommitSha, body.BaseCommitSha)
		out, err := cmd.Output()
		if err != nil {
			log.Print("err finding merge base " + err.Error())
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		mergeBase := string(out)
		if mergeBase == "" {
			log.Print("merge base not found")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		repo, err := git.PlainOpen(qpath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		headCommitHash := plumbing.NewHash(body.HeadCommitSha)
		baseCommitHash := plumbing.NewHash(mergeBase)

		var headCommit, baseCommit *object.Commit

		headCommit, err = object.GetCommit(repo.Storer, headCommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		baseCommit, err = object.GetCommit(repo.Storer, baseCommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var headTree, baseTree *object.Tree

		headTree, err = object.GetTree(repo.Storer, headCommit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		baseTree, err = object.GetTree(repo.Storer, baseCommit.TreeHash)
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
