package plans

import (
	"crypto/sha256"
	"fmt"
	"time"
)

type planFileInfo struct {
	rel  string
	size int64
	mod  time.Time
}

// Files arrive in sorted relative-path order. Length-prefixing the path keeps
// unusual filenames from colliding with record separators. UTC normalizes the
// timestamp across JSON round trips and processes in different time zones.
func planFileFingerprint(files []planFileInfo) string {
	digest := sha256.New()
	for _, file := range files {
		_, _ = fmt.Fprintf(digest, "%d:%s:%d:%s\n", len(file.rel), file.rel, file.size, file.mod.UTC().Format(time.RFC3339Nano))
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}
