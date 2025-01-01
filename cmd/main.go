package main

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "erwtensoep",
	SilenceUsage: true,
}

func init() {
	flagName := addArchiveDirFlag(rootCmd)
	rootCmd.MarkFlagRequired(flagName)
}

func main() {
	rootCmd.Execute()
}
