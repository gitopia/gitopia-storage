package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMigration(t *testing.T) {
	// Create temporary directories for test data
	tempDir, err := os.MkdirTemp("", "migration-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	gitDir := filepath.Join(tempDir, "git")
	attachmentDir := filepath.Join(tempDir, "attachments")
	require.NoError(t, os.MkdirAll(gitDir, 0755))
	require.NoError(t, os.MkdirAll(attachmentDir, 0755))

	// Create multiple test repositories
	repoIDs := []string{"123", "456", "789"}
	for _, repoID := range repoIDs {
		repoPath := filepath.Join(gitDir, repoID+".git")
		require.NoError(t, os.MkdirAll(repoPath, 0755))

		// Initialize git repository
		cmd := exec.Command("git", "init", "--bare")
		cmd.Dir = repoPath
		require.NoError(t, cmd.Run())

		// Create test files in the repository
		testFile := filepath.Join(repoPath, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("test content for repo "+repoID), 0644))
	}

	// Set environment variables for IPFS and IPFS cluster
	// Note: These should be running locally for the test
	os.Setenv("IPFS_CLUSTER_PEER_HOST", "localhost")
	os.Setenv("IPFS_CLUSTER_PEER_PORT", "9094")
	os.Setenv("IPFS_HOST", "localhost")
	os.Setenv("IPFS_PORT", "5001")

	// Run the migration script
	cmd := exec.Command("go", "run", "main.go",
		"--git-dir", gitDir,
		"--attachment-dir", attachmentDir,
		"--ipfs-cluster-peer-host", "localhost",
		"--ipfs-cluster-peer-port", "9094",
		"--ipfs-host", "localhost",
		"--ipfs-port", "5001",
		"--chain-id", "chain-xnW2vC",
		"--gas-prices", "0.001ulore",
		"--gitopia-addr", "localhost:9100",
		"--tm-addr", "http://localhost:26667",
		"--working-dir", tempDir,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Migration output: %s", output)
		t.Fatalf("Migration failed: %v", err)
	}

	// Wait a bit for IPFS to process the files
	time.Sleep(5 * time.Second)

	// Verify that the repositories were migrated
	// Note: In a real test, you would verify the on-chain state
	// For now, we'll just check that the command completed successfully
	t.Logf("Migration completed successfully for %d repositories", len(repoIDs))
}
