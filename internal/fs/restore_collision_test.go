package fs

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
)

func TestRestoreFileDirectoryCollisions(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		name := "regular"
		if symlink {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			opts := Options{ArchiveDir: t.TempDir(), Storage: storage.NewMemoryBackend(), MaxSize: math.MaxInt64, SuperblockWriteThreshold: math.MaxInt64}
			sb, err := GetSuperblock(&opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, directory := range []bool{false, true} {
				source := t.TempDir()
				for _, prefix := range []string{"", "nested"} {
					p := filepath.Join(source, prefix, "a")
					if directory {
						p = filepath.Join(p, "child.txt")
					}
					if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
						t.Fatal(err)
					}
					if symlink && !directory {
						err = os.Symlink("target", p)
					} else {
						err = os.WriteFile(p, []byte("contents"), 0600)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				// These sort between an ancestor and its descendants, and before
				// the conflict. Validation must still reject before writing them.
				for _, name := range []string{"a-other", "0-first"} {
					if err := os.WriteFile(filepath.Join(source, name), []byte("contents"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if _, failures, err := sb.Merge(source); err != nil || failures != 0 {
					t.Fatalf("merge: %d failures, %v", failures, err)
				}
			}
			for _, tc := range []struct {
				patterns []string
				want     []string
			}{
				{[]string{"a"}, []string{"a"}},
				{[]string{"./a"}, []string{"a"}},
				{[]string{"a/"}, []string{"a/child.txt"}},
				{[]string{"a//"}, []string{"a/child.txt"}},
				{[]string{"a", "a"}, []string{"a"}},
				{[]string{"nested/a"}, []string{"nested/a"}},
				{[]string{"nested/a/"}, []string{"nested/a/child.txt"}},
				{[]string{"a", "nested/a/"}, []string{"a", "nested/a/child.txt"}},
			} {
				targets, err := sb.RestoreTargets(tc.patterns)
				if err != nil || !reflect.DeepEqual(targets, tc.want) {
					t.Fatalf("targets %v: %v, %v; want %v", tc.patterns, targets, err, tc.want)
				}
				destination := t.TempDir()
				if n, err := sb.Restore(tc.patterns, destination); err != nil || n != len(tc.want) {
					t.Fatalf("restore %v: %d, %v", tc.patterns, n, err)
				}
				for _, p := range tc.want {
					if symlink && filepath.Base(p) == "a" {
						if target, err := os.Readlink(filepath.Join(destination, p)); err != nil || target != "target" {
							t.Fatalf("restored link: %q, %v", target, err)
						}
					} else if data, err := os.ReadFile(filepath.Join(destination, p)); err != nil || string(data) != "contents" {
						t.Fatalf("restored file: %q, %v", data, err)
					}
				}
			}
			for _, patterns := range [][]string{{"a", "a/"}, {"nested"}, {"nested/"}, {"nested/*", "nested/a/*"}, {"0-first", "a", "a-other", "a/child.txt"}} {
				destination := t.TempDir()
				if err := os.WriteFile(filepath.Join(destination, "0-first"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				n, err := sb.Restore(patterns, destination)
				if err == nil || !strings.Contains(err.Error(), "restore path conflict") || n != 0 {
					t.Fatalf("conflicting restore %v: %d, %v", patterns, n, err)
				}
				if files, err := os.ReadDir(destination); err != nil || len(files) != 1 {
					t.Fatalf("conflicting restore created files: %v, %v", files, err)
				}
				if data, err := os.ReadFile(filepath.Join(destination, "0-first")); err != nil || string(data) != "preserve" {
					t.Fatalf("conflicting restore changed existing output: %q, %v", data, err)
				}
			}
			selected := map[string]string{}
			for _, name := range []string{"0-first", "a", "a-other", "a/child.txt"} {
				selected[name] = latestVersionID(sb.Entries[name])
			}
			destination := filepath.Join(t.TempDir(), "not-created")
			if n, err := sb.RestoreVersions(selected, destination); n != 0 || err == nil || !strings.Contains(err.Error(), "restore path conflict") {
				t.Fatalf("direct version restore bypassed conflict check: %d, %v", n, err)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("direct version restore created destination: %v", err)
			}
		})
	}
}
