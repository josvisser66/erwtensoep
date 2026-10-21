package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func TestRestoreCommandCollisions(t *testing.T) {
	previousOptions, previousOutputDir := options, outputDir
	t.Cleanup(func() { options, outputDir = previousOptions, previousOutputDir })
	opts, sb := savedVersionArchive(t)
	options = opts
	sb.Entries["a.txt/child.txt"] = make(map[string]interface{})
	for id, value := range sb.Entries["b.txt"] {
		copy := *value.(*fs.RegularFile)
		copy.Path = "a.txt/child.txt"
		sb.Entries[copy.Path][id] = &copy
	}
	if err := sb.Write(); err != nil {
		t.Fatal(err)
	}
	for _, operand := range []string{"a.txt", "a.txt/"} {
		outputDir = t.TempDir()
		cmd := &cobra.Command{}
		cmd.Flags().Bool("latest", false, "")
		cmd.SetIn(strings.NewReader("2\n"))
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := restoreCmd.RunE(cmd, []string{operand}); err != nil {
			t.Fatalf("restore %s: %v", operand, err)
		}
		file := operand
		if strings.HasSuffix(operand, "/") {
			file += "child.txt"
		}
		if data, err := os.ReadFile(filepath.Join(outputDir, file)); err != nil || string(data) != "old" {
			t.Fatalf("restore %s: %q, %v", operand, data, err)
		}
		if strings.Count(out.String(), "Saved versions of") != 1 || !strings.Contains(out.String(), "Saved versions of \"/"+file+"\"") {
			t.Fatalf("wrong object offered for version selection: %s", out.String())
		}
	}
	for _, latest := range []bool{false, true} {
		for _, operands := range [][]string{{"a.txt", "a.txt/"}, {"*.txt", "a.txt/*"}} {
			outputDir = filepath.Join(t.TempDir(), "not-created")
			cmd := &cobra.Command{}
			cmd.Flags().Bool("latest", latest, "")
			// Any attempt to read before detecting the conflict fails the test.
			cmd.SetIn(removalInputFailure{})
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := restoreCmd.RunE(cmd, operands); err == nil || !strings.Contains(err.Error(), "restore path conflict") {
				t.Fatalf("restore %v latest=%t: %v", operands, latest, err)
			}
			if out.Len() != 0 {
				t.Fatalf("conflicting restore prompted: %s", out.String())
			}
			if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
				t.Fatalf("conflicting restore created output: %v", err)
			}
		}
	}
}
