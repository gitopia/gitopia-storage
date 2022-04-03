package utils

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/gitopia/gitopia/x/gitopia/types"
	git "github.com/libgit2/git2go/v33"
	"github.com/spf13/viper"
)

const BranchPrefix = "refs/heads/"

type PullDiffRequestBody struct {
	BaseRepositoryID uint64       `json:"base_repository_id"`
	HeadRepositoryID uint64       `json:"head_repository_id"`
	BaseCommitSha    string       `json:"base_commit_sha"`
	HeadCommitSha    string       `json:"head_commit_sha"`
	OnlyStat         bool         `json:"only_stat"`
	Pagination       *PageRequest `json:"pagination"`
}

type PullRequestCommitsPostBody struct {
	BaseRepositoryID uint64 `json:"base_repository_id"`
	HeadRepositoryID uint64 `json:"head_repository_id"`
	BaseBranch       string `json:"base_branch"`
	HeadBranch       string `json:"head_branch"`
	BaseCommitSha    string `json:"base_commit_sha"`
	HeadCommitSha    string `json:"head_commit_sha"`
}

type PullRequestMergePostBody struct {
	BaseRepositoryID uint64     `json:"base_repository_id"`
	HeadRepositoryID uint64     `json:"head_repository_id"`
	BaseBranch       string     `json:"base_branch"`
	HeadBranch       string     `json:"head_branch"`
	MergeStyle       MergeStyle `json:"merge_style"`
	UserName         string     `json:"user_name"`
	UserEmail        string     `json:"user_email"`
	Sender           string     `json:"sender"`
	PullRequestIID   uint64     `json:"pull_request_iid"`
}

// MergeStyle represents the approach to merge commits into base branch.
type MergeStyle string

const (
	// MergeStyleMerge create merge commit
	MergeStyleMerge MergeStyle = "merge"
	// MergeStyleRebase rebase before merging
	MergeStyleRebase MergeStyle = "rebase"
	// MergeStyleRebaseMerge rebase before merging with merge commit (--no-ff)
	MergeStyleRebaseMerge MergeStyle = "rebase-merge"
	// MergeStyleSquash squash commits into single commit before merging
	MergeStyleSquash MergeStyle = "squash"
	// MergeStyleManuallyMerged pr has been merged manually, just mark it as merged directly
	MergeStyleManuallyMerged MergeStyle = "manually-merged"
	// MergeStyleRebaseUpdate not a merge style, used to update pull head by rebase
	MergeStyleRebaseUpdate MergeStyle = "rebase-update-only"
)

