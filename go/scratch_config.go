package main

import (
	"fmt"

	"github.com/Yankzy/usetoro/internal/config"
)

func main() {
	cfg, _, err := config.Load()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}
	fmt.Printf("AWS_ACCESS_KEY_ID: '%s'\n", cfg.AWSAccessKeyID)
	fmt.Printf("AWS_SECRET_ACCESS_KEY: '%s'\n", cfg.AWSSecretAccessKey)
	fmt.Printf("AWS_S3_BUCKET_NAME: '%s'\n", cfg.AWSS3BucketName)
	fmt.Printf("AWS_S3_REGION_NAME: '%s'\n", cfg.AWSS3RegionName)
}
