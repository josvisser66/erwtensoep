package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"josvisser.nl/erwtensoep/internal/fs"
	"josvisser.nl/erwtensoep/internal/storage"
)

const noteContents = "first\n\n\nlast\tline\n"

func testArchiveShell(t *testing.T) (*archiveShell, *bytes.Buffer) {
	t.Helper()
	backend := storage.NewMemoryBackend()
	opts := fs.Options{ArchiveDir: t.TempDir(), Storage: backend}
	sb, err := fs.GetSuperblock(&opts)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"alpha.txt": "alpha\n", ".hidden": "hidden\n", "-dash": "dash\n", "empty": "",
		"dir/note.txt": noteContents, "dir/.secret": "secret\n",
		"dir/sub/long.txt": "one\ntwo\nthree\nfour\nfive\n", "space dir/a b.txt": "space\n",
	}
	for name, data := range files {
		id := fmt.Sprintf("%016x", len(sb.Blobs)+1)
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		if _, err := io.WriteString(zw, data); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := backend.PutBlob("blocks/"+id, compressed.Bytes()); err != nil {
			t.Fatal(err)
		}
		sb.Blocks[id] = &fs.Block{ID: id, Size: uint32(len(data))}
		sb.Blobs[id] = &fs.Blob{ID: id, Size: strconv.Itoa(len(data)), Blocks: []string{id}}
		sb.Entries[name] = map[string]interface{}{"v1": &fs.RegularFile{
			Path: name, BlobID: id, FileStats: fs.FileStats{
				Size: strconv.Itoa(len(data)), Mode: 0640, Owner: "alice", Group: "staff", MTime: time.Unix(100, 0),
			},
		}}
	}
	for name, target := range map[string]string{
		"link": "dir", "deep-link": "dir/sub", "file-link": "dir/note.txt", "absolute": "/alpha.txt",
		"broken": "missing", "loop1": "loop2", "loop2": "loop1", "escape": "../../etc/passwd",
	} {
		sb.Entries[name] = map[string]interface{}{"v1": &fs.Symlink{
			Path: name, Target: target, FileStats: fs.FileStats{Size: strconv.Itoa(len(target)), Mode: os.ModeSymlink | 0777},
		}}
	}
	// Exercise the same serialized metadata that cli reads in production.
	if err := sb.Write(); err != nil {
		t.Fatal(err)
	}
	sb, err = fs.GetSuperblock(&opts)
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	return newArchiveShell(sb, strings.NewReader(""), out, io.Discard), out
}

func shellOutput(t *testing.T, s *archiveShell, out *bytes.Buffer, command string) string {
	t.Helper()
	out.Reset()
	if err := s.execute(command); err != nil && !errors.Is(err, errShellHelp) {
		t.Fatalf("%s: %v", command, err)
	}
	return out.String()
}

func TestShellNavigation(t *testing.T) {
	s, out := testArchiveShell(t)
	for _, step := range []struct{ command, cwd, output string }{
		{"cd dir/sub", "/dir/sub", ""},
		{"cd ..", "/dir", ""},
		{"cd -", "/dir/sub", "/dir/sub\n"},
		{"cd /../../", "/", ""},
		{`cd "space dir"`, "/space dir", ""},
		{"cd", "/", ""},
		{"cd link", "/link", ""},
		{"pwd -P", "/link", "/dir\n"},
		{"pwd -L", "/link", "/link\n"},
		{"cd -P sub", "/dir/sub", ""},
		{"cd /link", "/link", ""},
		{"cd ..", "/", ""},
		{"cd /deep-link", "/deep-link", ""},
		{"cd -P ..", "/dir", ""},
		{"cd", "/", ""},
	} {
		if got := shellOutput(t, s, out, step.command); got != step.output || s.cwd != step.cwd {
			t.Errorf("%s: output %q, cwd %q; want %q, %q", step.command, got, s.cwd, step.output, step.cwd)
		}
	}
	for _, command := range []string{"cd missing", "cd missing/..", "cd alpha.txt", "cd alpha.txt/..", "cd loop1", "cd broken", `cd ""`, "cd dir sub", "cd -x"} {
		before := s.cwd
		if err := s.execute(command); err == nil || s.cwd != before {
			t.Errorf("%s: error %v, cwd %q -> %q", command, err, before, s.cwd)
		}
	}
	for _, name := range []string{"alpha.txt/..", "alpha.txt/", "escape", "loop1"} {
		if _, err := s.resolve(name, true); err == nil {
			t.Errorf("resolve %q unexpectedly succeeded", name)
		}
	}
}

