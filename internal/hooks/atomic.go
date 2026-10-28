package hooks

import (
	"errors"
	"os"

	"github.com/sglavoie/agentmux/internal/atomicfile"
)

// writeSettings replaces a harness settings file in one step (see
// internal/atomicfile: sibling temp, exact mode, fsync, rename, directory
// fsync), so a failed merge never leaves it truncated. It is a variable so
// tests can fail the write and check what a failed merge leaves behind;
// production code never reassigns it.
var writeSettings = func(path string, data []byte, perm os.FileMode) error {
	return atomicfile.Write(path, data, perm)
}

// errInjectedFault is the sentinel returned by tests that fault-inject into
// writeSettings. Exported only via the test build path.
var errInjectedFault = errors.New("hooks: injected write fault")

// writeBackup publishes the recovery copy only after its contents and exact
// permissions are ready. An existing backup may have looser permissions than
// the settings file, and a failed write must leave the previous copy intact.
// Refuse a symlink rather than writing through it to an unrelated file.
func writeBackup(path string, data []byte, perm os.FileMode) error {
	return atomicfile.Write(path, data, perm,
		atomicfile.NoCreateParents(), atomicfile.RequireRegularTarget())
}
