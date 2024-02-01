package utils

type ApiResponse struct {
	Success      bool     `json:"success"`
	Message      string   `json:"message"`
	UploadId     string   `json:"uploadId"`
	BucketId     string   `json:"bucketId"`
	ProtocolLink string   `json:"protocolLink"`
	DynamicLinks []string `json:"dynamicLinks"`
	CID          string   `json:"cid"`
}
