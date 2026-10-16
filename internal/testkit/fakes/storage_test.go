package fakes_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"meet.space/internal/storage"
	"meet.space/internal/testkit/fakes"
)

// Security Rule 13 (private bucket): files reach visitors only through
// /files/..., so code must have no way to hand out a public or presigned
// link to an object. The storage interface therefore has only the methods
// reviewed against Rule 13, none named for a URL or link, and no method
// returns a string or bytes, or anything holding them, other than an
// object's content type.
//
// Security Rule 13; P0-HARNESS-06
func TestRule13_StorageInterfaceCannotProduceAnObjectURL(t *testing.T) {
	it := reflect.TypeFor[storage.Storage]()
	allowed := map[string]bool{"storage.Info.ContentType": true}
	// A new method fails here until someone has checked it can't give out
	// a link to an object and added it to this list.
	reviewed := map[string]bool{"Put": true, "Get": true, "Delete": true}
	for i := range it.NumMethod() {
		m := it.Method(i)
		if !reviewed[m.Name] {
			t.Errorf("Rule 13: storage.Storage.%s has not been reviewed: confirm it can't produce a link to an object, then add it to the reviewed list", m.Name)
		}
		name := strings.ToLower(m.Name)
		for _, w := range []string{"url", "uri", "sign", "public", "link", "href"} {
			if strings.Contains(name, w) {
				t.Errorf("Rule 13: storage.Storage.%s looks like it produces a link to an object", m.Name)
			}
		}
		for o := range m.Type.NumOut() {
			for _, p := range stringsIn(m.Type.Out(o), m.Name+" result", map[reflect.Type]bool{}) {
				if !allowed[p] {
					t.Errorf("Rule 13: storage.Storage.%s returns a string or bytes at %s, which could carry a URL", m.Name, p)
				}
			}
		}
	}
}

// stringsIn lists where a value of type t can hold a string or bytes: the
// type itself, or a field, element, key or function result reached from
// it. Interfaces (io.ReadCloser, error) are not followed.
func stringsIn(t reflect.Type, path string, seen map[reflect.Type]bool) []string {
	if seen[t] {
		return nil
	}
	seen[t] = true
	defer delete(seen, t)
	switch t.Kind() {
	case reflect.String:
		return []string{path}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return []string{path}
		}
		return stringsIn(t.Elem(), path+"[elem]", seen)
	case reflect.Pointer, reflect.Array, reflect.Chan:
		return stringsIn(t.Elem(), path+"[elem]", seen)
	case reflect.Map:
		return append(stringsIn(t.Key(), path+"[key]", seen), stringsIn(t.Elem(), path+"[value]", seen)...)
	case reflect.Struct:
		var out []string
		for f := range t.Fields() {
			out = append(out, stringsIn(f.Type, t.String()+"."+f.Name, seen)...)
		}
		return out
	case reflect.Func:
		var out []string
		for i := range t.NumOut() {
			out = append(out, stringsIn(t.Out(i), path+"()", seen)...)
		}
		return out
	}
	return nil
}

// Put stores what it read; Get streams it back with its size and content
// type; Delete removes it; a Get of an unknown key is ErrNotFound. Every
// call is recorded with its arguments.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestStorage_PutGetDeleteAreStoredAndRecorded(t *testing.T) {
	ctx := context.Background()
	s := &fakes.Storage{}
	const key, other = "groups/0192f1d2/0192f1d3.png", "groups/0192f1d2/0192f1d4.pdf"
	if err := s.Put(ctx, key, strings.NewReader("PNG!"), 4, "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, other, strings.NewReader("%PDF"), 4, "application/pdf"); err != nil {
		t.Fatal(err)
	}
	rc, info, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	if err := rc.Close(); err != nil {
		t.Error(err)
	}
	if string(body) != "PNG!" || info != (storage.Info{Size: 4, ContentType: "image/png"}) {
		t.Errorf("Get = %q, %+v", body, info)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get after Delete: %v, want ErrNotFound", err)
	}
	if got := s.Keys(); !slices.Equal(got, []string{other}) {
		t.Errorf("Keys() = %q, want only %q", got, other)
	}

	calls := s.Calls()
	want := []fakes.StorageCall{
		{Meta: fakes.Meta{N: 1, Method: "Put", MethodN: 1, Done: true}, Key: key, Size: 4, ContentType: "image/png", Data: []byte("PNG!")},
		{Meta: fakes.Meta{N: 2, Method: "Put", MethodN: 2, Done: true}, Key: other, Size: 4, ContentType: "application/pdf", Data: []byte("%PDF")},
		{Meta: fakes.Meta{N: 3, Method: "Get", MethodN: 1, Done: true}, Key: key},
		{Meta: fakes.Meta{N: 4, Method: "Delete", MethodN: 1, Done: true}, Keys: []string{key}},
	}
	if len(calls) != 5 {
		t.Fatalf("%d calls recorded, want 5: %+v", len(calls), calls)
	}
	if !reflect.DeepEqual(calls[:4], want) {
		t.Errorf("calls =\n%+v\nwant\n%+v", calls[:4], want)
	}
	if c := calls[4]; c.Method != "Get" || c.Key != key || !errors.Is(c.Err, storage.ErrNotFound) {
		t.Errorf("call 5 = %+v, want the failed Get", c)
	}
}

