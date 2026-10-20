package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
)

// The deprecation banner and the daemon/hooks pip strip are gone. Their content
// reaches the operator through the header's single health indicator, which names
// the worst reading and opens the details overlay carrying the rest. These tests
// pin what the indicator says, because a quiet header only works if the one
// thing it still says is right.

// A configuration notice is a health issue like any other, so it reaches the
// header rather than a banner of its own — and the indicator stays one line and
// one column-run whether or not the notice is there.
func TestHeaderHealthIndicatorNamesTheConfigurationNotice(t *testing.T) {
	t.Parallel()

	m := Model{
		width:                 120,
		daemonAlive:           true,
		daemonHealthCheckedAt: time.Now(),
		hookHealth:            commands.HookHealth{InSync: []string{"work"}},
		hookHealthCheckedAt:   time.Now(),
		topologyObservedAt:    time.Now(),
		deprecations: []config.Deprecation{
			{Key: "accounts", Replacement: "profiles", Location: "[[accounts]]"},
		},
	}
	if out := stripAnsi(headerHealthSegment(m).text); !strings.Contains(out, "config deprecated keys") {
		t.Errorf("indicator = %q, want the configuration notice", out)
	}
	if !strings.Contains(stripAnsi(renderHealthBox(m)), "accounts → profiles") {
		t.Errorf("details overlay does not carry the migration arrow:\n%s", stripAnsi(renderHealthBox(m)))
	}

	// Dismissal is session-scoped and silences only this notice.
	m.deprecationDismissed = true
	if out := stripAnsi(headerHealthSegment(m).text); !strings.Contains(out, "all checks ok") {
		t.Errorf("indicator = %q after dismissal, want the clean reading", out)
	}
}

// The indicator is a click target. It is the only route a pointer has into the
// details overlay, and the overlay has no key of its own, so losing the target
// would leave the reading unreadable to anyone not using the palette.
func TestHeaderHealthIndicatorIsAClickTarget(t *testing.T) {
	t.Parallel()

	m := Model{width: 120, daemonAlive: false, daemonHealthCheckedAt: time.Now()}
	header := renderHeaderWithHits(m)
	var found bool
	for _, hit := range header.hits {
		if hit.kind != mouseHitHealth {
			continue
		}
		found = true
		if hit.rect.X+hit.rect.Width != m.width {
			t.Errorf("indicator hit ends at %d, want the right edge %d", hit.rect.X+hit.rect.Width, m.width)
		}
	}
	if !found {
		t.Fatal("header publishes no health click target")
	}
}

// A degraded reading names the worst issue rather than counting, and several
// degraded readings count rather than picking one. Both remedies stay one click
// away in the overlay.
func TestHeaderHealthIndicatorRanksSeverityOverCount(t *testing.T) {
	t.Parallel()

	now := time.Now()
	one := Model{
		width:                 120,
		daemonAlive:           false,
		daemonHealthCheckedAt: now,
		hookHealth:            commands.HookHealth{InSync: []string{"work"}},
		hookHealthCheckedAt:   now,
		topologyObservedAt:    now,
	}
	if out := stripAnsi(headerHealthSegment(one).text); !strings.Contains(out, "daemon down") {
		t.Errorf("indicator = %q with one issue, want it named", out)
	}

	two := one
	two.hookHealth = commands.HookHealth{Drifted: []string{"work"}}
	if out := stripAnsi(headerHealthSegment(two).text); !strings.Contains(out, "2 issues") {
		t.Errorf("indicator = %q with two issues, want the count", out)
	}
	body := stripAnsi(renderHealthBox(two))
	for _, remedy := range []string{"amx daemon start", "amx config hooks-sync"} {
		if !strings.Contains(body, remedy) {
			t.Errorf("details overlay missing %q:\n%s", remedy, body)
		}
	}
}
