package main

import (
	"bufio"
	"bytes"
	"encoding/json"
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

func TestShellFileVersionSelection(t *testing.T) {
	for _, tc := range []struct{ command, answer, suffix string }{
		{"cat alpha.txt", "2\n", "alpha\n"},
		{"cat -n alpha.txt", "2\n", "     1\talpha\n"},
		{"cat alpha.txt", "1\n", noteContents},
		{"cat alpha.txt", "0\n3\nall\n1,2\n2\n", "alpha\n"},
		{"cat alpha.txt", "2", "alpha\n"},
		{"more alpha.txt", "2\n", "alpha\n"},
		{"du -b alpha.txt", "2\n", "6\talpha.txt\n"},
		{"ls -l alpha.txt", "2\n", time.Unix(100, 0).Format("2006-01-02 15:04") + " alpha.txt (2 versions)\n"},
		{"cat alpha.txt absolute alpha.txt", "2\n", "alpha\nalpha\nalpha\n"},
		{"cat --latest alpha.txt", "", noteContents},
		{"more --latest alpha.txt", "", noteContents},
		{"du --latest -b alpha.txt", "", fmt.Sprintf("%d\talpha.txt\n", len(noteContents))},
		{"ls --latest alpha.txt", "", "alpha.txt (2 versions)\n"},
	} {
		t.Run(tc.command+tc.answer, func(t *testing.T) {
			s, out := testArchiveShell(t)
			addShellRemovalVersion(s, "alpha.txt")
			before, _ := json.Marshal(s.sb)
			s.in = bufio.NewReader(strings.NewReader(tc.answer))
			got := shellOutput(t, s, out, tc.command)
			if !strings.HasSuffix(got, tc.suffix) {
				t.Fatalf("wrong selected output: %q, want suffix %q", got, tc.suffix)
			}
			prompts := 1
			if strings.Contains(tc.command, "--latest") {
				prompts = 0
			}
			if strings.Count(got, "Saved versions of") != prompts {
				t.Fatalf("wrong number of version lists: %q", got)
			}
			after, _ := json.Marshal(s.sb)
			if !bytes.Equal(before, after) || s.nodes["/alpha.txt"].entry != s.sb.Entries["alpha.txt"]["v2"] {
				t.Fatal("selection changed the index or browsing tree")
			}
		})
	}
}

func TestShellFileSelectionCancelSkipAndIO(t *testing.T) {
	for _, command := range []string{"cat alpha.txt empty", "more alpha.txt empty", "du -b alpha.txt empty", "ls -l alpha.txt empty"} {
		for _, input := range []string{"2\nq\n", "2\n", "2\ninvalid"} {
			s, out := testArchiveShell(t)
			addShellRemovalVersion(s, "alpha.txt")
			addShellRemovalVersion(s, "empty")
			s.in = bufio.NewReader(strings.NewReader(input))
			if err := s.execute(command); !errors.Is(err, errVersionSelectionCanceled) {
				t.Fatalf("%s: expected cancellation, got %v", command, err)
			}
			if !strings.HasSuffix(out.String(), "q to cancel): ") {
				t.Fatalf("operation output appeared before all choices: %q", out.String())
			}
		}
		s, _ := testArchiveShell(t)
		addShellRemovalVersion(s, "alpha.txt")
		s.in = bufio.NewReader(removalInputFailure{})
		if err := s.execute(command); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("%s input error: %v", command, err)
		}
		s.in = bufio.NewReader(strings.NewReader("2\n"))
		s.out = removalOutputFailure{}
		if err := s.execute(command); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("%s output error: %v", command, err)
		}
	}
	s, out := testArchiveShell(t)
	addShellRemovalVersion(s, "alpha.txt")
	s.in = bufio.NewReader(strings.NewReader("\n"))
	got := shellOutput(t, s, out, "cat alpha.txt dir/note.txt")
	if !strings.HasSuffix(got, noteContents) || strings.Contains(got, "alpha\n") {
		t.Fatalf("skipping one file did not continue to next: %q", got)
	}
}

