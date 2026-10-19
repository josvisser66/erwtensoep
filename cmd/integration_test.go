package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	before, err := os.ReadFile(sbPath)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	cli := exec.Command(bin, "--archive-dir", archiveDir, "cli")
	cli.Stdin = strings.NewReader("ls\nversions alpha.txt\ncat -n alpha.txt\nmore beta.txt\ndu -sb /\ncd missing\npwd\nexit\n")
	cli.Stdout, cli.Stderr = &stdout, &stderr
	if err := cli.Run(); err != nil {
		t.Fatalf("cli failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	for _, expected := range []string{"erwtensoep:/ ", "alpha.txt\nbeta.txt\n", "MODIFIED", "/alpha.txt\n", "     1\talpha", "beta", "9\t/\n"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("cli output missing %q: %s", expected, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "cd: /missing: no such file or directory") {
		t.Errorf("cli error missing: %s", stderr.String())
	}
	after, err := os.ReadFile(sbPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("cli modified archive metadata: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	stats := exec.Command(bin, "--archive-dir", archiveDir, "stats")
	stats.Stdout, stats.Stderr = &stdout, &stderr
	if err := stats.Run(); err != nil {
		t.Fatalf("stats failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var storedBytes int64
	if err := filepath.WalkDir(filepath.Join(archiveDir, "blocks"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err == nil {
			storedBytes += info.Size()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for label, want := range map[string]string{
		"Logical file data:":                "9 B (9 bytes)",
		"After whole-file sharing:":         "9 B (9 bytes)",
		"After block sharing:":              "9 B (9 bytes)",
		"Stored blocks (gzip):":             statsBytes(storedBytes),
		"Blocks shared between file paths:": "0 of 2",
		"Compression:":                      statsSavings(9, storedBytes, "unique block data"),
	} {
		assertStatsLine(t, stdout.String(), label, want)
	}
	after, err = os.ReadFile(sbPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("stats modified archive metadata: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	remove := exec.Command(bin, "--archive-dir", archiveDir, "cli")
	remove.Stdin = strings.NewReader("rm -iv alpha.txt\nn\ncat alpha.txt\nrm -v alpha.txt\nls\nexit\n")
	remove.Stdout, remove.Stderr = &stdout, &stderr
	if err := remove.Run(); err != nil || stderr.Len() != 0 {
		t.Fatalf("cli rm failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[y/N]: ") || !strings.Contains(stdout.String(), "erwtensoep:/ alpha") ||
		!strings.Contains(stdout.String(), "removed \"/alpha.txt\" (all saved versions)") {
		t.Fatalf("unexpected removal output: %s", stdout.String())
	}
	if data, err := os.ReadFile(filepath.Join(srcDir, "alpha.txt")); err != nil || string(data) != "alpha" {
		t.Fatalf("cli rm modified the source file: %v", err)
	}

	// Rebuilding from stored objects must not resurrect removed file versions.
	rebuild = exec.Command(bin, "--archive-dir", archiveDir, "rebuild")
	if out, err := rebuild.CombinedOutput(); err != nil {
		t.Fatalf("rebuild after rm failed: %v\n%s", err, out)
	}
	stdout.Reset()
	stderr.Reset()
	reopen := exec.Command(bin, "--archive-dir", archiveDir, "cli")
	reopen.Stdin = strings.NewReader("ls\ncat beta.txt\ncat alpha.txt\nexit\n")
	reopen.Stdout, reopen.Stderr = &stdout, &stderr
	if err := reopen.Run(); err != nil {
		t.Fatalf("cli reopen after rm failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "erwtensoep:/ beta.txt\n") || !strings.Contains(stdout.String(), "erwtensoep:/ beta") ||
		!strings.Contains(stderr.String(), "alpha.txt: no such file or directory") {
		t.Fatalf("removal did not persist: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	check := exec.Command(bin, "--archive-dir", archiveDir, "fsck", "-n")
	if out, err := check.CombinedOutput(); err != nil || !strings.Contains(string(out), "Archive is consistent") || strings.Contains(string(out), "Checking ") || strings.Contains(string(out), "Checked ") {
		t.Fatalf("fsck healthy archive: %v\n%s", err, out)
	}
	for _, flag := range []string{"-v", "--verbose"} {
		check = exec.Command(bin, "--archive-dir", archiveDir, "fsck", "-n", flag)
		stdout.Reset()
		stderr.Reset()
		check.Stdout, check.Stderr = &stdout, &stderr
		if err := check.Run(); err != nil || !strings.Contains(stdout.String(), "Archive is consistent") || !strings.Contains(stderr.String(), "Checked blocks:") || !strings.Contains(stderr.String(), "(100.0%)") {
			t.Fatalf("fsck %s progress: %v\nstdout=%s stderr=%s", flag, err, stdout.String(), stderr.String())
		}
	}
	emptyDir := filepath.Join(archiveDir, "blocks", "old", "empty")
	if err := os.MkdirAll(emptyDir, 0700); err != nil {
		t.Fatal(err)
	}
	check = exec.Command(bin, "--archive-dir", archiveDir, "fsck", "-n")
	if out, err := check.CombinedOutput(); err == nil || !strings.Contains(string(out), "[empty directory]") {
		t.Fatalf("fsck should report unresolved discrepancies with a nonzero exit: %v\n%s", err, out)
	}
	if _, err := os.Stat(emptyDir); err != nil {
		t.Fatalf("check-only fsck changed directories: %v", err)
	}
	check = exec.Command(bin, "--archive-dir", archiveDir, "fsck", "-y")
	if out, err := check.CombinedOutput(); err != nil || !strings.Contains(string(out), "Repair complete") {
		t.Fatalf("fsck repair: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Dir(emptyDir)); !os.IsNotExist(err) {
		t.Fatalf("fsck did not prune empty parents: %v", err)
	}
	check = exec.Command(bin, "--archive-dir", archiveDir, "fsck", "-n")
	if out, err := check.CombinedOutput(); err != nil || !strings.Contains(string(out), "Archive is consistent") {
		t.Fatalf("fsck repaired archive: %v\n%s", err, out)
	}
	check = exec.Command(bin, "--archive-dir", archiveDir, "fsck", "-ny")
	if out, err := check.CombinedOutput(); err == nil || !strings.Contains(string(out), "none of the others") {
		t.Fatalf("fsck accepted conflicting flags: %v\n%s", err, out)
	}
	missingArchive := filepath.Join(t.TempDir(), "missing")
	check = exec.Command(bin, "--archive-dir", missingArchive, "fsck", "-n")
	check.Stdin = strings.NewReader("yes\n")
	if out, err := check.CombinedOutput(); err == nil || !strings.Contains(string(out), "does not exist") {
		t.Fatalf("fsck accepted nonexistent archive: %v\n%s", err, out)
	}
	if _, err := os.Stat(missingArchive); !os.IsNotExist(err) {
		t.Fatal("check-only fsck created an archive directory")
	}
}
