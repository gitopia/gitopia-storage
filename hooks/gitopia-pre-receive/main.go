package main

import (
	"io"
	"log"
	"os"
	"strings"

	"github.com/gitopia/git-server/hooks/utils"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

var NonFFErr = errors.New("non fast-forward pushes are not allowed")

func receive(reader io.Reader) error {
	input, err := utils.Parse(reader)
	if err != nil {
		return errors.Wrap(err, "error parsing input")
	}

	allowForcePush, err := IsForcePushAllowedForBranch(input.RepoId, input.RefName)
	if err != nil {
		if strings.Contains(err.Error(), "branch not found") { // no branch found. allow push
			return nil
		} else { // no branch present for the first commit
			return errors.Wrap(err, "error fetching force push config")
		}
	}

	if allowForcePush {
		return nil
	}

	// Check if push is non fast-forward (force)
	force, err := input.IsForcePush()
	if err != nil {
		return errors.Wrap(err, "error checking force push")
	}

	// Reject force push
	if force {
		return NonFFErr
	}

	return nil
}

func main() {
	viper.AutomaticEnv()
	// Git hook data is provided via STDIN
	if err := receive(os.Stdin); err != nil {
		log.Println(err)
		os.Exit(1) // terminating with non-zero status will cancel push
	}
}
