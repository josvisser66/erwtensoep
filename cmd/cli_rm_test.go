package main

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"josvisser.nl/erwtensoep/internal/fs"
)

func TestShellRemoveFilesAndVersions(t *testing.T) {
	s, out := testArchiveShell(t)
	old := *s.sb.Entries["empty"]["v1"].(*fs.RegularFile)
	old.Path = "alpha.txt"
	s.sb.Entries["alpha.txt"]["older"] = &old
	s.in = bufio.NewReader(strings.NewReader("all\n"))
	got := shellOutput(t, s, out, "rm -v alpha.txt alpha.txt")
	if !strings.Contains(got, "Saved versions of") || !strings.HasSuffix(got, "removed \"/alpha.txt\" (all saved versions)\n") {
		t.Fatalf("verbose output: %q", got)
	}
	if _, exists := s.sb.Entries["alpha.txt"]; exists {
		t.Fatal("saved versions remain after rm")
	}
	if _, err := s.resolve("alpha.txt", false); !errors.Is(err, errArchiveNotExist) {
		t.Fatalf("removed file still resolves: %v", err)
	}
	if matches := s.completePath("al", false, false); len(matches) != 0 {
		t.Fatalf("removed file still completes: %v", matches)
	}
	if _, ok := s.sb.Blobs[old.BlobID]; !ok {
		t.Fatal("removed a blob still used by empty")
	}
	shellOutput(t, s, out, "cat empty")
}

func addShellRemovalVersion(s *archiveShell, name string) {
	v := *s.sb.Entries["dir/note.txt"]["v1"].(*fs.RegularFile)
	v.Path, v.MTime = name, time.Unix(200, 0)
	s.sb.Entries[name]["v2"] = &v
	s.nodes = archiveTree(s.sb)
}

func TestShellRemoveSelectedVersions(t *testing.T) {
	for _, tc := range []struct {
		answer, remaining, contents string
	}{
		{"1\n", "v1", "alpha\n"},
		{"2\n", "v2", noteContents},
		{"0\n3\n1,bad\n1-3\n2-1\n2,2\n", "v2", noteContents},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			s, out := testArchiveShell(t)
			addShellRemovalVersion(s, "alpha.txt")
			s.in = bufio.NewReader(strings.NewReader(tc.answer))
			got := shellOutput(t, s, out, "rm -v alpha.txt")
			if len(s.sb.Entries["alpha.txt"]) != 1 || s.sb.Entries["alpha.txt"][tc.remaining] == nil {
				t.Fatalf("wrong versions retained: %v", s.sb.Entries["alpha.txt"])
			}
			if !strings.Contains(got, "MODIFIED") || !strings.Contains(got, "v2") || !strings.Contains(got, "1 remaining") {
				t.Fatalf("missing version list or removal result: %s", got)
			}
			if got := shellOutput(t, s, out, "cat alpha.txt"); got != tc.contents {
				t.Fatalf("latest remaining version unreadable: %q", got)
			}
			if got := shellOutput(t, s, out, "ls alpha.txt"); got != "alpha.txt\n" {
				t.Fatalf("version count not refreshed: %s", got)
			}
		})
	}
}

func TestShellRemoveVersionChoicesAndFlags(t *testing.T) {
	for _, tc := range []struct {
		command, answer string
		remaining       int
		picker          bool
	}{
		{"rm alpha.txt", "1-2\n", 0, true},
		{"rm alpha.txt", "ALL\n", 0, true},
		{"rm alpha.txt", "\n", 2, true},
		{"rm alpha.txt", "q\n", 2, true},
		{"rm alpha.txt", "", 2, true},
		{"rm -fi alpha.txt", "1\n", 1, true},
		{"rm -if alpha.txt", "", 0, false},
		{"rm -f alpha.txt", "", 0, false},
		{"rm --all-versions alpha.txt", "", 0, false},
		{"rm -i --all-versions alpha.txt", "n\n", 2, false},
		{"rm --all-versions -i alpha.txt", "y\n", 0, false},
	} {
		t.Run(tc.command+tc.answer, func(t *testing.T) {
			s, out := testArchiveShell(t)
			addShellRemovalVersion(s, "alpha.txt")
			s.in = bufio.NewReader(strings.NewReader(tc.answer))
			got := shellOutput(t, s, out, tc.command)
			if len(s.sb.Entries["alpha.txt"]) != tc.remaining || strings.Contains(got, "Remove versions (") != tc.picker {
				t.Fatalf("remaining=%d output=%s", len(s.sb.Entries["alpha.txt"]), got)
			}
		})
	}
}

