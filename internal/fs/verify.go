package fs

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Verify checks that referenced blobs exist and that stored blobs are referenced.
// It runs in two phases: scan stored blobs (optional decompression) and then
// compare the stored sets with the superblock references.
func (sb *Superblock) Verify(shallow bool) error {
	type verifyIssue struct {
		key     string
		prefix  string
		id      string
		detail  string
		matches []string
	}

	var (
		missing []verifyIssue
		orphans []string
		corrupt []verifyIssue
	)

	// Build maps from IDs to entry paths for tracing.
	blockRefs := make(map[string][]string)
	blobRefs := make(map[string][]string)
	for path, versions := range sb.Entries {
		for _, v := range versions {
			if rf, ok := v.(*RegularFile); ok {
				blobRefs[rf.BlobID] = append(blobRefs[rf.BlobID], path)
			}
		}
	}
	for blobID, blob := range sb.Blobs {
		for _, blockID := range blob.Blocks {
			blockRefs[blockID] = append(blockRefs[blockID], blobRefs[blobID]...)
		}
	}

	addIssue := func(dst *[]verifyIssue, prefix, id string, detail string) {
		key := blobKey(prefix, id)
		var matches []string
		switch prefix {
		case "blocks":
			matches = append(matches, blockRefs[id]...)
		case "blobs":
			matches = append(matches, blobRefs[id]...)
		case "entries":
			matches = append(matches, id)
		}
		*dst = append(*dst, verifyIssue{
			key:     key,
			prefix:  prefix,
			id:      id,
			detail:  detail,
			matches: matches,
		})
	}

	// verifyStored scans stored blobs under a prefix and records their IDs.
	// When shallow is false, it also fully decompresses each blob to detect corruption.
	verifyStored := func(prefix string, stored map[string]struct{}) error {
		var scanned uint64
		done := make(chan struct{})
		fmt.Fprintf(os.Stderr, "\rVerify scan (%s): 0", prefix)
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					fmt.Fprintf(os.Stderr, "\rVerify scan (%s): %d", prefix, atomic.LoadUint64(&scanned))
				case <-done:
					return
				}
			}
		}()

		err := sb.storage.ListBlobs(prefix+"/", func(key string) error {
			id := key[len(prefix)+1:]
			stored[id] = struct{}{}

			if !shallow {
				// Read and decompress to verify blob integrity.
				rdr, err := sb.storage.GetBlob(key)
				if err != nil {
					addIssue(&corrupt, prefix, id, err.Error())
					atomic.AddUint64(&scanned, 1)
					return nil
				}
				gzr, err := gzip.NewReader(rdr)
				if err != nil {
					_ = rdr.Close()
					addIssue(&corrupt, prefix, id, err.Error())
					atomic.AddUint64(&scanned, 1)
					return nil
				}
				if _, err := io.Copy(io.Discard, gzr); err != nil {
					addIssue(&corrupt, prefix, id, err.Error())
				}
				_ = gzr.Close()
				_ = rdr.Close()
			}
			atomic.AddUint64(&scanned, 1)
			return nil
		})

		close(done)
		fmt.Fprintf(os.Stderr, "\rVerify scan (%s): %d\n", prefix, atomic.LoadUint64(&scanned))
		return err
	}

	storedBlocks := make(map[string]struct{})
	if err := verifyStored("blocks", storedBlocks); err != nil {
		return err
	}
	storedBlobs := make(map[string]struct{})
	if err := verifyStored("blobs", storedBlobs); err != nil {
		return err
	}
	storedEntries := make(map[string]struct{})
	if err := verifyStored("entries", storedEntries); err != nil {
		return err
	}

	var total uint64
	var checked uint64
	progressDone := make(chan struct{})
	// Progress for reference checks against the superblock.
	printProgress := func(d, total uint64) {
		if total == 0 {
			fmt.Fprint(os.Stderr, "\rVerify references: 100.0% (0/0)")
			return
		}
		fmt.Fprintf(os.Stderr, "\rVerify references: %.1f%% (%d/%d)", float64(d)/float64(total)*100, d, total)
	}
	printProgress(atomic.LoadUint64(&checked), atomic.LoadUint64(&total))
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				printProgress(atomic.LoadUint64(&checked), atomic.LoadUint64(&total))
			case <-progressDone:
				return
			}
		}
	}()

	blockIDs := make([]string, 0, len(sb.Blocks))
	for id := range sb.Blocks {
		blockIDs = append(blockIDs, id)
	}
	blobIDs := make([]string, 0, len(sb.Blobs))
	for id := range sb.Blobs {
		blobIDs = append(blobIDs, id)
	}
	entryIDs := make([]string, 0, len(sb.Entries))
	for _, versions := range sb.Entries {
		for id := range versions {
			entryIDs = append(entryIDs, id)
		}
	}
	atomic.StoreUint64(&total, uint64(len(blockIDs)+len(blobIDs)+len(entryIDs)))

	// checkRefs records missing blobs referenced by the superblock.
	checkRefs := func(prefix string, ids []string, stored map[string]struct{}) {
		for _, id := range ids {
			if _, ok := stored[id]; !ok {
				addIssue(&missing, prefix, id, "missing")
			}
			atomic.AddUint64(&checked, 1)
		}
	}

	checkRefs("blocks", blockIDs, storedBlocks)
	checkRefs("blobs", blobIDs, storedBlobs)
	checkRefs("entries", entryIDs, storedEntries)

	referencedEntries := make(map[string]struct{}, len(entryIDs))
	for _, id := range entryIDs {
		referencedEntries[id] = struct{}{}
	}

	// Orphan scan checks stored blobs that are not referenced by the superblock.
	var orphanTotal uint64
	orphanTotal += uint64(len(storedBlocks))
	orphanTotal += uint64(len(storedBlobs))
	orphanTotal += uint64(len(storedEntries))
	var orphanChecked uint64
	orphanDone := make(chan struct{})
	printOrphanProgress := func(d, total uint64) {
		if total == 0 {
			fmt.Fprint(os.Stderr, "\rVerify orphans: 100.0% (0/0)")
			return
		}
		fmt.Fprintf(os.Stderr, "\rVerify orphans: %.1f%% (%d/%d)", float64(d)/float64(total)*100, d, total)
	}
	printOrphanProgress(atomic.LoadUint64(&orphanChecked), orphanTotal)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				printOrphanProgress(atomic.LoadUint64(&orphanChecked), orphanTotal)
			case <-orphanDone:
				return
			}
		}
	}()

	for id := range storedBlocks {
		if _, ok := sb.Blocks[id]; !ok {
			orphans = append(orphans, blobKey("blocks", id))
		}
		atomic.AddUint64(&orphanChecked, 1)
	}
	for id := range storedBlobs {
		if _, ok := sb.Blobs[id]; !ok {
			orphans = append(orphans, blobKey("blobs", id))
		}
		atomic.AddUint64(&orphanChecked, 1)
	}
	for id := range storedEntries {
		if _, ok := referencedEntries[id]; !ok {
			orphans = append(orphans, blobKey("entries", id))
		}
		atomic.AddUint64(&orphanChecked, 1)
	}

	close(orphanDone)
	printOrphanProgress(atomic.LoadUint64(&orphanChecked), orphanTotal)
	fmt.Fprintln(os.Stderr)

	close(progressDone)
	printProgress(atomic.LoadUint64(&checked), atomic.LoadUint64(&total))
	fmt.Fprintln(os.Stderr)

	if len(missing) == 0 && len(orphans) == 0 && len(corrupt) == 0 {
		return nil
	}

	msg := "verify failed"
	if len(missing) > 0 {
		msg += fmt.Sprintf(": %d missing", len(missing))
	}
	if len(orphans) > 0 {
		msg += fmt.Sprintf(": %d unreferenced", len(orphans))
	}
	if len(corrupt) > 0 {
		msg += fmt.Sprintf(": %d corrupt", len(corrupt))
	}
	for _, issue := range missing {
		fmt.Fprintf(os.Stderr, "Missing %s: %s\n", issue.prefix, issue.key)
		if len(issue.matches) > 0 {
			fmt.Fprintf(os.Stderr, "  Referenced by: %s\n", strings.Join(issue.matches, ", "))
		}
	}
	for _, issue := range corrupt {
		fmt.Fprintf(os.Stderr, "Corrupt %s: %s (%s)\n", issue.prefix, issue.key, issue.detail)
		if len(issue.matches) > 0 {
			fmt.Fprintf(os.Stderr, "  Referenced by: %s\n", strings.Join(issue.matches, ", "))
		}
	}
	for _, key := range orphans {
		fmt.Fprintf(os.Stderr, "Unreferenced: %s\n", key)
	}
	return errors.New(msg)
}
