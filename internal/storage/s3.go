package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3Backend stores the same relative object layout as LocalBackend under a
// bucket prefix. S3 keys have no real directories to create or prune.
type S3Backend struct {
	client *s3.Client
	bucket string
	prefix string // Empty at bucket root, otherwise ends in exactly one slash.
}

func IsS3Archive(location string) bool {
	return strings.HasPrefix(strings.ToLower(location), "s3:")
}

// ParseS3Archive rejects ambiguous locations instead of silently cleaning them
// into a different archive. A trailing slash and a bucket-root archive are valid.
func ParseS3Archive(location string) (bucket, prefix string, err error) {
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "s3" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(u.Host, ":\\") {
		return "", "", fmt.Errorf("archive must be s3://bucket/prefix without credentials, query, or fragment")
	}
	for _, c := range u.Host {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.') {
			return "", "", fmt.Errorf("invalid S3 bucket name")
		}
	}
	if strings.Contains(u.Path, "//") {
		return "", "", fmt.Errorf("S3 prefix must not contain empty components")
	}
	prefix = strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	if prefix != "" && (path.IsAbs(prefix) || path.Clean(prefix) != prefix || prefix == "." || strings.HasPrefix(prefix, "../")) {
		return "", "", fmt.Errorf("S3 prefix must not contain empty, . or .. components")
	}
	for _, c := range prefix {
		if c == '\\' || c < 32 || c == 127 {
			return "", "", fmt.Errorf("S3 prefix must not contain backslashes or control characters")
		}
	}
	if prefix == ".." {
		return "", "", fmt.Errorf("S3 prefix must not contain .. components")
	}
	return u.Host, prefix, nil
}

// NewS3Backend uses only explicit environment credentials, never AWS profiles
// or instance metadata. Endpoint and signing region are independent so the
// same implementation works with AWS and other S3-compatible providers.
func NewS3Backend(location, endpoint, region string) (*S3Backend, error) {
	bucket, prefix, err := ParseS3Archive(location)
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint_url (--endpoint-url) is required for an S3 archive")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("endpoint_url must be an http(s) service URL without credentials, bucket, path, query, or fragment")
	}
	accessKey, secretKey := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must both be set for S3 storage")
	}
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}
	creds := aws.Credentials{AccessKeyID: accessKey, SecretAccessKey: secretKey,
		SessionToken: os.Getenv("AWS_SESSION_TOKEN"), Source: "Environment"}
	client := s3.New(s3.Options{
		Region: region, BaseEndpoint: aws.String(strings.TrimSuffix(endpoint, "/")), UsePathStyle: true,
		Credentials: aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return creds, nil
		})),
		// Avoid optional aws-chunked/checksum extensions unsupported by some
		// S3 implementations. Archive gzip and content checks remain unchanged.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		RetryMaxAttempts:           3,
		HTTPClient: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // Require the configured provider endpoint.
		}},
	})
	if prefix != "" {
		prefix += "/"
	}
	return &S3Backend{client: client, bucket: bucket, prefix: prefix}, nil
}

func (b *S3Backend) objectKey(key string) (string, error) {
	if key == "" || path.IsAbs(key) || path.Clean(key) != key || key == "." || key == ".." ||
		strings.HasPrefix(key, "../") || strings.ContainsAny(key, "\\\x00") {
		return "", pathInvalid("key", key)
	}
	if section, hash, ok := splitFanoutKey(key); ok {
		key = filepath.ToSlash(fanoutPath(section, hash))
	}
	return b.prefix + key, nil
}

func s3ObjectError(op, key string, err error) error {
	if err == nil {
		return nil
	}
	var api smithy.APIError
	var response *smithyhttp.ResponseError
	code := ""
	if errors.As(err, &api) {
		code = api.ErrorCode()
	}
	// A missing bucket is a setup error, not a missing archive object.
	if code != "NoSuchBucket" && (code == "NoSuchKey" || code == "NotFound" ||
		(errors.As(err, &response) && response.HTTPStatusCode() == http.StatusNotFound)) {
		err = os.ErrNotExist
	}
	return &os.PathError{Op: op, Path: key, Err: err}
}

func (b *S3Backend) GetBlob(key string) (io.ReadCloser, error) {
	object, err := b.objectKey(key)
	if err != nil {
		return nil, err
	}
	result, err := b.client.GetObject(context.Background(), &s3.GetObjectInput{Bucket: &b.bucket, Key: &object})
	if err != nil {
		return nil, s3ObjectError("get", key, err)
	}
	return result.Body, nil
}

