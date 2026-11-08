package sessionactions

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// Surface names the dashboard entry point an action was chosen from. It is what
// a cancellation returns to, which is the only thing about the origin the
// controller itself has an opinion on.
type Surface string

const (
	// SurfaceNone is an action chosen from neither entry point — a direct
	// dashboard key, or a test. Cancelling returns to the session list.
	SurfaceNone Surface = ""
	// SurfaceMenu is the per-row actions menu opened with `a`.
	SurfaceMenu Surface = "menu"
	// SurfacePalette is the command palette opened with `:`.
	SurfacePalette Surface = "palette"
)

// Origin is where the operator was when they chose the action, captured so a
// cancellation can put them back there.
//
// It is deliberately a value held by the controller rather than the host's live
// mode state. The two hosts both tear their surface down before an action runs —
// the palette clears its query, the menu closes — so by the time a nested
// confirmation is cancelled there is nothing left in the model to return to.
// Storing the return context separately from the transient mode is what makes
// "cancel puts me back where I was" expressible at all.
type Origin struct {
	// Surface is the entry point to restore.
	Surface Surface
	// MenuCursor and MenuOffset are the per-row menu's selection and scroll
	// position.
	//
	// The cursor is what a cancellation actually has to restore: an operator who
	// walked down to Close, thought better of it, and pressed n should find the
	// cursor still on Close rather than back on the entry the menu opened with.
	// The offset is restored alongside it and then re-derived from the cursor by
	// the host, so a terminal resized while the confirmation was up cannot put
	// the selection outside the window it comes back to.
	MenuCursor int
	MenuOffset int
	// PaletteQuery, PaletteCommand, PaletteCursor, and PaletteOffset are the
	// palette's typed query and the row it was sitting on.
	//
	// The row is recorded twice on purpose, and the identity is the one that
	// matters. A palette restored from its query re-runs the match against a
	// model that has moved on — the dashboard refreshed, a session went idle, a
	// command became visible — so the index the operator was on can now name a
	// different command. PaletteCommand is what the host looks for first;
	// PaletteCursor is the fallback for a command that the recomputed results no
	// longer contain at all, which is the one case where the position is the best
	// remaining answer.
	PaletteQuery   string
	PaletteCommand commands.ID
	PaletteCursor  int
	PaletteOffset  int
}

// Host is the session tab and window IDs the dashboard itself is running in.
//
// It is tracked separately from every selection the dashboard makes because it
// answers a different question: not "which row is the operator acting on" but
// "which row is the operator acting FROM". The two coincide often enough to be
// confused — an operator restarting the session in the tab beside the dashboard
// is the ordinary case — and the consequence of confusing them is a surface that
// cannot warn that it is about to close itself.
//
// It is also independent of the source window a dashboard was launched from:
// that value is consumed by the first refresh that can use it and then cleared,
// and it is reassigned by a cross-window selection handoff, so it names the tab
// the dashboard should SELECT rather than the tab it LIVES in.
type Host struct {
	TabID    int
	WindowID int
}

// Hosts reports whether target names the tab the dashboard itself lives in, so
// a surface can say that running this action will take the surface with it.
func (h Host) Hosts(target Target) bool {
	if !target.HasSession || h.TabID <= 0 {
		return false
	}
	return target.TabID() == h.TabID
}

// Stage is how far one chosen action has got.
type Stage string

const (
	// StageIdle is "no action is pending".
	StageIdle Stage = ""
	// StageProfile means the controller is waiting for a replacement profile.
	StageProfile Stage = "profile"
	// StageConfirm means the controller is waiting for a yes or a no.
	StageConfirm Stage = "confirm"
)

// Pending is the action the controller is part-way through. It is a value with
// an explicit Active flag rather than a pointer, because the host model is
// copied by value on every update and a pointer would alias the copies.
type Pending struct {
	Active bool
	Stage  Stage
	// Action is the identity the operator chose.
	Action commands.ID
	// Target is the row captured when they chose it. It is never re-read: an
	// action aimed at a row that has since moved is refused, not retargeted.
	Target Target
	// Profile is the replacement chosen at StageProfile, empty before that.
	Profile string
	// Origin is where to go back to on cancellation.
	Origin Origin
}

