package storage

import "io"

// Backend defines the minimal operations needed to store and retrieve archive data.
// All keys are backend-relative and opaque to callers.
type Backend interface {
	// GetBlob returns a reader for an existing blob by key.
	GetBlob(key string) (io.ReadCloser, error)
	// PutBlob stores a blob by key, replacing any existing blob.
	PutBlob(key string, data []byte) error
	// DeleteBlob removes a blob by key.
	DeleteBlob(key string) error
	// ListBlobs enumerates all blob keys with the given prefix.
	ListBlobs(prefix string, fn func(key string) error) error
}
