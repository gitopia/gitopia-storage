package utils

type PageRequest struct {
	Key        []byte `json:"key,omitempty"`
	Offset     uint64 `json:"offset,omitempty"`
	Limit      uint64 `json:"limit,omitempty"`
	CountTotal bool   `json:"count_total,omitempty"`
	Reverse    bool   `json:"reverse,omitempty"`
}

type PageResponse struct {
	NextKey []byte `json:"next_key,omitempty"`
	Total   uint64 `json:"total,omitempty"`
}
