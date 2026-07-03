package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/cespare/xxhash/v2"

	"github.com/rishang/seek/config"
)

// S3Store caches entries as objects in an S3-compatible bucket. Each entry's
// content is the object body; the origin URL, format, write time, and TTL ride
// along as user metadata. Expiry is lazy: a Get past the TTL deletes the object
// and reports a miss (a bucket lifecycle rule is the recommended backstop).
//
// It is safe for concurrent use — the underlying s3.Client is concurrency-safe.
type S3Store struct {
	client *s3.Client
	bucket string
}

// object metadata keys (stored under the x-amz-meta- namespace by S3).
const (
	metaURL       = "seek-url"
	metaFormat    = "seek-format"
	metaCreatedAt = "seek-created-at" // unix seconds
	metaTTL       = "seek-ttl"        // seconds
)

// OpenS3 builds an S3-backed Store from cfg. It does not verify connectivity;
// the first Get/Set surfaces any credential or endpoint error.
func OpenS3(cfg config.S3Config) (*S3Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("cache: s3 bucket is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("cache: s3 access_key and secret_key are required")
	}

	opts := []func(*s3.Options){
		func(o *s3.Options) {
			o.Region = orDefault(cfg.Region, "us-east-1")
			o.Credentials = credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")
		},
	}
	if cfg.Endpoint != "" {
		// Custom endpoints (MinIO, RustFS, ...) need path-style addressing;
		// virtual-host style assumes bucket.<endpoint> DNS, which they lack.
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		})
	}

	return &S3Store{client: s3.New(s3.Options{}, opts...), bucket: cfg.Bucket}, nil
}

// objectKey derives a stable object name from the cache key. The op/provider/
// format prefix namespaces the object (mirroring the SQLite composite primary
// key), so within a prefix only the URL varies — hashing the URL alone yields a
// collision-free name.
func objectKey(k Key) string {
	return fmt.Sprintf("seek/%s/%s/%s/%016x",
		orDefault(k.Op, "_"), orDefault(k.Provider, "_"), orDefault(k.Format, "_"),
		xxhash.Sum64String(k.URL))
}

func (s *S3Store) Get(ctx context.Context, k Key) (Entry, bool, error) {
	name := objectKey(k)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(name),
	})
	if err != nil {
		if isNotFound(err) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("cache: s3 get: %w", err)
	}
	defer out.Body.Close()

	// Expiry check from metadata; a malformed/absent timestamp is treated as
	// fresh so we never discard an otherwise valid object over bad metadata.
	if created, ok := atoi(out.Metadata[metaCreatedAt]); ok {
		ttl := time.Duration(atoiOr(out.Metadata[metaTTL], int64(DefaultTTL.Seconds()))) * time.Second
		if time.Since(time.Unix(created, 0)) > ttl {
			_ = s.delete(ctx, name)
			return Entry{}, false, nil
		}
	}

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return Entry{}, false, fmt.Errorf("cache: s3 read body: %w", err)
	}
	return Entry{Content: string(body), Format: out.Metadata[metaFormat]}, true, nil
}

func (s *S3Store) Set(ctx context.Context, k Key, content string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectKey(k)),
		Body:   strings.NewReader(content),
		Metadata: map[string]string{
			metaURL:       k.URL,
			metaFormat:    k.Format,
			metaCreatedAt: strconv.FormatInt(time.Now().Unix(), 10),
			metaTTL:       strconv.FormatInt(int64(ttl.Seconds()), 10),
		},
	})
	if err != nil {
		return fmt.Errorf("cache: s3 set: %w", err)
	}
	return nil
}

func (s *S3Store) delete(ctx context.Context, name string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(name),
	})
	return err
}

// Close is a no-op: the S3 client holds no persistent connection to release.
func (s *S3Store) Close() error { return nil }

// isNotFound reports whether err is an S3 "object does not exist" error, across
// the NoSuchKey and bare 404 (NotFound) shapes different servers return.
func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	return false
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func atoi(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func atoiOr(s string, fallback int64) int64 {
	if n, ok := atoi(s); ok {
		return n
	}
	return fallback
}
