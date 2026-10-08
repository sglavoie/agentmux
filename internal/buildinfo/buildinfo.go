// Package buildinfo identifies the built amx artifact: semantic version, VCS
// revision, dirty marker, build time, Go version, and the binary's content
// hash. The CLI stamps it into version output and the daemon reports it from
// TypeHealth, so a deployed daemon can never be mistaken for the source tree
// or for a different installed binary.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

// Info is one artifact's identity. Version is the semantic version constant;
// Revision/Dirty/BuildTime come from the embedded VCS stamp (empty when the
// binary was built outside a git checkout, e.g. `go test`); BinaryHash is the
// SHA-256 of the executable file, computed once at first use so a daemon
// whose on-disk binary is later replaced keeps reporting the artifact it is
// actually running.
type Info struct {
	Version    string `json:"version"`
	Revision   string `json:"revision,omitempty"`
	Dirty      bool   `json:"dirty,omitempty"`
	BuildTime  string `json:"build_time,omitempty"`
	GoVersion  string `json:"go_version,omitempty"`
	BinaryHash string `json:"binary_hash,omitempty"`
}

var (
	mu      sync.Mutex
	version = "dev"
	cached  *Info

	// Test seams.
	readBuildInfo = debug.ReadBuildInfo
	executable    = os.Executable
)

// SetVersion installs the semantic version (the cmd package owns the
// constant). It invalidates the cached Info so a later Current reflects it.
func SetVersion(v string) {
	mu.Lock()
	version = v
	cached = nil
	mu.Unlock()
}

// Current returns this process's artifact identity, computing and caching it
// on first use. It never fails: missing VCS stamps or an unreadable
// executable simply leave the corresponding fields empty.
func Current() Info {
	mu.Lock()
	defer mu.Unlock()
	if cached != nil {
		return *cached
	}
	info := collect()
	cached = &info
	return info
}

func collect() Info {
	info := Info{Version: version, GoVersion: runtime.Version()}
	if bi, ok := readBuildInfo(); ok {
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				info.Revision = setting.Value
			case "vcs.modified":
				info.Dirty = setting.Value == "true"
			case "vcs.time":
				info.BuildTime = setting.Value
			}
		}
	}
	info.BinaryHash = hashExecutable()
	return info
}

func hashExecutable() string {
	path, err := executable()
	if err != nil {
		return ""
	}
	return HashFile(path)
}

// HashFile returns the hex SHA-256 of the file at path, the same content hash
// BinaryHash carries, or "" when the file cannot be read. It lets a caller ask
// whether a binary on disk is the artifact a running process reported.
func HashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ShortRevision is the abbreviated VCS revision with a "-dirty" suffix when
// the working tree was modified, or "unknown" when no VCS stamp is embedded.
func (i Info) ShortRevision() string {
	rev := i.Revision
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev == "" {
		rev = "unknown"
	}
	if i.Dirty {
		rev += "-dirty"
	}
	return rev
}

// String renders the identity for human-facing version output.
func (i Info) String() string {
	parts := []string{fmt.Sprintf("revision %s", i.ShortRevision())}
	if i.BuildTime != "" {
		parts = append(parts, "built "+i.BuildTime)
	}
	if i.GoVersion != "" {
		parts = append(parts, i.GoVersion)
	}
	if i.BinaryHash != "" {
		hash := i.BinaryHash
		if len(hash) > 12 {
			hash = hash[:12]
		}
		parts = append(parts, "sha256:"+hash)
	}
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(parts, ", "))
}

// SameArtifact reports whether two identities describe the same built binary.
// Content hashes decide when both sides have one; otherwise it falls back to
// the version/revision/dirty triple, so a daemon built before hashing was
// available still compares meaningfully.
func (i Info) SameArtifact(other Info) bool {
	if i.BinaryHash != "" && other.BinaryHash != "" {
		return i.BinaryHash == other.BinaryHash
	}
	return i.Version == other.Version && i.Revision == other.Revision && i.Dirty == other.Dirty
}
