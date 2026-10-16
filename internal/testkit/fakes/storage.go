package fakes

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"sort"

	"meet.space/internal/storage"
)

// StorageCall is one call to Storage.
type StorageCall struct {
	Meta
	// Key is the key passed to Put or Get.
	Key string
	// Keys are the keys passed to Delete.
	Keys []string
	// Size and ContentType are what the caller passed to Put.
	Size        int64
	ContentType string
	// Data is every byte Put read from its reader.
	Data []byte
}

func (c *StorageCall) clone() StorageCall {
	d := *c
	d.Keys = slices.Clone(c.Keys)
	d.Data = slices.Clone(c.Data)
	return d
}

type object struct {
	data        []byte
	contentType string
}

// Storage is a recording, in-memory storage.Storage.
type Storage struct {
	recorder[StorageCall, *StorageCall]
	objects map[string]object
}

// Put reads r to the end and, unless the call is made to fail, stores
// what it read under key.
//
// OPEN: what Put should do when r yields a different number of bytes
// than size. The documents don't say; the fake stores what it read and
// records both, so a test can check.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	data, readErr := io.ReadAll(r)
	c := s.do(ctx, "Put", StorageCall{Key: key, Size: size, ContentType: contentType, Data: data},
		func(c *StorageCall) error {
			if readErr != nil {
				return readErr
			}
			if s.objects == nil {
				s.objects = map[string]object{}
			}
			s.objects[key] = object{slices.Clone(data), contentType}
			return nil
		})
	return c.Err
}

// Get returns a reader over a copy of the object at key, or an error
// wrapping storage.ErrNotFound.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, storage.Info, error) {
	var got object
	c := s.do(ctx, "Get", StorageCall{Key: key}, func(*StorageCall) error {
		o, ok := s.objects[key]
		if !ok {
			return fmt.Errorf("fake storage: get %q: %w", key, storage.ErrNotFound)
		}
		got = object{slices.Clone(o.data), o.contentType}
		return nil
	})
	if c.Err != nil {
		return nil, storage.Info{}, c.Err
	}
	return io.NopCloser(bytes.NewReader(got.data)), storage.Info{Size: int64(len(got.data)), ContentType: got.contentType}, nil
}

// Delete removes the objects at keys unless the call is made to fail.
// See the OPEN note on storage.Storage.Delete: for now a missing key is
// not an error here, as in S3; no test depends on that yet.
func (s *Storage) Delete(ctx context.Context, keys ...string) error {
	c := s.do(ctx, "Delete", StorageCall{Keys: slices.Clone(keys)}, func(*StorageCall) error {
		for _, k := range keys {
			delete(s.objects, k)
		}
		return nil
	})
	return c.Err
}

// Seed stores an object without recording a call, for setting up a test.
func (s *Storage) Seed(key string, data []byte, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects == nil {
		s.objects = map[string]object{}
	}
	s.objects[key] = object{slices.Clone(data), contentType}
}

// Object returns a copy of the object stored at key.
func (s *Storage) Object(key string) (data []byte, info storage.Info, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	if !ok {
		return nil, storage.Info{}, false
	}
	return slices.Clone(o.data), storage.Info{Size: int64(len(o.data)), ContentType: o.contentType}, true
}

// Keys returns the keys of every stored object, sorted.
func (s *Storage) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.objects))
	for k := range s.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
