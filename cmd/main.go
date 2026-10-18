package main

import (
	"errors"
	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/storage"
	"os"
	"strings"
)

var rootCmd = &cobra.Command{
	Use:          "erwtensoep",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := applyConfig(cmd); err != nil {
			return err
		}
		if err := ensureArchiveDir(cmd); err != nil {
			return err
		}
		switch strings.ToLower(backend) {
		case "local":
			// Leave unset to allow default local backend creation with ArchiveDir.
			options.Storage = nil
		case "memory":
			options.Storage = storage.NewMemoryBackend()
		default:
			return errors.New("unknown storage backend: " + backend)
		}
		return nil
	},
}

func init() {
	// All commands share the archive directory flag.
	addArchiveDirFlag(rootCmd)
	addBackendFlag(rootCmd)
}

func main() {
	// Execute the CLI root command.
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
