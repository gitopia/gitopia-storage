package app

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/format/objfile"
)

// Serve loose git objects
func (s *Server) ObjectsHandler(w http.ResponseWriter, r *http.Request) {
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

		repoId, err := strconv.ParseUint(repositoryId, 10, 64)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		if err := s.CacheRepository(repoId); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		repoPath := filepath.Join(s.Config.Dir, fmt.Sprintf("%d.git", repoId))
		repo, err := git.PlainOpen(repoPath)
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
