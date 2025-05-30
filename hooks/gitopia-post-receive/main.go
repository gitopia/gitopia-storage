package main

import (
	"io"
	"log"
	"os"
	"os/exec"

	"github.com/gitopia/gitopia-storage/hooks/utils"
	"github.com/pkg/errors"
)

func receive(reader io.Reader) error {
	input, err := utils.Parse(reader)
	if err != nil {
		return errors.Wrap(err, "error parsing input")
	}
	// Check if push is non fast-forward (force)
	force, err := input.IsForcePush()
	if err != nil {
		return errors.Wrap(err, "error checking force push")
	}

	// create dangling ref
	if force || input.Action == utils.DeleteAction {
		cmd := exec.Command("git", "update-ref", "refs/dangling/"+input.OldRev, input.OldRev)
		errPipe, err := cmd.StderrPipe()
		if err != nil {
			return errors.Wrap(err, "error getting update ref err stream")
		}
		defer errPipe.Close()

		err = cmd.Start()
		if err != nil {
			return errors.Wrap(err, "error craeting update-ref command")
		}

		er, err := io.ReadAll(errPipe)
		if err != nil {
			return errors.Wrap(err, "error reading update ref err stream")
		}

		err = cmd.Wait()
		if err != nil {
			return errors.Wrap(err, "error waiting to run update-ref command:"+string(er))
		}
	}

	return nil
}

func main() {
	// Git hook data is provided via STDIN
	if err := receive(os.Stdin); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
