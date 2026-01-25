package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	rootCmd.AddCommand(removeCmd)
}

var removeCmd = &cobra.Command{
	Use:   "rm [patterns...]",
	Short: "Remove files from the archive",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return errors.New("no remove patterns specified")
		}

		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}

		targets, err := sb.RemoveTargets(args)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Println("No matching entries to remove.")
			return nil
		}

		fmt.Println("The following entries will be removed:")
		for _, t := range targets {
			fmt.Println(" -", t)
		}

		fmt.Print("Proceed with removal? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			return errors.New("remove canceled")
		}

		_, err = sb.Remove(args)
		return err
	},
}
