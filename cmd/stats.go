package main

import (
	"errors"
	"fmt"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() {
	rootCmd.AddCommand(statsCmd)
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Gives some stats about the archive",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return errors.New("no arguments are supported")
		}

		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}

		fmt.Printf("%8d entries\n", len(sb.Entries))

		var maxVersions, totalVersions int
		var total2 int64

		for _, entry := range sb.Entries {
			l := len(entry)
			totalVersions += l
			if l > maxVersions {
				maxVersions = l
			}

			for _, version := range entry {
				if v, ok := version.(*fs.RegularFile); ok {
					total2 += size(v.Size)
				}
			}
		}

		fmt.Printf("%8d versions\n", totalVersions)
		fmt.Printf("%8d blobs\n", len(sb.Blobs))
		fmt.Printf("%8d blocks\n", len(sb.Blocks))

		var total int64

		for _, blob := range sb.Blobs {
			total += size(blob.Size)
		}

		fmt.Printf("%s bytes in blobs (%s bytes)\n", humanize.Bytes(uint64(total)), humanize.Comma(total))
		total = 0

		for _, block := range sb.Blobs {
			total += size(block.Size)
		}

		fmt.Printf("%s bytes in blocks before compression (%s bytes)\n", humanize.Bytes(uint64(total)), humanize.Comma(total))
		fmt.Printf("%s bytes in versions (%s bytes)\n", humanize.Bytes(uint64(total2)), humanize.Comma(total2))

		return nil
	},
}
