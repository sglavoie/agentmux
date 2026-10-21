package debugger

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// A Kitty nobody can reach from here is reported in plain words, not as the
// kitten command line that failed.
func TestCollectStatusWordsAnUnreachableKittyPlainly(t *testing.T) {
	report, err := CollectStatus(t.Context(), StatusOptions{
		Paths:  diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state")),
		Daemon: testHealthClient{}, Kitty: testKittyClient{err: fmt.Errorf("kitty command failed: kitten @ ls\n  error: %w", kitty.ErrNoKittyTerminal)},
		AMXBin: "/tmp/amx", CLIBuild: buildinfo.Info{Version: "test"},
		Now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range report.Findings {
		if finding.Check == "kitty_unavailable" {
			if finding.Message != kitty.UnreachableMessage || strings.Contains(finding.Message, "kitten") {
				t.Fatalf("kitty_unavailable = %q", finding.Message)
			}
			return
		}
	}
	t.Fatalf("no kitty_unavailable finding in %+v", report.Findings)
}
