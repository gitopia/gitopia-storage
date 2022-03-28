package main

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
	"os/exec"
	"strings"
	"time"

	"github.com/gitopia/git-server/utils"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
)

type uploadAttachmentResponse struct {
	Sha  string `json:"sha"`
	Size int64  `json:"size"`
}

type forkRepositoryPostBody struct {
	SourceRepositoryID uint64 `json:"source_repository_id"`
	TargetRepositoryID uint64 `json:"target_repository_id"`
}

type forkRepositoryResponseData struct {
	Forked bool `json:"forked"`
}

type forkRepositoryResponse struct {
	Data  forkRepositoryResponseData `json:"data"`
	Error string                     `json:"error"`
}

type pullRequestCheckResponseData struct {
	IsMergeable bool `json:"is_mergeable"`
}

type pullRequestCheckResponse struct {
	Data  pullRequestCheckResponseData `json:"data"`
	Error string                       `json:"error"`
}

type pullRequestMergeResponseData struct {
	Merged         bool   `json:"merged"`
	MergeCommitSha string `json:"merge_commit_sha"`
}

type pullRequestMergeResponse struct {
	Data  pullRequestMergeResponseData `json:"data"`
	Error string                       `json:"error"`
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

	tmpFile.Seek(0, io.SeekStart)

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
	if err != nil {
		logError("cannot find release", err)
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

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

func (s *Server) pullRequestCommitsHandler(w http.ResponseWriter, r *http.Request) {
	var body utils.PullRequestCommitsPostBody

	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&body)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	quarantineRepoPath, err := utils.CreateQuarantineRepo(body.BaseRepositoryID, body.HeadRepositoryID, body.BaseBranch, body.HeadBranch)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(quarantineRepoPath)

	var cmd *exec.Cmd
	if len(body.BaseCommitSha) == 0 {
		baseBranch := fmt.Sprintf("origin/%s", body.BaseBranch)
		headBranch := fmt.Sprintf("head_repo/%s", body.HeadBranch)
		cmd = exec.Command("git", "rev-list", headBranch, fmt.Sprintf("^%s", baseBranch))
	} else {
		cmd = exec.Command("git", "rev-list", body.HeadCommitSha, fmt.Sprintf("^%s", body.BaseCommitSha))
	}

	cmd.Dir = quarantineRepoPath
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	logs := string(out)
	logs = strings.TrimSpace(logs)
	if len(logs) == 0 {
		json.NewEncoder(w).Encode([]string{})
		return
	}

	commitShas := strings.Split(logs, "\n")
	json.NewEncoder(w).Encode(commitShas)
}

func (s *Server) pullRequestCheckHandler(w http.ResponseWriter, r *http.Request) {
	var body utils.PullRequestMergePostBody
	var resp pullRequestCheckResponse

	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&body)
	if err != nil {
		resp.Error = http.StatusText(http.StatusBadRequest)
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusBadRequest)
		return
	}

	quarantineRepoPath, err := utils.CreateQuarantineRepo(body.BaseRepositoryID, body.HeadRepositoryID, body.BaseBranch, body.HeadBranch)
	if err != nil {
		resp.Error = http.StatusText(http.StatusInternalServerError)
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(quarantineRepoPath)

	trackingBranch := "tracking"

	// Read base branch index
	cmd := exec.Command("git", "read-tree", "HEAD")
	cmd.Dir = quarantineRepoPath
	out, err := cmd.Output()
	if err != nil {
		log.Printf("git read-tree HEAD: %v\n%s\n", err, string(out))
		resp.Error = fmt.Sprintf("Unable to read base branch in to the index: %v\n%s\n", err, string(out))
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}

	commitTimeStr := time.Now().Format(time.RFC3339)

	// Because this may call hooks we should pass in the environment
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+body.UserName,
		"GIT_AUTHOR_EMAIL="+body.UserEmail,
		"GIT_AUTHOR_DATE="+commitTimeStr,
		"GIT_COMMITTER_NAME="+body.UserName,
		"GIT_COMMITTER_EMAIL="+body.UserEmail,
		"GIT_COMMITTER_DATE="+commitTimeStr,
	)

	cmd = exec.Command("git", "merge", "--no-ff", "--no-commit", trackingBranch)
	cmd.Env = env
	prHead := types.PullRequestHead{
		RepositoryId: body.HeadRepositoryID,
		Branch:       body.HeadBranch,
	}
	prBase := types.PullRequestBase{
		RepositoryId: body.BaseRepositoryID,
		Branch:       body.BaseBranch,
	}
	if err := utils.RunMergeCommand(prHead, prBase, cmd, quarantineRepoPath); err != nil {
		log.Printf("Unable to merge tracking into base: %v\n", err)
		resp.Error = err.Error()
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}

	resp.Data.IsMergeable = true
	json.NewEncoder(w).Encode(resp)
}
