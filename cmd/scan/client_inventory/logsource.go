package client_inventory

import "context"

// LogChunk is one discrete unit of broker-log data to read: an S3 object key,
// or a CloudWatch time window. Source-neutral so the scanner can treat every
// source the same way.
type LogChunk struct {
	// ID identifies the chunk to its own source (an S3 object key for S3, the
	// log group for CloudWatch). Opaque to the scanner.
	ID string
}

// LogSource abstracts WHERE broker log lines come from. Both the S3 path
// (s3LogSource) and the CloudWatch Logs path (cloudWatchLogSource) implement
// it; everything downstream — the KafkaApiTracePattern gate, the
// KafkaApiTraceLineParser, composite-key dedup, and state upsert — is
// source-agnostic and unchanged.
type LogSource interface {
	// Name is a human-readable label for the source, used in log/terminal output.
	Name() string
	// ListChunks enumerates the units of log data to read.
	ListChunks(ctx context.Context) ([]LogChunk, error)
	// ReadChunk returns the raw broker log bytes for one chunk, newline-delimited,
	// ready to feed to the gate + parser exactly as an S3 log file would be.
	ReadChunk(ctx context.Context, c LogChunk) ([]byte, error)
}

// --- S3 implementation: wraps the existing S3Service, preserving the S3 path ---

type s3LogSource struct {
	svc    S3Service
	bucket string
	prefix string
	uri    string
}

// NewS3LogSource adapts the existing S3Service to the LogSource interface. The
// S3 behaviour (list .log.gz objects under a prefix, download + gunzip each) is
// unchanged — this only re-expresses it as chunks.
func NewS3LogSource(svc S3Service, s3Uri string) (*s3LogSource, error) {
	bucket, prefix, err := svc.ParseS3URI(s3Uri)
	if err != nil {
		return nil, err
	}
	return &s3LogSource{svc: svc, bucket: bucket, prefix: prefix, uri: s3Uri}, nil
}

func (s *s3LogSource) Name() string { return s.uri }

func (s *s3LogSource) ListChunks(ctx context.Context) ([]LogChunk, error) {
	keys, err := s.svc.ListLogFiles(ctx, s.bucket, s.prefix)
	if err != nil {
		return nil, err
	}
	chunks := make([]LogChunk, 0, len(keys))
	for _, k := range keys {
		chunks = append(chunks, LogChunk{ID: k})
	}
	return chunks, nil
}

func (s *s3LogSource) ReadChunk(ctx context.Context, c LogChunk) ([]byte, error) {
	return s.svc.DownloadAndDecompressLogFile(ctx, s.bucket, c.ID)
}

// Compile-time proof s3LogSource satisfies LogSource.
var _ LogSource = (*s3LogSource)(nil)
