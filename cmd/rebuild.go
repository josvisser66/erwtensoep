package main

import (
	"errors"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	rootCmd.AddCommand(rebuildCmd)
}

var rebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Rebuilds superblock",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return errors.New("No arguments are expected")
		}

		// Rebuilds metadata from blobs and entries on disk.
		return fs.RebuildSuperblock(&options)
	},
}
