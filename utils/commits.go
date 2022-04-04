package utils

import (
	"fmt"
	"strconv"
	"strings"

	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/object"
)

type CommitsRequestBody struct {
	RepositoryID uint64       `json:"repository_id"`
	InitCommitId string       `json:"init_commit_id"`
	Path         string       `json:"path"`
	Pagination   *PageRequest `json:"pagination"`
}

type CommitAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type CommitCommitter struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type CommitsResponse struct {
	Commits    []*Commit     `json:"commits,omitempty"`
	Pagination *PageResponse `json:"pagination,omitempty"`
}

type Commit struct {
	Id        string           `json:"id"`
	Title     string           `json:"title"`
	Author    *CommitAuthor    `json:"author"`
	Committer *CommitCommitter `json:"committer"`
	CreatedAt int64            `json:"created_at"`
	Message   string           `json:"message"`
	Tree      string           `json:"tree"`
	ParentIds []string         `json:"parent_ids"`
}

func GrabCommit(commitObj object.Commit) (res *Commit, err error) {
	var parentIds []string
	var title string
	var message string
	for _, hash := range commitObj.ParentHashes {
		parentIds = append(parentIds, hash.String())
	}

	i := strings.Index(commitObj.Message, "\n")
	if i == -1 {
		title = commitObj.Message
	} else {
		title = commitObj.Message[:i]
		message = commitObj.Message[i+1:]
	}
	commit := Commit{
		Id:    commitObj.Hash.String(),
		Title: title,
		Author: &CommitAuthor{
			Name:  commitObj.Author.Name,
			Email: commitObj.Author.Email,
			Date:  commitObj.Author.When.String(),
		},
		Committer: &CommitCommitter{
			Name:  commitObj.Committer.Name,
			Email: commitObj.Committer.Email,
			Date:  commitObj.Committer.When.String(),
		},
		CreatedAt: commitObj.Committer.When.Unix(),
		Message:   message,
		Tree:      commitObj.TreeHash.String(),
		ParentIds: parentIds,
	}
	return &commit, nil
}

func PaginateCommitHistoryResponse(
	repoPath string,
	repo *git.Repository,
	pageRequest *PageRequest,
	defaultLimit uint64,
	body *CommitsRequestBody,
	onResult func(commit Commit) error,
) (*PageResponse, error) {

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
	}

	totalCommits, err := CountCommits(repoPath, body.InitCommitId, body.Path)
	if err != nil {
		return nil, err
	}
	count, err := strconv.ParseUint(string(totalCommits), 10, 64)
	if err != nil {
		return nil, err
	}

	if len(key) != 0 {
		var nextKey []byte

		start := BytesToUInt64(key)
		shown := start + limit

		commitHashes, err := CommitHistory(repoPath, body.InitCommitId, body.Path, int(start), int(limit))
		if err != nil {
			return nil, err
		}

		for _, commitHash := range commitHashes {
			commitHash := plumbing.NewHash(commitHash)
			commitObj, err := object.GetCommit(repo.Storer, commitHash)
			if err != nil {
				return nil, err
			}
			commit, err := GrabCommit(*commitObj)
			if err != nil {
				return nil, err
			}
			err = onResult(*commit)
			if err != nil {
				return nil, err
			}
		}

		if count > shown {
			nextKey = UInt64ToBytes(uint64(shown))
		}
		res := &PageResponse{NextKey: nextKey}
		if countTotal {
			res.Total = uint64(count)
		}

		return res, nil
	}

	shown := offset + limit

	var nextKey []byte

	commitHashes, err := CommitHistory(repoPath, body.InitCommitId, body.Path, int(offset), int(limit))
	if err != nil {
		return nil, err
	}

	for _, commitHash := range commitHashes {
		commitHash := plumbing.NewHash(commitHash)
		commitObj, err := object.GetCommit(repo.Storer, commitHash)
		if err != nil {
			return nil, err
		}
		commit, err := GrabCommit(*commitObj)
		if err != nil {
			return nil, err
		}
		err = onResult(*commit)
		if err != nil {
			return nil, err
		}
	}

	if count > shown {
		nextKey = UInt64ToBytes(uint64(shown))
	}
	res := &PageResponse{NextKey: nextKey}
	if countTotal {
		res.Total = uint64(count)
	}

	return res, nil
}