func TestShellRemoveVersionCancellationIsAtomic(t *testing.T) {
	for _, answer := range []string{"1\nq\n", "1\n", "1\ninvalid"} {
		s, out := testArchiveShell(t)
		addShellRemovalVersion(s, "alpha.txt")
		addShellRemovalVersion(s, "empty")
		s.in = bufio.NewReader(strings.NewReader(answer))
		shellOutput(t, s, out, "rm alpha.txt empty")
		if len(s.sb.Entries["alpha.txt"]) != 2 || len(s.sb.Entries["empty"]) != 2 {
			t.Fatal("canceled selection removed previously selected versions")
		}
	}
}

func TestShellRemoveVersionSelectionKeepsFollowingCommands(t *testing.T) {
	s, out := testArchiveShell(t)
	addShellRemovalVersion(s, "alpha.txt")
	var errs bytes.Buffer
	s.errOut = &errs
	s.history = &commandHistory{}
	s.in = bufio.NewReader(strings.NewReader("rm alpha.txt\n1\ncat alpha.txt\nexit\n"))
	if err := s.run(); err != nil || errs.Len() != 0 || !strings.Contains(out.String(), "erwtensoep:/ alpha\n") || s.history.Len() != 0 {
		t.Fatalf("error=%v output=%q diagnostics=%q", err, out.String(), errs.String())
	}
}

func TestShellRemoveRecursiveVersionSelection(t *testing.T) {
	s, out := testArchiveShell(t)
	addShellRemovalVersion(s, "dir/note.txt")
	s.in = bufio.NewReader(strings.NewReader("1\n"))
	shellOutput(t, s, out, "cd dir/sub")
	shellOutput(t, s, out, "rm -r /dir")
	if s.cwd != "/dir" || len(s.sb.Entries["dir/note.txt"]) != 1 || s.nodes["/dir/sub"] != nil {
		t.Fatalf("recursive selection did not preserve selected history: cwd=%s", s.cwd)
	}
	if got := shellOutput(t, s, out, "cat note.txt"); got != noteContents {
		t.Fatalf("retained recursive version unreadable: %q", got)
	}
}

func TestShellRemoveGlobsAndLiteralNames(t *testing.T) {
	s, out := testArchiveShell(t)
	for _, name := range []string{"literal*.txt", "literal-other.txt", "literal[1].txt", "literal1.txt"} {
		copy := *s.sb.Entries["alpha.txt"]["v1"].(*fs.RegularFile)
		copy.Path = name
		s.sb.Entries[name] = map[string]interface{}{"v1": &copy}
	}
	s.nodes = archiveTree(s.sb)
	shellOutput(t, s, out, `rm 'literal*.txt' literal\[1\].txt -- -dash`)
	for _, name := range []string{"literal*.txt", "literal[1].txt", "-dash"} {
		if s.nodes["/"+name] != nil {
			t.Errorf("%s was not removed", name)
		}
	}
	for _, name := range []string{"literal-other.txt", "literal1.txt"} {
		if s.nodes["/"+name] == nil {
			t.Errorf("literal operand accidentally matched %s", name)
		}
	}
	shellOutput(t, s, out, "cd dir")
	shellOutput(t, s, out, "rm *.txt")
	if s.nodes["/dir/note.txt"] != nil || s.nodes["/dir/.secret"] == nil || s.nodes["/dir/sub/long.txt"] == nil {
		t.Fatal("glob did not stay in the current directory")
	}
	shellOutput(t, s, out, `rm "/space dir/a b.txt"`)
	if s.nodes["/space dir"] != nil {
		t.Fatal("empty inferred directory remains")
	}
}

