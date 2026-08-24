package screenstate

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureRoot mirrors internal/contextlimit/marker_test.go: the classifier keeps
// its real screen captures in a sibling testdata/ directory and reads them
// verbatim.
//
// Every file under it is a byte-for-byte `kitten @ get-text --extent=screen`
// capture of a live harness pane, never hand-edited. That rule is the point of
// the directory rather than a tidiness preference: the bug this package exists
// to close survived a full test suite because every committed "Claude is
// working" screen was hand-modelled with an `esc to interrupt` hint that Claude
// Code 2.1.x had already stopped rendering. A hand-adjusted capture can only
// ever confirm what its author already believed.
const fixtureRoot = "testdata"

// readScreenFixture returns a captured pane screen verbatim. Callers get the raw
// bytes, padding included, because the padding is exactly what NormalizeLines
// and Fingerprint are being asked about.
func readScreenFixture(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(fixtureRoot, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return string(data)
}
