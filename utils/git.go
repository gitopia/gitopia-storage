package utils

import (
	"os/exec"
	"strings"
)

// GetFullCommitID returns full length (40) of commit ID by given short SHA in a repository.
func GetFullCommitID(repoPath, shortID string) (string, error) {
	cmd := exec.Command("git", "rev-parse", shortID)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	commitID := string(out)
	return strings.TrimSpace(commitID), nil
}
