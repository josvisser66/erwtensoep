package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"josvisser.nl/erwtensoep/internal/testutil/s3test"
)

func s3Environment(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", s3test.AccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", s3test.SecretKey)
	t.Setenv("AWS_SESSION_TOKEN", "test-session")
	t.Setenv("AWS_REGION", "test-region-1")
	t.Setenv("AWS_DEFAULT_REGION", "")
}

func TestS3ArchiveValidation(t *testing.T) {
	for _, location := range []string{"bucket/prefix", "s3://", "s3:bucket/key", "s3://user:secret@bucket/prefix", "s3://bucket:443/prefix", "s3://bucket/a?x=1", "s3://bucket/a#frag", "s3://bucket/a/../b", "s3://bucket/..", "s3://bucket/a//b", "s3://bucket//", "s3://bucket/%00", "s3://bucket/a%5Cb"} {
		if _, _, err := ParseS3Archive(location); err == nil {
			t.Errorf("accepted invalid archive %q", location)
		}
	}
	for _, tc := range []struct{ uri, prefix string }{
		{"s3://bucket", ""}, {"s3://bucket/", ""}, {"s3://bucket/archive/nested/", "archive/nested"}, {"s3://bucket/archive%20one", "archive one"},
	} {
		bucket, prefix, err := ParseS3Archive(tc.uri)
		if err != nil || bucket != "bucket" || prefix != tc.prefix {
			t.Errorf("parse %q = %q, %q, %v", tc.uri, bucket, prefix, err)
		}
	}
}

func TestS3Configuration(t *testing.T) {
	s3Environment(t)
	for _, endpoint := range []string{"", "example.com", "ftp://example.com", "https://user:secret@example.com", "https://example.com/bucket", "https://example.com?x=1", "https://example.com/#fragment"} {
		if _, err := NewS3Backend("s3://bucket/prefix", endpoint, ""); err == nil {
			t.Errorf("accepted invalid endpoint %q", endpoint)
		}
	}
	for _, provider := range []struct{ endpoint, region string }{
		{"https://s3.us-east-1.amazonaws.com", "us-east-1"},
		{"https://s3.us-west-004.backblazeb2.com", "us-west-004"},
		{"https://s3.us-east-1.idrivee2.com", "us-east-1"},
		{"https://s3.us-east-1.wasabisys.com", "us-east-1"},
	} {
		b, err := NewS3Backend("s3://bucket/prefix", provider.endpoint, provider.region)
		if err != nil {
			t.Fatal(err)
		}
		o := b.client.Options()
		if *o.BaseEndpoint != provider.endpoint || o.Region != provider.region || !o.UsePathStyle {
			t.Fatalf("incorrect provider configuration: %s", provider.endpoint)
		}
	}
	for _, tc := range []struct{ region, env, fallback, want string }{
		{"explicit", "env", "fallback", "explicit"}, {"", "env", "fallback", "env"},
		{"", "", "fallback", "fallback"}, {"", "", "", "us-east-1"},
	} {
		t.Setenv("AWS_REGION", tc.env)
		t.Setenv("AWS_DEFAULT_REGION", tc.fallback)
		b, err := NewS3Backend("s3://bucket", "https://example.com", tc.region)
		if err != nil || b.client.Options().Region != tc.want {
			t.Fatalf("region selection: %v", err)
		}
	}
	for _, variable := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		t.Run(variable, func(t *testing.T) {
			t.Setenv(variable, "")
			if _, err := NewS3Backend("s3://bucket", "https://example.com", ""); err == nil || !strings.Contains(err.Error(), variable) {
				t.Fatalf("missing credential: %v", err)
			}
		})
	}
}

