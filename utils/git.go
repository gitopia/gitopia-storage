package utils

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
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
	args := []string{"log", "--pretty=%H", "--max-count=1", revision}
	if path != "" {
		args = append(args, "--", path)
	}
	cmd := exec.Command("git", args...)
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
	args := []string{
		"log",
		"--pretty=%H",
		"--max-count=" + strconv.Itoa(limit),
		"--skip=" + strconv.Itoa(offset),
		revision,
	}
	if path != "" {
		args = append(args, "--", path)
	}
	cmd := exec.Command("git", args...)
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
	args := []string{"rev-list", "--count", revision}
	if path != "" {
		args = append(args, "--", path)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	count := string(out)
	return strings.TrimSpace(count), nil
}

func GitCommand(name string, args ...string) (*exec.Cmd, io.ReadCloser) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()
	// cmd.Env = append(cmd.Env, env...)

	r, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout

	return cmd, r
}

func CleanUpProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	process := cmd.Process
	if process != nil && process.Pid > 0 {
		syscall.Kill(-process.Pid, syscall.SIGTERM)
	}

	go cmd.Wait()
}