func CreateQuarantineRepo(baseRepositoryID uint64, headRepositoryID uint64, baseBranch string, headBranch string) (string, error) {
	// Clone base repo
	tmpBasePath, err := ioutil.TempDir(os.TempDir(), "merge-")
	if err != nil {
		log.Fatal(err)
	}

	gitDir := viper.GetString("git_dir")
	baseRepoPath := path.Join(gitDir, fmt.Sprintf("%v.git", baseRepositoryID))
	headRepoPath := path.Join(gitDir, fmt.Sprintf("%v.git", headRepositoryID))

	_, err = git.InitRepository(tmpBasePath, false)
	if err != nil {
		log.Printf("git init tmpBasePath: %v\n", err)
		os.RemoveAll(tmpBasePath)
		return "", err
	}

	remoteRepoName := "head_repo"
	baseBranchName := "base"

	// Add head repo remote.
	addCacheRepo := func(staging, cache string) error {
		p := filepath.Join(staging, ".git", "objects", "info", "alternates")
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			log.Printf("Could not create .git/objects/info/alternates file in %s: %v\n", staging, err)
			os.RemoveAll(tmpBasePath)
			return err
		}
		defer f.Close()
		data := filepath.Join(cache, "objects")
		if _, err := fmt.Fprintln(f, data); err != nil {
			log.Printf("Could not write to .git/objects/info/alternates file in %s: %v\n", staging, err)
			os.RemoveAll(tmpBasePath)
			return err
		}
		return nil
	}

	if err := addCacheRepo(tmpBasePath, baseRepoPath); err != nil {
		log.Printf("Unable to add base repository to temporary repo [%s -> %s]: %v\n", baseRepoPath, tmpBasePath, err)
		os.RemoveAll(tmpBasePath)
		return "", fmt.Errorf("Unable to add base repository to temporary repo [%s -> tmpBasePath]: %v", baseRepoPath, err)
	}

	cmd := exec.Command("git", "remote", "add", "-t", baseBranch, "-m", baseBranch, "origin", baseRepoPath)
	cmd.Dir = tmpBasePath
	out, err := cmd.Output()
	if err != nil {
		log.Printf("Unable to add base repository as origin [%s -> %s]: %v\n%s\n", baseRepoPath, tmpBasePath, err, string(out))
		os.RemoveAll(tmpBasePath)
		return "", fmt.Errorf("Unable to add base repository as origin [%s -> tmpBasePath]: %v\n%s", baseRepoPath, err, out)
	}

	cmd = exec.Command("git", "fetch", "origin", "--no-tags", "--", baseBranch+":"+baseBranchName, baseBranch+":original_"+baseBranchName)
	cmd.Dir = tmpBasePath
	out, err = cmd.Output()
	if err != nil {
		log.Printf("Unable to fetch origin base branch [%s:%s -> base, original_base in %s]: %v:\n%s\n", baseRepoPath, baseBranch, tmpBasePath, err, string(out))
		os.RemoveAll(tmpBasePath)
		return "", fmt.Errorf("Unable to fetch origin base branch [%s:%s -> base, original_base in tmpBasePath]: %v\n%s", baseRepoPath, baseBranch, err, string(out))
	}

	cmd = exec.Command("git", "symbolic-ref", "HEAD", BranchPrefix+baseBranchName)
	cmd.Dir = tmpBasePath
	out, err = cmd.Output()
	if err != nil {
		log.Printf("Unable to set HEAD as base branch [%s]: %v\n%s\n", tmpBasePath, err, string(out))
		os.RemoveAll(tmpBasePath)
		return "", fmt.Errorf("Unable to set HEAD as base branch [tmpBasePath]: %v\n%s", err, string(out))
	}

	if err := addCacheRepo(tmpBasePath, headRepoPath); err != nil {
		log.Printf("Unable to add head repository to temporary repo [%s -> %s]: %v\n", headRepoPath, tmpBasePath, err)
		os.RemoveAll(tmpBasePath)
		return "", fmt.Errorf("Unable to head base repository to temporary repo [%s -> tmpBasePath]: %v", headRepoPath, err)
	}

	cmd = exec.Command("git", "remote", "add", remoteRepoName, headRepoPath)
	cmd.Dir = tmpBasePath
	out, err = cmd.Output()
	if err != nil {
		log.Printf("Unable to add head repository as head_repo [%s -> %s]: %v\n%s\n", headRepoPath, tmpBasePath, err, string(out))
		os.RemoveAll(tmpBasePath)
		return "", fmt.Errorf("Unable to add head repository as head_repo [%s -> tmpBasePath]: %v\n%s", headRepoPath, err, string(out))
	}

	trackingBranch := "tracking"

	// Fetch head branch
	cmd = exec.Command("git", "fetch", "--no-tags", remoteRepoName, BranchPrefix+headBranch+":"+trackingBranch)
	cmd.Dir = tmpBasePath
	out, err = cmd.Output()
	if err != nil {
		os.RemoveAll(tmpBasePath)
		log.Printf("Unable to fetch head_repo head branch [%s:%s -> tracking in %s]: %v:\n%s\n", headRepoPath, headBranch, tmpBasePath, err, string(out))
		return "", fmt.Errorf("Unable to fetch head_repo head branch [%s:%s -> tracking in tmpBasePath]: %v\n%s", headRepoPath, headBranch, err, string(out))
	}

	return tmpBasePath, nil
}

func CommitAndSignNoAuthor(pr types.PullRequest, message, signArg, quarantineRepoPath string, env []string) error {
	if signArg == "" {
		cmd := exec.Command("git", "commit", "-m", message)
		cmd.Env = env
		cmd.Dir = quarantineRepoPath
		out, err := cmd.Output()
		if err != nil {
			err = fmt.Errorf("git commit [%v:%s -> %v:%s]: %v\n%s", pr.Head.RepositoryId, pr.Head.Branch, pr.Base.RepositoryId, pr.Base.Branch, err, string(out))
			return err
		}
	} else {
		cmd := exec.Command("git", "commit", signArg, "-m", message)
		cmd.Env = env
		cmd.Dir = quarantineRepoPath
		out, err := cmd.Output()
		if err != nil {
			err = fmt.Errorf("git commit [%v:%s -> %v:%s]: %v\n%s", pr.Head.RepositoryId, pr.Head.Branch, pr.Base.RepositoryId, pr.Base.Branch, err, string(out))
			return err
		}
	}
	return nil
}

func RunMergeCommand(prHead types.PullRequestHead, prBase types.PullRequestBase, cmd *exec.Cmd, quarantineRepoPath string) error {
	cmd.Dir = quarantineRepoPath
	out, err := cmd.Output()
	if err != nil {
		// Merge will leave a MERGE_HEAD file in the .git folder if there is a conflict
		if _, statErr := os.Stat(filepath.Join(quarantineRepoPath, ".git", "MERGE_HEAD")); statErr == nil {
			// We have a merge conflict error
			err = fmt.Errorf("MergeConflict [%v:%s -> %v:%s]: %v\n%s", prHead.RepositoryId, prHead.Branch, prBase.RepositoryId, prBase.Branch, err, string(out))
			return err
		} else if strings.Contains(string(out), "refusing to merge unrelated histories") {
			err = fmt.Errorf("MergeUnrelatedHistories [%v:%s -> %v:%s]: %v\n%s", prHead.RepositoryId, prHead.Branch, prBase.RepositoryId, prBase.Branch, err, string(out))
			return err
		}
		err = fmt.Errorf("git merge [%v:%s -> %v:%s]: %v\n%s", prHead.RepositoryId, prHead.Branch, prBase.RepositoryId, prBase.Branch, err, string(out))
		return err
	}

	return nil
}
