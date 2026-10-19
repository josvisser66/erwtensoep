package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"josvisser.nl/erwtensoep/internal/testutil/s3test"
)

func TestIntegrationMergeTarSources(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "erwtensoep")
	build := exec.Command("go", "build", "-o", bin, "./cmd")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	var sources []string
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "local.txt"), []byte("from directory"), 0600); err != nil {
		t.Fatal(err)
	}
	sources = append(sources, sourceDir)
	for i, suffix := range []string{".tar", ".tar.gz", ".tgz"} {
		file := filepath.Join(t.TempDir(), "input"+suffix)
		var data bytes.Buffer
		var output io.Writer = &data
		var gz *gzip.Writer
		if suffix != ".tar" {
			gz = gzip.NewWriter(&data)
			output = gz
		}
		tw := tar.NewWriter(output)
		name := []string{"first.txt", "nested/second.txt", "third.txt"}[i]
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0640, Size: 8, ModTime: time.Unix(100, 0)}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("from tar")); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := tw.WriteHeader(&tar.Header{Name: "skip-fifo", Typeflag: tar.TypeFifo, Mode: 0600}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if gz != nil {
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(file, data.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, file)
	}
	for _, backend := range []string{"local", "s3"} {
		t.Run(backend, func(t *testing.T) {
			location := t.TempDir()
			prefix := []string{"-a", location, "--storage-backend=auto"}
			// Isolate configuration and credentials from the user's environment.
			env := append(os.Environ(), "HOME="+t.TempDir(), "AWS_ACCESS_KEY_ID="+s3test.AccessKey, "AWS_SECRET_ACCESS_KEY="+s3test.SecretKey,
				"AWS_SESSION_TOKEN=", "AWS_REGION=test-region-1")
			var server *s3test.Server
			if backend == "s3" {
				server = s3test.New(t)
				prefix = []string{"-a", "s3://bucket/tar-import", "--endpoint-url", server.URL, "--storage-backend=auto"}
			}
			run := func(wantError bool, args ...string) (string, string) {
				t.Helper()
				cmd := exec.Command(bin, append(append([]string{}, prefix...), args...)...)
				cmd.Env = env
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				if (err != nil) != wantError {
					t.Fatalf("%v: %v\n%s\n%s", args, err, stdout.String(), stderr.String())
				}
				return stdout.String(), stderr.String()
			}
			_, warnings := run(false, append([]string{"merge", "--dry-run"}, sources...)...)
			if strings.Count(warnings, "WARNING: merging files from tar archive") != 3 || !strings.Contains(warnings, "the tar file itself will not be archived") {
				t.Fatalf("missing expansion warnings: %s", warnings)
			}
			if server != nil {
				if len(server.Snapshot()) != 0 {
					t.Fatal("dry run wrote to S3")
				}
			} else if entries, err := os.ReadDir(location); err != nil || len(entries) != 0 {
				t.Fatalf("dry run wrote local objects: %v", err)
			}
			_, warnings = run(false, append([]string{"merge"}, sources...)...)
			if strings.Count(warnings, "WARNING: merging files from tar archive") != 3 || !strings.Contains(warnings, "skipping unsupported tar member") {
				t.Fatalf("wrong import warnings: %s", warnings)
			}
			listing, _ := run(false, "ls")
			for _, name := range []string{"local.txt", "first.txt", "nested/second.txt", "third.txt"} {
				if !strings.Contains(listing, name) {
					t.Fatalf("missing %s: %s", name, listing)
				}
			}
			if strings.Contains(listing, "input.tar") || strings.Contains(listing, "skip-fifo") {
				t.Fatalf("unexpected entries: %s", listing)
			}
			restored := t.TempDir()
			run(false, "restore", "--latest", "-o", restored, "local.txt", "first.txt", "nested", "third.txt")
			for _, name := range []string{"first.txt", "nested/second.txt", "third.txt"} {
				if data, err := os.ReadFile(filepath.Join(restored, name)); err != nil || string(data) != "from tar" {
					t.Fatalf("restored %s: %q, %v", name, data, err)
				}
			}
			if out, _ := run(false, "fsck", "-n"); !strings.Contains(out, "Archive is consistent") {
				t.Fatalf("fsck: %s", out)
			}
			if out, _ := run(false, append([]string{"merge"}, sources...)...); !strings.Contains(out, "Overall result: 0 changes, 0 errors") {
				t.Fatalf("repeat import: %s", out)
			}
			// A later bad input must not lose completed imports from earlier sources.
			if err := os.WriteFile(filepath.Join(sourceDir, "before-error.txt"), []byte("saved"), 0600); err != nil {
				t.Fatal(err)
			}
			bad := filepath.Join(t.TempDir(), "bad.tgz")
			if err := os.WriteFile(bad, []byte("not gzip"), 0600); err != nil {
				t.Fatal(err)
			}
			run(true, "merge", sourceDir, bad)
			if out, _ := run(false, "ls"); !strings.Contains(out, "before-error.txt") {
				t.Fatalf("completed import lost after later error: %s", out)
			}
		})
	}
}
