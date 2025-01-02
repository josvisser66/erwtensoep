package main

import (
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"regexp"
)

func init() {
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Lists various things",
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

func list(args []string, cb func(string, map[string]interface{}) error) error {
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
			if err := cb(path, versions); err != nil {
				return err
			}
		}
	}

	return nil
}