func TestShellRemoveDirectoriesAndSymlinks(t *testing.T) {
	s, out := testArchiveShell(t)
	// A symlink within a deleted directory must not delete its outside target.
	s.sb.Entries["dir/outside"] = map[string]interface{}{"v1": &fs.Symlink{Path: "dir/outside", Target: "/alpha.txt"}}
	// Older file versions at the directory path must survive directory removal.
	copy := *s.sb.Entries["alpha.txt"]["v1"].(*fs.RegularFile)
	copy.Path = "dir"
	s.sb.Entries["dir"] = map[string]interface{}{"v1": &copy}
	s.nodes = archiveTree(s.sb)
	for _, command := range []string{"rm dir/", "rm -r link/", "rm -f alpha.txt/", "rm -rf /", "rm -rf dir/..", "rm -rf dir/."} {
		before := len(s.sb.Entries)
		if err := s.execute(command); err == nil || len(s.sb.Entries) != before {
			t.Errorf("%s: error %v, entries %d -> %d", command, err, before, len(s.sb.Entries))
		}
	}
	shellOutput(t, s, out, "rm -r link broken loop1")
	if s.nodes["/link"] != nil || s.nodes["/broken"] != nil || s.nodes["/loop1"] != nil || s.nodes["/dir/note.txt"] == nil {
		t.Fatal("removing symlinks changed their targets")
	}
	shellOutput(t, s, out, "cd dir")
	shellOutput(t, s, out, "cd sub")
	shellOutput(t, s, out, "rm -Rv /dir/")
	if s.cwd != "/" || s.previous != "" {
		t.Fatalf("invalid navigation state after recursive removal: cwd=%q previous=%q", s.cwd, s.previous)
	}
	for name := range s.sb.Entries {
		if strings.HasPrefix(name, "dir/") {
			t.Errorf("recursive removal left %s", name)
		}
	}
	if got := shellOutput(t, s, out, "cat dir"); got != "alpha\n" {
		t.Fatalf("same-named file was changed: %q", got)
	}
	if got := shellOutput(t, s, out, "cat alpha.txt"); got != "alpha\n" {
		t.Fatalf("outside data was changed: %q", got)
	}
	shellOutput(t, s, out, "ls")
}

func TestShellRemoveThroughSymlinkAndRefreshCwd(t *testing.T) {
	s, out := testArchiveShell(t)
	shellOutput(t, s, out, "cd deep-link")
	shellOutput(t, s, out, "rm long.txt")
	if s.nodes["/dir/sub"] != nil || s.nodes["/deep-link"] == nil || s.cwd != "/" {
		t.Fatalf("unexpected tree or cwd after last file removed: cwd=%s", s.cwd)
	}
	if s.nodes["/dir/note.txt"] == nil {
		t.Fatal("deleted unrelated sibling")
	}
}

func TestShellRemoveForceAndPartialErrors(t *testing.T) {
	s, out := testArchiveShell(t)
	for _, command := range []string{"rm", "rm missing", "rm -f alpha.txt/child", "rm -f loop1/child", "rm --bad alpha.txt"} {
		if err := s.execute(command); err == nil {
			t.Errorf("%s unexpectedly succeeded", command)
		}
	}
	for _, command := range []string{"rm -f", "rm -f missing", "rm -f absent/*"} {
		shellOutput(t, s, out, command)
	}
	if err := s.execute("rm missing alpha.txt"); err == nil || s.nodes["/alpha.txt"] != nil {
		t.Fatalf("valid operand was not removed with missing operand: %v", err)
	}
}

func TestShellRemoveInteractive(t *testing.T) {
	for _, tc := range []struct {
		command, answer   string
		removed, prompted bool
	}{
		{"rm -i alpha.txt", "yes\n", true, true},
		{"rm -i alpha.txt", "n\n", false, true},
		{"rm -i alpha.txt", "\n", false, true},
		{"rm -i alpha.txt", "", false, true},
		{"rm -fi alpha.txt", "y\n", true, true},
		{"rm -if alpha.txt", "", true, false},
		{"rm --force --interactive alpha.txt", "no\n", false, true},
	} {
		t.Run(tc.command+tc.answer, func(t *testing.T) {
			s, out := testArchiveShell(t)
			s.in = bufio.NewReader(strings.NewReader(tc.answer))
			shellOutput(t, s, out, tc.command)
			if (s.nodes["/alpha.txt"] == nil) != tc.removed || strings.Contains(out.String(), "[y/N]") != tc.prompted {
				t.Fatalf("removed=%t output=%q", s.nodes["/alpha.txt"] == nil, out.String())
			}
		})
	}
}

func TestShellRemoveConfirmationKeepsFollowingCommands(t *testing.T) {
	s, out := testArchiveShell(t)
	var errs bytes.Buffer
	s.errOut = &errs
	s.history = &commandHistory{}
	s.in = bufio.NewReader(strings.NewReader("rm -i alpha.txt\ny\ncat dir/note.txt\nexit\n"))
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if errs.Len() != 0 || !strings.Contains(out.String(), noteContents) || s.nodes["/alpha.txt"] != nil || s.history.Len() != 0 {
		t.Fatalf("output=%q errors=%q history=%v", out.String(), errs.String(), s.history.entries)
	}
}
