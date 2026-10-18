package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	rootCmd.AddCommand(statsCmd)
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show archive storage, compression, and block sharing statistics",
	Long: "Show storage savings from sharing file contents and blocks, then compressing\n" +
		"the unique blocks. Includes all saved regular-file versions; stored data sizes\n" +
		"exclude metadata, backups, unreferenced objects, and filesystem allocation overhead.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}
		stats, err := sb.Stats()
		if err != nil {
			return err
		}
		return writeStats(cmd.OutOrStdout(), stats)
	},
}

func writeStats(out io.Writer, s *fs.ArchiveStats) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "Archive contents (all saved versions)")
	fmt.Fprintf(w, "  Paths:\t%d\n", s.Paths)
	fmt.Fprintf(w, "  Versions:\t%d (%d regular files, %d symlinks)\n", s.Versions, s.RegularVersions, s.SymlinkVersions)
	fmt.Fprintf(w, "  Most versions of one path:\t%d\n", s.MaxVersions)
	fmt.Fprintf(w, "  Unique file contents (blobs):\t%d\n", s.Blobs)
	fmt.Fprintf(w, "  Unique data blocks:\t%d\n", s.Blocks)
	if s.UnreferencedBlobs != 0 || s.UnreferencedBlocks != 0 {
		fmt.Fprintf(w, "  Unreferenced indexed objects:\t%d blobs, %d blocks (excluded below)\n", s.UnreferencedBlobs, s.UnreferencedBlocks)
	}

	fmt.Fprintln(w, "\nStorage breakdown")
	fmt.Fprintf(w, "  Logical file data:\t%s\n", statsBytes(s.LogicalBytes))
	fmt.Fprintf(w, "  After whole-file sharing:\t%s\n", statsBytes(s.BlobBytes))
	fmt.Fprintf(w, "  After block sharing:\t%s\n", statsBytes(s.BlockBytes))
	fmt.Fprintf(w, "  Stored blocks (gzip):\t%s\n", statsBytes(s.StoredBytes))

	fmt.Fprintln(w, "\nSavings")
	fmt.Fprintf(w, "  Whole-file sharing:\t%s\n", statsSavings(s.LogicalBytes, s.BlobBytes, "logical data"))
	fmt.Fprintf(w, "  Additional block sharing:\t%s\n", statsSavings(s.BlobBytes, s.BlockBytes, "unique file contents"))
	fmt.Fprintf(w, "  Total sharing (subtotal):\t%s\n", statsSavings(s.LogicalBytes, s.BlockBytes, "logical data"))
	fmt.Fprintf(w, "  Compression:\t%s\n", statsSavings(s.BlockBytes, s.StoredBytes, "unique block data"))
	fmt.Fprintf(w, "  Overall (sharing + compression):\t%s\n", statsSavings(s.LogicalBytes, s.StoredBytes, "logical data"))
	if s.StoredBytes > 0 {
		fmt.Fprintf(w, "  Logical/stored size ratio:\t%.2f:1\n", float64(s.LogicalBytes)/float64(s.StoredBytes))
	} else {
		fmt.Fprintln(w, "  Logical/stored size ratio:\tn/a (no stored block data)")
	}

	fmt.Fprintln(w, "\nBlock reuse")
	fmt.Fprintf(w, "  References across file versions:\t%d\n", s.BlockReferences)
	fmt.Fprintf(w, "  Blocks referenced more than once:\t%d of %d\n", s.ReusedBlocks, s.Blocks)
	fmt.Fprintf(w, "  Blocks shared between file paths:\t%d of %d\n", s.CrossFileBlocks, s.Blocks)
	fmt.Fprintf(w, "  Most references to one block:\t%d\n", s.MaxBlockReferences)
	fmt.Fprintln(w, "\nSharing includes identical files, saved versions, and repeated blocks within a file.")
	fmt.Fprintln(w, "Cross-file sharing counts distinct paths, including their older versions.")
	fmt.Fprintln(w, "Stored sizes include gzip overhead; metadata, backups, unreferenced objects,")
	fmt.Fprintln(w, "and filesystem allocation overhead are excluded.")
	return w.Flush()
}

func statsBytes(n int64) string {
	return fmt.Sprintf("%s (%s bytes)", humanize.Bytes(uint64(n)), humanize.Comma(n))
}

func statsSavings(before, after int64, baseline string) string {
	difference, effect := before-after, "saved"
	if difference < 0 {
		difference, effect = -difference, "increase"
	}
	percent := 0.0
	if before > 0 {
		percent = 100 * float64(difference) / float64(before)
	}
	return fmt.Sprintf("%s %s (%.1f%% of %s)", statsBytes(difference), effect, percent, baseline)
}
