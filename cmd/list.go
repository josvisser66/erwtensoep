package main

import (
	"bufio"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	lsCmd.Flags().Bool("latest", false, "Use the newest saved version for file operands without prompting")
	rootCmd.AddCommand(lsCmd)
}

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "Lists archive entries",
	Long:  "List matching archive paths with an (N versions) suffix when applicable.\nChoose a version when naming a file directly; directory and pattern listings\nshow the newest versions. Use --latest to skip version selection.",
	RunE: func(cmd *cobra.Command, args []string) error {
		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}
		latest, err := cmd.Flags().GetBool("latest")
		if err != nil {
			return err
		}
		v := newVersionSelector(sb, bufio.NewReader(cmd.InOrStdin()), cmd.OutOrStdout(), latest)
		var names []string
		for name := range sb.Entries {
			if matchesAny(name, args) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		selected := make(map[string]*archiveVersion)
		for _, name := range names {
			// Overview listings stay prompt-free; exact file operands select
			// the metadata version just like interactive ls FILE.
			v.latest = true
			for _, arg := range args {
				if filepath.ToSlash(arg) == filepath.ToSlash(name) && !strings.ContainsAny(arg, "*?[") {
					v.latest = latest
				}
			}
			choice, err := v.choose(name)
			if err != nil {
				return err
			}
			selected[name] = choice
		}
		for _, name := range names {
			if choice := selected[name]; choice != nil {
				stats := choice.entry.GetFileStats()
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %10s %s%s\n", stats.MTime.Format("2006-01-02 15:04"), stats.Size, name, versionCountSuffix(len(sb.Entries[name]))); err != nil {
					return err
				}
			}
		}
		return nil
	},
}

func versionCountSuffix(count int) string {
	if count > 1 {
		return fmt.Sprintf(" (%d versions)", count)
	}
	return ""
}

func matchesAny(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}

	path = filepath.ToSlash(path)
	for _, pattern := range patterns {
		p := filepath.ToSlash(pattern)
		if p == "" {
			continue
		}
		if strings.ContainsAny(p, "*?[") {
			if ok, _ := filepath.Match(p, path); ok {
				return true
			}
			continue
		}
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
