package session

import (
	"io"
	"log"
	"os"
	"strings"
)

// debugEnabled reports whether package debug logging is requested via env.
// The launch/close/restart debug loggers used to unconditionally open a
// world-readable file in /tmp on every binary invocation, which both leaked a
// file descriptor per package init and exposed session metadata (titles,
// directories, env vars) to other users on shared systems. Gating on
// amx_DEBUG/AMX_DEBUG keeps the diagnostic available when needed without
// running it by default.
func debugEnabled() bool {
	for _, key := range []string{"amx_DEBUG", "AMX_DEBUG"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func newFileLogger(path, prefix string) *log.Logger {
	if !debugEnabled() {
		return log.New(io.Discard, prefix, 0)
	}
	// 0600 so other users on a shared host cannot read session metadata.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return log.New(io.Discard, prefix, 0)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return log.New(io.Discard, prefix, 0)
	}
	return log.New(f, prefix, log.LstdFlags)
}
