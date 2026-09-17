// This file is the shared action controller: the one place that decides what
// happens between an operator choosing a session action and that action
// running.
//
// Both dashboard entry points — the per-row menu and the command palette — used
// to answer that question separately, and they answered it differently. The
// palette confirmed every restart while the menu honored confirm_destructive;
// replacing a profile confirmed nowhere; a confirmed close ran a different code
// path from an unconfirmed one, so one of them recorded an undo entry and the
// other did not. None of those were decisions anybody made twice: they were one
// decision spelled twice and then edited once.
//
// What the controller owns is the whole span between choice and dispatch:
// whether the action needs a profile, whether it needs a confirmation, whether
// the row it was aimed at is still the row it was aimed at, which dispatch a
// completion belongs to, and where the operator goes back to when they cancel.
// What it does NOT own is the doing: every action is an injected Effect the host
// supplies, because an effect closes over the host's own model and this package
// names no host model at all.
//
// The clock is injected for the same reason the effects are. A pending
// interaction records when it started, and a test that had to sleep to observe
// that would be a test of the sleep.

package sessionactions

import (
	"fmt"
	"strings"

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

// Host is the Kitty tab and window the dashboard itself is running in.
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
const maxInFlight = 16

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
// host's completion report. A host that dispatches a lifecycle effect and never
// reports its outcome would hold the tab until the bounded record evicts it;
// both hosts report every completion, including failures and unconfirmed ones,
// which is what keeps a failed operation retryable.
func (s State) lifecycleInFlight(action commands.ID, target Target) bool {
	if !Destructive(action) {
		return false
	}
	tab := target.TabID()
	if tab == 0 {
		return false
	}
	for _, dispatch := range s.inflight {
		if Destructive(dispatch.Action) && dispatch.Target.TabID() == tab {
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
	ReasonLifecycleInFlight = "a close or restart is already running for this session"
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

// SameSession reports whether two readings of a Kitty tab describe the same
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

// Controller is the shared decision path. It is immutable: its one field is the
// seam set, and the mutable half lives in State on the host model. That is what
// makes it safe to hold on a Bubble Tea model that is copied by value on every
// single update.
//
// It holds no clock, and that is a decision rather than an omission. The only
// thing it ever asked one for was a start timestamp on the pending action, put
// there for a dispatch timeout that was never built and read by nothing; the two
// times that ARE load-bearing — the recovery window and the toast deadline —
// belong to Recovery and to the host's own feedback, which carry their own.
type Controller[M any] struct {
	effects Effects[M]
}

// NewController assembles a controller from its effects.
func NewController[M any](effects Effects[M]) Controller[M] {
	return Controller[M]{effects: effects}
}

// Host state is not carried by the controller. The host owns it, because the
// host is what learns it from a topology refresh.

// Begin starts one action against one captured row.
//
// Availability is checked here as well as by the surface that offered the
// action, and that is not redundant: the surface asked when it drew the row, and
// what this asks is whether the answer is still true now that the operator has
// chosen it.
func (c Controller[M]) Begin(m M, action commands.ID, target Target, origin Origin) (M, Step, tea.Cmd) {
	definition, ok := Lookup(action)
	if !ok {
		return m, Step{Kind: StepNone}, nil
	}
	if detail, ok := c.validate(m, action, target, definition); !ok {
		return c.refuse(m, action, target, origin, detail)
	}
	pending := Pending{
		Active: true,
		Action: action,
		Target: target,
		Origin: origin,
	}
	if NeedsProfile(action) {
		pending.Stage = StageProfile
		m = c.store(m, pending)
		return m, Step{Kind: StepProfile, Action: action, Target: target, Origin: origin}, nil
	}
	return c.confirmOrDispatch(m, pending)
}

// ChooseProfile answers the profile selection the controller is waiting on.
//
// Choosing the profile a session already runs under is a no-op rather than a
// restart, case-insensitively, which is the behavior the profile picker has
// always had. It applies to a profile replacement only: launching another
// session under the same profile as the row it was started from is a perfectly
// ordinary thing to ask for.
func (c Controller[M]) ChooseProfile(m M, profile string) (M, Step, tea.Cmd) {
	state := c.effects.State(m)
	pending := state.pending
	if !pending.Active || pending.Stage != StageProfile {
		return m, Step{Kind: StepNone}, nil
	}
	if pending.Action == commands.OverrideProfile &&
		strings.EqualFold(profile, pending.Target.Session.EffectiveProfile()) {
		m = c.clear(m)
		m = c.dismiss(m)
		return m, Step{Kind: StepNone, Action: pending.Action, Target: pending.Target, Origin: pending.Origin}, nil
	}
	definition, ok := Lookup(pending.Action)
	if !ok {
		return m, Step{Kind: StepNone}, nil
	}
	// The picker is a surface the operator can sit in, so the row may have gone
	// or been replaced while it was open. Ask again rather than trusting the
	// answer that opened it.
	if detail, ok := c.validate(m, pending.Action, pending.Target, definition); !ok {
		return c.refuse(m, pending.Action, pending.Target, pending.Origin, detail)
	}
	pending.Profile = profile
	return c.confirmOrDispatch(m, pending)
}

// Confirm answers the confirmation the controller is waiting on.
//
// A confirmation that is not outstanding produces StepNone, which is what makes
// a second yes harmless: the first one cleared the pending action before the
// effect ran, so there is nothing left for the second to dispatch.
func (c Controller[M]) Confirm(m M) (M, Step, tea.Cmd) {
	state := c.effects.State(m)
	pending := state.pending
	if !pending.Active || pending.Stage != StageConfirm {
		return m, Step{Kind: StepNone}, nil
	}
	definition, ok := Lookup(pending.Action)
	if !ok {
		return m, Step{Kind: StepNone}, nil
	}
	if detail, ok := c.validate(m, pending.Action, pending.Target, definition); !ok {
		return c.refuse(m, pending.Action, pending.Target, pending.Origin, detail)
	}
	return c.dispatch(m, pending)
}

// Cancel backs out of the nested interaction the controller is waiting on and
// puts the operator back where they chose the action.
func (c Controller[M]) Cancel(m M) (M, Step) {
	state := c.effects.State(m)
	pending := state.pending
	if !pending.Active {
		return m, Step{Kind: StepNone}
	}
	m = c.clear(m)
	m = c.restore(m, pending)
	return m, Step{
		Kind:   StepCancelled,
		Action: pending.Action,
		Target: pending.Target,
		Origin: pending.Origin,
	}
}

// Complete correlates one reported completion with the dispatch it answers.
//
// A completion whose request is not in flight is not this controller's — an
// undo, a direct key, a reply that has already been reported — and is left to
// the caller to handle, which the false return says.
//
// The completion is a value rather than an error because an action that did not
// fail has not necessarily been seen to work; Completion.Outcome is where that
// distinction is made, once, for every host.
func (c Controller[M]) Complete(m M, request RequestID, completion Completion) (M, Result, bool) {
	if !request.Valid() {
		return m, Result{}, false
	}
	state := c.effects.State(m)
	index := -1
	for i, dispatch := range state.inflight {
		if dispatch.Request == request {
			index = i
			break
		}
	}
	if index < 0 {
		return m, Result{}, false
	}
	dispatch := state.inflight[index]
	remaining := make([]Dispatch, 0, len(state.inflight)-1)
	remaining = append(remaining, state.inflight[:index]...)
	remaining = append(remaining, state.inflight[index+1:]...)
	state.inflight = remaining
	m = c.effects.SetState(m, state)

	result := Result{
		Action:  dispatch.Action,
		Request: dispatch.Request,
		Target:  dispatch.Target,
		Profile: dispatch.Profile,
		Outcome: completion.Outcome(),
	}
	if completion.Err != nil {
		result.Err = completion.Err
		result.Detail = completion.Err.Error()
	}
	return m, result, true
}

// confirmOrDispatch is the last fork before an action runs: a destructive action
// stops at the confirmation when the operator has asked for one, and every other
// action goes straight through.
func (c Controller[M]) confirmOrDispatch(m M, pending Pending) (M, Step, tea.Cmd) {
	if c.confirms(m, pending.Action) {
		prompt, ok := ConfirmPromptFor(pending.Action, pending.Profile)
		if ok {
			pending.Stage = StageConfirm
			m = c.store(m, pending)
			return m, Step{
				Kind:    StepConfirm,
				Action:  pending.Action,
				Target:  pending.Target,
				Profile: pending.Profile,
				Origin:  pending.Origin,
				Prompt:  prompt,
			}, nil
		}
	}
	return c.dispatch(m, pending)
}

// confirms reads the operator's confirm_destructive preference for one action.
func (c Controller[M]) confirms(m M, action commands.ID) bool {
	if !Destructive(action) {
		return false
	}
	env := c.env(m)
	return env.Config != nil && env.Config.Defaults.ConfirmDestructive
}

// dispatch mints a request, records it for correlation, leaves the action
// surfaces, and runs the effect.
//
// The order is the contract. The pending action is cleared and the state is
// written back BEFORE the effect runs, so a second confirmation arriving against
// the model the effect returns finds nothing to dispatch. Dismiss runs before the
// effect too, so an effect that opens a surface of its own — a transcript search,
// an event log — is not immediately closed by the controller's own bookkeeping.
func (c Controller[M]) dispatch(m M, pending Pending) (M, Step, tea.Cmd) {
	run, ok := c.effects.Run[pending.Action]
	if !ok {
		return c.refuse(m, pending.Action, pending.Target, pending.Origin, ReasonNoEffect)
	}
	state := c.effects.State(m)
	state.last++
	request := state.last
	dispatch := Dispatch{
		Action:  pending.Action,
		Request: request,
		Target:  pending.Target,
		Profile: pending.Profile,
	}
	state.pending = Pending{}
	state.inflight = appendInFlight(state.inflight, dispatch)
	m = c.effects.SetState(m, state)
	m = c.dismiss(m)

	m, cmd := run(m, dispatch)
	if c.effects.Stamp != nil {
		cmd = c.effects.Stamp(cmd, request)
	}
	return m, Step{
		Kind:    StepDispatched,
		Action:  dispatch.Action,
		Request: request,
		Target:  dispatch.Target,
		Profile: dispatch.Profile,
		Origin:  pending.Origin,
	}, cmd
}

// refuse abandons an action and returns the operator to the ordinary list with
// the reason. It dismisses rather than restores: a refusal is an answer, and
// putting the operator back in the menu they would only have to leave again
// hides it behind one more keystroke.
func (c Controller[M]) refuse(m M, action commands.ID, target Target, origin Origin, detail string) (M, Step, tea.Cmd) {
	m = c.clear(m)
	m = c.dismiss(m)
	return m, Step{
		Kind:   StepRefused,
		Action: action,
		Target: target,
		Origin: origin,
		Detail: detail,
	}, nil
}

// validate is the pre-dispatch check: the captured row must still be listed,
// must still be the same session, and must still be allowed to run the action.
//
// It reads the LIVE row for the availability verdict and dispatches against the
// CAPTURED one. That split is the whole point: a row that has been locked since
// it was chosen must be refused, and a row that has moved must not be swapped
// for whatever is at its old address.
//
// Every path that reaches dispatch — Begin, ChooseProfile, and Confirm — passes
// through here first, in the same synchronous call, which is why the
// single-flight guard is stated here once instead of at each of them.
func (c Controller[M]) validate(m M, action commands.ID, target Target, definition Definition) (string, bool) {
	// Asked before availability: the operator's own in-flight close or restart
	// is the more useful answer when both apply, because it explains why the
	// row is about to change rather than describing the changed row.
	if c.effects.State(m).lifecycleInFlight(action, target) {
		return ReasonLifecycleInFlight, false
	}
	env := c.env(m)
	if availability := definition.AvailabilityFor(env, target); !availability.Available {
		return availability.Reason, false
	}
	if c.effects.Live == nil {
		return "", true
	}
	live, found := c.effects.Live(m, target)
	if !found {
		return ReasonSessionGone, false
	}
	if !SameSession(target.Session, live) {
		return ReasonSessionReplaced, false
	}
	if availability := definition.AvailabilityFor(env, Capture(live)); !availability.Available {
		return availability.Reason, false
	}
	if _, ok := c.effects.Run[action]; !ok {
		return ReasonNoEffect, false
	}
	return "", true
}

func (c Controller[M]) env(m M) Env {
	if c.effects.Env == nil {
		return Env{}
	}
	return c.effects.Env(m)
}

// dismiss and restore are the two navigation seams read through a nil check, so
// a host that has no surface to leave — the overview, a test driving the
// controller alone — can supply neither.
func (c Controller[M]) dismiss(m M) M {
	if c.effects.Dismiss == nil {
		return m
	}
	return c.effects.Dismiss(m)
}

func (c Controller[M]) restore(m M, pending Pending) M {
	if c.effects.Restore == nil {
		return m
	}
	return c.effects.Restore(m, pending)
}

func (c Controller[M]) store(m M, pending Pending) M {
	state := c.effects.State(m)
	state.pending = pending
	return c.effects.SetState(m, state)
}

func (c Controller[M]) clear(m M) M {
	state := c.effects.State(m)
	state.pending = Pending{}
	return c.effects.SetState(m, state)
}

// appendInFlight records a dispatch, copying rather than appending in place so
// two copies of a value-copied host model cannot write through the same backing
// array, and dropping the oldest entry once the record is full.
func appendInFlight(existing []Dispatch, dispatch Dispatch) []Dispatch {
	start := 0
	if len(existing) >= maxInFlight {
		start = len(existing) - maxInFlight + 1
	}
	out := make([]Dispatch, 0, len(existing)-start+1)
	out = append(out, existing[start:]...)
	return append(out, dispatch)
}
