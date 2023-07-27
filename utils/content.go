package utils

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"

	"github.com/gitopia/go-git/v5/plumbing/object"
)

type ContentType int

const (
	TREE ContentType = iota
	BLOB
)

func (c ContentType) String() string {
	return [...]string{"TREE", "BLOB"}[c]
}

type ContentRequestBody struct {
	RepositoryID      uint64       `json:"repository_id"`
	RefId             string       `json:"ref_id"`
	Path              string       `json:"path"`
	IncludeLastCommit bool         `json:"include_last_commit"`
	From              uint64       `json:"from"`
	To                uint64       `json:"to"`
	Pagination        *PageRequest `json:"pagination"`
	NoRestriction     bool         `json:"no_restriction"`
}

type ContentResponse struct {
	Content    []*Content    `json:"content,omitempty"`
	Pagination *PageResponse `json:"pagination,omitempty"`
}

type Content struct {
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	Sha        string  `json:"sha"`
	Type       string  `json:"type"`
	Size       int64   `json:"size,omitempty"`
	Content    string  `json:"content,omitempty"`
	Encoding   string  `json:"encoding,omitempty"`
	LastCommit *Commit `json:"last_commit,omitempty"`
}

func GrabFileContent(blob object.Blob, treeEntry object.TreeEntry, pathPrefix string, noRestriction bool) (res *Content, err error) {
	var encodedData, encoding string
	if blob.Size < 1000000 || noRestriction {
		blobReader, err := blob.Reader()
		if err != nil {
			return nil, err
		}
		blobReader.Close()
		data, err := ioutil.ReadAll(blobReader)
		encodedData = base64.StdEncoding.EncodeToString(data)
		encoding = "base64"
	}

	fileContent := Content{
		Name: treeEntry.Name,
		Path: func() string {
			if pathPrefix == "" {
				return treeEntry.Name
			} else {
				return pathPrefix
			}
		}(),
		Sha:      treeEntry.Hash.String(),
		Type:     BLOB.String(),
		Size:     blob.Size,
		Content:  encodedData,
		Encoding: encoding,
	}
	return &fileContent, nil
}

func GrabTreeContent(treeEntry object.TreeEntry, pathPrefix string) (res *Content, err error) {
	treeContent := Content{
		Name: treeEntry.Name,
		Path: func() string {
			if pathPrefix == "" {
				return treeEntry.Name
			} else {
				return pathPrefix + "/" + treeEntry.Name
			}
		}(),
		Sha: treeEntry.Hash.String(),
		Type: func() string {
			if treeEntry.Mode.IsFile() {
				return BLOB.String()
			} else {
				return TREE.String()
			}
		}(),
	}
	return &treeContent, nil
}

func PaginateTreeContentResponse(
	tree *object.Tree,
	pageRequest *PageRequest,
	defaultLimit uint64,
	pathPrefix string,
	onResult func(treeContent Content) error,
) (*PageResponse, error) {

	totalTreeEntriesCount := uint64(len(tree.Entries))

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

		for i := BytesToUInt64(key); uint64(i) < totalTreeEntriesCount; i++ {
			if count == limit {
				nextKey = UInt64ToBytes(uint64(i))
				break
			}

			treeEntry, err := GrabTreeContent(tree.Entries[i], pathPrefix)
			if err != nil {
				return nil, err
			}
			err = onResult(*treeEntry)
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

	for i := offset; uint64(i) < totalTreeEntriesCount; i++ {
		if uint64(i) < end {
			diff, err := GrabTreeContent(tree.Entries[i], pathPrefix)
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
		res.Total = totalTreeEntriesCount
	}

	return res, nil
}