// Dispatch is one action handed to its effect: what to do, to which row, under
// which profile, and under which request identity.
type Dispatch struct {
	Action  commands.ID
	Request RequestID
	Target  Target
	Profile string
}

// Effect is what an action DOES in the host model. It is injected rather than
// declared here because every one of them closes over the host's own client,
// configuration, toasts, and modes.
type Effect[M any] func(M, Dispatch) (M, tea.Cmd)

// Effects is everything the controller needs from its host. Each field is a
// seam a test replaces rather than a behavior this package implements.
//
// State and SetState are the two that must be supplied: they are where the
// controller's own state lives, so there is no sensible default for them and a
// nil one panics rather than degrading. Every other field is read through a nil
// check, because a host with no surface to leave and nothing to stamp is a real
// host — the overview is one — and not merely a test fixture.
type Effects[M any] struct {
	// Env projects the live host state every availability rule reads.
	Env func(M) Env
	// Live resolves the row the captured target names, as the host currently
	// sees it. A target the host can no longer find is refused.
	Live func(M, Target) (session.RunningSession, bool)
	// Dismiss leaves the action surfaces for the ordinary session list. It runs
	// before the effect so an effect that opens a surface of its own wins.
	Dismiss func(M) M
	// Restore puts the operator back where the cancelled action was chosen.
	Restore func(M, Pending) M
	// Stamp marks a dispatched action's command with its request identity, so
	// the completion it eventually reports can be correlated with the dispatch.
	// A host with nothing to stamp may leave it nil.
	Stamp func(tea.Cmd, RequestID) tea.Cmd
	// Run is what each action does, keyed by identity. An action with no effect
	// is refused rather than silently dropped.
	Run map[commands.ID]Effect[M]
	// State reads and SetState writes the controller's own state on the host
	// model. They exist because the host model is copied by value: the
	// controller cannot hold mutable state of its own without that state being
	// stale in whichever copy the host goes on to use.
	State    func(M) State
	SetState func(M, State) M
}

// State is the controller's mutable state, held by the host model. The zero
// value is a controller with nothing pending and nothing in flight.
type State struct {
	pending  Pending
	last     RequestID
	inflight []Dispatch
}

// Pending reports the action the controller is part-way through.
func (s State) Pending() Pending { return s.pending }

// Busy reports whether an action is waiting on the operator.
func (s State) Busy() bool { return s.pending.Active }

// InFlight reports the dispatches that have not yet reported a completion, in
// dispatch order. The slice is a copy: the state is carried on a value-copied
// host model and a shared backing array would be written through by every copy.
func (s State) InFlight() []Dispatch {
	return append([]Dispatch(nil), s.inflight...)
}

// maxInFlight bounds the correlation record. Actions that report a completion
// are close and restart, one operator keystroke at a time; the bound exists so
// an effect that never reports back cannot grow the state without limit, not
// because the ordinary case comes anywhere near it.
//
// It is a bound on the EVICTABLE half of the record only. An entry that holds a
// tab's lifecycle guard is never dropped to make room, because dropping it would
// permit the second close or restart the guard exists to refuse — a silent double
// dispatch bought with a smaller slice. The exemption is self-bounding rather
// than unbounded: the guard is single-flight per tab, so the retained entries can
// never outnumber the live tabs, and every one of them is retired by the
// completion its host reports.
const maxInFlight = 16

// holdsLifecycleGuard reports whether one recorded dispatch is what makes a tab
// single-flight — the exact condition lifecycleInFlight matches on, asked of a
// record rather than of a request.
//
// It is the rule the correlation bound must not overrule: an entry answering true
// here is load-bearing state, not bookkeeping, because its presence is the only
// thing refusing a second lifecycle action against that tab. An unattributed
// dispatch names no tab and therefore guards nothing, so it stays evictable like
// any other.
func (d Dispatch) holdsLifecycleGuard() bool {
	return Destructive(d.Action) && d.Target.TabID() != 0
}