func TestShellListing(t *testing.T) {
	s, out := testArchiveShell(t)
	for _, tc := range []struct{ command, want string }{
		{"ls dir", "note.txt\nsub\n"},
		{"ls -a dir", ".\n..\n.secret\nnote.txt\nsub\n"},
		{"ls -Ar dir", "sub\nnote.txt\n.secret\n"},
		{"ls -SF dir", "note.txt\nsub/\n"},
		{"ls -dF dir link", "dir/\nlink@\n"},
		{"ls link", "note.txt\nsub\n"},
		{"ls -RF dir", "dir:\nnote.txt\nsub/\n\ndir/sub:\nlong.txt\n"},
		{"ls dir/*.txt", "dir/note.txt\n"},
		{"ls link/*/*.txt", "link/sub/long.txt\n"},
		{`ls space\ dir/"a b.txt"`, "space dir/a b.txt\n"},
		{"ls -- -dash", "-dash\n"},
		{"ls -r -- -dash alpha.txt", "alpha.txt\n-dash\n"},
		{"ls -S dir/.secret dir/note.txt", "dir/note.txt\ndir/.secret\n"},
	} {
		if got := shellOutput(t, s, out, tc.command); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.command, got, tc.want)
		}
	}
	if got := shellOutput(t, s, out, "ls -lh dir/note.txt file-link"); !strings.Contains(got, "alice staff") || !strings.Contains(got, "file-link -> dir/note.txt") || !strings.Contains(got, " B") {
		t.Errorf("long listing missing metadata: %q", got)
	}
	out.Reset()
	if err := s.execute("ls missing alpha.txt"); err == nil || out.String() != "alpha.txt\n" {
		t.Errorf("multi-operand ls: output=%q err=%v", out.String(), err)
	}
}

func TestShellCat(t *testing.T) {
	s, out := testArchiveShell(t)
	for _, tc := range []struct{ command, want string }{
		{"cat alpha.txt empty absolute", "alpha\nalpha\n"},
		{"cat file-link", noteContents},
		{"cat -n alpha.txt absolute", "     1\talpha\n     2\talpha\n"},
		{"cat -bnsET dir/note.txt", "     1\tfirst$\n$\n     2\tlast^Iline$\n"},
		{"cat -- -dash", "dash\n"},
		{`cat "space dir/a b.txt"`, "space\n"},
	} {
		if got := shellOutput(t, s, out, tc.command); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.command, got, tc.want)
		}
	}
	for _, command := range []string{"cat", "cat dir", "cat broken", "cat loop1", "cat escape", "cat --unknown"} {
		if err := s.execute(command); err == nil {
			t.Errorf("%s unexpectedly succeeded", command)
		}
	}
	out.Reset()
	if err := s.execute("cat missing alpha.txt"); err == nil || out.String() != "alpha\n" {
		t.Errorf("multi-operand cat: output=%q err=%v", out.String(), err)
	}
}

func TestShellDu(t *testing.T) {
	s, out := testArchiveShell(t)
	subSize := len("one\ntwo\nthree\nfour\nfive\n")
	total := subSize + len(noteContents) + len("secret\n")
	for _, tc := range []struct{ command, want string }{
		{"du -sb dir", fmt.Sprintf("%d\tdir\n", total)},
		{"du -b -d 1 dir", fmt.Sprintf("%d\tdir/sub\n%d\tdir\n", subSize, total)},
		{"du -ab --max-depth=1 dir", fmt.Sprintf("7\tdir/.secret\n%d\tdir/note.txt\n%d\tdir/sub\n%d\tdir\n", len(noteContents), subSize, total)},
		{"du -sbc alpha.txt absolute", "6\talpha.txt\n10\tabsolute\n16\ttotal\n"},
		{"du -sk dir", "1\tdir\n"},
		{"du -sm dir", "1\tdir\n"},
		{"du -sb link", "3\tlink\n"},
	} {
		if got := shellOutput(t, s, out, tc.command); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.command, got, tc.want)
		}
	}
	for _, command := range []string{"du -d -1", "du -d nope", "du -sd 2", "du missing"} {
		if err := s.execute(command); err == nil {
			t.Errorf("%s unexpectedly succeeded", command)
		}
	}
}

func TestShellUsesLatestVersion(t *testing.T) {
	s, out := testArchiveShell(t)
	newVersion := *s.sb.Entries["dir/note.txt"]["v1"].(*fs.RegularFile)
	newVersion.Path = "alpha.txt"
	newVersion.MTime = time.Unix(200, 0)
	s.sb.Entries["alpha.txt"]["v2"] = &newVersion
	s.nodes = archiveTree(s.sb)
	if got := shellOutput(t, s, out, "cat --latest alpha.txt"); got != noteContents {
		t.Fatalf("did not read the latest version: %q", got)
	}
	if got := shellOutput(t, s, out, "du --latest -sb alpha.txt"); got != fmt.Sprintf("%d\talpha.txt\n", len(noteContents)) {
		t.Fatalf("did not count only the latest version: %q", got)
	}
}

