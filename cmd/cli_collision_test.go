package main

import (
	"bufio"
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"josvisser.nl/erwtensoep/internal/fs"
)

func collisionShell(t *testing.T) (*archiveShell, *bytes.Buffer) {
	t.Helper()
	s, out := testArchiveShell(t)
	for _, name := range []string{"a", "nested/a"} {
		old := *s.sb.Entries["alpha.txt"]["v1"].(*fs.RegularFile)
		old.Path = name
		newer := *s.sb.Entries["dir/note.txt"]["v1"].(*fs.RegularFile)
		newer.Path, newer.MTime = name, time.Unix(200, 0)
		child := old
		child.Path = name + "/child.txt"
		s.sb.Entries[name] = map[string]interface{}{"v1": &old, "v2": &newer}
		s.sb.Entries[child.Path] = map[string]interface{}{"v1": &child}
	}
	s.sb.Entries["alias"] = map[string]interface{}{"v1": &fs.Symlink{Path: "alias", Target: "/a"}}
	s.nodes = archiveTree(s.sb)
	return s, out
}

func TestShellCollisionCommands(t *testing.T) {
	s, out := collisionShell(t)
	for _, tc := range []struct{ command, want string }{
		{"ls --latest a", "a (2 versions)\n"},
		{"ls a/", "child.txt\n"},
		{"ls --latest -dF a a/", "a (2 versions)\na/\n"},
		{"ls nested", "a (2 versions)\na/\n"},
		{"ls -R nested", "nested:\na (2 versions)\na/\n\nnested/a/:\nchild.txt\n"},
		{"cat --latest a", noteContents},
		{"more --latest a", noteContents},
		{"cat a/child.txt", "alpha\n"},
		{"cat --latest alias", noteContents},
		{"cat alias/child.txt", "alpha\n"},
		{"du --latest -sb a", fmt.Sprintf("%d\ta\n", len(noteContents))},
		{"du -sb a/", "6\ta/\n"},
		{"du --latest -ab nested", fmt.Sprintf("%d\tnested/a\n6\tnested/a/child.txt\n6\tnested/a/\n%d\tnested\n", len(noteContents), len(noteContents)+6)},
	} {
		if got := shellOutput(t, s, out, tc.command); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.command, got, tc.want)
		}
	}
	for _, command := range []string{"cat a/", "more a/", "versions a/"} {
		out.Reset()
		if err := s.execute(command); err == nil || out.Len() != 0 {
			t.Errorf("%s must reject directory without version prompts: %v, %q", command, err, out.String())
		}
	}
	assertVersionIDs(t, shellOutput(t, s, out, "versions a"), []string{"v2", "v1"})
	for _, command := range []string{"cat a", "more a", "ls a", "du -sb a"} {
		s.in = bufio.NewReader(strings.NewReader("2\n"))
		got := shellOutput(t, s, out, command)
		if strings.Count(got, "Saved versions of") != 1 || strings.Contains(got, "child.txt") {
			t.Errorf("%s must select only the file: %q", command, got)
		}
	}
	for _, command := range []string{"cd a", "cd -P /a", "cd /alias", "cd -P /alias"} {
		shellOutput(t, s, out, command)
		if got := shellOutput(t, s, out, "pwd -P"); got != "/a\n" {
			t.Errorf("%s: physical pwd %q", command, got)
		}
		if got := shellOutput(t, s, out, "cat child.txt"); got != "alpha\n" {
			t.Errorf("%s: relative child %q", command, got)
		}
		shellOutput(t, s, out, "cd /")
	}
}

