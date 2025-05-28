package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"

	"github.com/pkg/errors"
)

type PinataClient struct {
	jwtToken string
	client   *http.Client
}

type PinataResponse struct {
	Data struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Cid           string `json:"cid"`
		Size          int    `json:"size"`
		NumberOfFiles int    `json:"number_of_files"`
		MimeType      string `json:"mime_type"`
		GroupID       any    `json:"group_id"`
	} `json:"data"`
}

type PinataListResponse struct {
	Files []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"files"`
}

func NewPinataClient(jwtToken string) *PinataClient {
	return &PinataClient{
		jwtToken: jwtToken,
		client:   &http.Client{},
	}
}

func (p *PinataClient) PinFile(ctx context.Context, filePath, name string) (*PinataResponse, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open file")
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create form file")
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return nil, errors.Wrap(err, "failed to copy file content")
	}

	err = writer.WriteField("network", "public")
	if err != nil {
		return nil, errors.Wrap(err, "failed to write network field")
	}

	err = writer.Close()
	if err != nil {
		return nil, errors.Wrap(err, "failed to close writer")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://uploads.pinata.cloud/v3/files", body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.jwtToken))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("pinata request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var pinataResp PinataResponse
	if err := json.NewDecoder(resp.Body).Decode(&pinataResp); err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}

	return &pinataResp, nil
}

func (p *PinataClient) UnpinFile(ctx context.Context, fileName string) error {
	// First list files to get the file ID
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://api.pinata.cloud/v3/files?name=%s", fileName), nil)
	if err != nil {
		return errors.Wrap(err, "failed to create list request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.jwtToken))

	resp, err := p.client.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send list request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return errors.Errorf("pinata list request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var listResp PinataListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return errors.Wrap(err, "failed to decode list response")
	}

	if len(listResp.Files) == 0 {
		return errors.New("no file found with the given name")
	}

	// Delete the file using its ID
	deleteReq, err := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("https://api.pinata.cloud/v3/files/%s", listResp.Files[0].ID), nil)
	if err != nil {
		return errors.Wrap(err, "failed to create delete request")
	}

	deleteReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.jwtToken))

	deleteResp, err := p.client.Do(deleteReq)
	if err != nil {
		return errors.Wrap(err, "failed to send delete request")
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(deleteResp.Body)
		return errors.Errorf("pinata delete request failed with status %d: %s", deleteResp.StatusCode, string(bodyBytes))
	}

	return nil
}
