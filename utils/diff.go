package utils

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/object"
)

type DiffType int

const (
	MODIFY DiffType = iota
	ADD
	DELETE
	RENAME
	BINARY
	EMPTY
)

func (dp DiffType) String() string {
	return [...]string{"MODIFY", "ADD", "DELETE", "RENAME", "BINARY", "EMPTY"}[dp]
}

type DiffRequestBody struct {
	RepositoryID      uint64       `json:"repository_id"`
	PreviousCommitSha string       `json:"previous_commit_sha"`
	CommitSha         string       `json:"commit_sha"`
	OnlyStat          bool         `json:"only_stat"`
	Pagination        *PageRequest `json:"pagination"`
}

type DiffResponse struct {
	Diff       []*Diff       `json:"diff,omitempty"`
	Pagination *PageResponse `json:"pagination,omitempty"`
}

type Diff struct {
	FileName string   `json:"file_name"`
	Stat     DiffStat `json:"stat"`
	Patch    string   `json:"patch"`
	Type     string   `json:"type"`
}

type DiffStat struct {
	Addition uint64 `json:"addition"`
	Deletion uint64 `json:"deletion"`
}

type DiffStatResponse struct {
	Stat         DiffStat `json:"stat"`
	FilesChanged uint64   `json:"files_changed"`
}

func GrabDiff(change object.Change) (res *Diff, err error) {
	patch, err := change.Patch()
	if err != nil {
		return nil, err
	}
	addition := 0
	deletion := 0
	stats := patch.Stats()

	for _, l := range stats {
		addition += l.Addition
		deletion += l.Deletion
	}

	var fileName, diffType string

	from, to, err := change.Files()
	if from != nil {
		if ok, _ := from.IsBinary(); ok {
			diffType = BINARY.String()
		}
		fileName = change.From.Name
	}
	if to != nil {
		if ok, _ := to.IsBinary(); ok {
			diffType = BINARY.String()
		}
		fileName = change.To.Name
	}

	if from == nil {
		diffType += ADD.String()
		if addition == 0 && deletion == 0 && diffType == ADD.String() {
			diffType = EMPTY.String()
		}
	} else if to == nil {
		diffType += DELETE.String()
	} else if from.Name == to.Name {
		diffType += MODIFY.String()
	} else if from.Name != to.Name {
		diffType += RENAME.String()
	}

	diffStat := DiffStat{
		Addition: uint64(addition),
		Deletion: uint64(deletion),
	}
	diff := Diff{
		FileName: fileName,
		Stat:     diffStat,
		Patch:    patch.String(),
		Type:     diffType,
	}
	return &diff, nil
}

func PaginateDiffResponse(
	changes object.Changes,
	pageRequest *PageRequest,
	defaultLimit uint64,
	onResult func(diff Diff) error,
) (*PageResponse, error) {

	totalDiffCount := uint64(changes.Len())

	// if the PageRequest is nil, use default PageRequest
	if pageRequest == nil {
		pageRequest = &PageRequest{}
	}

	offset := pageRequest.Offset
	key := pageRequest.Key
	limit := pageRequest.Limit
	countTotal := pageRequest.CountTotal

	if offset > 0 && key != nil {
		return nil, fmt.Errorf("invalid request, either offset or key is expected, got both")
	}

	if limit == 0 {
		limit = defaultLimit

		// show total issue count when the limit is zero/not supplied
		countTotal = true
	}

	if len(key) != 0 {

		var count uint64
		var nextKey []byte

		for i := BytesToUInt64(key); uint64(i) < totalDiffCount; i++ {
			if count == limit {
				nextKey = UInt64ToBytes(uint64(i))
				break
			}

			diff, err := GrabDiff(*changes[i])
			if err != nil {
				return nil, err
			}
			err = onResult(*diff)
			if err != nil {
				return nil, err
			}

			count++
		}

		return &PageResponse{
			NextKey: nextKey,
		}, nil
	}

	end := offset + limit

	var nextKey []byte

	for i := offset; uint64(i) < totalDiffCount; i++ {
		if uint64(i) < end {
			diff, err := GrabDiff(*changes[i])
			if err != nil {
				return nil, err
			}
			err = onResult(*diff)
			if err != nil {
				return nil, err
			}
		} else if uint64(i) == end+1 {
			nextKey = UInt64ToBytes(uint64(i))
			break
		}
	}

	res := &PageResponse{NextKey: nextKey}
	if countTotal {
		res.Total = totalDiffCount
	}

	return res, nil
}
