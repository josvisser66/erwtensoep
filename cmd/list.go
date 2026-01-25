package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	rootCmd.AddCommand(lsCmd)
}

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "Lists archive entries",
	RunE: func(cmd *cobra.Command, args []string) error {
		return list(args, func(path string, versions map[string]interface{}) error {
			e := latestVersion(versions)
			stats := e.GetFileStats()
			fmt.Printf("%s %10s %s\n", stats.MTime.Format("2006-01-02 15:04"), stats.Size, path)
			return nil
		})
	},
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

func list(args []string, cb func(string, map[string]interface{}) error) error {
	sb, err := fs.GetSuperblock(&options)
	if err != nil {
		return err
	}

	for path, versions := range sb.Entries {
		if matchesAny(path, args) {
			if err := cb(path, versions); err != nil {
				return err
			}
		}
	}

	return nil
}

func latestVersion(versions map[string]interface{}) fs.Entry {
	var (
		maxEntry fs.Entry
		maxTime  time.Time
	)

	for _, v := range versions {
		e := v.(fs.Entry)
		if t := e.GetFileStats().MTime; t.After(maxTime) {
			maxTime = t
			maxEntry = e
		}
	}

	return maxEntry
}
