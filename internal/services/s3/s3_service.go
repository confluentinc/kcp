package s3

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Service struct {
	client *s3.Client
}

func NewS3Service(client *s3.Client) *S3Service {
	return &S3Service{client: client}
}

func (s *S3Service) ParseS3URI(s3Uri string) (string, string, error) {
	if !strings.HasPrefix(s3Uri, "s3://") {
		return "", "", fmt.Errorf("invalid S3 URI: must start with 's3://'")
	}

	uriPath := strings.TrimPrefix(s3Uri, "s3://")

	parts := strings.SplitN(uriPath, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		return "", "", fmt.Errorf("invalid S3 URI: missing bucket name")
	}

	bucket := parts[0]
	prefix := ""
	if len(parts) > 1 {
		prefix = parts[1]
	}

	return bucket, prefix, nil
}

func (s *S3Service) ListLogFiles(ctx context.Context, bucket, prefix string) ([]string, error) {
	var logFiles []string

	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	input := &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	}

	paginator := s3.NewListObjectsV2Paginator(s.client, input)

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range page.Contents {
			if obj.Key != nil && strings.HasSuffix(*obj.Key, ".log.gz") {
				logFiles = append(logFiles, *obj.Key)
			}
		}
	}

	return logFiles, nil
}

// DownloadAndDecompressLogFile downloads and decompresses a log file from S3
func (s *S3Service) DownloadAndDecompressLogFile(ctx context.Context, bucket, key string) ([]byte, error) {
	input := &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}

	result, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to download file %s: %w", key, err)
	}
	defer result.Body.Close()

	// Decompress the gzipped content
	gzipReader, err := gzip.NewReader(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader for %s: %w", key, err)
	}
	defer gzipReader.Close()

	content, err := io.ReadAll(gzipReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read decompressed content from %s: %w", key, err)
	}

	return content, nil
}
