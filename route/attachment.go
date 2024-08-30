package route

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/gitopia/gitopia/v4/x/gitopia/types"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const MAX_UPLOAD_SIZE = 2 * 1024 * 1024 * 1024 // 2GB

type uploadAttachmentResponse struct {
	Sha  string `json:"sha"`
	Size int64  `json:"size"`
}

func ReleaseAttachmentExists(attachments []*types.Attachment, name string) (int, bool) {
	for i, v := range attachments {
		if v.Name == name {
			return i, true
		}
	}
	return 0, false
}

func UploadAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" {
		defer r.Body.Close()

		r.Body = http.MaxBytesReader(w, r.Body, MAX_UPLOAD_SIZE)
		if err := r.ParseMultipartForm(MAX_UPLOAD_SIZE); err != nil {
			http.Error(w, "The uploaded file is too big. Please choose an file that's less than 2GB in size", http.StatusBadRequest)
			return
		}

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
			log.Printf("cannot create temporary file, %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmpFile.Name())

		sha := sha256.New()
		_, err = io.Copy(io.MultiWriter(sha, tmpFile), file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		attachmentDir := viper.GetString("ATTACHMENT_DIR")
		shaString := hex.EncodeToString(sha.Sum(nil))
		filePath := fmt.Sprintf("%s/%s", attachmentDir, shaString)
		localFile, err := os.Create(filePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer localFile.Close()

		tmpFile.Seek(0, io.SeekStart)

		_, err = io.Copy(localFile, tmpFile)
		if err != nil {
			log.Printf("cannot copy from temp file to attachment dir, %s", err.Error())
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
}

func GetAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "GET" {
		defer r.Body.Close()

		fileName := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

		releaseURL := strings.TrimSuffix(r.URL.Path, "/"+fileName)
		blocks := strings.SplitN(releaseURL, "/", 5)

		if len(blocks) != 5 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		address := blocks[2]
		repositoryName := blocks[3]
		tagName := blocks[4]

		grpcUrl := viper.GetString("GITOPIA_ADDR")
		grpcConn, err := grpc.Dial(grpcUrl,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer grpcConn.Close()

		queryClient := types.NewQueryClient(grpcConn)

		res, err := queryClient.RepositoryRelease(context.Background(), &types.QueryGetRepositoryReleaseRequest{
			Id:             address,
			RepositoryName: repositoryName,
			TagName:        tagName,
		})
		if err != nil {
			log.Printf("cannot find release, %s", err.Error())
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		i, exists := ReleaseAttachmentExists(res.Release.Attachments, fileName)
		if !exists {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		sha := res.Release.Attachments[i].Sha
		filePath := fmt.Sprintf("%s/%s", viper.GetString("ATTACHMENT_DIR"), sha)
		file, err := os.Open(filePath)
		if err != nil {
			log.Printf("attachment does not exist, %s", err.Error())
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		defer file.Close()

		_, err = io.Copy(w, file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	return
}
