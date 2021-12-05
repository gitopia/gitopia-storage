package utils

import (
	"os/exec"
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
