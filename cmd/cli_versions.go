package main

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/pflag"
	"josvisser.nl/erwtensoep/internal/fs"
)

func (s *archiveShell) versions(args []string) error {
	f := pflag.NewFlagSet("versions", pflag.ContinueOnError)
	human := f.BoolP("human-readable", "h", false, "Show sizes in human-readable units")
	reverse := f.BoolP("reverse", "r", false, "Show oldest versions first")
	if err := s.parseFlags(f, args, "versions [-hr] FILE"); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("usage: versions [-hr] FILE (exactly one file is required)")
	}
	n, err := s.resolve(f.Arg(0), false)
	if err != nil {
		return err
	}
	if n.isDir() {
		return fmt.Errorf("%s: is a directory (use the file name without a trailing slash to list file versions)", f.Arg(0))
	}
	// Read the full history of the resolved file, not only the browsing version.
	saved := s.sb.Entries[n.entry.GetPath()]
	rows, err := sortedArchiveVersions(saved)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Arg(0), err)
	}
	if *reverse {
		slices.Reverse(rows)
	}
	return writeArchiveVersions(s.out, n.path, rows, *human, false)
}

type archiveVersion struct {
	id    string
	entry fs.Entry
}

func sortedArchiveVersions(saved map[string]interface{}) ([]archiveVersion, error) {
	rows := make([]archiveVersion, 0, len(saved))
	for id, value := range saved {
		entry, ok := value.(fs.Entry)
		if !ok {
			return nil, fmt.Errorf("invalid archived version %s", id)
		}
		rows = append(rows, archiveVersion{id, entry})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		at, bt := a.entry.GetFileStats().MTime, b.entry.GetFileStats().MTime
		if at.Equal(bt) {
			return a.id > b.id // Match archiveTree's newest-version tie break.
		}
		return at.After(bt)
	})
	return rows, nil
}

func writeArchiveVersions(out io.Writer, name string, rows []archiveVersion, human, numbered bool) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	header := "VERSION\tMODIFIED\tSIZE\tMODE\tPATH"
	if numbered {
		header = "#\t" + header
	}
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}
	for i, row := range rows {
		stats := row.entry.GetFileStats()
		size := stats.Size
		if human {
			if bytes, err := strconv.ParseUint(size, 10, 64); err == nil {
				size = humanize.IBytes(bytes)
			}
		}
		displayName := name
		if link, ok := row.entry.(*fs.Symlink); ok {
			displayName += " -> " + link.Target
		}
		if numbered {
			if _, err := fmt.Fprintf(w, "%d\t", i+1); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", row.id,
			stats.MTime.Format(time.RFC3339Nano), size, stats.Mode, displayName); err != nil {
			return err
		}
	}
	return w.Flush()
}
