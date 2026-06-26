package infra

import (
	"context"
	"fmt"
	"io"
	"time"

	appconfig "github.com/Yankzy/usetoro/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Service defines the interface for our S3 operations
type S3Service interface {
	UploadFileToS3(ctx context.Context, key string, body io.Reader, contentType string) error
	DownloadS3File(ctx context.Context, key string) (io.ReadCloser, error)
	GeneratePresignedURL(ctx context.Context, key string, expiresIn time.Duration) (string, error)
}

type s3Service struct {
	client     *s3.Client
	presignCli *s3.PresignClient
	bucketName string
}

// NewS3Service creates a new AWS S3 service using credentials from the app config
func NewS3Service(cfg *appconfig.Config) (S3Service, error) {
	if cfg.AWSAccessKeyID == "" || cfg.AWSSecretAccessKey == "" || cfg.AWSS3BucketName == "" || cfg.AWSS3RegionName == "" {
		return nil, fmt.Errorf("AWS S3 credentials and bucket configuration are required")
	}

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.AWSS3RegionName),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AWSAccessKeyID,
			cfg.AWSSecretAccessKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)
	presignClient := s3.NewPresignClient(client)

	return &s3Service{
		client:     client,
		presignCli: presignClient,
		bucketName: cfg.AWSS3BucketName,
	}, nil
}

// UploadFile uploads a stream of data to an S3 bucket
func (s *s3Service) UploadFileToS3(ctx context.Context, key string, body io.Reader, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	}

	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to upload file to S3: %w", err)
	}

	return nil
}

// DownloadFile downloads a file from an S3 bucket
func (s *s3Service) DownloadS3File(ctx context.Context, key string) (io.ReadCloser, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	}

	result, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to download file from S3: %w", err)
	}

	return result.Body, nil
}

// GeneratePresignedURL generates a temporary, cryptographically secure public URL for an S3 object
func (s *s3Service) GeneratePresignedURL(ctx context.Context, key string, expiresIn time.Duration) (string, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	}

	presignedReq, err := s.presignCli.PresignGetObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = expiresIn
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return presignedReq.URL, nil
}
