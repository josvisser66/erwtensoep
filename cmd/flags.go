package main

import (
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
	"math"
)

var (
	options       fs.Options
	long          bool
	backend       string
	outputDir     string
	shallowVerify bool
)

func addArchiveDirFlag(cmd *cobra.Command) string {
	flagName := "archive-dir"
	// ArchiveDir points to the root of the backup archive.
	cmd.PersistentFlags().StringVarP(&options.ArchiveDir, flagName, "a", "", "Location of the archive directory")
	return flagName
}

func addMinSizeFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().Int64Var(&options.MinSize, "min-size", 0, "Minimum size of file to handle")
}

func addMaxSizeFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().Int64Var(&options.MaxSize, "max-size", math.MaxInt64, "Maximum size of file to handle")
}

func addDryRunFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolVar(&options.DryRun, "dry-run", false, "Do not write to the archive")
}

func addSuperblockWriteThresholdFlag(cmd *cobra.Command) {
	// Threshold controls how frequently the superblock is flushed while merging.
	cmd.PersistentFlags().Int64Var(&options.SuperblockWriteThreshold, "sb-write-threshold", humanize.GByte, "Write the Superblock after this many bytes have been written to the archive")
}

func addLongFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolVarP(&long, "long", "l", false, "Lists long information, much like ls")
}

func addBackendFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&backend, "storage-backend", "local", "Storage backend to use (local, memory)")
}

func addOutputDirFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory for restore")
}

func addVerifyFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolVar(&shallowVerify, "shallow", false, "Shallow verification (skip blob decompression)")
}
