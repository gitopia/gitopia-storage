package storage

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/pkg/errors"
)

type FilebaseProvider struct {
	s3Client *s3.S3
	bucket   string
}

func NewFilebaseProvider(accessKey, secretKey, bucket, region, endpoint string) (*FilebaseProvider, error) {
	// Create AWS config for Filebase
	s3Config := &aws.Config{
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
		Endpoint:         aws.String(endpoint),
		Region:           aws.String(region),
		S3ForcePathStyle: aws.Bool(true),
	}

	// Create session
	sess, err := session.NewSessionWithOptions(session.Options{
		Config: *s3Config,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create AWS session")
	}

	// Create S3 client
	s3Client := s3.New(sess)

	return &FilebaseProvider{
		s3Client: s3Client,
		bucket:   bucket,
	}, nil
}

func (f *FilebaseProvider) Name() string {
	return "filebase"
}

func (f *FilebaseProvider) PinFile(ctx context.Context, filePath, name string) (*PinResponse, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open file")
	}
	defer file.Close()

	// Get file info for size
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get file info")
	}

	// Create put object input
	putObjectInput := &s3.PutObjectInput{
		Body:   file,
		Bucket: aws.String(f.bucket),
		Key:    aws.String(name),
	}

	// Upload file
	result, err := f.s3Client.PutObjectWithContext(ctx, putObjectInput)
	if err != nil {
		return nil, errors.Wrap(err, "failed to upload file to Filebase")
	}

	// Generate a unique ID from the ETag (remove quotes)
	id := ""
	if result.ETag != nil {
		id = *result.ETag
		if len(id) > 2 && id[0] == '"' && id[len(id)-1] == '"' {
			id = id[1 : len(id)-1]
		}
	}

	return &PinResponse{
		ID:   id,
		Name: name,
		Size: int(fileInfo.Size()),
	}, nil
}

func (f *FilebaseProvider) UnpinFile(ctx context.Context, name string) error {
	// Create delete object input
	deleteObjectInput := &s3.DeleteObjectInput{
		Bucket: aws.String(f.bucket),
		Key:    aws.String(name),
	}

	// Delete file
	_, err := f.s3Client.DeleteObjectWithContext(ctx, deleteObjectInput)
	if err != nil {
		return errors.Wrap(err, "failed to delete file from Filebase")
	}

	return nil
}
