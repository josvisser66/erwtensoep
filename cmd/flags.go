package main

import (
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"math"
)

var (
	archiveDir string
	maxSize    int64
	minSize    int64
	dryRun     bool
)

func addArchiveDirFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&archiveDir, "archive", "a", "", "Location of the archive directory")
}

func addMinSize(cmd *cobra.Command) {
	cmd.PersistentFlags().Int64Var(&minSize, "min-size", 0, "Minimum size of file to handle")
}

func addMaxSize(cmd *cobra.Command) {
	cmd.PersistentFlags().Int64Var(&maxSize, "max-size", math.MaxInt64, "Maximum size of file to handle")
}

func addDryRun(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Do not write to the archive")
}

func makeOptions() *fs.Options {
	return &fs.Options{
		MinSize: minSize,
		MaxSize: maxSize,
	}
}
