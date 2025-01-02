package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"regexp"
	"time"
)

func init() {
	listCmd.AddCommand(listEntriesCmd)
	addLongFlag(listEntriesCmd)
}

func matchesOne(path string, regexps []*regexp.Regexp) bool {
	if len(regexps) == 0 {
		return true
	}

	for _, re := range regexps {
		if re.Match([]byte(path)) {
			return true
		}
	}

	return false
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
		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}

		var regexps []*regexp.Regexp

		for _, arg := range args {
			re, err := regexp.Compile(arg)
			if err != nil {
				return err
			}
			regexps = append(regexps, re)
		}

		for path, versions := range sb.Entries {
			if matchesOne(path, regexps) {
				if long {
					e := findLatestVersion(versions)
					fmt.Printf("%3d %s %s\n", len(versions), e.GetFileStats().String(), path)
				} else {
					fmt.Println(path)
				}
			}
		}

		return nil
	},
}
