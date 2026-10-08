package debug

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestSecondsTextDropsZeroUnits(t *testing.T) {
	cases := map[int64]string{
		0:     "0s",
		12:    "12s",
		60:    "1m",
		1802:  "30m2s",
		3600:  "1h",
		3660:  "1h1m",
		3601:  "1h0m1s",
		14400: "4h",
	}
	for seconds, want := range cases {
		if got := secondsText(seconds); got != want {
			t.Errorf("secondsText(%d) = %q, want %q", seconds, got, want)
		}
	}
}

// debug status prints storage in binary units with how much of the quota is
// spent, and how long ago each hook was last seen, so a journal at 97% of its
// quota or a hook quiet for hours shows without arithmetic.
func TestDebugStatusTextReadsSizesAndAges(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 41, 0, 0, time.UTC)
	seen := now.Add(-95 * time.Second)
	report := debugger.StatusReport{
		SchemaVersion: 2,
		GeneratedAt:   now,
		Hooks: debugger.HookReport{Profiles: []debugger.HookProfile{
			{Profile: "personal-claude", Harness: "claude", LastSeen: &seen},
			{Profile: "copilot", Harness: "copilot"},
		}},
		Journal: debugger.JournalReport{
			Health:   diagnostics.JournalHealth{Bytes: 260151084},
			MaxBytes: 256 << 20,
		},
	}
	var out bytes.Buffer
	if err := writeDebugStatusText(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"last_seen=2026-10-08T00:39:25Z age=1m35s ",
		"last_seen=never ",
		" bytes=248.1MiB spool=0 spool_bytes=0B total_bytes=248.1MiB ",
		" quota=256.0MiB used=96%\n",
		"native logs: ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status missing %q:\n%s", want, out.String())
		}
	}
	// A store with no quota has nothing to be a percentage of.
	if strings.Contains(out.String(), "native logs: ") && strings.Contains(strings.SplitAfter(out.String(), "native logs: ")[1], "used=") {
		t.Errorf("native logs without a quota printed used=:\n%s", out.String())
	}
}
