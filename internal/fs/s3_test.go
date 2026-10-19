package fs

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
	"josvisser.nl/erwtensoep/internal/testutil/s3test"
)

func TestS3StorageInitializationAndOrphanRecovery(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", s3test.AccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", s3test.SecretKey)
	t.Setenv("AWS_SESSION_TOKEN", "")
	server := s3test.New(t)
	newOptions := func() *Options {
		return &Options{ArchiveDir: "s3://bucket/work", EndpointURL: server.URL, Region: "test-region-1",
			MaxSize: math.MaxInt64, SuperblockWriteThreshold: math.MaxInt64}
	}
	opts := newOptions()
	sb, err := GetSuperblock(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opts.Storage.(*storage.S3Backend); !ok || opts.ArchiveDir != "s3://bucket/work" {
		t.Fatal("filesystem layer did not preserve the S3 URI and select S3 storage")
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "original.txt"), []byte("recover this data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sb.Merge(source); err != nil {
		t.Fatal(err)
	}
	if err := sb.Write(); err != nil {
		t.Fatal(err)
	}
	for _, versions := range sb.Entries {
		for id := range versions {
			if err := opts.Storage.DeleteBlob("entries/" + id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := opts.Storage.DeleteBlob("superblock.json.gz"); err != nil {
		t.Fatal(err)
	}
	report, err := CheckArchive(newOptions(), nil)
	if err != nil || len(report.Recovered) != 1 || !strings.HasPrefix(report.Recovered[0], "lost+found/") {
		t.Fatalf("orphan recovery: %+v, %v", report, err)
	}
	if err := report.Repair(); err != nil {
		t.Fatal(err)
	}
	clean, err := CheckArchive(newOptions(), nil)
	if err != nil || len(clean.Issues) != 0 {
		t.Fatalf("repaired archive: %+v, %v", clean, err)
	}
	reloaded, err := GetSuperblock(newOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range reloaded.Entries[report.Recovered[0]] {
		r, err := reloaded.OpenRegularFile(entry.(*RegularFile))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || string(data) != "recover this data" {
			t.Fatalf("recovered bytes: %q, %v", data, err)
		}
	}
}
