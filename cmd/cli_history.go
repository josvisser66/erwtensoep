package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const historyLimit = 100

// entries are oldest first; the terminal's History interface indexes newest
// first. pending contains only this session's additions, so saving does not
// overwrite commands entered by another session or resurrect expired entries.
type commandHistory struct {
	entries []string
	pending []string
	path    string
	warned  bool
}

func (h *commandHistory) Len() int        { return len(h.entries) }
func (h *commandHistory) At(i int) string { return h.entries[len(h.entries)-1-i] }
func (h *commandHistory) Add(line string) {
	if strings.TrimSpace(line) == "" || (h.Len() > 0 && h.At(0) == line) {
		return
	}
	h.entries = appendHistory(h.entries, line)
	if h.path != "" {
		h.pending = appendHistory(h.pending, line)
	}
}

func appendHistory(entries []string, line string) []string {
	if strings.TrimSpace(line) == "" || (len(entries) > 0 && entries[len(entries)-1] == line) {
		return entries
	}
	entries = append(entries, line)
	if len(entries) > historyLimit {
		copy(entries, entries[len(entries)-historyLimit:])
		entries = entries[:historyLimit]
	}
	return entries
}

func defaultHistoryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".erwtensoep_history"), nil
}

// Called only for interactive sessions. Passing the path provider explicitly
// lets tests use temporary files without reading or writing the user's history.
func (s *archiveShell) loadHistory(historyPath func() (string, error)) {
	h := &commandHistory{}
	s.history = h
	filename, err := historyPath()
	if err == nil {
		h.path = filename
		h.entries, err = readHistory(filename)
	}
	if err != nil {
		s.historyWarning(err)
	}
}

func readHistory(filename string) ([]string, error) {
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: history must be a regular file", filename)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	// An empty history file is equivalent to a new history.
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var lines []string
	if err := json.Unmarshal(data, &lines); err != nil {
		return nil, fmt.Errorf("%s: invalid history: %w", filename, err)
	}
	var entries []string
	for _, line := range lines {
		entries = appendHistory(entries, line)
	}
	return entries, nil
}

func (h *commandHistory) save() error {
	if h.path == "" || len(h.pending) == 0 {
		return nil
	}
	// A separate lock survives atomic replacement of the history file. Never
	// wait indefinitely for another session: retain pending commands and retry
	// on the next command or exit if its brief write currently holds the lock.
	lock, err := os.OpenFile(h.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("lock %s: %w", h.path, err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	entries, err := readHistory(h.path)
	if err != nil {
		return err // Preserve unreadable or malformed files instead of clobbering them.
	}
	for _, line := range h.pending {
		entries = appendHistory(entries, line)
	}
	file, err := os.CreateTemp(filepath.Dir(h.path), ".erwtensoep-history-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	// CreateTemp uses 0600 permissions. Encode whole commands, preserving
	// quotes, Unicode, and any embedded newlines without splitting entries.
	writeErr := json.NewEncoder(file).Encode(entries)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), h.path); err != nil {
		return err
	}
	h.pending = nil
	return nil
}

func (s *archiveShell) saveHistory() {
	if s.history == nil || len(s.history.pending) == 0 {
		return
	}
	if err := s.history.save(); err != nil {
		s.historyWarning(err)
	} else {
		s.history.warned = false
	}
}

func (s *archiveShell) historyWarning(err error) {
	if !s.history.warned {
		fmt.Fprintf(s.errOut, "Warning: command history: %v; using in-memory history until it can be saved.\n", err)
		s.history.warned = true
	}
}
