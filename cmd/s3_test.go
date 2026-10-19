package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"josvisser.nl/erwtensoep/internal/storage"
	"josvisser.nl/erwtensoep/internal/testutil/s3test"
)

func TestS3FlagsConfigAndBackendSelection(t *testing.T) {
	previousOptions, previousBackend := options, backend
	t.Cleanup(func() { options, backend = previousOptions, previousBackend })
	t.Setenv("AWS_ACCESS_KEY_ID", s3test.AccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", s3test.SecretKey)
	cmd := &cobra.Command{Use: "fsck"}
	cmd.SetGlobalNormalizationFunc(rootCmd.GlobalNormalizationFunc())
	addArchiveDirFlag(cmd)
	addBackendFlag(cmd)
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	uri, endpoint, region := "s3://bucket/archive", "https://example.com", "region-1"
	applyConfigSection(cmd, &config{ArchiveDir: &uri, EndpointURL: &endpoint, Region: &region})
	if options.ArchiveDir != uri || options.EndpointURL != endpoint || options.Region != region {
		t.Fatal("S3 configuration was not applied")
	}
	if err := cmd.PersistentFlags().Set("endpoint_url", "https://override.example.com"); err != nil {
		t.Fatal(err)
	}
	applyConfigSection(cmd, &config{EndpointURL: &endpoint})
	if options.EndpointURL != "https://override.example.com" {
		t.Fatal("configuration replaced an explicit endpoint alias flag")
	}
	if err := ensureArchiveDir(cmd); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auto", "s3", "local"} {
		backend = name
		if err := configureBackend(); err != nil {
			t.Fatal(err)
		}
		if _, ok := options.Storage.(*storage.S3Backend); !ok {
			t.Fatalf("S3 URI did not select S3 with backend %s", name)
		}
	}
	backend = "memory"
	if err := configureBackend(); err == nil {
		t.Fatal("memory backend accepted S3 URL")
	}
	backend = "s3"
	options.ArchiveDir = filepath.Join(t.TempDir(), "missing")
	if err := ensureArchiveDir(cmd); err == nil || !strings.Contains(err.Error(), "s3://") {
		t.Fatalf("S3 selector accepted local location: %v", err)
	}
	if _, err := os.Stat(options.ArchiveDir); !os.IsNotExist(err) {
		t.Fatal("S3 selector created a local directory")
	}
}

