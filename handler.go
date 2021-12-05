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
	"path/filepath"
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

func (s *Server) forkRepositoryHandler(w http.ResponseWriter, r *http.Request) {
	var body forkRepositoryPostBody
	var resp forkRepositoryResponse

	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&body)
	if err != nil {
		resp.Error = http.StatusText(http.StatusBadRequest)
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusBadRequest)
		return
	}

	if body.TargetRepositoryID == 0 {
		resp.Error = http.StatusText(http.StatusBadRequest)
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusBadRequest)
		return
	}

	sourceRepoPath := path.Join(s.config.Dir, fmt.Sprintf("%v.git", body.SourceRepositoryID))
	targetRepoPath := path.Join(s.config.Dir, fmt.Sprintf("%v.git", body.TargetRepositoryID))
	cmd := exec.Command("git", "clone", "--shared", "--bare", sourceRepoPath, targetRepoPath)
	out, err := cmd.Output()
	if err != nil {
		log.Printf("Unable to fork repository: %s\n", string(out))
		resp.Error = string(out)
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}

	resp.Data.Forked = true
	json.NewEncoder(w).Encode(resp)
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

	baseBranch := fmt.Sprintf("origin/%s", body.BaseBranch)
	headBranch := fmt.Sprintf("head_repo/%s", body.HeadBranch)
	cmd := exec.Command("git", "rev-list", headBranch, fmt.Sprintf("^%s", baseBranch))
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