// lifecycleInFlight reports whether a dispatch that ends or replaces a harness
// process already owns this target's tab. It is what makes a lifecycle action
// single-flight per tab: without it, a,r,a,r on one row before the first result
// arrives validates the same unchanged cached row twice and runs Restart twice,
// and since a restart launches the replacement before closing the original, the
// operator ends up with two replacements for one row.
//
// The tab is the unit rather than the window or the session because the tab is
// what a close acts on and what a restart replaces, and because close, restart,
// and profile replacement must exclude EACH OTHER on it, not merely repeats of
// themselves. An unattributed target — no session, so no tab — is never a
// conflict: a zero tab names no tab, and treating two of them as the same one
// would refuse unrelated rows.
//
// The record is retired by Complete, so the guard's release is exactly the
// host's completion report — and it is the ONLY release. Nothing expires it and
// nothing evicts it: appendInFlight makes room among the other entries instead,
// so an operator who dispatches sixteen unrelated actions while a restart is
// parked still finds that restart holding its tab. A host that dispatched a
// lifecycle effect and never reported its outcome would hold that tab for as long
// as it ran, which is the deliberate direction — a held tab refuses with a
// sentence naming the operation that holds it, where a released one runs a second
// restart and leaves two replacements for one row. Both hosts report every
// completion, including failures and unconfirmed ones, which is what keeps a
// failed operation retryable.
func (s State) lifecycleInFlight(action commands.ID, target Target) bool {
	if !Destructive(action) {
		return false
	}
	tab := target.TabID()
	if tab == 0 {
		return false
	}
	for _, dispatch := range s.inflight {
		if dispatch.holdsLifecycleGuard() && dispatch.Target.TabID() == tab {
			return true
		}
	}
	return false
}

// StepKind is what the host must do about one controller call.
type StepKind string

const (
	// StepNone means nothing happened and the host should change nothing. A
	// second confirmation of an already-dispatched action lands here, which is
	// what makes double dispatch impossible rather than merely unlikely.
	StepNone StepKind = ""
	// StepRefused means the action will not be attempted. Detail is the reason,
	// in the operator's words.
	StepRefused StepKind = "refused"
	// StepProfile means the host should open its profile picker.
	StepProfile StepKind = "profile"
	// StepConfirm means the host should open its confirmation, using Prompt.
	StepConfirm StepKind = "confirm"
	// StepDispatched means the effect has run and its command is returned
	// beside the step.
	StepDispatched StepKind = "dispatched"
	// StepCancelled means the operator backed out and the host has been
	// restored to Origin.
	StepCancelled StepKind = "cancelled"
)

// Step is what one controller call decided. It names the action and the row so
// a host can render a refusal or a confirmation without consulting its own
// state, which is the state the controller has just told it to change.
type Step struct {
	Kind    StepKind
	Action  commands.ID
	Request RequestID
	Target  Target
	Profile string
	Origin  Origin
	Prompt  ConfirmPrompt
	Detail  string
}

// Refusal reasons the controller adds to the availability vocabulary. They are
// the ways a row that WAS a legal target stops being one between the moment it
// was chosen and the moment the action would run.
const (
	// ReasonSessionGone is a target the host can no longer find at all.
	ReasonSessionGone = "the session is no longer listed"
	// ReasonSessionReplaced is a tab that still exists but now holds a
	// different session. Retargeting it would run the action against a session
	// the operator never chose.
	ReasonSessionReplaced = "the tab now holds a different session"
	// ReasonNoEffect is a declared action the host supplied no effect for,
	// which is a programming error rather than a state a dashboard reaches.
	ReasonNoEffect = "the action is not available here"
	// ReasonLifecycleInFlight is a tab whose harness process is already being
	// ended or replaced. Unlike the three above it is not a way the world moved
	// under the operator: it is what the operator themselves started a keystroke
	// ago and the reason the row is about to change.
	ReasonLifecycleInFlight = "an end-session or restart request is already running for this session"
)