func (b *S3Backend) PutBlob(key string, data []byte) error {
	object, err := b.objectKey(key)
	if err != nil {
		return err
	}
	_, err = b.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: &b.bucket, Key: &object, Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))),
	})
	return s3ObjectError("put", key, err)
}

func (b *S3Backend) DeleteBlob(key string) error {
	object, err := b.objectKey(key)
	if err != nil {
		return err
	}
	_, err = b.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{Bucket: &b.bucket, Key: &object})
	return s3ObjectError("delete", key, err)
}

func (b *S3Backend) BlobSize(key string) (int64, error) {
	object, err := b.objectKey(key)
	if err != nil {
		return 0, err
	}
	result, err := b.client.HeadObject(context.Background(), &s3.HeadObjectInput{Bucket: &b.bucket, Key: &object})
	if err != nil {
		return 0, s3ObjectError("stat", key, err)
	}
	if result.ContentLength == nil || *result.ContentLength < 0 {
		return 0, fmt.Errorf("S3 object %s has no valid content length", key)
	}
	return *result.ContentLength, nil
}

// listPhysical returns exact physical keys relative to the archive prefix.
// Always retain the prefix's slash boundary: archive/ must not match archive-old/.
func (b *S3Backend) listPhysical(prefix string, fn func(string) error) error {
	requestPrefix := b.prefix + prefix
	input := &s3.ListObjectsV2Input{Bucket: &b.bucket, Prefix: &requestPrefix}
	tokens := make(map[string]bool)
	for {
		page, err := b.client.ListObjectsV2(context.Background(), input)
		if err != nil {
			return fmt.Errorf("list S3 archive: %w", err)
		}
		for _, object := range page.Contents {
			if object.Key == nil || !strings.HasPrefix(*object.Key, requestPrefix) {
				return fmt.Errorf("S3 listing returned an object outside the requested prefix")
			}
			if err := fn(strings.TrimPrefix(*object.Key, b.prefix)); err != nil {
				return err
			}
		}
		if !aws.ToBool(page.IsTruncated) {
			return nil
		}
		token := aws.ToString(page.NextContinuationToken)
		if token == "" || tokens[token] {
			return fmt.Errorf("S3 listing returned an invalid continuation token")
		}
		tokens[token] = true
		input.ContinuationToken = &token
	}
}

func canonicalS3Object(physical string) (string, bool) {
	section, suffix, _ := strings.Cut(physical, "/")
	if !archiveSection(section) {
		return physical, true
	}
	hash, ok := hashFromFanout(suffix)
	return section + "/" + hash, ok
}

func (b *S3Backend) ListBlobs(prefix string, fn func(string) error) error {
	if prefix == "." {
		prefix = ""
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" {
		if _, err := b.objectKey(prefix); err != nil {
			return err
		}
		prefix += "/"
	}
	var keys []string
	if err := b.listPhysical(prefix, func(physical string) error {
		if key, ok := canonicalS3Object(physical); ok {
			keys = append(keys, key)
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := fn(key); err != nil {
			return err
		}
	}
	return nil
}

func (b *S3Backend) InspectArchive() (*Inventory, error) {
	result := &Inventory{}
	for _, section := range []string{"blocks/", "blobs/", "entries/"} {
		if err := b.listPhysical(section, func(physical string) error {
			if key, ok := canonicalS3Object(physical); ok {
				result.Objects = append(result.Objects, key)
			} else {
				result.InvalidPaths = append(result.InvalidPaths, physical)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(result.Objects)
	sort.Strings(result.InvalidPaths)
	return result, nil
}

func (b *S3Backend) RemoveInvalidArchivePath(name string) error {
	section, _, _ := strings.Cut(name, "/")
	if _, valid := canonicalS3Object(name); valid || !archiveSection(section) || !strings.Contains(name, "/") {
		return pathInvalid("repair", name)
	}
	// Do not path.Clean this name: S3 permits literal dot components and repeated
	// slashes. Delete precisely the invalid object returned by the inventory.
	object := b.prefix + name
	_, err := b.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{Bucket: &b.bucket, Key: &object})
	return s3ObjectError("repair", name, err)
}

func (b *S3Backend) PruneEmptyArchiveDirs() (int, error) { return 0, nil }

var _ Backend = (*S3Backend)(nil)
var _ ArchiveInspector = (*S3Backend)(nil)