func (s *Server) pullRequestMergeHandler(w http.ResponseWriter, r *http.Request) {
	var body utils.PullRequestMergePostBody
	var resp pullRequestMergeResponse

	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&body)
	if err != nil {
		resp.Error = http.StatusText(http.StatusBadRequest)
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusBadRequest)
		return
	}

	message := fmt.Sprintf("Merge pull request #%v from %s/%s", body.PullRequestIID, body.Sender, body.HeadBranch)

	quarantineRepoPath, err := utils.CreateQuarantineRepo(body.BaseRepositoryID, body.HeadRepositoryID, body.BaseBranch, body.HeadBranch)
	if err != nil {
		resp.Error = http.StatusText(http.StatusInternalServerError)
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(quarantineRepoPath)

	baseBranch := "base"
	trackingBranch := "tracking"
	stagingBranch := "staging"

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

	// Merge commits.
	switch body.MergeStyle {
	case utils.MergeStyleMerge:
		cmd := exec.Command("git", "merge", "--no-ff", "--no-commit", trackingBranch)
		cmd.Env = env
		if err := utils.RunMergeCommand(&body, cmd, quarantineRepoPath); err != nil {
			log.Printf("Unable to merge tracking into base: %v\n", err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}

		if err := utils.CommitAndSignNoAuthor(&body, message, "", quarantineRepoPath, env); err != nil {
			log.Printf("Unable to make final commit: %v\n", err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}
	case utils.MergeStyleRebase:
		fallthrough
	case utils.MergeStyleRebaseUpdate:
		fallthrough
	case utils.MergeStyleRebaseMerge:
		// Checkout head branch
		cmd = exec.Command("git", "checkout", "-b", stagingBranch, trackingBranch)
		cmd.Dir = quarantineRepoPath
		out, err = cmd.Output()
		if err != nil {
			err = fmt.Errorf("git checkout base prior to merge post staging rebase  [%v:%s -> %v:%s]: %v\n%s", body.HeadRepositoryID, body.HeadBranch, body.BaseRepositoryID, body.BaseBranch, err, string(out))
			log.Println(err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}

		// Rebase before merging
		cmd = exec.Command("git", "rebase", baseBranch)
		cmd.Dir = quarantineRepoPath
		out, err = cmd.Output()
		if err != nil {
			// Rebase will leave a REBASE_HEAD file in .git if there is a conflict
			if _, statErr := os.Stat(filepath.Join(quarantineRepoPath, ".git", "REBASE_HEAD")); statErr == nil {
				var commitSha string
				ok := false
				failingCommitPaths := []string{
					filepath.Join(quarantineRepoPath, ".git", "rebase-apply", "original-commit"), // Git < 2.26
					filepath.Join(quarantineRepoPath, ".git", "rebase-merge", "stopped-sha"),     // Git >= 2.26
				}
				for _, failingCommitPath := range failingCommitPaths {
					if _, statErr := os.Stat(filepath.Join(failingCommitPath)); statErr == nil {
						commitShaBytes, readErr := os.ReadFile(filepath.Join(failingCommitPath))
						if readErr != nil {
							// Abandon this attempt to handle the error
							err = fmt.Errorf("git rebase staging on to base [%v:%s -> %v:%s]: %v\n%s", body.HeadRepositoryID, body.HeadBranch, body.BaseRepositoryID, body.BaseBranch, err, string(out))
						}
						commitSha = strings.TrimSpace(string(commitShaBytes))
						ok = true
						break
					}
				}
				if !ok {
					err = fmt.Errorf("git rebase staging on to base [%v:%s -> %v:%s]: %v\n%s", body.HeadRepositoryID, body.HeadBranch, body.BaseRepositoryID, body.BaseBranch, err, string(out))
					log.Println(out)
					resp.Error = err.Error()
					b, _ := json.Marshal(resp)
					http.Error(w, string(b), http.StatusInternalServerError)
					return
				}
				err = fmt.Errorf("RebaseConflict at %s [%v:%s -> %v:%s]: %v\n%s", commitSha, body.HeadRepositoryID, body.HeadBranch, body.BaseRepositoryID, body.BaseBranch, err, string(out))
				log.Println(err)
				resp.Error = err.Error()
				b, _ := json.Marshal(resp)
				http.Error(w, string(b), http.StatusInternalServerError)
				return
			}
			err = fmt.Errorf("git rebase staging on to base [%v:%s -> %v:%s]: %v\n%s", body.HeadRepositoryID, body.HeadBranch, body.BaseRepositoryID, body.BaseBranch, err, string(out))
			log.Println(err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}

		// not need merge, just update by rebase. so skip
		if body.MergeStyle == utils.MergeStyleRebaseUpdate {
			break
		}

		// Checkout base branch again
		cmd = exec.Command("git", "checkout", baseBranch)
		cmd.Dir = quarantineRepoPath
		out, err = cmd.Output()
		if err != nil {
			err = fmt.Errorf("git checkout base prior to merge post staging rebase  [%v:%s -> %v:%s]: %v\n%s", body.HeadRepositoryID, body.HeadBranch, body.BaseRepositoryID, body.BaseBranch, err, string(out))
			log.Println(err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}

		cmd = exec.Command("git", "merge")
		if body.MergeStyle == utils.MergeStyleRebase {
			cmd.Args = append(cmd.Args, "--ff-only")
		} else {
			cmd.Args = append(cmd.Args, "--no-ff", "--no-commit")
		}
		cmd.Args = append(cmd.Args, stagingBranch)

		// Prepare merge with commit
		if err := utils.RunMergeCommand(&body, cmd, quarantineRepoPath); err != nil {
			log.Printf("Unable to merge staging into base: %v\n", err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}
		if body.MergeStyle == utils.MergeStyleRebaseMerge {
			if err := utils.CommitAndSignNoAuthor(&body, message, "", quarantineRepoPath, env); err != nil {
				log.Printf("Unable to make final commit: %v\n", err)
				resp.Error = err.Error()
				b, _ := json.Marshal(resp)
				http.Error(w, string(b), http.StatusInternalServerError)
				return
			}
		}
	case utils.MergeStyleSquash:
		// Merge with squash
		cmd := exec.Command("git", "merge", "--squash", trackingBranch)
		if err := utils.RunMergeCommand(&body, cmd, quarantineRepoPath); err != nil {
			log.Printf("Unable to merge --squash tracking into base: %v\n", err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}

		cmd = exec.Command("git", "commit", fmt.Sprintf("--author='%s <%s>'", body.UserName, body.UserEmail), "-m", message)
		cmd.Env = env
		cmd.Dir = quarantineRepoPath
		out, err = cmd.Output()
		if err != nil {
			err = fmt.Errorf("git commit [%v:%s -> %v:%s]: %v\n%s", body.HeadRepositoryID, body.HeadBranch, body.BaseRepositoryID, body.BaseBranch, err, string(out))
			log.Println(err)
			resp.Error = err.Error()
			b, _ := json.Marshal(resp)
			http.Error(w, string(b), http.StatusInternalServerError)
			return
		}

	default:
		err = fmt.Errorf("Invalid merge style: %v", body.MergeStyle)
		log.Println(err)
		resp.Error = err.Error()
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusBadRequest)
		return
	}

	mergeCommitSha, err := utils.GetFullCommitSha(quarantineRepoPath, baseBranch)
	if err != nil {
		log.Println(err)
		resp.Error = err.Error()
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}

	env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+body.UserName,
		"GIT_AUTHOR_EMAIL="+body.UserEmail,
		"GIT_COMMITTER_NAME="+body.UserName,
		"GIT_COMMITTER_EMAIL="+body.UserEmail)

	var pushCmd *exec.Cmd
	if body.MergeStyle == utils.MergeStyleRebaseUpdate {
		// force push the rebase result to head brach
		pushCmd = exec.Command("git", "push", "-f", "head_repo", stagingBranch+":refs/heads/"+body.HeadBranch)
	} else {
		pushCmd = exec.Command("git", "push", "origin", baseBranch+":refs/heads/"+body.BaseBranch)
	}

	// Push back to upstream.
	pushCmd.Env = env
	pushCmd.Dir = quarantineRepoPath
	out, err = pushCmd.Output()
	if err != nil {
		if strings.Contains(string(out), "non-fast-forward") {

		} else if strings.Contains(string(out), "! [remote rejected]") {

		}
		err = fmt.Errorf("git push: %s", string(out))
		log.Println(err)
		resp.Error = err.Error()
		b, _ := json.Marshal(resp)
		http.Error(w, string(b), http.StatusInternalServerError)
	}

	resp.Data.Merged = true
	resp.Data.MergeCommitSha = mergeCommitSha
	json.NewEncoder(w).Encode(resp)
}
