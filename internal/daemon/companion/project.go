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
	Kitty KittyTree
	// Others are the window trees of Kitty instances other than Kitty's,
	// keyed by the socket each was read through. A state recorded against one
	// of those sockets takes its tab, title, and identity from that tree; one
	// whose instance has no tree here is published from its state alone.
	Others       map[string][]kitty.OSWindow
	Notification Notification
	// ASLTargets are the registered supervisor poll targets, which say which
	// run a window is driving; ASLPending is the subset currently waiting on an
	// answer (asl.SupervisorRegistry.Targets and Pending).
	ASLTargets []asl.SupervisorTarget
	ASLPending []asl.PendingQuestion
	// Runtime names the runtime session bound to each slot, keyed like
	// States. A slot whose Backend is not Kitty is not a Kitty window: v1
	// never lists it, and v2 lists it by its runtime identity alone.
	Runtime map[int]RuntimeSlot
	// SnapshotVersion selects the shape: CompanionSnapshotVersionRuntime
	// projects v2, anything else the v1 snapshot old clients read.
	SnapshotVersion int
	// Now decides only whether the configured quiet hours are in force. It is
	// never published, so it cannot make two projections of the same world
	// differ except across a quiet-hours boundary.
	Now time.Time
}

// RuntimeSlot is the runtime session holding one slot and its target.
type RuntimeSlot struct {
	SessionID  string
	Backend    string
	Instance   string
	Generation string
	Pane       string
}

func (r RuntimeSlot) kitty() bool { return r.Backend == "kitty" }

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
	runtime := in.SnapshotVersion == protocol.CompanionSnapshotVersionRuntime
	version := protocol.CompanionSnapshotVersion
	if runtime {
		version = protocol.CompanionSnapshotVersionRuntime
	}
	out := protocol.CompanionSnapshot{
		V:            version,
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
	if runtime {
		candidates = append(candidates, runtimeSessions(in, runs, len(candidates))...)
	}

	sockets := map[string]bool{}
	for _, c := range candidates {
		countAttention(&out.Attention, c.session)
		if c.session.KittySocket != "" {
			sockets[c.session.KittySocket] = true
		}
	}
	out.Kitty.Sockets = sortedKeys(sockets)
	out.ASL.Waiting = projectWaiting(in, runtime)

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
				s := windowSession(osw, tab, parts, win, socket, runs)
				if st, ok := in.States[win.ID]; ok && sameInstance(in.Sockets[win.ID], in.Kitty.Socket) {
					applyState(&s, st)
				}
				if in.SnapshotVersion == protocol.CompanionSnapshotVersionRuntime {
					s.Runtime = kittyRuntime(in.Runtime, win.ID, in.Kitty.Socket)
				}
				s.Attention = protocol.AttentionStates[s.IconState] || tab.NeedsAttention
				out = append(out, candidate{session: s, order: len(out)})
			}
		}
	}
	return out
}

// windowSession is the session one tree window shows before any daemon state
// is laid over it.
func windowSession(osw kitty.OSWindow, tab kitty.Tab, parts palette.TitleParts, win kitty.Window, socket string, runs map[int]string) protocol.CompanionSession {
	vars := win.UserVars
	return protocol.CompanionSession{
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
}

// foreignSessions projects the daemon states recorded against a Kitty instance
// other than the tree's, ordered by socket and window so the order is stable.
// One whose instance's tree is in Others takes its tab and title from it; the
// rest carry state and identity only.
func foreignSessions(in Input, runs map[int]string, base int) []candidate {
	if in.Kitty.Socket == "" {
		return nil
	}
	var ids []int
	for id := range in.States {
		if r, ok := in.Runtime[id]; ok && !r.kitty() {
			continue
		}
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
		needsAttention := false
		if osw, tab, win, ok := kitty.FindWindowLocationByID(in.Others[in.Sockets[id]], id); ok {
			s = windowSession(*osw, *tab, palette.ParseTitleParts(tab.Title), *win, in.Sockets[id], runs)
			needsAttention = tab.NeedsAttention
		}
		applyState(&s, in.States[id])
		if s.Harness == "" && s.Conversation != nil {
			s.Harness = display(s.Conversation.Harness)
		}
		if in.SnapshotVersion == protocol.CompanionSnapshotVersionRuntime {
			s.Runtime = kittyRuntime(in.Runtime, id, in.Sockets[id])
		}
		s.Attention = protocol.AttentionStates[s.IconState] || needsAttention
		out = append(out, candidate{session: s, order: base + len(out)})
	}
	return out
}

// kittyRuntime is the runtime block of a Kitty window, when a runtime session
// of that same Kitty instance holds it.
func kittyRuntime(slots map[int]RuntimeSlot, windowID int, socket string) *protocol.CompanionRuntime {
	r, ok := slots[windowID]
	if !ok || !r.kitty() || !sameInstance(r.Instance, socket) {
		return nil
	}
	return companionRuntime(r)
}

func companionRuntime(r RuntimeSlot) *protocol.CompanionRuntime {
	id := identifier(r.SessionID)
	if id == "" {
		return nil
	}
	return &protocol.CompanionRuntime{
		SessionID:  id,
		Backend:    identifier(r.Backend),
		Instance:   identifier(r.Instance),
		Generation: identifier(r.Generation),
		Pane:       identifier(r.Pane),
	}
}

// runtimeSessions projects the v2-only sessions: daemon state held by a
// runtime session on a backend other than Kitty, ordered by runtime-session
// ID. They are addressed by their runtime block alone; no Kitty field is set,
// because the session has no Kitty identity to report.
func runtimeSessions(in Input, runs map[int]string, base int) []candidate {
	var slots []int
	for slot, r := range in.Runtime {
		if _, ok := in.States[slot]; ok && !r.kitty() && identifier(r.SessionID) != "" {
			slots = append(slots, slot)
		}
	}
	sort.Slice(slots, func(i, j int) bool { return in.Runtime[slots[i]].SessionID < in.Runtime[slots[j]].SessionID })
	out := make([]candidate, 0, len(slots))
	for _, slot := range slots {
		s := protocol.CompanionSession{
			ASLRunID: identifier(runs[slot]),
			Runtime:  companionRuntime(in.Runtime[slot]),
		}
		applyState(&s, in.States[slot])
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

// projectWaiting lists the runs waiting on an answer. A run driven from a
// non-Kitty runtime session is v2-only, and names that session rather than its
// slot, which is no window ID; v2 also names the runtime session of a run
// driven from a claimed Kitty window.
func projectWaiting(in Input, runtime bool) []protocol.CompanionASLWaiting {
	var out []protocol.CompanionASLWaiting
	for _, p := range in.ASLPending {
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
		if r, ok := in.Runtime[p.Target.WindowID]; ok {
			if !r.kitty() {
				if !runtime {
					continue
				}
				w.WindowID = 0
			}
			if runtime {
				w.RuntimeSessionID = identifier(r.SessionID)
			}
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
		if out[i].RuntimeSessionID != out[j].RuntimeSessionID {
			return out[i].RuntimeSessionID < out[j].RuntimeSessionID
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
