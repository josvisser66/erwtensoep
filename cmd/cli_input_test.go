package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"josvisser.nl/erwtensoep/internal/fs"
)

func TestShellCompletion(t *testing.T) {
	s, _ := testArchiveShell(t)
	for _, tc := range []struct{ input, want string }{
		{"ca", "cat "},
		{"  mo", "  more "},
		{"help pw", "help pwd "},
		{"cat al", "cat alpha.txt "},
		{"cat -n al", "cat -n alpha.txt "},
		{"cd di", "cd dir/"},
		{"cd -P di", "cd -P dir/"},
		{"cd dir/s", "cd dir/sub/"},
		{"cat /dir/no", "cat /dir/note.txt "},
		{"cat link/no", "cat link/note.txt "},
		{"cd li", "cd link/"},
		{"cat deep-link/../no", "cat deep-link/../note.txt "},
		{"ls .h", "ls .hidden "},
		{"ls dir/.s", "ls dir/.secret "},
		{"cd ..", "cd ../"},
		{"ls -- -da", "ls -- -dash "},
		{"ls ./-da", "ls ./-dash "},
		{"cat sp", `cat space\ dir/`},
		{`cat space\ dir/a`, `cat space\ dir/a\ b.txt `},
		{`cd "space d`, `cd "space dir/"`},
		{`cat "space dir/a`, `cat "space dir/a b.txt" `},
		{`cat 'space dir/a`, `cat 'space dir/a b.txt' `},
		{`cat space\`, `cat space\ dir/`},
		{"cd alpha", "cd alpha"},
		{"cd broken", "cd broken"},
		{"cd loop", "cd loop"},
		{"cat missing", "cat missing"},
		{"cat /missing/", "cat /missing/"},
		{"cat -n", "cat -n"},
		{"more -n ", "more -n "},
		{"du --max-depth ", "du --max-depth "},
		{"pwd ", "pwd "},
		{"quit ", "quit "},
		{"unknown al", "unknown al"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, pos, matches := s.complete(tc.input, len(tc.input))
			if got != tc.want || pos != len(tc.want) || len(matches) != 0 {
				t.Fatalf("got (%q, %d, %q), want (%q, %d)", got, pos, matches, tc.want, len(tc.want))
			}
			if got != tc.input {
				if _, err := splitShellWords(got); err != nil {
					t.Fatalf("completed line cannot be parsed: %v", err)
				}
			}
		})
	}
	if err := s.execute("cd dir"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{
		{"cat no", "cat note.txt "},
		{"cat ../al", "cat ../alpha.txt "},
		{"cat /al", "cat /alpha.txt "},
	} {
		if got, _, _ := s.complete(tc.input, len(tc.input)); got != tc.want {
			t.Errorf("in /dir, %q -> %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestShellCompletionAmbiguityAndCursor(t *testing.T) {
	s, _ := testArchiveShell(t)
	for _, tc := range []struct {
		input   string
		matches []string
	}{
		{"", shellCommands},
		{"c", []string{"cat", "cd"}},
		{"help c", []string{"cat", "cd"}},
		{"ls dir/", []string{"dir/note.txt", "dir/sub/"}},
		{"cd dir/.", []string{"dir/../", "dir/./"}},
	} {
		got, pos, matches := s.complete(tc.input, len(tc.input))
		if got != tc.input || pos != len(tc.input) || !reflect.DeepEqual(matches, tc.matches) {
			t.Errorf("%q -> (%q, %d, %q), want matches %q", tc.input, got, pos, matches, tc.matches)
		}
	}
	got, pos, matches := s.complete("ls loo", len("ls loo"))
	if got != "ls loop" || pos != len(got) || len(matches) != 0 {
		t.Fatalf("common prefix: (%q, %d, %q)", got, pos, matches)
	}
	for _, tc := range []struct {
		input, prefix, want, cursorPrefix string
	}{
		{"cat alpha.txt empty", "cat al", "cat alpha.txt empty", "cat alpha.txt "},
		{`cat "space dir/a b.txt" empty`, `cat "space dir/a`, `cat "space dir/a b.txt" empty`, `cat "space dir/a b.txt" `},
		{"ca alpha.txt", "ca", "cat alpha.txt", "cat "},
		{"cat dir/sub/long.txt", "cat dir/s", "cat dir/sub/", "cat dir/sub/"},
	} {
		got, pos, _ := s.complete(tc.input, len(tc.prefix))
		if got != tc.want || pos != len(tc.cursorPrefix) {
			t.Errorf("%q at %q -> (%q, %d), want (%q, %d)", tc.input, tc.prefix, got, pos, tc.want, len(tc.cursorPrefix))
		}
	}
	for _, pos := range []int{-1, 100, len("cat ") + 1} {
		input := "cat é"
		got, _, _ := s.complete(input, pos)
		if got != input {
			t.Errorf("invalid cursor %d changed line: %q", pos, got)
		}
	}
}

func TestCompletionPreservesSpecialFilenames(t *testing.T) {
	s, _ := testArchiveShell(t)
	for _, name := range []string{"a'b.txt", `a"b.txt`, `a\b.txt`, "a*.txt", "a?.txt", "a[1].txt", "één.txt", "中文.txt"} {
		s.sb.Entries["special/"+name] = map[string]interface{}{"v1": &fs.RegularFile{Path: "special/" + name}}
	}
	s.nodes = archiveTree(s.sb)
	for _, name := range []string{"a'b.txt", `a"b.txt`, `a\b.txt`, "a*.txt", "a?.txt", "a[1].txt", "één.txt", "中文.txt"} {
		for _, quote := range []byte{0, '\'', '"'} {
			prefix := quoteCompletion("special/"+name, quote)
			input := "cat " + prefix
			got, pos, _ := s.complete(input, len(input))
			words, err := splitShellWords(got)
			if err != nil || len(words) != 2 || words[1].text != "special/"+name || words[1].glob || pos != len(got) {
				t.Fatalf("%q -> %q (%#v), err %v", input, got, words, err)
			}
		}
	}
	// The shared prefix of two non-ASCII names must end on a rune boundary.
	s.sb.Entries["één.txt"] = map[string]interface{}{"v1": &fs.RegularFile{}}
	s.sb.Entries["ëén.txt"] = map[string]interface{}{"v1": &fs.RegularFile{}}
	s.nodes = archiveTree(s.sb)
	got, pos, _ := s.complete("cat ", len("cat "))
	if !utf8.ValidString(got) || !utf8.ValidString(got[:pos]) {
		t.Fatalf("completion split a rune: %q at %d", got, pos)
	}
}

func TestLineEditorHistoryAndCompletion(t *testing.T) {
	s, out := testArchiveShell(t)
	// Exercise actual terminal key handling, including draft restoration and
	// editing recalled commands. No OS terminal is needed for this stream.
	s.in = bufio.NewReader(strings.NewReader("pw\t\rcd di\t\r\x1b[A\x1b[A\r" +
		"cat al\x1b[A\x1b[B\t\r" +
		"\x1b[A\x01\x06\x06\x06 -n\r" +
		"\r\x1b[A\r"))
	editor := s.lineEditor()
	for i, want := range []string{"pwd ", "cd dir/", "pwd ", "cat alpha.txt ", "cat -n alpha.txt ", "", "cat -n alpha.txt "} {
		got, err := editor.ReadLine()
		if err != nil || got != want {
			t.Fatalf("line %d: got %q, err %v, want %q", i, got, err, want)
		}
	}
	if editor.History.Len() != 5 {
		t.Fatalf("blank or duplicate command added to history: %d entries", editor.History.Len())
	}
	if !strings.Contains(out.String(), "erwtensoep:/ ") {
		t.Fatalf("prompt missing from editor output: %q", out.String())
	}
}

func TestLineEditorListsMatchesAndKeepsHistoryAcrossDirectories(t *testing.T) {
	s, out := testArchiveShell(t)
	s.in = bufio.NewReader(strings.NewReader("c\t\x15cd di\t\rcat no\t\r\x1b[A\r"))
	for _, want := range []string{"cd dir/", "cat note.txt ", "cat note.txt "} {
		got, err := s.lineEditor().ReadLine()
		if err != nil || got != want {
			t.Fatalf("got %q, err %v, want %q", got, err, want)
		}
		if err := s.execute(got); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(out.String(), "cat\r\ncd\r\n") || !strings.Contains(out.String(), "erwtensoep:/dir ") {
		t.Fatalf("missing matches or updated prompt: %q", out.String())
	}
}

func TestLineEditorDoesNotConsumePagerKeys(t *testing.T) {
	s, _ := testArchiveShell(t)
	s.paging = true
	s.in = bufio.NewReader(strings.NewReader("more -n 2 dir/sub/long.txt\rqpwd\r\x04"))
	editor := s.lineEditor()
	line, err := editor.ReadLine()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.execute(line); err != nil {
		t.Fatal(err)
	}
	if line, err := editor.ReadLine(); err != nil || line != "pwd" {
		t.Fatalf("command after pager: %q, %v", line, err)
	}
	if _, err := editor.ReadLine(); !errors.Is(err, io.EOF) {
		t.Fatalf("Ctrl-D: %v", err)
	}
}

func TestLineEditorHistoryBoundedAndSessionLocal(t *testing.T) {
	s, _ := testArchiveShell(t)
	var input strings.Builder
	for i := 0; i < 105; i++ {
		fmt.Fprintf(&input, "ls %d\r", i)
	}
	s.in = bufio.NewReader(strings.NewReader(input.String()))
	editor := s.lineEditor()
	for i := 0; i < 105; i++ {
		if _, err := editor.ReadLine(); err != nil {
			t.Fatal(err)
		}
	}
	if editor.History.Len() != 100 || editor.History.At(0) != "ls 104" || editor.History.At(99) != "ls 5" {
		t.Fatalf("incorrect bounded history: %d entries", editor.History.Len())
	}
	other, _ := testArchiveShell(t)
	if other.lineEditor().History.Len() != 0 {
		t.Fatal("history leaked into another session")
	}
}
