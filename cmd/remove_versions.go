package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"josvisser.nl/erwtensoep/internal/fs"
)

// Collect every choice before changing the archive. Canceling or encountering
// an input/output error therefore cannot leave a partly applied selection.
func selectRemovalVersions(sb *fs.Superblock, targets []string, in *bufio.Reader, out io.Writer, allVersions, interactive bool) (map[string][]string, error) {
	selected := make(map[string][]string)
	for _, name := range targets {
		rows, err := sortedArchiveVersions(sb.Entries[name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if len(rows) == 0 {
			continue
		}
		displayName := "/" + filepath.ToSlash(name)
		if len(rows) > 1 && !allVersions {
			ids, canceled, err := promptRemovalVersions(in, out, displayName, rows)
			if err != nil || canceled {
				return nil, err
			}
			if len(ids) > 0 {
				selected[name] = ids
			}
			continue
		}
		if interactive {
			if _, err := fmt.Fprintf(out, "Remove %q and all %d saved version(s)? [y/N]: ", displayName, len(rows)); err != nil {
				return nil, err
			}
			answer, err := in.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			answer = strings.ToLower(strings.TrimSpace(answer))
			if errors.Is(err, io.EOF) && answer == "" {
				return nil, nil
			}
			if answer == "q" || answer == "quit" || answer == "cancel" {
				return nil, nil
			}
			if answer != "y" && answer != "yes" {
				continue
			}
		}
		for _, row := range rows {
			selected[name] = append(selected[name], row.id)
		}
	}
	return selected, nil
}

func promptRemovalVersions(in *bufio.Reader, out io.Writer, name string, rows []archiveVersion) ([]string, bool, error) {
	if err := writeVersionChoices(out, name, rows); err != nil {
		return nil, false, err
	}
	for {
		if _, err := fmt.Fprint(out, "Remove versions (e.g. 1,3-5; all; Enter to skip; q to cancel): "); err != nil {
			return nil, false, err
		}
		answer, readErr := in.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, false, readErr
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer == "q" || answer == "quit" || answer == "cancel" || (answer == "" && errors.Is(readErr, io.EOF)) {
			return nil, true, nil
		}
		indices, err := parseRemovalSelection(answer, len(rows))
		if err != nil {
			if errors.Is(readErr, io.EOF) {
				return nil, true, nil
			}
			if _, err := fmt.Fprintln(out, err); err != nil {
				return nil, false, err
			}
			continue
		}
		var ids []string
		for _, index := range indices {
			ids = append(ids, rows[index].id)
		}
		return ids, false, nil
	}
}

// Parse a complete answer before accepting any of its numbers. Indices are
// zero-based, deduplicated, and returned in the displayed order.
func parseRemovalSelection(answer string, count int) ([]int, error) {
	if answer == "" || answer == "n" || answer == "no" || answer == "none" || answer == "skip" {
		return nil, nil
	}
	chosen := make([]bool, count)
	if answer == "all" || answer == "a" {
		for i := range chosen {
			chosen[i] = true
		}
	} else {
		invalid := fmt.Errorf("Invalid selection: use numbers from 1 to %d, ranges such as 1-3, all, Enter to skip, or q to cancel.", count)
		parts := strings.Fields(strings.ReplaceAll(answer, ",", " "))
		if len(parts) == 0 {
			return nil, invalid
		}
		for _, part := range parts {
			first, last, ranged := strings.Cut(part, "-")
			start, err := strconv.Atoi(first)
			if err != nil || start < 1 || start > count {
				return nil, invalid
			}
			end := start
			if ranged {
				end, err = strconv.Atoi(last)
				if err != nil || end < start || end > count {
					return nil, invalid
				}
			}
			for i := start - 1; i < end; i++ {
				chosen[i] = true
			}
		}
	}
	var indices []int
	for i, yes := range chosen {
		if yes {
			indices = append(indices, i)
		}
	}
	return indices, nil
}