func TestShellFileSelectionSymlinksAndPaths(t *testing.T) {
	s, out := testArchiveShell(t)
	addShellRemovalVersion(s, "alpha.txt")
	link := *s.sb.Entries["file-link"]["v1"].(*fs.Symlink)
	link.Target, link.MTime = "/alpha.txt", time.Unix(300, 0)
	s.sb.Entries["file-link"]["v2"] = &link
	s.nodes = archiveTree(s.sb)
	s.in = bufio.NewReader(strings.NewReader("1\n2\n"))
	got := shellOutput(t, s, out, "cat file-link")
	if !strings.HasSuffix(got, "alpha\n") || strings.Count(got, "Saved versions of") != 2 {
		t.Fatalf("did not select symlink and its target: %q", got)
	}
	s.in = bufio.NewReader(strings.NewReader("2\n"))
	if got := shellOutput(t, s, out, "cat file-link"); !strings.HasSuffix(got, noteContents) {
		t.Fatalf("did not read older symlink target: %q", got)
	}
	// A file can become a symlink in a later version. Do not follow the newest
	// link before giving the user a chance to choose the older regular file.
	s.sb.Entries["alpha.txt"]["v3"] = &fs.Symlink{Path: "alpha.txt", Target: "missing", FileStats: fs.FileStats{MTime: time.Unix(400, 0)}}
	s.nodes = archiveTree(s.sb)
	s.in = bufio.NewReader(strings.NewReader("3\n"))
	if got := shellOutput(t, s, out, "cat alpha.txt"); !strings.HasSuffix(got, "alpha\n") {
		t.Fatalf("older regular file hidden by symlink: %q", got)
	}
	// Old files at an inferred directory remain readable by an explicit file
	// command; directory browsing and traversal still use the directory.
	file := *s.sb.Entries["alpha.txt"]["v1"].(*fs.RegularFile)
	file.Path = "dir"
	s.sb.Entries["dir"] = map[string]interface{}{"v1": &file}
	s.nodes = archiveTree(s.sb)
	if got := shellOutput(t, s, out, "cat dir"); got != "alpha\n" {
		t.Fatalf("historical file hidden by inferred directory: %q", got)
	}
	for _, name := range []string{"space dir/a b.txt", "-dash", "dir/.secret"} {
		addShellRemovalVersion(s, name)
		s.in = bufio.NewReader(strings.NewReader("2\n"))
		if got := shellOutput(t, s, out, fmt.Sprintf("cat -- %q", name)); strings.Count(got, "Saved versions of") != 1 {
			t.Fatalf("missing chooser for %q: %q", name, got)
		}
	}
}

func TestShellDuSelectsDescendantsAndListingRemainsOverview(t *testing.T) {
	s, out := testArchiveShell(t)
	v := *s.sb.Entries["alpha.txt"]["v1"].(*fs.RegularFile)
	v.Path, v.MTime = "dir/note.txt", time.Unix(200, 0)
	s.sb.Entries[v.Path]["v2"] = &v
	addShellRemovalVersion(s, "dir/.secret")
	s.nodes = archiveTree(s.sb)
	s.in = bufio.NewReader(strings.NewReader("\n2\n"))
	got := shellOutput(t, s, out, "du -sbc dir")
	total := len(noteContents) + len("one\ntwo\nthree\nfour\nfive\n")
	if !strings.HasSuffix(got, fmt.Sprintf("%d\tdir\n%d\ttotal\n", total, total)) {
		t.Fatalf("wrong selected directory total: %q", got)
	}
	for _, command := range []string{"ls dir", "ls -lR dir", "ls --latest dir/note.txt"} {
		if got := shellOutput(t, s, out, command); strings.Contains(got, "Choose version") || !strings.Contains(got, "(2 versions)") {
			t.Fatalf("listing should show overview: %q", got)
		}
	}
}

func TestShellVersionSelectionKeepsPagerAndCommands(t *testing.T) {
	s, out := testArchiveShell(t)
	addShellRemovalVersion(s, "alpha.txt")
	var diagnostics bytes.Buffer
	s.errOut = &diagnostics
	s.history = &commandHistory{}
	s.paging = true
	s.in = bufio.NewReader(strings.NewReader("more -n 2 alpha.txt\n1\nqcat alpha.txt\n2\npwd\nexit\n"))
	if err := s.run(); err != nil || diagnostics.Len() != 0 {
		t.Fatalf("run: %v, diagnostics %q", err, diagnostics.String())
	}
	got := out.String()
	if !strings.Contains(got, "--More--") || !strings.Contains(got, "alpha\nerwtensoep:/ /\n") || s.history.Len() != 0 {
		t.Fatalf("selection consumed pager/command input or recorded answers: %q", got)
	}
}