func TestShellCollisionCompletionAndGlobs(t *testing.T) {
	s, _ := collisionShell(t)
	for _, command := range []string{"ls", "cat", "more", "du", "rm", "versions"} {
		line := command + " nested/a"
		got, _, matches := s.complete(line, len(line))
		if got != line || !reflect.DeepEqual(matches, []string{"nested/a", "nested/a/"}) {
			t.Errorf("%s completion: %q, %v", command, got, matches)
		}
	}
	for _, tc := range []struct{ line, want string }{
		{"cd nested/a", "cd nested/a/"},
		{"cat a/ch", "cat a/child.txt "},
		{"cat alias/ch", "cat alias/child.txt "},
	} {
		if got, _, _ := s.complete(tc.line, len(tc.line)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.line, got, tc.want)
		}
	}
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"nested/*", []string{"nested/a", "nested/a/"}},
		{"nested/*/", []string{"nested/a/"}},
		{"nested/*/*", []string{"nested/a/child.txt"}},
		{"nested/a/*", []string{"nested/a/child.txt"}},
		{"alias/*", []string{"alias/child.txt"}},
	} {
		words, err := splitShellWords(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.expand(words[0]); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

func TestShellSymlinkDirectoryCollision(t *testing.T) {
	s, out := collisionShell(t)
	child := *s.sb.Entries["alpha.txt"]["v1"].(*fs.RegularFile)
	child.Path = "alias/local.txt"
	s.sb.Entries[child.Path] = map[string]interface{}{"v1": &child}
	s.nodes = archiveTree(s.sb)
	// The bare alias follows its target file; a slash selects its own inferred
	// directory rather than the target's same-named directory.
	if got := shellOutput(t, s, out, "cat --latest alias"); got != noteContents {
		t.Fatalf("bare symlink: %q", got)
	}
	if got := shellOutput(t, s, out, "ls alias/"); got != "local.txt\n" {
		t.Fatalf("inferred directory: %q", got)
	}
	if got, _, matches := s.complete("cat alias", len("cat alias")); got != "cat alias" || !reflect.DeepEqual(matches, []string{"alias", "alias/"}) {
		t.Fatalf("symlink/directory completion: %q, %v", got, matches)
	}
	shellOutput(t, s, out, "cd alias")
	if got := shellOutput(t, s, out, "pwd -P"); got != "/alias\n" {
		t.Fatalf("cd followed symlink instead of inferred directory: %q", got)
	}
	shellOutput(t, s, out, "rm -rf /alias/")
	if len(s.sb.Entries["alias"]) != 1 || len(s.sb.Entries["a/child.txt"]) != 1 || len(s.sb.Entries[child.Path]) != 0 {
		t.Fatal("directory removal changed the symlink or target")
	}
	if err := s.execute("rm -rf /alias/"); err == nil {
		t.Fatal("trailing slash followed final symlink after inferred directory disappeared")
	}
}

func TestShellCollisionRemoval(t *testing.T) {
	for _, tc := range []struct {
		command, answer string
		versions        int
		child           bool
	}{
		{"rm a", "1\n", 1, true},
		{"rm -r a", "all\n", 0, true},
		{"rm -rf a/", "", 2, false},
		{"rm -rf a a/", "", 0, false},
		{"rm -rf a/ a", "", 0, false},
		{"rm -rf /alias/child.txt", "", 2, false},
		{"rm -rf nested/*/", "", 2, true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			s, out := collisionShell(t)
			s.in = bufio.NewReader(strings.NewReader(tc.answer))
			shellOutput(t, s, out, "cd a")
			args := strings.Fields(tc.command)
			for i := 1; i < len(args); i++ {
				if !strings.HasPrefix(args[i], "-") && !strings.HasPrefix(args[i], "/") {
					args[i] = "/" + args[i]
				}
			}
			shellOutput(t, s, out, strings.Join(args, " "))
			if len(s.sb.Entries["a"]) != tc.versions || (len(s.sb.Entries["a/child.txt"]) > 0) != tc.child {
				t.Fatalf("wrong selection: file=%d child=%t", len(s.sb.Entries["a"]), len(s.sb.Entries["a/child.txt"]) > 0)
			}
			wantCwd := "/"
			if tc.child {
				wantCwd = "/a"
			}
			if s.cwd != wantCwd {
				t.Fatalf("cwd %q, want %q", s.cwd, wantCwd)
			}
			if tc.command == "rm -rf nested/*/" && (len(s.sb.Entries["nested/a"]) != 2 || len(s.sb.Entries["nested/a/child.txt"]) != 0) {
				t.Fatal("directory glob removed same-named file or retained descendants")
			}
		})
	}
}
