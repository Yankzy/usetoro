package infra

import (
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestNewS3Service_MissingConfig(t *testing.T) {
	cfg := &config.Config{
		AWSAccessKeyID:     "",
		AWSSecretAccessKey: "",
		AWSS3BucketName:    "",
		AWSS3RegionName:    "",
	}

	svc, err := NewS3Service(cfg)
	assert.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "AWS S3 credentials and bucket configuration are required")
}

func TestNewS3Service_ValidConfig(t *testing.T) {
	cfg := &config.Config{
		AWSAccessKeyID:     "dummy-access-key",
		AWSSecretAccessKey: "dummy-secret-key",
		AWSS3BucketName:    "dummy-bucket",
		AWSS3RegionName:    "us-east-1",
	}

	svc, err := NewS3Service(cfg)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
}
