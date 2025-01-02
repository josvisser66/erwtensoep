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
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Hello World!")
	},
}
