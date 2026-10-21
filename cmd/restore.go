package main

import (
	"bufio"
	"errors"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	rootCmd.AddCommand(restoreCmd)
	addOutputDirFlag(restoreCmd)
	restoreCmd.Flags().Bool("latest", false, "Restore the newest saved version of each file without prompting")
}

var restoreCmd = &cobra.Command{
	Use:   "restore [patterns...]",
	Short: "Restore files and directories from the archive",
	Long:  "Restore matching files and symlinks. When multiple versions are saved,\nchoose one from a numbered list. Enter skips a file; q cancels before writing.\nUse --latest to restore the newest versions without prompting.\n\nIf a file and directory share a name, a selects the file and a/ selects\nonly the directory contents. A plain name selects descendants only when no\nfile has that name. Conflicting selections fail before any output is written;\nnarrow broad patterns or restore files and directories into separate destinations.",
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

		targets, err := sb.RestoreTargets(args)
		if err != nil {
			return err
		}
		latest, err := cmd.Flags().GetBool("latest")
		if err != nil {
			return err
		}
		v := newVersionSelector(sb, bufio.NewReader(cmd.InOrStdin()), cmd.OutOrStdout(), latest)
		selected := make(map[string]string)
		for _, name := range targets {
			choice, err := v.choose(name)
			if err != nil {
				return err
			}
			if choice != nil {
				selected[name] = choice.id
			}
		}
		_, err = sb.RestoreVersions(selected, outputDir)
		return err
	},
}
