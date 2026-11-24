// Package storage menyediakan klien S3 (Garage) untuk penyimpanan objek.
package storage

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	cfgpkg "github.com/nyxsr/meme-portal/api/internal/config"
)

func NewStorage(ctx context.Context, cfg cfgpkg.StorageConfig) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: gagal memuat konfigurasi aws: %w", err)
	}

	scheme := "http"
	if cfg.UseSSL {
		scheme = "https"
	}

	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(scheme + "://" + cfg.Endpoint)
		o.UsePathStyle = true
		// Garage (S3-compatible) tidak mengenal checksum tambahan bawaan SDK;
		// tanpa ini presigned PUT URL memuat parameter checksum dan ditolak.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		o.HTTPClient = &http.Client{}
	}), nil
}