// Bytes handed to Put, read from Get or returned by Object are copies:
// changing them never changes what is stored or recorded.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestStorage_StoresAndReturnsCopies(t *testing.T) {
	ctx := context.Background()
	s := &fakes.Storage{}
	src := []byte("original")
	if err := s.Put(ctx, "k", bytes.NewReader(src), int64(len(src)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	copy(src, "XXXXXXXX")

	rc, _, _ := s.Get(ctx, "k")
	got, _ := io.ReadAll(rc)
	copy(got, "YYYYYYYY")
	data, _, _ := s.Object("k")
	copy(data, "ZZZZZZZZ")
	calls := s.Calls()
	copy(calls[0].Data, "WWWWWWWW")

	if data, info, ok := s.Object("k"); !ok || string(data) != "original" || info.Size != 8 {
		t.Errorf("stored object = %q, %+v, %v; want original", data, info, ok)
	}
	if d := s.Calls()[0].Data; string(d) != "original" {
		t.Errorf("recorded Put data = %q, want original", d)
	}
}

// A failed Put stores nothing, a failed Delete deletes nothing, a failed
// Get returns no reader. A Put whose reader fails is a failed Put.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestStorage_FailedCallsHaveNoEffect(t *testing.T) {
	ctx := context.Background()
	s := &fakes.Storage{}
	s.Seed("groups/g/keep.png", []byte("png"), "image/png")
	s.FailWhen(func(c fakes.StorageCall) bool { return c.Method == "Put" && c.MethodN == 1 }, errBoom)
	s.FailWhen(func(c fakes.StorageCall) bool { return c.Method == "Delete" }, errBoom)
	s.FailWhen(func(c fakes.StorageCall) bool { return c.Method == "Get" && c.Key == "groups/g/keep.png" }, errBoom)

	if err := s.Put(ctx, "groups/g/new.png", strings.NewReader("png"), 3, "image/png"); !errors.Is(err, errBoom) {
		t.Errorf("Put = %v, want boom", err)
	}
	if err := s.Delete(ctx, "groups/g/keep.png"); !errors.Is(err, errBoom) {
		t.Errorf("Delete = %v, want boom", err)
	}
	if rc, info, err := s.Get(ctx, "groups/g/keep.png"); !errors.Is(err, errBoom) || rc != nil || info != (storage.Info{}) {
		t.Errorf("Get = %v, %+v, %v; want nil, zero Info, boom", rc, info, err)
	}
	readErr := errors.New("connection reset")
	if err := s.Put(ctx, "groups/g/broken.png", iotest.ErrReader(readErr), 3, "image/png"); !errors.Is(err, readErr) {
		t.Errorf("Put with failing reader = %v, want the read error", err)
	}
	if got := s.Keys(); !slices.Equal(got, []string{"groups/g/keep.png"}) {
		t.Errorf("Keys() = %q, want only the seeded object", got)
	}
	if n := len(s.Calls()); n != 4 {
		t.Errorf("%d calls recorded, want 4 (failed calls are recorded; Seed is not)", n)
	}
}
