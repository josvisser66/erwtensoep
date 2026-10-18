package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func historyShell(t *testing.T, filename string) (*archiveShell, *bytes.Buffer) {
	t.Helper()
	s, _ := testArchiveShell(t)
	warnings := &bytes.Buffer{}
	s.errOut = warnings
	s.loadHistory(func() (string, error) { return filename, nil })
	return s, warnings
}

func storedHistory(t *testing.T, filename string) []string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestHistoryPersistsAndIsRecalledInAnotherSession(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "history")
	first, warnings := historyShell(t, filename)
	first.in = bufio.NewReader(strings.NewReader("pwd\rcat 'space dir/a b.txt'\r"))
	for i := 0; i < 2; i++ {
		if _, err := first.lineEditor().ReadLine(); err != nil {
			t.Fatal(err)
		}
		first.saveHistory()
	}
	if warnings.Len() != 0 {
		t.Fatalf("unexpected warning: %s", warnings)
	}
	want := []string{"pwd", "cat 'space dir/a b.txt'"}
	if got := storedHistory(t, filename); !reflect.DeepEqual(got, want) {
		t.Fatalf("history before exit: %q, want %q", got, want)
	}
	second, warnings := historyShell(t, filename)
	second.in = bufio.NewReader(strings.NewReader("\x1b[A\x1b[A\r" + "ls dir\x1b[A\x1b[B\r"))
	for _, want := range []string{"pwd", "ls dir"} {
		got, err := second.lineEditor().ReadLine()
		if err != nil || got != want {
			t.Fatalf("recalled line %q, err %v; want %q", got, err, want)
		}
		second.saveHistory()
	}
	if warnings.Len() != 0 {
		t.Fatalf("unexpected warning: %s", warnings)
	}
	info, err := os.Stat(filename)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("history permissions: %v, err %v", info, err)
	}
}

func TestHistoryBoundedAndPreservesWholeCommands(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "history")
	s, _ := historyShell(t, filename)
	for i := 0; i < 105; i++ {
		s.history.Add(fmt.Sprintf("ls %d", i))
	}
	s.history.Add("ls 104")
	s.history.Add("  \t  ")
	weird := "cat \"中文/a\\b\" 'file\nname' "
	s.history.Add(weird)
	s.saveHistory()
	entries := storedHistory(t, filename)
	if len(entries) != historyLimit || entries[0] != "ls 6" || entries[historyLimit-1] != weird {
		t.Fatalf("incorrect bounded history: %q", entries)
	}
	other, _ := historyShell(t, filename)
	if other.history.Len() != historyLimit || other.history.At(0) != weird || other.history.At(99) != "ls 6" || len(other.history.pending) != 0 {
		t.Fatal("history not restored in order, or loaded commands marked for saving")
	}
	// Imported history is also capped and filtered, even if another tool wrote it.
	entries = append(entries, "", "  ", weird, "last")
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	other, _ = historyShell(t, filename)
	if other.history.Len() != historyLimit || other.history.At(0) != "last" || other.history.At(99) != "ls 7" {
		t.Fatalf("imported history not filtered and capped: %q", other.history.entries)
	}
}

func TestHistoryMergesOverlappingSessions(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "history")
	first, _ := historyShell(t, filename)
	second, _ := historyShell(t, filename)
	first.history.Add("ls")
	first.saveHistory()
	second.history.Add("pwd")
	second.saveHistory()
	first.history.Add("cd dir")
	first.saveHistory()
	second.saveHistory() // An idle/stale session must not overwrite newer entries.
	if got := storedHistory(t, filename); !reflect.DeepEqual(got, []string{"ls", "pwd", "cd dir"}) {
		t.Fatalf("lost commands from another session: %q", got)
	}
	// Old sessions must not restore entries that have since aged out of the file.
	for i := 0; i < 100; i++ {
		second.history.Add(fmt.Sprintf("ls %d", i))
	}
	second.saveHistory()
	first.history.Add("cat alpha.txt")
	first.saveHistory()
	got := storedHistory(t, filename)
	if len(got) != 100 || got[0] != "ls 1" || got[99] != "cat alpha.txt" {
		t.Fatalf("stale history resurrected: %q", got)
	}
}

func TestHistoryLockFailureRetainsCommandsForRetry(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "history")
	s, warnings := historyShell(t, filename)
	lock, err := os.OpenFile(filename+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	s.history.Add("pwd")
	s.saveHistory()
	s.history.Add("ls")
	s.saveHistory()
	if len(s.history.pending) != 2 || s.history.Len() != 2 || strings.Count(warnings.String(), "Warning:") != 1 {
		t.Fatalf("pending commands or warning handling: %#v, %q", s.history, warnings.String())
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	s.saveHistory()
	if len(s.history.pending) != 0 || !reflect.DeepEqual(storedHistory(t, filename), []string{"pwd", "ls"}) {
		t.Fatal("pending history not saved on retry")
	}
}

func TestHistoryFailuresDoNotBlockShellOrOverwriteFiles(t *testing.T) {
	for _, contents := range []string{"not json", `["pwd",`, `{"wrong":true}`} {
		t.Run(contents, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "history")
			if err := os.WriteFile(filename, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			s, warnings := historyShell(t, filename)
			s.history.Add("pwd")
			s.saveHistory()
			if s.history.At(0) != "pwd" || strings.Count(warnings.String(), "Warning:") != 1 {
				t.Fatalf("history fallback: %#v, %q", s.history, warnings.String())
			}
			if err := s.execute("ls"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filename)
			if err != nil || string(data) != contents {
				t.Fatalf("overwrote invalid history: %q, %v", data, err)
			}
			if err := os.Remove(filename); err != nil {
				t.Fatal(err)
			}
			s.saveHistory()
			if got := storedHistory(t, filename); !reflect.DeepEqual(got, []string{"pwd"}) {
				t.Fatalf("history did not recover: %q", got)
			}
		})
	}
	filename := filepath.Join(t.TempDir(), "missing", "history")
	s, warnings := historyShell(t, filename)
	s.history.Add("pwd")
	s.saveHistory()
	if warnings.Len() == 0 || s.history.At(0) != "pwd" {
		t.Fatal("missing history directory did not fall back to in-memory history")
	}
	// A missing home directory lookup should also leave a usable shell.
	s.loadHistory(func() (string, error) { return "", errors.New("home unavailable") })
	s.history.Add("ls")
	if s.history.At(0) != "ls" || !strings.Contains(warnings.String(), "home unavailable") {
		t.Fatal("home lookup error did not leave usable history")
	}
}

func TestHistoryRejectsSymlinksAndDirectories(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(`["pwd"]`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{dir, link} {
		if _, err := readHistory(filename); err == nil {
			t.Fatalf("accepted a non-regular history file: %s", filename)
		}
	}
	s, _ := historyShell(t, link)
	s.history.Add("ls")
	s.saveHistory()
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("history symlink replaced: %v, %v", info, err)
	}
	if got := storedHistory(t, target); !reflect.DeepEqual(got, []string{"pwd"}) {
		t.Fatalf("symlink target modified: %q", got)
	}
}

func TestPipedCommandsDoNotCreateHistory(t *testing.T) {
	s, _ := testArchiveShell(t)
	s.errOut = io.Discard
	s.in = bufio.NewReader(strings.NewReader("pwd\nexit\n"))
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if s.history != nil {
		t.Fatal("noninteractive shell initialized persistent history")
	}
}
