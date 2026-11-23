package store

import (
	"os"
	"strings"
	"time"

	"github.com/gm64x/usage-cli/internal/fsutil"
)

const defaultRenameMaxWait = fsutil.DefaultRenameMaxWait

// IsValidShardFile reports whether a directory entry name is a valid finalized JSON shard
// and not an in-flight temporary file.
func IsValidShardFile(name string) bool {
	return strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, ".tmp-")
}

// WriteAtomic writes data to a unique temp file in the target directory, syncs and closes it,
// and atomically renames it onto path with bounded exponential-backoff retry.
func WriteAtomic(path string, origin string, data []byte, perm os.FileMode) error {
	return fsutil.WriteAtomic(path, origin, data, perm)
}

// RetryRename renames src to dst with exponential backoff and jitter up to maxWait
// to tolerate transient Windows sharing/AV locks on NTFS.
func RetryRename(src, dst string, maxWait time.Duration) error {
	return fsutil.RetryRename(src, dst, maxWait)
}
