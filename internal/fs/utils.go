package fs

import "path"

func blobKey(prefix, hash string) string {
	return path.Join(prefix, hash)
}
