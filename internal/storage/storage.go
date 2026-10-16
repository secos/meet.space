// Package storage is the interface to the private uploads bucket (S3 in
// production). Upload keys look like groups/{group_id}/{uuidv7}{ext}.
//
// Security Rule 13: the bucket blocks all public access and files reach
// visitors only through the app's /files/... route, streamed on the
// visitor's own hostname. So nothing here can produce a public or
// presigned URL: a caller can store, read and delete objects, never link
// to them.
//
// Phase 0 has only the interface and the recording fake in
// internal/testkit/fakes. The S3 client (and the local ./uploads
// directory used in development) arrive with the first upload.
package storage

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned by Get when no object has the key.
var ErrNotFound = errors.New("storage: object not found")

// Info describes a stored object.
type Info struct {
	Size        int64
	ContentType string
}

// Storage stores, streams and deletes objects in the private bucket.
type Storage interface {
	// Put stores size bytes read from r under key with the given
	// content type, replacing any object already there.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error

	// Get opens the object at key. The caller closes the reader. It
	// returns ErrNotFound (test with errors.Is) when the key is unknown.
	Get(ctx context.Context, key string) (io.ReadCloser, Info, error)

	// Delete removes the objects at keys.
	//
	// OPEN: whether deleting a key that does not exist is an error. The
	// documents don't say. S3 treats it as success; a local directory
	// would not unless the implementation makes it so.
	Delete(ctx context.Context, keys ...string) error
}
