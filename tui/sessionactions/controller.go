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
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
)

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
	return config.WorkspaceConfirmation(env.Config != nil && env.Config.Defaults.ConfirmDestructive)
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
// array, and dropping the oldest EVICTABLE entry once the record is full.
//
// Which entry is dropped is the whole of this function. Dropping the oldest
// outright is what the record used to do, and it was wrong in a way a leak guard
// is not allowed to be: the oldest entry is the likeliest to be an unfinished
// close or restart, and dropping it released that tab's exclusion and threw away
// the correlation its completion would have arrived under — so sixteen unrelated
// keystrokes turned a refused second restart into a second running one, and the
// original result into a completion nobody claimed. The guard entries are
// therefore skipped over and the oldest ordinary dispatch goes instead, and when
// every entry is a guard the record is allowed to grow past the bound: it can
// only do so one entry per live tab, which is a smaller number than any leak the
// bound was defending against.
func appendInFlight(existing []Dispatch, dispatch Dispatch) []Dispatch {
	out := make([]Dispatch, 0, len(existing)+1)
	out = append(out, existing...)
	for len(out) >= maxInFlight {
		evict := -1
		for i, recorded := range out {
			if !recorded.holdsLifecycleGuard() {
				evict = i
				break
			}
		}
		if evict < 0 {
			break
		}
		out = append(out[:evict], out[evict+1:]...)
	}
	return append(out, dispatch)
}
