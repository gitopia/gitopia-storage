package utils

import (
	"fmt"
	"strings"

	"github.com/gitopia/go-git/v5/plumbing/object"
	"github.com/gitopia/go-git/v5/plumbing/storer"
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

func PaginateTreeCommitsResponse(
	commitIter object.CommitIter,
	pageRequest *PageRequest,
	defaultLimit uint64,
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

	if len(key) != 0 {

		var count uint64
		var nextKey []byte

		start := BytesToUInt64(key)
		end := start + limit - 1

		commitIter.ForEach(func(commitObj *object.Commit) error {
			count++

			if count < start {
				return nil
			}

			if count <= end {
				commit, err := GrabCommit(*commitObj)
				if err != nil {
					return err
				}
				err = onResult(*commit)
				if err != nil {
					return err
				}
			} else if count == end+1 {
				nextKey = UInt64ToBytes(uint64(count))

				if !countTotal {
					return storer.ErrStop
				}
			}
			return nil
		})

		res := &PageResponse{NextKey: nextKey}
		if countTotal {
			res.Total = count
		}

		return res, nil
	}

	end := offset + limit

	var nextKey []byte
	var count uint64

	commitIter.ForEach(func(commitObj *object.Commit) error {
		count++

		if count <= offset {
			return nil
		}

		if count <= end {
			commit, err := GrabCommit(*commitObj)
			if err != nil {
				return err
			}
			err = onResult(*commit)
			if err != nil {
				return err
			}
		} else if count == end+1 {
			nextKey = UInt64ToBytes(uint64(count))

			if !countTotal {
				return storer.ErrStop
			}
		}
		return nil
	})

	res := &PageResponse{NextKey: nextKey}
	if countTotal {
		res.Total = count
	}

	return res, nil
}

func PaginatePathTreeCommitsResponse(
	commitIter object.CommitIter,
	pageRequest *PageRequest,
	defaultLimit uint64,
	path string,
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

	if len(key) != 0 {

		var count uint64
		var nextKey []byte

		start := BytesToUInt64(key)
		end := start + limit - 1

		commitIter.ForEach(func(commitObj *object.Commit) error {

			if commitObj.NumParents() > 1 {
				return nil
			}

			commitTree, err := commitObj.Tree()
			if err != nil {
				return err
			}

			parentCommit, err := commitObj.Parent(0)
			if err != nil {
				_, err := commitTree.FindEntry(path)
				if err != nil {
					return nil
				}

				count++
				if count < start {
					return nil
				}

				if count <= end {
					commit, err := GrabCommit(*commitObj)
					if err != nil {
						return err
					}
					err = onResult(*commit)
					if err != nil {
						return err
					}
				} else if count == end+1 {
					nextKey = UInt64ToBytes(uint64(count))
				}
				return storer.ErrStop
			}

			parentCommitTree, err := parentCommit.Tree()
			if err != nil {
				return err
			}

			currentCommitTreeEntry, currentCommitTreeEntryErr := commitTree.FindEntry(path)
			if currentCommitTreeEntryErr != nil {
				return storer.ErrStop
			}
			parentCommitTreeEntry, parentCommitTreeEntryErr := parentCommitTree.FindEntry(path)
			if parentCommitTreeEntryErr != nil {
				count++
				if count < start {
					return nil
				}

				if count <= end {
					commit, err := GrabCommit(*commitObj)
					if err != nil {
						return err
					}
					err = onResult(*commit)
					if err != nil {
						return err
					}
				} else if count == end+1 {
					nextKey = UInt64ToBytes(uint64(count))
				}
				return storer.ErrStop
			}

			if currentCommitTreeEntry.Hash != parentCommitTreeEntry.Hash {
				count++
				if count < start {
					return nil
				}

				if count <= end {
					commit, err := GrabCommit(*commitObj)
					if err != nil {
						return err
					}
					err = onResult(*commit)
					if err != nil {
						return err
					}
				} else if count == end+1 {
					nextKey = UInt64ToBytes(uint64(count))

					if !countTotal {
						return storer.ErrStop
					}
				}
			}

			return nil
		})

		res := &PageResponse{NextKey: nextKey}
		if countTotal {
			res.Total = count
		}

		return res, nil
	}

	end := offset + limit

	var nextKey []byte
	var count uint64

	commitIter.ForEach(func(commitObj *object.Commit) error {

		if commitObj.NumParents() > 1 {
			return nil
		}

		commitTree, err := commitObj.Tree()
		if err != nil {
			return err
		}

		parentCommit, err := commitObj.Parent(0)
		if err != nil {
			_, err := commitTree.FindEntry(path)
			if err != nil {
				return nil
			}

			count++
			if count <= offset {
				return nil
			}

			if count <= end {
				commit, err := GrabCommit(*commitObj)
				if err != nil {
					return err
				}
				err = onResult(*commit)
				if err != nil {
					return err
				}
			} else if count == end+1 {
				nextKey = UInt64ToBytes(uint64(count))
			}
			return storer.ErrStop
		}

		parentCommitTree, err := parentCommit.Tree()
		if err != nil {
			return err
		}

		currentCommitTreeEntry, currentCommitTreeEntryErr := commitTree.FindEntry(path)
		if currentCommitTreeEntryErr != nil {
			return storer.ErrStop
		}
		parentCommitTreeEntry, parentCommitTreeEntryErr := parentCommitTree.FindEntry(path)
		if parentCommitTreeEntryErr != nil {
			count++
			if count <= offset {
				return nil
			}

			if count <= end {
				commit, err := GrabCommit(*commitObj)
				if err != nil {
					return err
				}
				err = onResult(*commit)
				if err != nil {
					return err
				}
			} else if count == end+1 {
				nextKey = UInt64ToBytes(uint64(count))
			}
			return storer.ErrStop
		}

		if currentCommitTreeEntry.Hash != parentCommitTreeEntry.Hash {
			count++
			if count <= offset {
				return nil
			}

			if count <= end {
				commit, err := GrabCommit(*commitObj)
				if err != nil {
					return err
				}
				err = onResult(*commit)
				if err != nil {
					return err
				}
			} else if count == end+1 {
				nextKey = UInt64ToBytes(uint64(count))

				if !countTotal {
					return storer.ErrStop
				}
			}
		}

		return nil
	})

	res := &PageResponse{NextKey: nextKey}
	if countTotal {
		res.Total = count
	}

	return res, nil
}