func TestS3BackendRoundTripAndIsolation(t *testing.T) {
	s3Environment(t)
	server := s3test.New(t)
	b, err := NewS3Backend("s3://bucket/archives/my%20archive/", server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte{0, 1, 2, 255, 0, 127}
	var want []string
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("blocks/%016x", i)
		want = append(want, key)
		if err := b.PutBlob(key, data); err != nil {
			t.Fatal(err)
		}
	}
	server.Put("archives/my archive-other/blocks/0000/0000/0000/0009", []byte("unrelated"))
	server.Put("outside", []byte("unrelated"))
	var got []string
	if err := b.ListBlobs("blocks/", func(key string) error { got = append(got, key); return nil }); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("paginated listing: %v, %v", got, err)
	}
	r, err := b.GetBlob(want[0])
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(r)
	if err := errors.Join(err, r.Close()); err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("object content: %v, %v", actual, err)
	}
	if n, err := BlobSize(b, want[0]); err != nil || n != int64(len(data)) {
		t.Fatalf("stored size: %d, %v", n, err)
	}
	if err := b.DeleteBlob(want[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetBlob(want[0]); !os.IsNotExist(err) {
		t.Fatalf("missing object must wrap os.ErrNotExist: %v", err)
	}
	if _, err := b.BlobSize(want[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing HEAD: %v", err)
	}
	for _, req := range server.Requests() {
		if !strings.Contains(req.Authorization, "/test-region-1/s3/aws4_request") || req.Token != "test-session" || req.Encoding == "aws-chunked" {
			t.Fatalf("incorrect signing or incompatible transfer: %+v", req)
		}
	}
	objects := server.Snapshot()
	if string(objects["outside"]) != "unrelated" || string(objects["archives/my archive-other/blocks/0000/0000/0000/0009"]) != "unrelated" {
		t.Fatal("changed an object outside the archive prefix")
	}
	for _, key := range []string{"../outside", "/outside", "blocks/../../outside", "blocks//id", "blocks/..", "a\\b"} {
		if err := b.PutBlob(key, data); err == nil {
			t.Fatalf("accepted invalid object key %q", key)
		}
	}
}

func TestS3InventoryRepairAndErrors(t *testing.T) {
	s3Environment(t)
	server := s3test.New(t)
	b, err := NewS3Backend("s3://bucket/archive", server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	server.Put("archive/blocks/0000/0000/0000/0001", []byte("block"))
	invalid := []string{"blocks/empty/", "entries/bad", "blocks/../superblock.json.gz"}
	for _, key := range invalid {
		server.Put("archive/"+key, []byte("invalid"))
	}
	server.Put("archive/superblock.json.gz", []byte("keep"))
	inventory, err := b.InspectArchive()
	if err != nil || len(inventory.Objects) != 1 || len(inventory.InvalidPaths) != 3 || len(inventory.EmptyDirs) != 0 {
		t.Fatalf("inventory: %+v, %v", inventory, err)
	}
	for _, key := range inventory.InvalidPaths {
		if err := b.RemoveInvalidArchivePath(key); err != nil {
			t.Fatal(err)
		}
	}
	if objects := server.Snapshot(); len(objects) != 2 || string(objects["archive/superblock.json.gz"]) != "keep" {
		t.Fatalf("repair deleted the wrong keys: %v", objects)
	}
	for _, key := range []string{"../outside", "superblock.json.gz", "blocks/0000/0000/0000/0001"} {
		if err := b.RemoveInvalidArchivePath(key); err == nil {
			t.Fatalf("invalid repair accepted %q", key)
		}
	}
	server.Fail("GET", "archive/superblock.json.gz", "AccessDenied", 403)
	if _, err := b.GetBlob("superblock.json.gz"); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("permission denied treated as a missing index: %v", err)
	}
	missing, _ := NewS3Backend("s3://missing/archive", server.URL, "")
	if _, err := missing.GetBlob("superblock.json.gz"); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing bucket treated as an empty archive: %v", err)
	}
	server.Fail("GET", "", "AccessDenied", 403)
	if _, err := b.InspectArchive(); err == nil {
		t.Fatal("inventory ignored listing failure")
	}
}

func TestS3RejectsIncompleteOrOutOfScopeListings(t *testing.T) {
	s3Environment(t)
	for _, response := range []string{
		`<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>`,
		`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>same</NextContinuationToken></ListBucketResult>`,
		`<ListBucketResult><Contents><Key>archive-other/blocks/a</Key></Contents></ListBucketResult>`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, response)
			}))
			defer server.Close()
			b, _ := NewS3Backend("s3://bucket/archive", server.URL, "")
			if _, err := b.InspectArchive(); err == nil {
				t.Fatal("accepted unsafe/incomplete inventory")
			}
		})
	}
}
