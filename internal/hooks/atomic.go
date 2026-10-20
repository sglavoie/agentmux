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

// writeBackup writes data to path with permissions perm, ensuring perm is
// enforced even when path already exists. os.WriteFile only applies perm on
// creation: an existing file keeps its previous mode, which can leave a
// stale-permission .bak after the user tightens the original settings file
// to 0o600. We chmod after the write so the backup mode always tracks the
// caller-provided perm.
func writeBackup(path string, data []byte, perm os.FileMode) error {
	writePerm := perm | 0o200
	if info, err := os.Stat(path); err == nil {
		if info.Mode().IsRegular() && info.Mode().Perm()&0o200 == 0 {
			if err := os.Chmod(path, info.Mode().Perm()|0o200); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(path, data, writePerm); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}
