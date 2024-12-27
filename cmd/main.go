package main

import (
	"flag"
	"fmt"
	"josvisser.nl/erwtensoep/internal/fs"
	"os"
)

func main() {
	backupDir := flag.String("backup-dir", "", "backup directory")
	srcDir := flag.String("src-dir", "", "source directory")
	flag.Parse()

	if *backupDir == "" || *srcDir == "" {
		fmt.Println("Please provide --backup-dir and --src-dir")
		os.Exit(1)
	}

	sb, err := fs.ReadSuperblock(*backupDir)
	if err != nil {
		panic(err)
	}

	changes, errors, err := sb.Walk(*srcDir)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Result: %d changes, %d errors\n", changes, errors)
	if changes > 0 {
		if err := sb.Write(); err != nil {
			fmt.Printf("Writing superblock failed: %v\n", err)
			// The on disk block cache is tainted but the on-disk superblock
			// file does not reflect this. Time for garbage collection :-)
		}
	}
}
