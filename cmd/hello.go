package main

import (
	"fmt"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(helloCmd)
}

var helloCmd = &cobra.Command{
	Use:   "hello",
	Short: "Says hello",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Hello!")
		return nil
	},
}
