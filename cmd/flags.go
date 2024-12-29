package main

import (
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"math"
)

var (
	archiveDir string
	sizeLimit  int64
)

func addArchiveDirFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&archiveDir, "archive", "a", "", "Location of the archive directory")
}

func addSizeLimit(cmd *cobra.Command) {
	cmd.PersistentFlags().Int64Var(&sizeLimit, "size-limit", math.MaxInt64, "Maximum size of file to handle")
}

func makeOptions() *fs.Options {
	return &fs.Options{
		SizeLimit: sizeLimit,
	}
}