// ConfirmPrompt is the wording a destructive action's confirmation shows: the
// verb the prompt is built around and the sentence explaining what pressing yes
// will do.
//
// The consequences are harness-neutral. They used to name Claude, which was
// wrong on a Codex or Copilot row and had been wrong since the second harness
// landed; a sentence that names the wrong program is worse than one that names
// none, because an operator has no way to tell it is describing something else.
type ConfirmPrompt struct {
	Verb        string
	Consequence string
}

// Confirmation verbs. They are the values the host's own confirm renderer
// switches on, so they are spelled here once rather than at each call site.
const (
	ConfirmClose          = "close"
	ConfirmRestart        = "restart"
	ConfirmReplaceProfile = "restart with profile"
)

// Consequence sentences for the two profile-independent prompts.
const (
	closeConsequence   = "The harness process exits; its transcript is preserved."
	restartConsequence = "The harness process restarts; its transcript is preserved and is resumed when the profile is configured to."
)

// Destructive reports whether an action ends or replaces a live harness process
// and therefore honors confirm_destructive. It is one answer read by both
// surfaces, which is the point: a restart reached by name and a restart reached
// by mnemonic are the same act.
//
// It is also the single-flight set: exactly the actions that end or replace a
// process are the ones at most one of which may be in flight against a tab. The
// two questions have the same answer because they are the same property — an
// action worth confirming is one whose effect the next keystroke cannot take
// back — so they are spelled once rather than as two tables that could drift.
//
// Launching another session is deliberately absent. It needs a profile, so it
// passes through the same picker a profile replacement does, but it ends
// nothing: the session it launches is new and the row it was started from is
// untouched. It is therefore neither confirmed nor single-flighted: two of them
// in a row are two deliberate launches, not a double dispatch.
func Destructive(id commands.ID) bool {
	switch id {
	case commands.CloseSession, commands.RestartSession, commands.OverrideProfile:
		return true
	}
	return false
}

// NeedsProfile reports whether an action must be told which profile to use
// before it can run.
func NeedsProfile(id commands.ID) bool {
	switch id {
	case commands.OverrideProfile, commands.LaunchAnother:
		return true
	}
	return false
}

// ConfirmPromptFor is the wording one destructive action confirms with. profile
// is the replacement already chosen, which is why a profile replacement can name
// it: the picker comes before the confirmation, so by the time the prompt is
// built there is a concrete answer to confirm rather than a decision still to
// make.
func ConfirmPromptFor(id commands.ID, profile string) (ConfirmPrompt, bool) {
	switch id {
	case commands.CloseSession:
		return ConfirmPrompt{Verb: ConfirmClose, Consequence: closeConsequence}, true
	case commands.RestartSession:
		return ConfirmPrompt{Verb: ConfirmRestart, Consequence: restartConsequence}, true
	case commands.OverrideProfile:
		return ConfirmPrompt{
			Verb:        ConfirmReplaceProfile,
			Consequence: fmt.Sprintf("The harness process restarts under profile %q; its transcript is preserved and is resumed when the profile is configured to.", profile),
		}, true
	}
	return ConfirmPrompt{}, false
}

// SameSession reports whether two readings of a session describe the same
// conversation.
//
// It is deliberately conservative in both directions. Identity evidence is used
// where it exists — a launch identity or a harness session identity present on
// both sides must agree — and ignored where it does not, because a row seen
// before the daemon has attributed it carries neither and refusing every such
// row would make the dashboard unusable during its first seconds. What is always
// required is the tab, because the tab is what every action addresses.
func SameSession(a, b session.RunningSession) bool {
	if a.TabID != b.TabID {
		return false
	}
	if a.WindowID != 0 && b.WindowID != 0 && a.WindowID != b.WindowID {
		return false
	}
	if a.LaunchID != "" && b.LaunchID != "" && a.LaunchID != b.LaunchID {
		return false
	}
	if a.HarnessSessionID != "" && b.HarnessSessionID != "" && a.HarnessSessionID != b.HarnessSessionID {
		return false
	}
	return true
}
