package main

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	rootCmd.AddCommand(mergeCmd)
	addMinSize(mergeCmd)
	addMaxSize(mergeCmd)
	addDryRun(mergeCmd)
	addSuperblockWriteThreshold(mergeCmd)
}

var mergeCmd = &cobra.Command{
	Use:   "merge",
	Short: "Merge a source directory into the archive",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return errors.New("no source directories specified")
		}

		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}

		var totalChanges, totalErrors int

		for _, arg := range args {
			fmt.Println(arg)
			c, e, err := sb.Merge(arg)
			if err != nil {
				return err
			}
			fmt.Printf("Result for %s: %d changes, %d errors\n", arg, c, e)
			totalChanges += c
			totalErrors += e
		}

		if len(args) > 1 {
			fmt.Printf("Overall result: %d changes, %d errors\n", totalChanges, totalErrors)
		}

		if totalChanges > 0 {
			if err := sb.Write(); err != nil {
				// The on disk block cache is tainted but the on-disk superblock
				// file does not reflect this. Time for garbage collection :-)
				return err
			}
		}

		return nil
	},
}
