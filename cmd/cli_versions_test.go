package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"josvisser.nl/erwtensoep/internal/fs"
)

func TestShellVersions(t *testing.T) {
	s, out := testArchiveShell(t)
	// Versions sharing content must remain distinct, including equal-mtime
	// metadata changes. The sort order should agree with the browsing tree.
	for id, timestamp := range map[string]time.Time{
		"older": time.Unix(-100, 0),
		"v2":    time.Unix(200, 123456789),
		"v3":    time.Unix(200, 123456789),
	} {
		v := *s.sb.Entries["dir/note.txt"]["v1"].(*fs.RegularFile)
		v.MTime = timestamp
		v.Size = "2048"
		if id == "v3" {
			v.Mode = 0600
		}
		s.sb.Entries["dir/note.txt"][id] = &v
	}
	s.nodes = archiveTree(s.sb)
	before, err := json.Marshal(s.sb)
	if err != nil {
		t.Fatal(err)
	}
	shellOutput(t, s, out, "cd link")
	for _, command := range []string{"versions note.txt", "versions /dir/note.txt", "versions ../link/note.txt"} {
		got := shellOutput(t, s, out, command)
		assertVersionIDs(t, got, []string{"v3", "v2", "v1", "older"})
		for _, want := range []string{"VERSION", "MODIFIED", "SIZE", "MODE", "PATH", "/dir/note.txt",
			time.Unix(200, 123456789).Format(time.RFC3339Nano), "2048", "-rw-------"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing %q: %s", command, want, got)
			}
		}
	}
	got := shellOutput(t, s, out, "versions -hr note.txt")
	assertVersionIDs(t, got, []string{"older", "v1", "v2", "v3"})
	if !strings.Contains(got, "2.0 KiB") {
		t.Errorf("human-readable sizes missing: %s", got)
	}
	after, err := json.Marshal(s.sb)
	if err != nil || string(before) != string(after) || s.cwd != "/link" {
		t.Fatalf("versions changed the archive or current directory: %v", err)
	}
}

func assertVersionIDs(t *testing.T, output string, want []string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var got []string
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			t.Fatalf("empty version row: %q", output)
		}
		got = append(got, fields[0])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("version IDs = %v, want %v; output:\n%s", got, want, output)
	}
}

func TestShellVersionsPathsAndErrors(t *testing.T) {
	s, out := testArchiveShell(t)
	for _, command := range []string{`versions "space dir/a b.txt"`, "versions -- -dash", "versions .hidden", "versions empty", "versions dir/*.txt"} {
		assertVersionIDs(t, shellOutput(t, s, out, command), []string{"v1"})
	}
	for _, command := range []string{"versions", "versions alpha.txt empty", `versions ""`,
		"versions missing", "versions dir", "versions /", "versions alpha.txt/..", "versions --unknown", "versions loop1/note.txt"} {
		out.Reset()
		if err := s.execute(command); err == nil || out.Len() != 0 {
			t.Errorf("%s: error %v, output %q", command, err, out.String())
		}
	}
	if got := shellOutput(t, s, out, "help versions"); !strings.Contains(got, "Usage: versions [-hr] FILE") {
		t.Fatalf("missing help: %s", got)
	}
	if got := shellOutput(t, s, out, "help"); !strings.Contains(got, "versions [-hr] FILE") {
		t.Fatalf("missing command in shell help: %s", got)
	}
	// Paths that have become inferred directories still retain old file entries.
	s.sb.Entries["dir"] = map[string]interface{}{"old-file": &fs.RegularFile{Path: "dir", FileStats: fs.FileStats{Size: "3"}}}
	s.nodes = archiveTree(s.sb)
	assertVersionIDs(t, shellOutput(t, s, out, "versions dir"), []string{"old-file"})
}

func TestShellVersionsSymlinkHistory(t *testing.T) {
	s, out := testArchiveShell(t)
	old := *s.sb.Entries["file-link"]["v1"].(*fs.Symlink)
	old.Target = "previous-target"
	old.MTime = time.Unix(10, 0)
	s.sb.Entries["file-link"]["v2"] = &old
	s.nodes = archiveTree(s.sb)
	got := shellOutput(t, s, out, "versions file-link")
	assertVersionIDs(t, got, []string{"v2", "v1"})
	for _, target := range []string{"/file-link -> previous-target", "/file-link -> dir/note.txt"} {
		if !strings.Contains(got, target) {
			t.Errorf("link history missing %q: %s", target, got)
		}
	}
	for _, name := range []string{"link", "broken", "loop1"} {
		assertVersionIDs(t, shellOutput(t, s, out, "versions "+name), []string{"v1"})
	}
}
