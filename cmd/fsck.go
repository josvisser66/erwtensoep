package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	var yes, no, verbose bool
	command := &cobra.Command{
		Use:   "fsck",
		Short: "Check archive objects and offer to repair discrepancies",
		Long: "Read the superblock and fully validate stored entries, blobs, and blocks.\n" +
			"List discrepancies and proposed repairs before asking for confirmation.\n" +
			"Recover orphan blobs under lost+found and remove unusable objects and empty\n" +
			"storage directories. Keep the archive idle throughout checking and repair.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFSCK(&options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), yes, no, verbose)
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Apply the listed repairs without prompting")
	command.Flags().BoolVarP(&no, "no", "n", false, "Check only; never modify the archive or prompt")
	command.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show progress while checking the archive")
	command.MarkFlagsMutuallyExclusive("yes", "no")
	rootCmd.AddCommand(command)
}

var errFSCKUnrepaired = errors.New("archive has unrepaired discrepancies")

func runFSCK(options *fs.Options, in io.Reader, out, progressOut io.Writer, yes, no, verbose bool) error {
	lastPhase := ""
	lastCount := 0
	lastTotal := 0
	lastUpdate := time.Time{}
	var progress func(string, int, int)
	if verbose {
		progress = func(phase string, count, total int) {
			if phase != lastPhase || time.Since(lastUpdate) >= 2*time.Second {
				if phase != lastPhase && lastPhase != "" {
					fmt.Fprintf(progressOut, "\rChecked %s: %s\n", lastPhase, fsckProgressCount(lastCount, lastTotal))
				}
				fmt.Fprintf(progressOut, "\rChecking %s: %s", phase, fsckProgressCount(count, total))
				lastUpdate = time.Now()
			}
			lastPhase, lastCount, lastTotal = phase, count, total
		}
	}
	report, err := fs.CheckArchive(options, progress)
	if lastPhase != "" {
		fmt.Fprintf(progressOut, "\rChecked %s: %s\n", lastPhase, fsckProgressCount(lastCount, lastTotal))
	}
	if err != nil {
		return err
	}
	if len(report.Issues) == 0 {
		fmt.Fprintf(out, "Archive is consistent: %d entries, %d blobs, %d blocks.\n", report.Entries, report.Blobs, report.Blocks)
		return nil
	}
	fmt.Fprintf(out, "%d discrepancies:\n", len(report.Issues))
	for _, issue := range report.Issues {
		fmt.Fprintf(out, "  [%s] %q: %s\n    Repair: %s\n", issue.Kind, issue.Key, issue.Detail, issue.Action)
	}
	fmt.Fprintf(out, "\nProposed index: %d entries, %d blobs, %d blocks.\n", report.Entries, report.Blobs, report.Blocks)
	fmt.Fprintf(out, "Recover %d blobs in lost+found; recreate %d missing entry records; prune empty storage directories.\n", len(report.Recovered), report.RecreatedEntries)
	if no {
		fmt.Fprintln(out, "Check only; no changes made.")
		return errFSCKUnrepaired
	}
	if !yes {
		fmt.Fprint(out, "Apply these repairs? Corrupt objects and unusable references will be removed. [y/N]: ")
		answer, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(out, "No changes made.")
			return errFSCKUnrepaired
		}
	}
	if err := report.Repair(); err != nil {
		return fmt.Errorf("repair incomplete: %w", err)
	}
	fmt.Fprintln(out, "Repair complete. The superblock has been rewritten.")
	return nil
}

func fsckProgressCount(count, total int) string {
	if total < 0 {
		return fmt.Sprintf("%d objects (counting...)", count)
	}
	percent := 100.0
	if total > 0 {
		percent = 100 * float64(count) / float64(total)
	}
	return fmt.Sprintf("%d/%d objects (%.1f%%)", count, total, percent)
}
