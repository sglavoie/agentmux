// Package companion projects the daemon's state into the CompanionSnapshot a
// subscribed companion renders. It is a pure function over values the caller
// has already copied: it performs no Kitty, IPC, filesystem, or journal I/O,
// reads no clock of its own, and never imports the daemon root, so the hub can
// run it outside stateMu against a copy taken under it.
//
// Two rules make the projection what it is. A session is read from the Kitty
// window tree first and from daemon state second, and the two are joined only
// when they are provably about the same Kitty instance — window IDs are
// meaningful only inside the instance that issued them, so a state recorded
// against another socket never decorates a tree window that merely shares its
// number. And the snapshot carries facts rather than ages, so projecting an
// unchanged world twice yields byte-identical JSON and Digest can tell the hub
// there is nothing to publish.
package companion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// Input is everything one projection reads. Every field is a value the caller
// owns outright; Project neither retains nor mutates any of it.
type Input struct {
	Daemon protocol.CompanionDaemon
	// States is the daemon's per-window visible state, keyed by bare window ID,
	// exactly as the state query publishes it.
	States map[int]protocol.WindowStateSnapshot
	// Sockets records the Kitty socket each window's hooks named, keyed by the
	// same bare window ID. It is what attributes a state to an instance.
	Sockets map[int]string
	// Kitty is the daemon's cached window tree and the socket it was read
	// through. A cold tree (Ready false) contributes no sessions.
	Kitty        KittyTree
	Notification Notification
	// ASLTargets are the registered supervisor poll targets, which say which
	// run a window is driving; ASLPending is the subset currently waiting on an
	// answer (asl.SupervisorRegistry.Targets and Pending).
	ASLTargets []asl.SupervisorTarget
	ASLPending []asl.PendingQuestion
	// Now decides only whether the configured quiet hours are in force. It is
	// never published, so it cannot make two projections of the same world
	// differ except across a quiet-hours boundary.
	Now time.Time
}

// KittyTree is the window tree a projection reads sessions from.
type KittyTree struct {
	Ready   bool
	Stale   bool
	Socket  string
	Windows []kitty.OSWindow
}

// Notification is the notifier's posture as the caller read it.
type Notification struct {
	Backend      string
	AppConnected bool
	Snooze       protocol.SnoozeState
	// QuietHours is the configured "HH:MM-HH:MM" spec, verbatim.
	QuietHours string
}

// Field bounds. Display text is clamped, because a shortened title is still a
// title. Identifiers are never clamped — a shortened socket or session ID names
// something else — so one over identifierMax is omitted instead.
const (
	displayMax    = 128
	identifierMax = 256
)

// snapshotBudget is how many encoded bytes the snapshot payload may use, which
// leaves the stream frame envelope ample room under CompanionFrameMaxBytes.
// Clamping bounds a typical session, but JSON escaping can still multiply a
// hostile string several times over, so the budget is enforced by measuring
// rather than by arithmetic.
var snapshotBudget = protocol.CompanionFrameMaxBytes - 4096

// workingStates are the active-class icons, the same set the reducer treats as
// expirable activity: a session showing one is busy and needs nobody.
var workingStates = map[string]bool{"bolt": true, "subagent": true, "compact": true}

// candidate is one projected session with the order it is published in.
type candidate struct {
	session protocol.CompanionSession
	order   int
}

