package storage

import (
	"io"
	"os"
	"sort"
	"testing"
)

func TestLocalBackendPutGetDelete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	b := NewLocalBackend(dir)

	key := "data.txt"
	if err := b.PutBlob(key, []byte("hello")); err != nil {
		t.Fatalf("PutBlob: %v", err)
	}

	reader, err := b.GetBlob(key)
	if err != nil {
		t.Fatalf("GetBlob: %v", err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("expected content %q, got %q", "hello", string(got))
	}

	if err := b.DeleteBlob(key); err != nil {
		t.Fatalf("DeleteBlob: %v", err)
	}
	if _, err := b.GetBlob(key); err == nil || !os.IsNotExist(err) {
		t.Fatalf("expected missing blob, got %v", err)
	}
}

func TestLocalBackendListBlobs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	b := NewLocalBackend(dir)

	keys := []string{
		"blocks/0123456789abcdef",
		"blocks/ffffffffffffffff",
		"entries/aaaaaaaaaaaaaaaa",
		"misc/data.txt",
	}
	for _, key := range keys {
		if err := b.PutBlob(key, []byte("x")); err != nil {
			t.Fatalf("PutBlob %s: %v", key, err)
		}
	}

	var got []string
	if err := b.ListBlobs("blocks/", func(key string) error {
		got = append(got, key)
		return nil
	}); err != nil {
		t.Fatalf("ListBlobs: %v", err)
	}

	expected := []string{"blocks/0123456789abcdef", "blocks/ffffffffffffffff"}
	sort.Strings(got)
	sort.Strings(expected)
	if len(got) != len(expected) {
		t.Fatalf("expected %d keys, got %d (%v)", len(expected), len(got), got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, got)
		}
	}
}
