package utils

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
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

func GitCommand(name string, args ...string) (*exec.Cmd, io.ReadCloser, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()

	r, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create stdout pipe")
	}
	cmd.Stderr = cmd.Stdout

	return cmd, r, nil
}

func CleanUpProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	process := cmd.Process
	if process == nil {
		return
	}

	// Check if process is still running
	if process.Pid <= 0 {
		return
	}

	// Try to kill the process group
	if err := syscall.Kill(-process.Pid, syscall.SIGTERM); err != nil {
		// If SIGTERM fails, try SIGKILL
		syscall.Kill(-process.Pid, syscall.SIGKILL)
	}

	// Wait for process to finish (with timeout to prevent hanging)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		// Process finished normally
	case <-time.After(5 * time.Second):
		// Force kill if process doesn't terminate within 5 seconds
		syscall.Kill(-process.Pid, syscall.SIGKILL)
		<-done // Wait for the goroutine to finish
	}
}

func GetPackfileName(repoPath string) (string, error) {
	// Walk the directory and get the packfile name
	var packfileName string
	packfileDir := path.Join(repoPath, "objects", "pack")
	err := filepath.Walk(packfileDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check if the file has a .pack extension
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".pack") {
			packfileName = path
			return nil // Found the file, no need to continue walking
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	// Check if a .pack file was found
	if packfileName == "" {
		return "", errors.New("No .pack file found")
	}

	return packfileName, nil
}
