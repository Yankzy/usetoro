package ses

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/config"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// SESService defines the core interface for SES operations
type SESService interface {
	// Identity methods
	GenerateDKIMKeypair() (privateKeyPEM, publicKeyPEM []byte, err error)
	RegisterDomain(ctx context.Context, domainName string, privateKeyPEM []byte) error
	GetDNSRecords(domainName string, publicKeyPEM []byte) []string

	// Inbound methods
	ParseInboundPayload(payload []byte) (*InboundEmail, error)
	
	// Outbound methods
	SendAgentReply(ctx context.Context, req SendAgentReplyRequest) error
}

type sesService struct {
	client *sesv2.Client
	cfg    *config.Config
}

// NewSESService creates a new AWS SES v2 service
func NewSESService(cfg *config.Config) (SESService, error) {
	if cfg.AWSAccessKeyID == "" || cfg.AWSSecretAccessKey == "" || cfg.AWSS3RegionName == "" {
		return nil, fmt.Errorf("AWS credentials missing for SES")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithRegion(cfg.AWSS3RegionName), // We can reuse the same region
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AWSAccessKeyID,
			cfg.AWSSecretAccessKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config for SES: %w", err)
	}

	return &sesService{
		client: sesv2.NewFromConfig(awsCfg),
		cfg:    cfg,
	}, nil
}
