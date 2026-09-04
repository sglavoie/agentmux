package dashboard

import (
	"go/build"
	"strings"
	"testing"
)

// TestDashboardLeafHoldsNoBubbleTeaState is the package doc's boundary claim,
// asserted rather than asserted-in-prose. The dashboard is a domain leaf: every
// entry point takes the facts it needs explicitly, so the root keeps ownership
// of the Model and adapts it at the call site.
//
// Importing the root package is already impossible — the root imports this one,
// so a back-edge would be an import cycle the compiler rejects. What a compiler
// would happily accept is this package growing a tea.Model, a tea.Cmd, or a
// tea.Msg of its own, which is how a leaf stops being one without ever naming
// the root. That is the edge this test forbids.
//
// The check reads the package's own import list rather than its transitive
// closure on purpose: a leaf that holds no Bubble Tea type in its own source
// cannot hold Bubble Tea state, whatever its dependencies do internally.
func TestDashboardLeafHoldsNoBubbleTeaState(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("reading the package's imports: %v", err)
	}
	if len(pkg.Imports) == 0 {
		t.Fatal("the package reported no imports at all, so this check would pass vacuously")
	}
	for _, path := range pkg.Imports {
		if strings.Contains(path, "bubbletea") {
			t.Errorf("the dashboard leaf imports %q; it must take its facts as explicit values, not hold program state", path)
		}
		if path == "github.com/sglavoie/agentmux/tui" {
			t.Errorf("the dashboard leaf imports the root package %q", path)
		}
	}
}
