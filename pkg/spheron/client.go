package spheron

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
)

type SpheronClientInterface interface {
	UploadFileInChunks(filePath string) (FinalizeResponse, error)
}

type SpheronClient struct {
	HttpClient   *http.Client
	ApiToken     string
	BaseURL      string
	Organization string
	Bucket       string
	Protocol     string
}

type InitiateResponse struct {
	UploadID            string `json:"uploadId"`
	ParallelUploadCount int    `json:"parallelUploadCount"`
	PayloadSize         int    `json:"payloadSize"`
}

type FinalizeResponse struct {
	Success      bool     `json:"success"`
	Message      string   `json:"message"`
	UploadID     string   `json:"uploadId"`
	BucketID     string   `json:"bucketId"`
	ProtocolLink string   `json:"protocolLink"`
	DynamicLinks []string `json:"dynamicLinks"`
	CID          string   `json:"cid"`
}

func (c *SpheronClient) UploadFileInChunks(filePath string) (FinalizeResponse, error) {
	// Initiate a new upload
	sessionResponse, err := c.InitiateUpload()
	if err != nil {
		return FinalizeResponse{}, fmt.Errorf("failed to initiate upload: %w", err)
	}

	if err := c.UploadChunks(filePath, sessionResponse.UploadID, sessionResponse.PayloadSize, sessionResponse.ParallelUploadCount); err != nil {
		return FinalizeResponse{}, fmt.Errorf("failed to upload chunks: %w", err)
	}

	finalizeResponse, err := c.FinalizeUpload(sessionResponse.UploadID, "UPLOAD")
	if err != nil {
		return FinalizeResponse{}, fmt.Errorf("failed to finalize upload: %w", err)
	}

	return finalizeResponse, nil
}

func (c *SpheronClient) InitiateUpload() (InitiateResponse, error) {
	baseURL := fmt.Sprintf("%s/v1/upload/initiate?protocol=%s&bucket=%s&organization=%s", c.BaseURL, c.Protocol, c.Bucket, c.Organization)
	request, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return InitiateResponse{}, fmt.Errorf("failed to create initiate request: %w", err)
	}

	request.Header.Set("Authorization", "Bearer "+c.ApiToken)
	resp, err := c.HttpClient.Do(request)
	if err != nil {
		return InitiateResponse{}, fmt.Errorf("failed to initiate upload: %w", err)
	}
	defer resp.Body.Close()

	var response InitiateResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return InitiateResponse{}, fmt.Errorf("failed to decode initiate response: %w", err)
	}

	return response, nil
}

func (c *SpheronClient) UploadChunks(filePath string, uploadID string, payloadSize, parallelUploads int) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	fileSize := fileInfo.Size()
	// Calculate the total number of chunks
	totalChunks := int(math.Ceil(float64(fileSize) / float64(payloadSize)))

	chunksChan := make(chan int, totalChunks)
	errorsChan := make(chan error, totalChunks)

	// Generate chunk indexes
	for i := 0; i < totalChunks; i++ {
		chunksChan <- i
	}
	close(chunksChan)

	var wg sync.WaitGroup
	for i := 0; i < parallelUploads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunkIndex := range chunksChan {
				err := c.uploadChunk(file, uploadID, chunkIndex, payloadSize, totalChunks)
				errorsChan <- err
			}
		}()
	}

	wg.Wait()
	close(errorsChan)

	// Check for errors
	for err := range errorsChan {
		if err != nil {
			// Initiate cancellation of the upload due to an error
			c.FinalizeUpload(uploadID, "CANCEL")
			return fmt.Errorf("failed to upload chunk: %w", err)
		}
	}

	return nil
}

func (c *SpheronClient) uploadChunk(file *os.File, uploadID string, chunkIndex, payloadSize, totalChunks int) error {
	// Calculate the start position of the chunk in the file
	startPos := int64(chunkIndex) * int64(payloadSize)

	// Seek to the start position
	_, err := file.Seek(startPos, 0)
	if err != nil {
		return fmt.Errorf("failed to seek file: %w", err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	fileSize := fileInfo.Size()

	// Create a buffer to read the chunk into
	chunkSize := int(math.Min(float64(payloadSize), float64(fileSize-startPos)))
	buffer := make([]byte, chunkSize)

	// Read the chunk into the buffer
	_, err = file.Read(buffer)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read chunk: %w", err)
	}

	// Prepare the multipart request
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Create form file
	part, err := writer.CreateFormFile(fmt.Sprintf("chunk-%d-%d", chunkIndex, totalChunks), "filename")
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	// Write the chunk to the form
	_, err = part.Write(buffer)
	if err != nil {
		return fmt.Errorf("failed to write chunk to form: %w", err)
	}

	// Close the writer to finalize the multipart form data
	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close writer: %w", err)
	}

	// Upload the chunk
	uploadURL := fmt.Sprintf("%s/v1/upload/%s/data", c.BaseURL, uploadID)
	request, err := http.NewRequest("POST", uploadURL, &requestBody)
	if err != nil {
		return fmt.Errorf("failed to create request for chunk upload: %w", err)
	}

	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+c.ApiToken)

	resp, err := c.HttpClient.Do(request)
	if err != nil {
		return fmt.Errorf("failed to upload chunk: %w", err)
	}
	defer resp.Body.Close()

	var uploadResponse struct {
		UploadSize int `json:"uploadSize"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&uploadResponse); err != nil {
		return fmt.Errorf("failed to decode upload response: %w", err)
	}

	return nil
}

func (c *SpheronClient) FinalizeUpload(uploadID, action string) (FinalizeResponse, error) {
	baseURL := fmt.Sprintf("%s/v1/upload/%s/finish?action=%s", c.BaseURL, uploadID, action)
	request, err := http.NewRequest("POST", baseURL, nil)
	if err != nil {
		return FinalizeResponse{}, fmt.Errorf("failed to create finalize request: %w", err)
	}

	request.Header.Set("Authorization", "Bearer "+c.ApiToken)
	resp, err := c.HttpClient.Do(request)
	if err != nil {
		return FinalizeResponse{}, fmt.Errorf("failed to finalize upload: %w", err)
	}
	defer resp.Body.Close()

	var response FinalizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return FinalizeResponse{}, fmt.Errorf("failed to decode finalize response: %w", err)
	}

	return response, nil
}

func (c *SpheronClient) UploadFile(filePath string) (FinalizeResponse, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return FinalizeResponse{}, err
	}
	defer file.Close()

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	baseFileName := filepath.Base(filePath)
	part, err := writer.CreateFormFile("filekey", baseFileName)
	if err != nil {
		return FinalizeResponse{}, err
	}

	if _, err := io.Copy(part, file); err != nil {
		return FinalizeResponse{}, err
	}

	if err := writer.Close(); err != nil {
		return FinalizeResponse{}, err
	}

	parsedURL, err := url.Parse(c.BaseURL + "/v1/upload")
	if err != nil {
		return FinalizeResponse{}, err
	}

	params := url.Values{}
	params.Add("organization", c.Organization)
	params.Add("bucket", c.Bucket)
	params.Add("protocol", c.Protocol)
	parsedURL.RawQuery = params.Encode()

	request, err := http.NewRequest("POST", parsedURL.String(), &requestBody)
	if err != nil {
		return FinalizeResponse{}, err
	}

	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+c.ApiToken)

	resp, err := c.HttpClient.Do(request)
	if err != nil {
		return FinalizeResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return FinalizeResponse{}, err
	}

	var response FinalizeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return FinalizeResponse{}, err
	}

	return response, nil
}
