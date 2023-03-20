package route

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"

	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/format/objfile"
	"github.com/spf13/viper"
)

// Serve loose git objects
func ObjectsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "GET" {
		defer r.Body.Close()

		blocks := strings.Split(r.URL.Path, "/")

		if len(blocks) != 4 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		repositoryId := blocks[2]
		objectHash := blocks[3]

		RepoPath := path.Join(viper.GetString("GIT_DIR"), fmt.Sprintf("%s.git", repositoryId))
		repo, err := git.PlainOpen(RepoPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		hash := plumbing.NewHash(objectHash)
		var obj plumbing.EncodedObject
		obj, err = repo.Storer.EncodedObject(plumbing.AnyObject, hash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		readCloser, err := obj.Reader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer readCloser.Close()

		objWriter := objfile.NewWriter(w)
		defer objWriter.Close()

		err = objWriter.WriteHeader(obj.Type(), obj.Size())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_, err = io.Copy(objWriter, readCloser)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		return
	}
}