func TestShellMore(t *testing.T) {
	s, out := testArchiveShell(t)
	if got := shellOutput(t, s, out, "more -2 +2 dir/sub/long.txt"); got != "two\nthree\nfour\nfive\n" {
		t.Fatalf("noninteractive more: %q", got)
	}
	if got := shellOutput(t, s, out, "more -s dir/note.txt"); got != "first\n\nlast\tline\n" {
		t.Fatalf("squeezed more: %q", got)
	}
	s.paging = true
	s.in = bufio.NewReader(strings.NewReader("\nq"))
	got := shellOutput(t, s, out, "more -n 2 dir/sub/long.txt")
	if !strings.Contains(got, "one\ntwo\n--More--") || !strings.Contains(got, "three\n--More--") || strings.Contains(got, "four\n") {
		t.Fatalf("Enter/quit paging: %q", got)
	}
	s.in = bufio.NewReader(strings.NewReader(" "))
	got = shellOutput(t, s, out, "more -n 3 dir/sub/long.txt")
	if strings.Count(got, "--More--") != 1 || !strings.Contains(got, "four\nfive\n") {
		t.Fatalf("Space paging: %q", got)
	}
	s.in = bufio.NewReader(strings.NewReader(""))
	got = shellOutput(t, s, out, "more -n 1 alpha.txt")
	if strings.Contains(got, "--More--") {
		t.Fatalf("unexpected prompt at EOF: %q", got)
	}
	for _, command := range []string{"more", "more -0 alpha.txt", "more +0 alpha.txt", "more -n nope alpha.txt"} {
		if err := s.execute(command); err == nil {
			t.Errorf("%s unexpectedly succeeded", command)
		}
	}
}

func TestShellLoopAndHelp(t *testing.T) {
	s, out := testArchiveShell(t)
	errs := &bytes.Buffer{}
	s.errOut = errs
	s.in = bufio.NewReader(strings.NewReader("cd dir\ncat missing\nnope\n'\nls\nquit\ncat alpha.txt\n"))
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "erwtensoep:/ erwtensoep:/dir/ ") || !strings.Contains(out.String(), "note.txt\nsub\n") || strings.Contains(out.String(), "alpha\n") {
		t.Fatalf("shell output: %q", out.String())
	}
	if strings.Count(errs.String(), "\n") != 3 {
		t.Fatalf("expected three errors: %q", errs.String())
	}
	s.in = bufio.NewReader(strings.NewReader("pwd")) // EOF without a newline
	out.Reset()
	if err := s.run(); err != nil || !strings.Contains(out.String(), "/dir\n") {
		t.Fatalf("EOF: output %q, err %v", out.String(), err)
	}
	for _, name := range []string{"cd", "pwd", "ls", "cat", "more", "du", "rm"} {
		if got := shellOutput(t, s, out, "help "+name); !strings.Contains(got, "Usage: "+name) {
			t.Errorf("missing help for %s: %q", name, got)
		}
	}
}

func TestShellQuotingAndGlobs(t *testing.T) {
	s, _ := testArchiveShell(t)
	for _, tc := range []struct {
		line string
		want []string
	}{
		{`cat 'space dir/a b.txt' alpha\ file ""`, []string{"cat", "space dir/a b.txt", "alpha file", ""}},
		{`cat "a\"b" 'a\b' "a\b"`, []string{"cat", `a"b`, `a\b`, `a\b`}},
		{`ls dir/*`, []string{"ls", "dir/note.txt", "dir/sub"}},
		{`ls dir/.*`, []string{"ls", "dir/.secret"}},
		{`ls dir/*/`, []string{"ls", "dir/sub/"}},
		{`ls deep-link/../*.txt`, []string{"ls", "deep-link/../note.txt"}},
		{`ls dir/"*" dir/\*`, []string{"ls", "dir/*", "dir/*"}},
		{`ls d?r/[ns]*`, []string{"ls", "dir/note.txt", "dir/sub"}},
		{`ls unmatched*`, []string{"ls", "unmatched*"}},
	} {
		words, err := splitShellWords(tc.line)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, word := range words {
			got = append(got, s.expand(word)...)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.line, got, tc.want)
		}
	}
	for _, line := range []string{`cat "unfinished`, `cat dangling\`} {
		if _, err := splitShellWords(line); err == nil {
			t.Errorf("%s unexpectedly parsed", line)
		}
	}
}

func TestArchiveTreeLatestAndConflictingDirectories(t *testing.T) {
	old := &fs.RegularFile{Path: "file", FileStats: fs.FileStats{MTime: time.Unix(-100, 0)}}
	newest := &fs.RegularFile{Path: "file", FileStats: fs.FileStats{MTime: time.Time{}}}
	tie := &fs.RegularFile{Path: "file", FileStats: fs.FileStats{MTime: time.Time{}}}
	sb := &fs.Superblock{Entries: map[string]map[string]interface{}{
		"file": {"old": old, "a": newest, "z": tie},
		"dir":  {"old": old}, "dir/child": {"new": newest},
	}}
	// time.Time{} predates Unix(-100), so use a clearly older archive version.
	old.MTime = time.Time{}.Add(-time.Hour)
	nodes := archiveTree(sb)
	if nodes["/file"].entry != tie || !nodes["/dir"].isDir() || len(nodes["/dir"].children) != 1 {
		t.Fatalf("unexpected archive tree: %#v", nodes)
	}
}
