package main

import (
	"bytes"
	"strings"
	"testing"

	"josvisser.nl/erwtensoep/internal/fs"
)

func assertStatsLine(t *testing.T, output, label, want string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, label) {
			got := strings.TrimSpace(strings.TrimPrefix(line, label))
			if got != want {
				t.Errorf("%s got %q; want %q", label, got, want)
			}
			return
		}
	}
	t.Errorf("missing label %q in:\n%s", label, output)
}

func TestWriteStatsSavings(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	stats := &fs.ArchiveStats{
		LogicalBytes: 1000, BlobBytes: 600, BlockBytes: 400, StoredBytes: 100,
		UnreferencedBlobs: 1, UnreferencedBlocks: 2,
	}
	if err := writeStats(&out, stats); err != nil {
		t.Fatal(err)
	}
	for label, want := range map[string]string{
		"Whole-file sharing:":              "400 B (400 bytes) saved (40.0% of logical data)",
		"Additional block sharing:":        "200 B (200 bytes) saved (33.3% of unique file contents)",
		"Total sharing (subtotal):":        "600 B (600 bytes) saved (60.0% of logical data)",
		"Compression:":                     "300 B (300 bytes) saved (75.0% of unique block data)",
		"Overall (sharing + compression):": "900 B (900 bytes) saved (90.0% of logical data)",
		"Logical/stored size ratio:":       "10.00:1",
		"Unreferenced indexed objects:":    "1 blobs, 2 blocks (excluded below)",
	} {
		assertStatsLine(t, out.String(), label, want)
	}
}

func TestWriteStatsEmptyAndExpansion(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := writeStats(&out, &fs.ArchiveStats{}); err != nil {
		t.Fatal(err)
	}
	assertStatsLine(t, out.String(), "Logical/stored size ratio:", "n/a (no stored block data)")
	assertStatsLine(t, out.String(), "Compression:", "0 B (0 bytes) saved (0.0% of unique block data)")
	if strings.Contains(out.String(), "NaN") || strings.Contains(out.String(), "Inf") {
		t.Fatalf("undefined percentage in empty stats: %s", out.String())
	}
	out.Reset()
	if err := writeStats(&out, &fs.ArchiveStats{LogicalBytes: 4, BlobBytes: 4, BlockBytes: 4, StoredBytes: 30}); err != nil {
		t.Fatal(err)
	}
	assertStatsLine(t, out.String(), "Compression:", "26 B (26 bytes) increase (650.0% of unique block data)")
	assertStatsLine(t, out.String(), "Overall (sharing + compression):", "26 B (26 bytes) increase (650.0% of logical data)")
	assertStatsLine(t, out.String(), "Logical/stored size ratio:", "0.13:1")
}
