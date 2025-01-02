package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"time"
)

func init() {
	listCmd.AddCommand(listEntriesCmd)
	addLongFlag(listEntriesCmd)
}

func findLatestVersion(versions map[string]interface{}) fs.Entry {
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

var listEntriesCmd = &cobra.Command{
	Use:   "entries",
	Short: "Lists entries (files or symlinks) in the archive",
	RunE: func(cmd *cobra.Command, args []string) error {
		return list(args, func(path string, versions map[string]interface{}) error {
			if long {
				e := findLatestVersion(versions)
				fmt.Printf("%3d %s %s\n", len(versions), e.GetFileStats().String(), path)
			} else {
				fmt.Println(path)
			}

			return nil
		})
	},
}
