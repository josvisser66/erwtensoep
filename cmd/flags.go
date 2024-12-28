package main

import "github.com/spf13/cobra"

var archiveDir string

func addArchiveDirFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&archiveDir, "archive", "a", "", "Location of the archive directory")
}
