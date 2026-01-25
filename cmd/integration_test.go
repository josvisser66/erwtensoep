package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIntegrationBackupLocalArchive(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Dir(wd)

	bin := filepath.Join(t.TempDir(), "erwtensoep")
	build := exec.Command("go", "build", "-o", bin, "./cmd")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(out))
	}

	srcDir := t.TempDir()
	archiveDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "alpha.txt"), []byte("alpha"), 0600); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "beta.txt"), []byte("beta"), 0600); err != nil {
		t.Fatalf("write beta: %v", err)
	}

	cmd := exec.Command(bin, "--archive-dir", archiveDir, "merge", srcDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("merge failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	sbPath := filepath.Join(archiveDir, "superblock.json.gz")
	if _, err := os.Stat(sbPath); err != nil {
		t.Fatalf("superblock not written: %v", err)
	}

	var foundData bool
	err = filepath.WalkDir(archiveDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == "superblock.json.gz" || filepath.Base(path) == "superblock.json.gz.bak" {
			return nil
		}
		foundData = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk archive: %v", err)
	}
	if !foundData {
		t.Fatalf("expected archive data files to be written")
	}

	rebuild := exec.Command(bin, "--archive-dir", archiveDir, "rebuild")
	rebuild.Stdout = &stdout
	rebuild.Stderr = &stderr
	if err := rebuild.Run(); err != nil {
		t.Fatalf("rebuild failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	restoreDir := t.TempDir()
	restore := exec.Command(bin, "--archive-dir", archiveDir, "restore", "-o", restoreDir, "alpha.txt", "beta.txt")
	restore.Stdout = &stdout
	restore.Stderr = &stderr
	if err := restore.Run(); err != nil {
		t.Fatalf("restore failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if data, err := os.ReadFile(filepath.Join(restoreDir, "alpha.txt")); err != nil || string(data) != "alpha" {
		t.Fatalf("restore alpha mismatch: %v (%q)", err, string(data))
	}
	if data, err := os.ReadFile(filepath.Join(restoreDir, "beta.txt")); err != nil || string(data) != "beta" {
		t.Fatalf("restore beta mismatch: %v (%q)", err, string(data))
	}
}
