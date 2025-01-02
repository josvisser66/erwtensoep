package main

import (
	"github.com/spf13/cobra"
)

func init() {
	listCmd.AddCommand(listVersionsCmd)
}

var listVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Lists the versions of entries in the archive",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}
