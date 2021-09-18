package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type uploadAttachmentResponse struct {
	Sha  string `json:"sha"`
	Size int64  `json:"size"`
}

func uploadAttachmentHandler(w http.ResponseWriter, r *http.Request) {

	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	tmpFile, err := ioutil.TempFile(os.TempDir(), "attachment-")
	if err != nil {
		logError("cannot create temporary file", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpFile.Name())

	sha := sha256.New()
	_, err = io.Copy(io.MultiWriter(sha, tmpFile), file)

	attachmentDir := viper.GetString("attachment_dir")
	shaString := hex.EncodeToString(sha.Sum(nil))
	filePath := fmt.Sprintf("%s/%s", attachmentDir, shaString)
	localFile, err := os.Create(filePath)
	defer localFile.Close()
	_, err = io.Copy(localFile, tmpFile)
	if err != nil {
		logError("cannot copy from temp file to attachment dir", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")

	resp := uploadAttachmentResponse{
		Sha:  shaString,
		Size: handler.Size,
	}

	json.NewEncoder(w).Encode(resp)

	return
}

func getAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	blocks := strings.Split(r.URL.Path, "/")

	if len(blocks) != 3 {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	sha := blocks[2]
	filePath := fmt.Sprintf("%s/%s", viper.GetString("attachment_dir"), sha)
	file, err := os.Open(filePath)
	if err != nil {
		logError("attachment does not exist", err)
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	defer file.Close()

	_, err = io.Copy(w, file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	return
}
