package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	removeCmd.Flags().Bool("all-versions", false, "Select every saved version without asking which versions to remove")
	rootCmd.AddCommand(removeCmd)
}

var removeCmd = &cobra.Command{
	Use:   "rm [patterns...]",
	Short: "Remove files from the archive",
	Long:  "Remove archived versions matching the given paths or patterns.\nChoose from a numbered list when a file has multiple saved versions.\nUse --all-versions to select them all. Confirm the selection before removal.\n\nIf a file and directory share a name, a selects only the file and a/ selects\nonly directory contents. A bare name selects descendants when no file or symlink\nhas that name. Specify both a and a/ to remove both; no -r flag is needed.",
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
		out := cmd.OutOrStdout()
		if len(targets) == 0 {
			fmt.Fprintln(out, "No matching entries to remove.")
			return nil
		}

		allVersions, err := cmd.Flags().GetBool("all-versions")
		if err != nil {
			return err
		}
		reader := bufio.NewReader(cmd.InOrStdin())
		choices, err := selectRemovalVersions(sb, targets, reader, out, allVersions, false)
		if err != nil {
			return err
		}
		if len(choices) == 0 {
			fmt.Fprintln(out, "No versions selected for removal.")
			return nil
		}
		if _, err := fmt.Fprintln(out, "The following versions will be removed:"); err != nil {
			return err
		}
		for _, name := range targets {
			if ids := choices[name]; len(ids) > 0 {
				if _, err := fmt.Fprintf(out, " - %s (%d of %d saved versions)\n", name, len(ids), len(sb.Entries[name])); err != nil {
					return err
				}
			}
		}

		if _, err := fmt.Fprint(out, "Proceed with removal? [y/N]: "); err != nil {
			return err
		}
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			return errors.New("remove canceled")
		}

		_, err = sb.RemoveVersions(choices)
		return err
	},
}
