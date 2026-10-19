package main

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	rootCmd.AddCommand(mergeCmd)
	addMinSizeFlag(mergeCmd)
	addMaxSizeFlag(mergeCmd)
	addDryRunFlag(mergeCmd)
	addSuperblockWriteThresholdFlag(mergeCmd)
}

var mergeCmd = &cobra.Command{
	Use:   "merge SOURCE...",
	Short: "Merge directories or the contents of tar archives",
	Long:  "Merge one or more source directories, .tar, .tar.gz, or .tgz files.\nExplicit tar sources are expanded into archive entries, with a WARNING;\nthe tar file itself is not stored. Tar files within directories remain files.",
	RunE: func(cmd *cobra.Command, args []string) (resultErr error) {
		if len(args) == 0 {
			return errors.New("no sources specified")
		}

		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}

		var totalChanges, totalErrors int
		defer func() {
			// Keep completed imports even if a later source fails validation.
			if totalChanges > 0 {
				resultErr = errors.Join(resultErr, sb.Write())
			}
		}()

		for _, arg := range args {
			fmt.Println(arg)
			c, e, err := sb.Merge(arg)
			totalChanges += c
			totalErrors += e
			fmt.Printf("Result for %s: %d changes, %d errors\n", arg, c, e)
			if err != nil {
				return err
			}
		}

		if len(args) > 1 {
			fmt.Printf("Overall result: %d changes, %d errors\n", totalChanges, totalErrors)
		}

		return nil
	},
}
