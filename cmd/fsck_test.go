package main

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"josvisser.nl/erwtensoep/internal/fs"
	"josvisser.nl/erwtensoep/internal/storage"
)

func TestFSCKConfirmationAndCheckOnly(t *testing.T) {
	for _, tc := range []struct {
		name, input       string
		yes, no, repaired bool
	}{
		{"decline", "n\n", false, false, false},
		{"EOF", "", false, false, false},
		{"check only", "yes\n", false, true, false},
		{"confirm", "yes\n", false, false, true},
		{"yes flag", "", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "file"), []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
			backend := storage.NewMemoryBackend()
			opts := &fs.Options{ArchiveDir: t.TempDir(), Storage: backend, MaxSize: math.MaxInt64, SuperblockWriteThreshold: math.MaxInt64}
			sb, err := fs.GetSuperblock(opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := sb.Merge(source); err != nil {
				t.Fatal(err)
			}
			if err := sb.Write(); err != nil {
				t.Fatal(err)
			}
			for id := range sb.Blocks {
				if err := backend.PutBlob("blocks/"+id, []byte("broken")); err != nil {
					t.Fatal(err)
				}
			}
			var out, progress bytes.Buffer
			err = runFSCK(opts, strings.NewReader(tc.input), &out, &progress, tc.yes, tc.no, false)
			if progress.Len() != 0 {
				t.Fatalf("unexpected progress without verbose: %s", progress.String())
			}
			if tc.repaired && err != nil {
				t.Fatal(err)
			}
			if !tc.repaired && !errors.Is(err, errFSCKUnrepaired) {
				t.Fatalf("unchecked archive reported success: %v", err)
			}
			if !strings.Contains(out.String(), "[corrupt]") || !strings.Contains(out.String(), "Repair:") {
				t.Fatalf("missing repair report: %s", out.String())
			}
			if (tc.no || tc.yes) && strings.Contains(out.String(), "[y/N]") {
				t.Fatalf("unexpected prompt: %s", out.String())
			}
			reloaded, err := fs.GetSuperblock(opts)
			if err != nil {
				t.Fatal(err)
			}
			if (len(reloaded.Entries) == 0) != tc.repaired {
				t.Fatalf("unexpected repair result: %v", reloaded.Entries)
			}
			if tc.repaired {
				out.Reset()
				if err := runFSCK(opts, strings.NewReader(""), &out, &progress, false, true, true); err != nil || !strings.Contains(out.String(), "Archive is consistent") {
					t.Fatalf("second check: %v, %s", err, out.String())
				}
				for _, phase := range []string{"directory layout", "blocks", "blobs", "entries"} {
					if !strings.Contains(progress.String(), "Checked "+phase+": 0/0 objects (100.0%)") {
						t.Fatalf("missing verbose progress for %s: %s", phase, progress.String())
					}
				}
			}
		})
	}
}

func TestFSCKProgressCount(t *testing.T) {
	for _, tc := range []struct {
		count, total int
		want         string
	}{
		{0, -1, "0 objects (counting...)"},
		{0, 0, "0/0 objects (100.0%)"},
		{0, 4, "0/4 objects (0.0%)"},
		{1, 4, "1/4 objects (25.0%)"},
		{1, 3, "1/3 objects (33.3%)"},
		{4, 4, "4/4 objects (100.0%)"},
	} {
		if got := fsckProgressCount(tc.count, tc.total); got != tc.want {
			t.Errorf("progress(%d, %d) = %q, want %q", tc.count, tc.total, got, tc.want)
		}
	}
}