func savedVersionArchive(t *testing.T) (fs.Options, *fs.Superblock) {
	t.Helper()
	opts := fs.Options{ArchiveDir: t.TempDir(), Storage: storage.NewMemoryBackend(), MaxSize: math.MaxInt64, SuperblockWriteThreshold: math.MaxInt64}
	sb, err := fs.GetSuperblock(&opts)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	for i, content := range []string{"old", "new contents"} {
		for _, name := range []string{"a.txt", "b.txt"} {
			file := filepath.Join(source, name)
			if err := os.WriteFile(file, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(int64(100*(i+1)), 0)
			if err := os.Chtimes(file, stamp, stamp); err != nil {
				t.Fatal(err)
			}
		}
		if _, failures, err := sb.Merge(source); err != nil || failures != 0 {
			t.Fatalf("merge: %d failures, %v", failures, err)
		}
	}
	if err := sb.Write(); err != nil {
		t.Fatal(err)
	}
	return opts, sb
}

func TestRestoreCommandVersionSelection(t *testing.T) {
	previousOptions, previousOutputDir := options, outputDir
	t.Cleanup(func() { options, outputDir = previousOptions, previousOutputDir })
	opts, sb := savedVersionArchive(t)
	options = opts
	before, _ := json.Marshal(sb)
	for _, tc := range []struct {
		input, a, b      string
		latest, canceled bool
	}{
		{"2\n1\n", "old", "new contents", false, false},
		{"\n2\n", "existing", "old", false, false},
		{"2\nq\n", "existing", "", false, true},
		{"2\n", "existing", "", false, true},
		{"", "new contents", "new contents", true, false},
	} {
		t.Run(fmt.Sprintf("latest=%t/input=%q", tc.latest, tc.input), func(t *testing.T) {
			outputDir = t.TempDir()
			if err := os.WriteFile(filepath.Join(outputDir, "a.txt"), []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := &cobra.Command{}
			cmd.Flags().Bool("latest", tc.latest, "")
			cmd.SetIn(strings.NewReader(tc.input))
			var out bytes.Buffer
			cmd.SetOut(&out)
			err := restoreCmd.RunE(cmd, []string{"*.txt"})
			if errors.Is(err, errVersionSelectionCanceled) != tc.canceled || (err != nil && !tc.canceled) {
				t.Fatalf("restore: %v, output %s", err, out.String())
			}
			for name, want := range map[string]string{"a.txt": tc.a, "b.txt": tc.b} {
				data, err := os.ReadFile(filepath.Join(outputDir, name))
				if want == "" && errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil || string(data) != want {
					t.Fatalf("%s: %q, %v; want %q", name, data, err, want)
				}
			}
			if tc.latest && out.Len() != 0 {
				t.Fatalf("latest unexpectedly prompted: %s", out.String())
			}
		})
	}
	loaded, err := fs.GetSuperblock(&opts)
	after, _ := json.Marshal(loaded)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("restore changed archive: %v", err)
	}
}

func TestListCommandFileVersionSelection(t *testing.T) {
	previousOptions := options
	t.Cleanup(func() { options = previousOptions })
	options, _ = savedVersionArchive(t)
	for _, tc := range []struct {
		args           []string
		input          string
		latest, prompt bool
		stamp          string
	}{
		{[]string{"a.txt"}, "2\n", false, true, time.Unix(100, 0).Format("2006-01-02 15:04")},
		{[]string{"a.txt"}, "", true, false, time.Unix(200, 0).Format("2006-01-02 15:04")},
		{nil, "", false, false, time.Unix(200, 0).Format("2006-01-02 15:04")},
		{[]string{"*.txt"}, "", false, false, time.Unix(200, 0).Format("2006-01-02 15:04")},
	} {
		cmd := &cobra.Command{}
		cmd.Flags().Bool("latest", tc.latest, "")
		cmd.SetIn(strings.NewReader(tc.input))
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := lsCmd.RunE(cmd, tc.args); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Choose version") != tc.prompt || !strings.Contains(out.String(), tc.stamp) || !strings.Contains(out.String(), "a.txt (2 versions)") {
			t.Fatalf("wrong listing: %s", out.String())
		}
	}
}
