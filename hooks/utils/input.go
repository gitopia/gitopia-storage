package utils

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

const ZeroSHA = "0000000000000000000000000000000000000000"

const (
	BranchPushAction   = "branch.push"
	BranchCreateAction = "branch.create"
	BranchDeleteAction = "branch.delete"
	TagCreateAction    = "tag.create"
	TagDeleteAction    = "tag.delete"
)

type Input struct {
	Action   string
	RepoId   uint64
	RepoPath string
	OldRev   string
	NewRev   string
	Ref      string
	RefType  string
	RefName  string
}

func Parse(inputStream io.Reader) (*Input, error) {
	reader := bufio.NewReader(inputStream)

	line, _, err := reader.ReadLine()
	if err != nil {
		return nil, err
	}

	chunks := strings.Split(string(line), " ")
	if len(chunks) != 3 {
		return nil, fmt.Errorf("invalid hook input")
	}
	refchunks := strings.Split(chunks[2], "/")

	dir, _ := os.Getwd()
	b, _, _ := strings.Cut(filepath.Base(dir), ".") // "repoid.git"
	id, err := strconv.ParseUint(b, 10, 64)
	if err != nil {
		return nil, err
	}
	input := &Input{
		RepoId:   id,
		RepoPath: dir,
		OldRev:   chunks[0],
		NewRev:   chunks[1],
		Ref:      chunks[2],
		RefType:  refchunks[1],
		RefName:  refchunks[2],
	}
	input.Action = input.parseHookAction()

	return input, nil
}

func (i Input) parseHookAction() string {
	action := "push"
	context := "branch"

	if i.RefType == "tags" {
		context = "tag"
	}

	if i.OldRev == ZeroSHA && i.NewRev != ZeroSHA {
		action = "create"
	} else if i.OldRev != ZeroSHA && i.NewRev == ZeroSHA {
		action = "delete"
	}

	return fmt.Sprintf("%s.%s", context, action)
}

func (i Input) IsForcePush() (bool, error) {
	// New branch or tag OR deleted branch or tag
	if i.OldRev == ZeroSHA || i.NewRev == ZeroSHA {
		return false, nil
	}

	out, err := exec.Command("git", "merge-base", i.OldRev, i.NewRev).CombinedOutput()
	if err != nil {
		return false, errors.WithMessage(err, "git merge base failed" + string(out))
	}

	base := strings.TrimSpace(string(out))

	// Non fast-forwarded, meaning force
	return base != i.OldRev, nil
}
