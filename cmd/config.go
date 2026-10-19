package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"josvisser.nl/erwtensoep/internal/storage"
)

type config struct {
	ArchiveDir     *string `json:"archive-dir"`
	StorageBackend *string `json:"storage-backend"`
	EndpointURL    *string `json:"endpoint_url"`
	Region         *string `json:"region"`
	MinSize        *int64  `json:"min-size"`
	MaxSize        *int64  `json:"max-size"`
	DryRun         *bool   `json:"dry-run"`
	SBThreshold    *int64  `json:"sb-write-threshold"`
	Long           *bool   `json:"long"`
	OutputDir      *string `json:"output-dir"`
	Shallow        *bool   `json:"shallow"`
	Merge          *config `json:"merge"`
	Ls             *config `json:"ls"`
	Cli            *config `json:"cli"`
	Rm             *config `json:"rm"`
	Restore        *config `json:"restore"`
	Verify         *config `json:"verify"`
	Fsck           *config `json:"fsck"`
	Stats          *config `json:"stats"`
	Rebuild        *config `json:"rebuild"`
	Hello          *config `json:"hello"`
	Recipe         *config `json:"recipe"`
}

func loadConfig() (*config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".erwtensoep.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyConfig(cmd *cobra.Command) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg == nil {
		return nil
	}

	applyConfigSection(cmd, cfg)

	switch cmd.Name() {
	case "merge":
		applyConfigSection(cmd, cfg.Merge)
	case "ls":
		applyConfigSection(cmd, cfg.Ls)
	case "cli":
		applyConfigSection(cmd, cfg.Cli)
	case "rm":
		applyConfigSection(cmd, cfg.Rm)
	case "restore":
		applyConfigSection(cmd, cfg.Restore)
	case "verify":
		applyConfigSection(cmd, cfg.Verify)
	case "fsck":
		applyConfigSection(cmd, cfg.Fsck)
	case "stats":
		applyConfigSection(cmd, cfg.Stats)
	case "rebuild":
		applyConfigSection(cmd, cfg.Rebuild)
	case "hello":
		applyConfigSection(cmd, cfg.Hello)
	case "recipe":
		applyConfigSection(cmd, cfg.Recipe)
	}

	return nil
}

func applyConfigSection(cmd *cobra.Command, cfg *config) {
	if cfg == nil {
		return
	}

	setIfNotChanged(cmd, "archive-dir", cfg.ArchiveDir)
	setIfNotChanged(cmd, "storage-backend", cfg.StorageBackend)
	setIfNotChanged(cmd, "endpoint-url", cfg.EndpointURL)
	setIfNotChanged(cmd, "region", cfg.Region)
	setIfNotChanged(cmd, "min-size", cfg.MinSize)
	setIfNotChanged(cmd, "max-size", cfg.MaxSize)
	setIfNotChanged(cmd, "dry-run", cfg.DryRun)
	setIfNotChanged(cmd, "sb-write-threshold", cfg.SBThreshold)
	setIfNotChanged(cmd, "long", cfg.Long)
	setIfNotChanged(cmd, "output-dir", cfg.OutputDir)
	setIfNotChanged(cmd, "shallow", cfg.Shallow)
}

func setIfNotChanged[T any](cmd *cobra.Command, name string, value *T) {
	if value == nil {
		return
	}
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		flag = cmd.InheritedFlags().Lookup(name)
	}
	if flag == nil || flag.Changed {
		return
	}
	_ = flag.Value.Set(toString(value))
}

func toString[T any](value *T) string {
	switch v := any(*value).(type) {
	case string:
		return v
	case int64:
		return formatInt64(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func formatInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}

func requireArchive(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "hello", "recipe":
		return false
	default:
		return true
	}
}

func ensureArchiveDir(cmd *cobra.Command) error {
	if !requireArchive(cmd) {
		return nil
	}
	if options.ArchiveDir == "" {
		return errors.New("archive-dir is required")
	}
	if storage.IsS3Archive(options.ArchiveDir) {
		_, _, err := storage.ParseS3Archive(options.ArchiveDir)
		return err // S3 buckets must already exist; never create a local s3: tree.
	}
	if strings.Contains(options.ArchiveDir, "://") {
		return errors.New("unsupported archive URL scheme")
	}
	if strings.EqualFold(backend, "s3") {
		return errors.New("S3 storage requires archive-dir in the form s3://bucket/prefix")
	}

	if _, err := os.Stat(options.ArchiveDir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if cmd.Name() == "fsck" {
		return fmt.Errorf("archive directory %q does not exist", options.ArchiveDir)
	}

	fmt.Fprintf(os.Stderr, "Archive directory %q does not exist. Create it? [y/N]: ", options.ArchiveDir)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		return errors.New("archive directory does not exist")
	}
	return os.MkdirAll(options.ArchiveDir, 0700)
}
