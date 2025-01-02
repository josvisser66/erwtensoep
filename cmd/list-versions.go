package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	listCmd.AddCommand(listVersionsCmd)
}

var listVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Lists versions (files or symlinks) of entries in the archive",
	RunE: func(cmd *cobra.Command, args []string) error {
		return list(args, func(path string, versions map[string]interface{}) error {
			fmt.Println(path)

			for _, e := range versions {
				fmt.Println(e.(fs.Entry).GetFileStats().String())
			}

			return nil
		})
	},
}
