package main

import (
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"math"
)

var options fs.Options

func addArchiveDirFlag(cmd *cobra.Command) string {
	flagName := "archive-dir"
	cmd.PersistentFlags().StringVarP(&options.ArchiveDir, flagName, "a", "", "Location of the archive directory")
	return flagName
}

func addMinSize(cmd *cobra.Command) {
	cmd.PersistentFlags().Int64Var(&options.MinSize, "min-size", 0, "Minimum size of file to handle")
}

func addMaxSize(cmd *cobra.Command) {
	cmd.PersistentFlags().Int64Var(&options.MaxSize, "max-size", math.MaxInt64, "Maximum size of file to handle")
}

func addDryRun(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolVar(&options.DryRun, "dry-run", false, "Do not write to the archive")
}

func addSuperblockWriteThreshold(cmd *cobra.Command) {
	cmd.PersistentFlags().Int64Var(&options.SuperblockWriteThreshold, "sb-write-threshold", humanize.GByte, "Write the Superblock after this many bytes have been written to the archive")
}
