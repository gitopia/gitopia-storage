package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration(t *testing.T) {
	// Initialize test key
	mnemonic := "prize cycle gravity trumpet force initial print pulp correct maze mechanic what gallery debris ice announce chunk curtain gate deliver walk resist forest grid"
	cmd := exec.Command("go", "run", "main.go", "keys", "add", "gitopia-storage", "--keyring-backend", "test", "--recover")
	cmd.Stdin = strings.NewReader(mnemonic)
	output, err := cmd.CombinedOutput()
	fmt.Println(string(output))
	require.NoError(t, err)

	gitConfigs := []struct {
		key   string
		value string
	}{
		{"gitopia.tmAddr", "http://localhost:26667"},
		{"gitopia.grpcHost", "localhost:9100"},
		{"gitopia.gitServerHost", "http://localhost:5001"},
		{"gitopia.chainId", "chain-NMh8oi"},
	}

	for _, config := range gitConfigs {
		cmd := exec.Command("git", "config", "--global", config.key, config.value)
		err := cmd.Run()
		require.NoError(t, err)
	}

	// Cleanup function to remove git configs
	defer func() {
		for _, config := range gitConfigs {
			cmd := exec.Command("git", "config", "--unset", "--global", config.key)
			_ = cmd.Run()
		}
	}()

	// Run the migration script
	cmd = exec.Command("go", "run", "main.go",
		"--from", "gitopia-storage",
		"--keyring-backend", "test",
		"--fees", "200ulore",
	)

	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Logf("Migration output: %s", output)
		t.Fatalf("Migration failed: %v", err)
	}
}
