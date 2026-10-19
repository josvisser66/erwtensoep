package main

import (
	"errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		if !requireArchive(cmd) {
			return nil
		}
		if err := ensureArchiveDir(cmd); err != nil {
			return err
		}
		return configureBackend()
	},
}

func init() {
	rootCmd.SetGlobalNormalizationFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "endpoint_url" {
			name = "endpoint-url"
		}
		return pflag.NormalizedName(name)
	})
	// All commands share the archive directory flag.
	addArchiveDirFlag(rootCmd)
	addBackendFlag(rootCmd)
}

func configureBackend() error {
	options.Storage = nil
	switch strings.ToLower(backend) {
	case "auto", "local", "s3":
		if storage.IsS3Archive(options.ArchiveDir) {
			// The URL selects the provider backend even with an older config
			// whose storage-backend value is still the former default, local.
			var err error
			options.Storage, err = storage.NewS3Backend(options.ArchiveDir, options.EndpointURL, options.Region)
			return err
		}
		if strings.EqualFold(backend, "s3") {
			return errors.New("S3 storage requires archive-dir in the form s3://bucket/prefix")
		}
	case "memory":
		if storage.IsS3Archive(options.ArchiveDir) {
			return errors.New("memory storage cannot be used with an S3 archive URL")
		}
		options.Storage = storage.NewMemoryBackend()
	default:
		return errors.New("unknown storage backend: " + backend)
	}
	return nil
}

func main() {
	// Execute the CLI root command.
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
