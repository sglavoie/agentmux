package companion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// Input is everything one projection reads. Every field is a value the caller
// owns outright; Project neither retains nor mutates any of it.
type Input struct {
	Daemon protocol.CompanionDaemon
	// States is the daemon's visible state keyed by its reducer slot.
	States       map[int]protocol.WindowStateSnapshot
	Notification Notification
	// ASLTargets are the registered supervisor poll targets, which say which
	// run a window is driving; ASLPending is the subset currently waiting on an
	// answer (asl.SupervisorRegistry.Targets and Pending).
	ASLTargets []asl.SupervisorTarget
	ASLPending []asl.PendingQuestion
	// Runtime names the tmux runtime session bound to each slot, keyed like
	// States.
	Runtime map[int]RuntimeSlot
	// HarnessIcons is the configured harness emojis, longest first: the
	// badges a session's title may carry.
	HarnessIcons []string
	// Sort is the configured sort mode, "alpha" or "account": the order a
	// snapshot lists its sessions in (session.Order).
	Sort string
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
	// Project and Profile label a session; empty until the daemon reads them.
	Project string
	Profile string
	// Harness is the harness such a session was launched with, which names
	// it until a hook names its conversation. Empty until the daemon has
	// read it.
	Harness string
	// Color is the palette color such a session is shown in: the one picked
	// for it, else its profile's. Empty, or a name the palette lacks, is no
	// color.
	Color string
	// Name is the name the operator gave such a session, empty for none.
	Name string
	// Title is the title of such a session's group, empty until the daemon
	// has read it. Only the harness icon it carries is published.
	Title string
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
// title. Identifiers are never clamped — a shortened session ID names
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
	// entry is what the session order reads of the session.
	entry session.OrderEntry
}

// Project builds the companion snapshot for one moment of daemon state.
func Project(in Input) protocol.CompanionSnapshot {
	out := protocol.CompanionSnapshot{
		V:            protocol.CompanionSnapshotVersion,
		Daemon:       in.Daemon,
		Notification: projectNotification(in.Notification, in.Now),
	}
	runs := runsByWindow(in.ASLTargets, in.ASLPending)
	candidates := runtimeSessions(in, runs)
	session.Order(candidates, in.Sort, func(c candidate) session.OrderEntry { return c.entry })
	for i := range candidates {
		candidates[i].order = i
	}

	for _, c := range candidates {
		countAttention(&out.Attention, c.session)
	}
	out.ASL.Waiting = projectWaiting(in)

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

// harnessIcon is the configured badge carried by a session's group title.
func harnessIcon(title string, icons []string) string {
	base := palette.ParseTitleParts(title).Base
	for _, icon := range icons {
		if icon != "" && strings.HasPrefix(base, icon) {
			return icon
		}
	}
	return ""
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

// runtimeSessions projects tmux-backed daemon state by runtime-session ID.
func runtimeSessions(in Input, runs map[int]string) []candidate {
	var slots []int
	for slot, r := range in.Runtime {
		if _, ok := in.States[slot]; ok && r.Backend == "tmux" && identifier(r.SessionID) != "" {
			slots = append(slots, slot)
		}
	}
	sort.Slice(slots, func(i, j int) bool { return in.Runtime[slots[i]].SessionID < in.Runtime[slots[j]].SessionID })
	out := make([]candidate, 0, len(slots))
	for _, slot := range slots {
		s := protocol.CompanionSession{
			Harness:     display(in.Runtime[slot].Harness),
			Profile:     display(in.Runtime[slot].Profile),
			TabColor:    tabColor(in.Runtime[slot].Color),
			Name:        display(in.Runtime[slot].Name),
			HarnessIcon: harnessIcon(in.Runtime[slot].Title, in.HarnessIcons),
			Pinned:      palette.ParseTitleParts(in.Runtime[slot].Title).Starred,
			Project:     display(in.Runtime[slot].Project),
			ASLRunID:    identifier(runs[slot]),
			Runtime:     companionRuntime(in.Runtime[slot]),
		}
		applyState(&s, in.States[slot])
		s.Muted = in.States[slot].Muted
		if s.Conversation != nil {
			s.Harness = display(s.Conversation.Harness)
		}
		s.Attention = protocol.AttentionStates[s.IconState]
		out = append(out, candidate{
			session: s, order: len(out),
			entry: runtimeOrderEntry(in.Runtime[slot]),
		})
	}
	return out
}

// orderEntry is what the session order reads of a profile and group title.
func orderEntry(profile, title string) session.OrderEntry {
	parts := palette.ParseTitleParts(title)
	return session.OrderEntry{Profile: profile, Base: parts.Base, Starred: parts.Starred}
}

// runtimeOrderEntry is orderEntry for a session. Until the daemon
// has read its group's title it is placed by its project, which is what the
// title is built around. Its runtime-session ID breaks ties, as it does for
// the same session in session.SortRunning.
func runtimeOrderEntry(r RuntimeSlot) session.OrderEntry {
	entry := session.OrderEntry{Profile: r.Profile, Base: r.Project}
	if r.Title != "" {
		entry = orderEntry(r.Profile, r.Title)
	}
	entry.Tie = r.SessionID
	return entry
}

// tabColor is the named palette color's bright background, which black text
// reads on; empty for no color.
func tabColor(name string) string {
	color, err := palette.Lookup(name)
	if err != nil {
		return ""
	}
	return color.BrightBg
}

// applyState lays the daemon's state over a session. The daemon's icon wins
// over the title's: the title is painted from it and may simply lag.
func applyState(s *protocol.CompanionSession, st protocol.WindowStateSnapshot) {
	if st.StatusEvidence != nil {
		evidence := *st.StatusEvidence
		s.StatusEvidence = &evidence
	}
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

// runsByWindow names the run each slot drives. A slot with a waiting run
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

// projectWaiting lists the runs waiting on an answer by runtime session.
func projectWaiting(in Input) []protocol.CompanionASLWaiting {
	var out []protocol.CompanionASLWaiting
	for _, p := range in.ASLPending {
		run := identifier(p.Target.Candidate.RunID)
		if run == "" || p.Projection.UnansweredQuestions <= 0 {
			continue
		}
		r, ok := in.Runtime[p.Target.WindowID]
		if !ok || r.Backend != "tmux" || identifier(r.SessionID) == "" {
			continue
		}
		w := protocol.CompanionASLWaiting{
			RunID:            run,
			PlanDir:          identifier(p.Target.Candidate.PlanDir),
			RuntimeSessionID: identifier(r.SessionID),
			Unanswered:       p.Projection.UnansweredQuestions,
			LatestQuestionID: identifier(p.Projection.LatestQuestionID),
		}
		if !p.Projection.LatestQuestionCreatedAt.IsZero() {
			w.LatestQuestionCreatedAt = p.Projection.LatestQuestionCreatedAt.Unix()
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
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
