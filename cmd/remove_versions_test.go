package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"josvisser.nl/erwtensoep/internal/storage"
)

func TestRemoveCommandVersionSelection(t *testing.T) {
	previousOptions := options
	t.Cleanup(func() { options = previousOptions })
	for _, tc := range []struct {
		answer    string
		all       bool
		remaining int
		wantError bool
	}{
		{"1\ny\n", false, 1, false},
		{"1\nn\n", false, 2, true},
		{"0\n1-2\ny\n", false, 0, false},
		{"all\ny\n", false, 0, false},
		{"\n", false, 2, false},
		{"q\n", false, 2, false},
		{"", false, 2, false},
		{"y\n", true, 0, false},
		{"n\n", true, 2, true},
	} {
		t.Run(fmt.Sprintf("all=%t/answer=%q", tc.all, tc.answer), func(t *testing.T) {
			opts := fs.Options{ArchiveDir: t.TempDir(), Storage: storage.NewMemoryBackend(), MaxSize: math.MaxInt64, SuperblockWriteThreshold: math.MaxInt64}
			sb, err := fs.GetSuperblock(&opts)
			if err != nil {
				t.Fatal(err)
			}
			source := t.TempDir()
			for i, contents := range []string{"old", "new"} {
				file := filepath.Join(source, "note.txt")
				if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
				mtime := time.Unix(int64(100*(i+1)), 0)
				if err := os.Chtimes(file, mtime, mtime); err != nil {
					t.Fatal(err)
				}
				if _, failures, err := sb.Merge(source); err != nil || failures != 0 {
					t.Fatalf("merge: %d failures, %v", failures, err)
				}
			}
			if err := sb.Write(); err != nil {
				t.Fatal(err)
			}
			options = opts
			cmd := &cobra.Command{}
			cmd.Flags().Bool("all-versions", tc.all, "")
			cmd.SetIn(strings.NewReader(tc.answer))
			var out bytes.Buffer
			cmd.SetOut(&out)
			err = removeCmd.RunE(cmd, []string{"note.txt"})
			if (err != nil) != tc.wantError {
				t.Fatalf("remove: %v, output=%s", err, out.String())
			}
			loaded, err := fs.GetSuperblock(&opts)
			if err != nil || len(loaded.Entries["note.txt"]) != tc.remaining {
				t.Fatalf("wrong persisted version selection: %v", err)
			}
			if strings.Contains(out.String(), "Remove versions (") == tc.all {
				t.Fatalf("wrong selection prompt: %s", out.String())
			}
			if tc.remaining == 1 {
				for _, value := range loaded.Entries["note.txt"] {
					r, err := loaded.OpenRegularFile(value.(*fs.RegularFile))
					if err != nil {
						t.Fatal(err)
					}
					contents, err := io.ReadAll(r)
					r.Close()
					if err != nil || string(contents) != "old" {
						t.Fatalf("wrong remaining contents: %q, %v", contents, err)
					}
				}
			}
		})
	}
}

type removalInputFailure struct{}

func (removalInputFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type removalOutputFailure struct{}

func (removalOutputFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRemoveVersionPromptIOFailure(t *testing.T) {
	s, out := testArchiveShell(t)
	addShellRemovalVersion(s, "alpha.txt")
	addShellRemovalVersion(s, "empty")
	s.in = bufio.NewReader(io.MultiReader(strings.NewReader("1\n"), removalInputFailure{}))
	if err := s.execute("rm alpha.txt empty"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("input error: %v", err)
	}
	s.out = removalOutputFailure{}
	s.in = bufio.NewReader(strings.NewReader("all\n"))
	if err := s.execute("rm alpha.txt"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output error: %v", err)
	}
	if len(s.sb.Entries["alpha.txt"]) != 2 || len(s.sb.Entries["empty"]) != 2 {
		t.Fatal("prompt error removed versions")
	}
	s.out = out
}

func TestShellRemoveSelectedSymlinkVersion(t *testing.T) {
	s, out := testArchiveShell(t)
	v := *s.sb.Entries["file-link"]["v1"].(*fs.Symlink)
	v.Target, v.MTime = "alpha.txt", time.Unix(200, 0)
	s.sb.Entries["file-link"]["v2"] = &v
	s.nodes = archiveTree(s.sb)
	s.in = bufio.NewReader(strings.NewReader("1\n"))
	got := shellOutput(t, s, out, "rm file-link")
	if !strings.Contains(got, "-> alpha.txt") || !strings.Contains(got, "-> dir/note.txt") {
		t.Fatalf("missing symlink targets: %s", got)
	}
	if got := shellOutput(t, s, out, "cat file-link"); got != noteContents {
		t.Fatalf("remaining symlink target not restored: %q", got)
	}
	if got := shellOutput(t, s, out, "cat alpha.txt"); got != "alpha\n" {
		t.Fatalf("symlink removal changed target: %q", got)
	}
}
