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
	"path"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/gitopia/git-server/utils"
	"github.com/gitopia/gitopia/v2/x/gitopia/types"
	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/object"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const MAX_UPLOAD_SIZE = 2 * 1024 * 1024 * 1024 // 2GB

type uploadAttachmentResponse struct {
	Sha  string `json:"sha"`
	Size int64  `json:"size"`
}

type pullRequestCheckResponseData struct {
	IsMergeable bool `json:"is_mergeable"`
}

type pullRequestCheckResponse struct {
	Data  pullRequestCheckResponseData `json:"data"`
	Error string                       `json:"error"`
}

func getBranch(queryClient types.QueryClient, id, repoName, branchName string) (types.Branch, error) {
	res, err := queryClient.RepositoryBranch(context.Background(), &types.QueryGetRepositoryBranchRequest{
		Id:             id,
		RepositoryName: repoName,
		BranchName:     branchName,
	})
	if err != nil {
		return types.Branch{}, err
	}

	return res.Branch, nil
}

func branchExists(queryClient types.QueryClient, id, repoName, branchName string) bool {
	if _, err := getBranch(queryClient, id, repoName, branchName); err == nil {
		return true
	}

	return false
}

func getBranchNameAndTreePathFromPath(queryClient types.QueryClient, id, repoName string, parts []string) (string, string) {
	branchName := ""
	for i, part := range parts {
		branchName = strings.TrimPrefix(branchName+"/"+part, "/")
		if branchExists(queryClient, id, repoName, branchName) {
			treePath := strings.Join(parts[i+1:], "/")
			return branchName, treePath
		}
	}
	return "", ""
}

func uploadAttachmentHandler(w http.ResponseWriter, r *http.Request) {
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
		logError("cannot create temporary file", err)
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
		logError("cannot copy from temp file to attachment dir", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")

	resp := uploadAttachmentResponse{
		Sha:  shaString,
		Size: handler.Size,
	}

	json.NewEncoder(w).Encode(resp)
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
	filePath := fmt.Sprintf("%s/%s", viper.GetString("ATTACHMENT_DIR"), sha)
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

func (s *Server) getRawFileHandler(w http.ResponseWriter, r *http.Request) {
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

	blocks := strings.Split(r.URL.Path, "/")

	id := blocks[2]
	repoName := blocks[3]

	branchName, treePath := getBranchNameAndTreePathFromPath(queryClient, id, repoName, blocks[4:])
	if len(branchName) == 0 || len(treePath) == 0 {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	branch, err := getBranch(queryClient, id, repoName, branchName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	RepoPath := path.Join(s.config.Dir, fmt.Sprintf("%v.git", branch.RepositoryId))
	repo, err := git.PlainOpen(RepoPath)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	commitHash := plumbing.NewHash(branch.Sha)

	commit, err := object.GetCommit(repo.Storer, commitHash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	tree, err := object.GetTree(repo.Storer, commit.TreeHash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	treeEntry, err := tree.FindEntry(treePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if treeEntry.Mode.IsFile() {
		blob, err := object.GetBlob(repo.Storer, treeEntry.Hash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		reader, err := blob.Reader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		b, err := ioutil.ReadAll(reader)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(b)
		return
	}

	http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
}
