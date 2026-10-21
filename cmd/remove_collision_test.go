package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func TestRemoveCommandFileDirectoryCollisions(t *testing.T) {
	previousOptions := options
	t.Cleanup(func() { options = previousOptions })
	for _, tc := range []struct {
		operands                    []string
		answer                      string
		all, canceled               bool
		fileVersions, childVersions int
	}{
		{[]string{"a.txt"}, "1\ny\n", false, false, 1, 2},
		{[]string{"a.txt/"}, "1\ny\n", false, false, 2, 1},
		{[]string{"a.txt"}, "y\n", true, false, 0, 2},
		{[]string{"a.txt/"}, "y\n", true, false, 2, 0},
		{[]string{"a.txt", "./a.txt"}, "all\ny\n", false, false, 0, 2},
		{[]string{"a.txt", "a.txt/"}, "y\n", true, false, 0, 0},
		{[]string{"a.txt/", "a.txt"}, "y\n", true, false, 0, 0},
		{[]string{"a.txt", "a.txt/*"}, "y\n", true, false, 0, 0},
		{[]string{"a.txt/"}, "all\nn\n", false, true, 2, 2},
	} {
		t.Run(fmt.Sprint(tc.operands, tc.all, tc.canceled), func(t *testing.T) {
			opts, sb := savedVersionArchive(t)
			source := t.TempDir()
			child := filepath.Join(source, "a.txt", "child.txt")
			if err := os.MkdirAll(filepath.Dir(child), 0700); err != nil {
				t.Fatal(err)
			}
			for i, contents := range []string{"old", "new contents"} {
				if err := os.WriteFile(child, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(int64(100*(i+1)), 0)
				if err := os.Chtimes(child, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				if _, failures, err := sb.Merge(source); err != nil || failures != 0 {
					t.Fatalf("merge: %d failures, %v", failures, err)
				}
			}
			if err := sb.Write(); err != nil {
				t.Fatal(err)
			}
			options = opts
			cmd := &cobra.Command{}
			cmd.Flags().Bool("all-versions", tc.all, "")
			cmd.SetIn(strings.NewReader(tc.answer))
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := removeCmd.RunE(cmd, tc.operands); (err != nil) != tc.canceled {
				t.Fatalf("remove: %v, output %s", err, out.String())
			}
			if strings.Count(out.String(), "Proceed with removal?") != 1 {
				t.Fatalf("missing final confirmation: %s", out.String())
			}
			loaded, err := fs.GetSuperblock(&opts)
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]int{"a.txt": tc.fileVersions, "a.txt/child.txt": tc.childVersions, "b.txt": 2} {
				if len(loaded.Entries[name]) != want {
					t.Errorf("%s retained %d versions, want %d", name, len(loaded.Entries[name]), want)
				}
				// Neither the summary nor chooser may include an unselected path.
				selected := want < 2 || (tc.canceled && name == "a.txt/child.txt")
				if strings.Contains(out.String(), " - "+name+" (") != selected ||
					strings.Contains(out.String(), "Saved versions of \"/"+name+"\"") != (selected && !tc.all) {
					t.Errorf("wrong summary/chooser for %s: %s", name, out.String())
				}
				for _, value := range loaded.Entries[name] {
					r, err := loaded.OpenRegularFile(value.(*fs.RegularFile))
					if err != nil {
						t.Fatal(err)
					}
					data, readErr := io.ReadAll(r)
					closeErr := r.Close()
					if readErr != nil || closeErr != nil || (string(data) != "old" && string(data) != "new contents") {
						t.Fatalf("surviving content %s: %q, %v, %v", name, data, readErr, closeErr)
					}
				}
			}
			check, err := fs.CheckArchive(&opts, nil)
			if err != nil || len(check.Issues) != 0 {
				t.Fatalf("removal left inconsistencies: %v, %+v", err, check)
			}
		})
	}
}