// Project builds the companion snapshot for one moment of daemon state.
func Project(in Input) protocol.CompanionSnapshot {
	out := protocol.CompanionSnapshot{
		V:            protocol.CompanionSnapshotVersion,
		Daemon:       in.Daemon,
		Notification: projectNotification(in.Notification, in.Now),
		Kitty: protocol.CompanionKitty{
			Ready:  in.Kitty.Ready,
			Stale:  in.Kitty.Stale,
			Socket: identifier(in.Kitty.Socket),
		},
	}
	if in.Kitty.Ready {
		out.Dashboard = projectDashboard(in.Kitty)
	}

	runs := runsByWindow(in.ASLTargets, in.ASLPending)
	candidates := treeSessions(in, runs)
	candidates = append(candidates, foreignSessions(in, runs, len(candidates))...)

	sockets := map[string]bool{}
	for _, c := range candidates {
		countAttention(&out.Attention, c.session)
		if c.session.KittySocket != "" {
			sockets[c.session.KittySocket] = true
		}
	}
	out.Kitty.Sockets = sortedKeys(sockets)
	out.ASL.Waiting = projectWaiting(in.ASLPending)

	keep := min(len(candidates), protocol.CompanionSessionCap)
	priority := byPriority(candidates)
	out.Sessions, out.Truncated = selectSessions(priority, keep, len(candidates))
	if encodedSize(out) > snapshotBudget {
		// Find the largest prefix of the priority order that fits. Sizes grow
		// monotonically with the prefix, so a binary search is exact.
		lo, hi := 0, keep
		for lo < hi {
			mid := (lo + hi + 1) / 2
			out.Sessions, _ = selectSessions(priority, mid, len(candidates))
			if encodedSize(out) <= snapshotBudget {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		out.Sessions, out.Truncated = selectSessions(priority, lo, len(candidates))
	}
	return out
}

// Digest is the identity of a snapshot's content: equal for two projections
// of the same world, different the moment anything a client renders changes.
// The hub publishes only when it moves.
func Digest(s protocol.CompanionSnapshot) string {
	data, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func projectNotification(n Notification, now time.Time) protocol.CompanionNotification {
	out := protocol.CompanionNotification{
		Backend:      display(n.Backend),
		AppConnected: n.AppConnected,
		SnoozedUntil: n.Snooze.SnoozedUntil,
		Indefinite:   n.Snooze.Indefinite,
	}
	// An unparseable spec is published as absent: the notifier refuses it the
	// same way, so showing it would claim a quiet window nothing enforces.
	if window, ok, err := config.ParseQuietHours(n.QuietHours); err == nil && ok {
		out.QuietHours = n.QuietHours
		out.QuietNow = !now.IsZero() && window.Contains(now)
	}
	return out
}

func projectDashboard(tree KittyTree) protocol.CompanionDashboard {
	loc, ok := kitty.FindDashboardWindow(tree.Windows, 0)
	if !ok {
		return protocol.CompanionDashboard{}
	}
	out := protocol.CompanionDashboard{
		Open:        true,
		KittySocket: identifier(tree.Socket),
		TabID:       loc.TabID,
		WindowID:    loc.WindowID,
	}
	for _, osw := range tree.Windows {
		for _, tab := range osw.Tabs {
			if tab.ID == loc.TabID {
				out.OSWindowID = osw.ID
				return out
			}
		}
	}
	return out
}

// sameInstance reports whether a state recorded against socket may describe a
// window in a tree read through treeSocket. Two named sockets must agree. An
// unnamed side is accepted: a single-instance install records no socket at
// all, and refusing it would blank every session there.
func sameInstance(socket, treeSocket string) bool {
	return socket == "" || treeSocket == "" || socket == treeSocket
}

// treeSessions projects every managed window in the Kitty tree, in tree order.
func treeSessions(in Input, runs map[int]string) []candidate {
	if !in.Kitty.Ready {
		return nil
	}
	var out []candidate
	for _, osw := range in.Kitty.Windows {
		for _, tab := range osw.Tabs {
			parts := palette.ParseTitleParts(tab.Title)
			for _, win := range tab.Windows {
				vars := win.UserVars
				if vars.EffectiveProfile() == "" && vars.EffectiveHarness() == "" {
					continue
				}
				socket := in.Kitty.Socket
				if socket == "" {
					socket = in.Sockets[win.ID]
				}
				s := protocol.CompanionSession{
					WindowID:    win.ID,
					TabID:       tab.ID,
					OSWindowID:  osw.ID,
					KittySocket: identifier(socket),
					Harness:     display(vars.EffectiveHarness()),
					Profile:     display(vars.EffectiveProfile()),
					Project:     display(vars.EffectiveProject(parts.Base)),
					BaseTitle:   display(parts.Base),
					IconState:   display(parts.State),
					Muted:       parts.Muted || vars.Mute != "",
					Locked:      parts.Locked || vars.Locked,
					Looping:     parts.Looping,
					ASLRunID:    identifier(runs[win.ID]),
				}
				if st, ok := in.States[win.ID]; ok && sameInstance(in.Sockets[win.ID], in.Kitty.Socket) {
					applyState(&s, st)
				}
				s.Attention = protocol.AttentionStates[s.IconState] || tab.NeedsAttention
				out = append(out, candidate{session: s, order: len(out)})
			}
		}
	}
	return out
}

// foreignSessions projects the daemon states recorded against a Kitty instance
// other than the tree's. The daemon has no tree for that instance, so these
// carry state and identity only — no tab, no title — ordered by socket and
// window so the order is stable.
func foreignSessions(in Input, runs map[int]string, base int) []candidate {
	if in.Kitty.Socket == "" {
		return nil
	}
	var ids []int
	for id := range in.States {
		if socket := in.Sockets[id]; socket != "" && socket != in.Kitty.Socket {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		si, sj := in.Sockets[ids[i]], in.Sockets[ids[j]]
		if si != sj {
			return si < sj
		}
		return ids[i] < ids[j]
	})
	out := make([]candidate, 0, len(ids))
	for _, id := range ids {
		socket := identifier(in.Sockets[id])
		if socket == "" {
			// An over-long socket cannot be named, and a session no client can
			// address is not worth listing.
			continue
		}
		s := protocol.CompanionSession{
			WindowID:    id,
			KittySocket: socket,
			ASLRunID:    identifier(runs[id]),
		}
		applyState(&s, in.States[id])
		if s.Conversation != nil {
			s.Harness = display(s.Conversation.Harness)
		}
		s.Attention = protocol.AttentionStates[s.IconState]
		out = append(out, candidate{session: s, order: base + len(out)})
	}
	return out
}

// applyState lays the daemon's state over a session. The daemon's icon wins
// over the title's: the title is painted from it and may simply lag.
func applyState(s *protocol.CompanionSession, st protocol.WindowStateSnapshot) {
	s.IconState = display(st.IconState)
	s.Reason = display(st.Reason)
	s.Unseen = st.Unseen
	s.Looping = s.Looping || st.Looping
	s.BusySinceAt = st.BusySinceAt
	s.LastEventAt = st.LastEventAt
	s.PresentationChangedAtMS = st.PresentationChangedAtMS
	s.Branch = display(st.Branch)
	s.Dirty = st.Dirty
	if st.Conversation != nil && identifier(st.Conversation.Harness) != "" && identifier(st.Conversation.SessionID) != "" {
		identity := *st.Conversation
		s.Conversation = &identity
	}
}

// runsByWindow names the run each window drives. A window with a waiting run
// names that one; otherwise the lexically first, so the choice is stable.
func runsByWindow(targets []asl.SupervisorTarget, pending []asl.PendingQuestion) map[int]string {
	waiting := map[int]string{}
	for _, p := range pending {
		id, run := p.Target.WindowID, p.Target.Candidate.RunID
		if cur, ok := waiting[id]; !ok || run < cur {
			waiting[id] = run
		}
	}
	runs := map[int]string{}
	for _, t := range targets {
		id, run := t.WindowID, t.Candidate.RunID
		if cur, ok := runs[id]; !ok || run < cur {
			runs[id] = run
		}
	}
	maps.Copy(runs, waiting)
	return runs
}

func projectWaiting(pending []asl.PendingQuestion) []protocol.CompanionASLWaiting {
	var out []protocol.CompanionASLWaiting
	for _, p := range pending {
		run := identifier(p.Target.Candidate.RunID)
		if run == "" || p.Projection.UnansweredQuestions <= 0 {
			continue
		}
		w := protocol.CompanionASLWaiting{
			RunID:            run,
			PlanDir:          identifier(p.Target.Candidate.PlanDir),
			WindowID:         p.Target.WindowID,
			Unanswered:       p.Projection.UnansweredQuestions,
			LatestQuestionID: identifier(p.Projection.LatestQuestionID),
		}
		if !p.Projection.LatestQuestionCreatedAt.IsZero() {
			w.LatestQuestionCreatedAt = p.Projection.LatestQuestionCreatedAt.Unix()
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WindowID != out[j].WindowID {
			return out[i].WindowID < out[j].WindowID
		}
		return out[i].RunID < out[j].RunID
	})
	if len(out) > protocol.CompanionWaitingCap {
		out = out[:protocol.CompanionWaitingCap]
	}
	return out
}

func countAttention(a *protocol.CompanionAttention, s protocol.CompanionSession) {
	if s.Attention {
		a.Total++
	}
	switch s.IconState {
	case "stop":
		a.Stop++
	case "key":
		a.Key++
	case "error":
		a.Error++
	case "question":
		a.Question++
	}
	if workingStates[s.IconState] {
		a.Working++
	}
	if s.Unseen {
		a.Unseen++
	}
}

// byPriority orders candidates for truncation: sessions needing attention
// first, then everything else, each in publication order. A capped snapshot
// therefore drops idle and working rows before it drops one a human is
// waited on.
func byPriority(candidates []candidate) []candidate {
	out := append([]candidate(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].session.Attention && !out[j].session.Attention
	})
	return out
}

// selectSessions keeps the first keep candidates of the priority order and
// publishes them in their original order.
func selectSessions(priority []candidate, keep, total int) ([]protocol.CompanionSession, bool) {
	kept := append([]candidate(nil), priority[:keep]...)
	sort.Slice(kept, func(i, j int) bool { return kept[i].order < kept[j].order })
	sessions := make([]protocol.CompanionSession, 0, len(kept))
	for _, c := range kept {
		sessions = append(sessions, c.session)
	}
	return sessions, keep < total
}

func encodedSize(s protocol.CompanionSnapshot) int {
	data, err := json.Marshal(s)
	if err != nil {
		return 0
	}
	return len(data)
}

// display clamps free text to displayMax bytes on a rune boundary.
func display(s string) string {
	if len(s) <= displayMax {
		return s
	}
	cut := displayMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// identifier returns s, or "" when it is too long to publish whole.
func identifier(s string) string {
	if len(s) > identifierMax {
		return ""
	}
	return s
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
