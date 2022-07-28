package utils

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

// GetFullCommitID returns full length (40) of commit ID by given short SHA in a repository.
func GetFullCommitSha(repoPath, shortID string) (string, error) {
	cmd := exec.Command("git", "rev-parse", shortID)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	commitSha := string(out)
	return strings.TrimSpace(commitSha), nil
}

// LastCommitForPath returns the last commit which modified path for given revision.
func LastCommitForPath(repoPath, revision string, path string) (string, error) {
	cmd := exec.Command("bash", "-c", "git log --pretty=%H --max-count=1 "+revision+" -- \""+path+"\"")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	commitSha := string(out)
	return strings.TrimSpace(commitSha), nil
}

// CommitHistory returns the commit history for given revision or path.
func CommitHistory(repoPath, revision string, path string, offset int, limit int) ([]string, error) {
	cmd := exec.Command("bash", "-c", "git log --pretty=%H --max-count="+strconv.Itoa(limit)+" --skip="+strconv.Itoa(offset)+" "+revision+" -- "+path)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var commitHashes []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		commitHashes = append(commitHashes, strings.TrimSpace(scanner.Text()))
	}
	return commitHashes, nil
}

// CountCommits returns total count of commits.
func CountCommits(repoPath, revision string, path string) (string, error) {
	cmd := exec.Command("bash", "-c", "git rev-list --count "+revision+" -- "+path)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	count := string(out)
	return strings.TrimSpace(count), nil
}
