package kitty

import (
	"os"
	"strconv"
	"strings"
)

// NormalizeListenAddress converts a Kitty remote-control listen address into a
// form that a different process can reuse.
//
// Kitty exposes KITTY_LISTEN_ON in three forms:
//
//   - "unix:..." and "tcp:..." endpoints are transferable — any process may
//     dial them — so they pass through unchanged.
//   - "fd:N" is a process-local file descriptor handed to a
//     `launch --allow-remote-control` child. It is unusable once persisted or
//     inherited by the detached daemon, so it is rebuilt as
//     "unix:/tmp/kitty-<kittyPID>" to match the listen_on this project
//     requires in kitty.conf.
//
// Any other or malformed value — including an "fd:" form without a valid Kitty
// PID, or a prefix-only "unix:"/"tcp:" with no endpoint — yields "" so callers
// never forward a guessed or unusable descriptor.
func NormalizeListenAddress(addr, kittyPID string) string {
	addr = strings.TrimSpace(addr)
	switch {
	case addr == "":
		return ""
	case strings.HasPrefix(addr, "unix:"), strings.HasPrefix(addr, "tcp:"):
		if IsReusableListenAddress(addr) {
			return addr
		}
		return ""
	case strings.HasPrefix(addr, "fd:"):
		return normalizeFDListenAddress(addr, kittyPID)
	default:
		return ""
	}
}

// IsReusableListenAddress reports whether addr is a transferable Kitty
// remote-control endpoint that a different process may dial. A reusable
// endpoint is a "unix:" or "tcp:" scheme followed by a non-empty payload;
// prefix-only values such as "unix:" or "tcp:", process-local "fd:"
// descriptors, empty strings, and all other forms are rejected. This is the
// single validation rule shared by listener normalization and the daemon's
// per-window/fallback socket storage and dial paths.
func IsReusableListenAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	for _, prefix := range []string{"unix:", "tcp:"} {
		if strings.HasPrefix(addr, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(addr, prefix)) != ""
		}
	}
	return false
}

// normalizeFDListenAddress rebuilds a process-local "fd:N" descriptor into the
// stable Unix socket the project configures Kitty to listen on. It returns ""
// when the descriptor is malformed or the Kitty PID is missing/invalid.
func normalizeFDListenAddress(addr, kittyPID string) string {
	fd := strings.TrimSpace(strings.TrimPrefix(addr, "fd:"))
	if n, err := strconv.Atoi(fd); err != nil || n < 0 {
		return ""
	}
	pid := strings.TrimSpace(kittyPID)
	if p, err := strconv.Atoi(pid); err != nil || p <= 0 {
		return ""
	}
	return "unix:/tmp/kitty-" + pid
}

// NormalizedListenAddressFromEnv normalizes the current process's
// KITTY_LISTEN_ON using KITTY_PID for any "fd:" rewrite. It returns "" when no
// transferable address is available (e.g. outside Kitty, or an "fd:" descriptor
// without a resolvable Kitty PID).
func NormalizedListenAddressFromEnv() string {
	return NormalizeListenAddress(os.Getenv("KITTY_LISTEN_ON"), os.Getenv("KITTY_PID"))
}
