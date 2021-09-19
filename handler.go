package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
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

	if len(blocks) != 6 {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	address := blocks[2]
	repositoryName := blocks[3]
	tagName := blocks[4]
	fileName := blocks[5]

	grpcUrl := viper.GetString("gitopia_grpc_url")
	grpcConn, err := grpc.Dial(grpcUrl,
		grpc.WithInsecure(),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer grpcConn.Close()

	queryClient := types.NewQueryClient(grpcConn)

	res, err := queryClient.RepositoryRelease(context.Background(), &types.QueryGetRepositoryReleaseRequest{
		UserId:         address,
		RepositoryName: repositoryName,
		TagName:        tagName,
	})

	i, exists := ReleaseAttachmentExists(res.Release.Attachments, fileName)
	if !exists {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	sha := res.Release.Attachments[i].Sha
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
