package utils

type PullDiffRequestBody struct {
	BaseRepositoryID uint64       `json:"base_repository_id"`
	HeadRepositoryID uint64       `json:"head_repository_id"`
	BaseCommitSha    string       `json:"base_commit_sha"`
	HeadCommitSha    string       `json:"head_commit_sha"`
	OnlyStat         bool         `json:"only_stat"`
	Pagination       *PageRequest `json:"pagination"`
}
