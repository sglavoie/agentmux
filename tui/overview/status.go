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
//  0. A failed relaunch whose entry is STILL claimable. It is the one report
//     ABOUT the offer below it, and the stack puts the entry it names back on
//     that offer, so showing the offer instead would redraw the row exactly as
//     it looked before the keystroke — indistinguishable from `u` having been
//     dropped. It names the retry itself, for what is left of that entry's
//     window. Once the window closes it drops both the retry and this rank and
//     becomes an ordinary outcome at rank 2, because the reason it outranked the
//     offer was that it was about the offer.
//  1. A recoverable close. It outranks even a failed keystroke because it is the
//     only entry that is IRREVERSIBLY LOST if it is not shown: the offer expires
//     in five seconds and the session it would restore cannot be reconstructed
//     afterwards. Everything below it is still true on the next frame.
//  2. A failed focus, or the report of an action that ran. Both are the outcome
//     of something the operator just asked for, and nothing the surface could
//     say about ITSELF competes with that. They cannot both be set — starting an
//     action clears the focus error — so the ordering between them decides
//     nothing.
//  3. A failed FIRST load. No tree has ever landed, so there is no list to
//     interpret and the error is the entire truth about the surface.
//  4. Still loading. Same cold state, no error yet: the read is simply out. This
//     is what an operator sees for the first fraction of a second, and saying
//     nothing there would be indistinguishable from an empty Kitty.
//  5. A delayed refresh. A tree HAS landed, so the rows stay and the only
//     question is how old they have become. This outranks emptiness because a
//     list that cannot be refreshed might be stale about being empty, and "no
//     running sessions" asserted from a frozen reading would be a claim the
//     surface cannot back up.
//  6. No running sessions. The last CONFIRMED reading found nothing, and the
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
	if report := m.offerOwningReport(); report != "" {
		return report
	}
	if offer := m.recoveryOffer(); offer != "" {
		return offer
	}
	if m.focusErr != nil {
		return "Focus failed: " + m.focusErr.Error()
	}
	if report := m.activeReport(); report != "" {
		return report
	}
	if m.preferenceErr != nil {
		return "View preference unavailable: " + m.preferenceErr.Error()
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
	if m.portableErr != nil {
		// The tmux tiles are the last ones listed; say so rather than let
		// them pass for live ones.
		return "tmux outage: " + m.portableErr.Error()
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
