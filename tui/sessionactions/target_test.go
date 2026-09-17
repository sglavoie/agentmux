package sessionactions

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestZeroTargetCapturesNothing(t *testing.T) {
	var target Target
	if target.HasSession {
		t.Fatal("the zero target reports a captured session")
	}
	if target.TabID() != 0 || target.WindowID() != 0 {
		t.Fatalf("zero target identity = tab %d window %d, want 0/0", target.TabID(), target.WindowID())
	}
	if ctx := target.Context(); ctx.HasTarget || !reflect.DeepEqual(ctx, commands.Context{}) {
		t.Fatalf("zero target context = %+v, want the zero context", ctx)
	}
}

func TestCaptureRecordsTabAndWindowIdentity(t *testing.T) {
	row := session.RunningSession{TabID: 7, WindowID: 42, Base: "api", Project: "api", Profile: "work"}
	target := Capture(row)
	if !target.HasSession {
		t.Fatal("a captured row reports no session")
	}
	if target.TabID() != 7 {
		t.Errorf("tab = %d, want 7", target.TabID())
	}
	if target.WindowID() != 42 {
		t.Errorf("window = %d, want 42", target.WindowID())
	}
	if !reflect.DeepEqual(target.Session, row) {
		t.Errorf("captured session = %+v, want %+v", target.Session, row)
	}
}

// TestCaptureIsASnapshot is the retargeting guarantee: the row is copied, so a
// topology refresh that rewrites the caller's value cannot move the target an
// action was aimed at.
func TestCaptureIsASnapshot(t *testing.T) {
	row := session.RunningSession{TabID: 7, WindowID: 42, Locked: false}
	target := Capture(row)
	row.WindowID = 99
	row.Locked = true
	if target.WindowID() != 42 {
		t.Errorf("window = %d, want the captured 42", target.WindowID())
	}
	if target.Session.Locked {
		t.Error("the captured row followed a later mutation of its source")
	}
}

// TestContextRoundTrip pins the bridge to the palette's generic context in both
// directions: the two are the same capture wearing different names, and the
// availability gates in tui/commands stay the single implementation.
func TestContextRoundTrip(t *testing.T) {
	row := session.RunningSession{TabID: 3, WindowID: 11, Project: "api", Profile: "work", Muted: true}
	target := Capture(row)

	ctx := target.Context()
	if !ctx.HasTarget || !reflect.DeepEqual(ctx.Target, row) {
		t.Fatalf("context = %+v, want the captured row", ctx)
	}
	if back := FromContext(ctx); !reflect.DeepEqual(back, target) {
		t.Fatalf("round trip = %+v, want %+v", back, target)
	}

	if back := FromContext(commands.Context{}); !reflect.DeepEqual(back, Target{}) {
		t.Fatalf("empty context = %+v, want the zero target", back)
	}
	// A context flagged as empty is empty whatever row it carries: HasTarget is
	// the fact, the row is the payload.
	if back := FromContext(commands.Context{Target: row}); !reflect.DeepEqual(back, Target{}) {
		t.Fatalf("targetless context = %+v, want the zero target", back)
	}
}
