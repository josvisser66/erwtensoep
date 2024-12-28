package main

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use: "erwtensoep",
}

func init() {
	addArchiveDirFlag(rootCmd)
}

func main() {
	rootCmd.Execute()
}
