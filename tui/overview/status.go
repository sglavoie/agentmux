// The one sentence the surface says about itself.
//
// The grid shows sessions; the feedback row shows everything that is true about
// the surface rather than about a session. It is a single row by design — the
// chrome reserves exactly one and keeps it reserved even when empty, so nothing
// below it moves when something happens — which means the states have to be
// RANKED rather than listed.
//
// The ranking, highest first, and why each outranks the next:
//
//  1. A failed focus. The operator just pressed a key and it did not work.
//     Nothing the surface could say about itself competes with the report of
//     something that was asked for.
//  2. A failed FIRST load. No tree has ever landed, so there is no list to
//     interpret and the error is the entire truth about the surface.
//  3. Still loading. Same cold state, no error yet: the read is simply out. This
//     is what an operator sees for the first fraction of a second, and saying
//     nothing there would be indistinguishable from an empty Kitty.
//  4. A delayed refresh. A tree HAS landed, so the rows stay and the only
//     question is how old they have become. This outranks emptiness because a
//     list that cannot be refreshed might be stale about being empty, and "no
//     running sessions" asserted from a frozen reading would be a claim the
//     surface cannot back up.
//  5. No running sessions. The last CONFIRMED reading found nothing, and the
//     grid is blank for a reason worth naming rather than because it is broken.
//
// Below the delay threshold and with sessions to show, the row is empty. A
// surface that announced every two-second gap between reads would teach the
// operator to stop reading it.
package overview

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// feedback is the ranked sentence, or "" when there is nothing to report.
func (m model) feedback() string {
	if m.focusErr != nil {
		return "Focus failed: " + m.focusErr.Error()
	}
	age, warm := m.topologyAge()
	if !warm {
		if m.err != nil {
			return "Kitty error: " + m.err.Error()
		}
		return "Loading live sessions…"
	}
	if age >= protocol.KittySnapshotStaleAfter {
		return fmt.Sprintf("Kitty refresh delayed; showing cached sessions (%ds old)", int(age/time.Second))
	}
	if len(m.sessions) == 0 {
		return "No running sessions"
	}
	return ""
}

// topologyAge reports how old the tree on screen is, and whether there is one at
// all.
//
// The age is the reading the tree already carried when it was applied PLUS
// everything elapsed since. Adding the elapsed term is what lets the delay
// warning appear during an outage, when by definition no message is arriving to
// recompute anything.
//
// The threshold it is compared against is the daemon's own
// protocol.KittySnapshotStaleAfter, but the verdict is computed here rather than
// taken from the daemon's Stale flag: that flag describes one snapshot at the
// moment it answered and says nothing about a read that never reached a daemon.
//
// A backwards clock is clamped to zero rather than reported as a negative age.
// The false return is the cold state — no tree has ever been applied.
func (m model) topologyAge() (time.Duration, bool) {
	if m.topologyObservedAt.IsZero() {
		return 0, false
	}
	age := m.topology.Age + m.deps.now().Sub(m.topologyObservedAt)
	if age < 0 {
		return 0, true
	}
	return age, true
}
