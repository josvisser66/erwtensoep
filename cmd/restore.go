package main

import (
	"errors"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	rootCmd.AddCommand(restoreCmd)
	addOutputDirFlag(restoreCmd)
}

var restoreCmd = &cobra.Command{
	Use:   "restore [patterns...]",
	Short: "Restore files and directories from the archive",
	RunE: func(cmd *cobra.Command, args []string) error {
		if outputDir == "" {
			return errors.New("output directory not specified")
		}
		if len(args) == 0 {
			return errors.New("no restore patterns specified")
		}

		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}

		_, err = sb.Restore(args, outputDir)
		return err
	},
}