func TestIntegrationS3Archive(t *testing.T) {
	server := s3test.New(t)
	bin := filepath.Join(t.TempDir(), "erwtensoep")
	build := exec.Command("go", "build", "-o", bin, "./cmd")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// Never use real credentials, even when present in the test environment.
	env := append(os.Environ(), "AWS_ACCESS_KEY_ID="+s3test.AccessKey, "AWS_SECRET_ACCESS_KEY="+s3test.SecretKey,
		"AWS_SESSION_TOKEN=", "AWS_REGION=test-region-1", "AWS_DEFAULT_REGION=")
	uri := "s3://bucket/archives/work"
	missingEndpoint := exec.Command(bin, "-a", uri, "--endpoint-url=", "--storage-backend=auto", "stats")
	missingEndpoint.Env = env
	if out, err := missingEndpoint.CombinedOutput(); err == nil || !strings.Contains(string(out), "endpoint_url") {
		t.Fatalf("S3 without an endpoint: %v\n%s", err, out)
	}
	if len(server.Requests()) != 0 {
		t.Fatal("endpoint validation made an HTTP request")
	}
	for _, name := range []string{"hello", "recipe"} {
		command := exec.Command(bin, "-a", uri, "--endpoint-url=", name)
		command.Env = append(env, "AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s required S3 credentials: %v\n%s", name, err, out)
		}
	}
	run := func(wantError bool, input string, args ...string) string {
		t.Helper()
		command := exec.Command(bin, append([]string{"-a", uri, "--endpoint_url", server.URL, "--storage-backend", "auto"}, args...)...)
		command.Env = env
		command.Stdin = strings.NewReader(input)
		out, err := command.CombinedOutput()
		if (err != nil) != wantError {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "documents"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"documents/notes.txt", "copy.txt"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("shared data\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run(false, "", "merge", "--dry-run", source)
	if len(server.Snapshot()) != 0 {
		t.Fatal("dry run wrote S3 objects")
	}
	run(false, "", "merge", "--dry-run=false", "--min-size=0", "--max-size=9223372036854775807", source)
	if out := run(false, "", "ls"); !strings.Contains(out, "documents/notes.txt") {
		t.Fatalf("listing: %s", out)
	}
	if out := run(false, "cat documents/notes.txt\nexit\n", "cli"); !strings.Contains(out, "shared data") {
		t.Fatalf("CLI read: %s", out)
	}
	if out := run(false, "", "stats"); !strings.Contains(out, "Stored blocks (gzip)") {
		t.Fatalf("stats: %s", out)
	}
	destination := t.TempDir()
	run(false, "", "restore", "-o", destination, "documents", "copy.txt")
	if data, err := os.ReadFile(filepath.Join(destination, "documents/notes.txt")); err != nil || string(data) != "shared data\n" {
		t.Fatalf("restore: %q, %v", data, err)
	}
	run(false, "", "verify", "--shallow=false")
	if out := run(false, "", "fsck", "-nv"); !strings.Contains(out, "Archive is consistent") || !strings.Contains(out, "100.0%") {
		t.Fatalf("fsck: %s", out)
	}
	original := server.Snapshot()["archives/work/superblock.json.gz"]
	run(false, "", "rebuild")
	if objects := server.Snapshot(); !bytes.Equal(objects["archives/work/superblock.json.gz"], original) || len(objects["archives/work/rebuilt_superblock.json"]) == 0 {
		t.Fatal("rebuild changed the active index or wrote no separate index")
	}
	// Removal must retain the shared blob until the remaining path is gone.
	run(false, "rm documents/notes.txt\nexit\n", "cli")
	run(false, "", "fsck", "-n")
	if out := run(false, "cat copy.txt\nexit\n", "cli"); !strings.Contains(out, "shared data") {
		t.Fatalf("shared data removed: %s", out)
	}
	// A corrupt block cascades through the blob and entry; fsck also removes
	// invalid object keys and folder markers, without touching nearby prefixes.
	for key := range server.Snapshot() {
		if strings.HasPrefix(key, "archives/work/blocks/") {
			server.Put(key, []byte("corrupt"))
		}
	}
	server.Put("archives/work/entries/bad", []byte("bad metadata"))
	server.Put("archives/work/blocks/empty/", nil)
	server.Put("archives/work-other/entries/bad", []byte("unrelated"))
	before := server.Snapshot()
	if out := run(true, "", "fsck", "-n"); !strings.Contains(out, "[corrupt]") || !strings.Contains(out, "[invalid layout]") {
		t.Fatalf("corruption report: %s", out)
	}
	if !reflect.DeepEqual(before, server.Snapshot()) {
		t.Fatal("check-only fsck modified S3 objects")
	}
	run(false, "", "fsck", "-y")
	run(false, "", "fsck", "-n")
	objects := server.Snapshot()
	if string(objects["archives/work-other/entries/bad"]) != "unrelated" || len(objects["archives/work/superblock.json.gz.fsck.bak"]) == 0 {
		t.Fatal("repair crossed prefix boundary or did not save an index backup")
	}
	rdr, err := gzip.NewReader(bytes.NewReader(objects["archives/work/superblock.json.gz"]))
	if err != nil {
		t.Fatal(err)
	}
	defer rdr.Close()
	var index fs.Superblock
	if err := json.NewDecoder(rdr).Decode(&index); err != nil || len(index.Entries) != 0 || len(index.Blobs) != 0 || len(index.Blocks) != 0 {
		t.Fatalf("repair did not remove damaged data: %+v, %v", &index, err)
	}
	// Access failures must not yield a destructive repair plan.
	server.Fail("GET", "archives/work/superblock.json.gz", "AccessDenied", 403)
	if out := run(true, "", "fsck", "-y"); !strings.Contains(out, "AccessDenied") {
		t.Fatalf("missing permission error: %s", out)
	}
	if !reflect.DeepEqual(objects, server.Snapshot()) {
		t.Fatal("permission failure modified archive")
	}
}
