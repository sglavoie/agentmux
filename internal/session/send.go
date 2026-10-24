package session

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// Sending input to an agent nobody is attached to.
//
// Send is the one place that decides whether a pane may be written to. It
// answers a prompt or enters a prompt for a running agent the way a person at
// its terminal would, and it is held to what that person can see:
//
//   - Text is refused while the agent's screen is asking something, because
//     the characters of a sentence are also the keys that answer a menu. It
//     is written to every other screen its harness's adapter can read,
//     including one the adapter draws no conclusion from: a session that has
//     not had a turn yet, and Copilot at rest, both read that way.
//   - Keys are refused unless the screen is asking something, because a key
//     meant for a prompt that has gone is typed into whatever replaced it.
//   - A screen that cannot be read, or belongs to a harness with no adapter
//     to look for a prompt on it, refuses both.
//
// A caller that has looked for itself passes AnyScreen.
//
// Send never answers an agent-session-loop supervisor question: that answer
// belongs to the run's mailbox, and the caller refuses before it gets here.

// SendOperationVar is the pane metadata key holding the last send operation
// and how far it got, as "<operation-id>:<pending|sent>".
const SendOperationVar = "amx_send_op"

const (
	sendPending = "pending"
	sendSent    = "sent"
)

// Screen words reported for a send, from what the agent's screen said just
// before it was written to.
const (
	// SendScreenBlocked is a screen asking the operator something.
	SendScreenBlocked = "blocked"
	// SendScreenWorking is a turn in flight.
	SendScreenWorking = "working"
	// SendScreenIdle is a finished turn with an empty composer.
	SendScreenIdle = "idle"
	// SendScreenUnknown is a screen with no prompt showing that says nothing
	// more: a session before its first turn, or a harness whose resting
	// screen has no marker.
	SendScreenUnknown = "unknown"
	// SendScreenUnchecked is a screen the caller asked not to be read.
	SendScreenUnchecked = "unchecked"
)

// Refusal codes. Each is a different thing for the caller to do next.
const (
	// SendRefusedPromptShowing: text was sent while the screen asks something.
	SendRefusedPromptShowing = "prompt_showing"
	// SendRefusedNoPrompt: keys were sent and nothing is being asked.
	SendRefusedNoPrompt = "no_prompt_showing"
	// SendRefusedUnreadable: the screen could not be read, or no adapter
	// knows what a prompt looks like on it.
	SendRefusedUnreadable = "screen_unreadable"
	// SendRefusedOutcomeUnknown: this operation was started before and never
	// seen to finish, so nobody can say whether its input was written.
	SendRefusedOutcomeUnknown = "operation_outcome_unknown"
	// SendRefusedGone: the pane no longer carries the runtime session.
	SendRefusedGone = "not_found"
)

// SendRefusal is a send that was decided against. Nothing was written.
type SendRefusal struct {
	Code    string
	Message string
}

func (e *SendRefusal) Error() string { return e.Message }

// SendTerminal is what sending needs from a backend.
type SendTerminal interface {
	backend.Discoverer
	backend.Observer
	backend.MetadataWriter
	backend.Typist
}

// SendRequest is one input for one live runtime session.
type SendRequest struct {
	// Record is the session's registry record, already validated as live on
	// the backend that Send is given.
	Record runtimesession.Record
	Input  backend.Input
	// OperationID names this send, so asking for it twice writes it once.
	OperationID string
	// AnyScreen skips the screen rules above.
	AnyScreen bool
	// LockPath is the file whose sidecar lock serializes sends.
	LockPath string
	// Now is when the send is made.
	Now time.Time
	// Classify reads a screen's state for a harness, and reports false when
	// the harness has no screen adapter; nil uses the registered adapters.
	Classify func(harnessName, screen string) (screenstate.State, bool)
}

// SendOutcome is what a send did.
type SendOutcome struct {
	// Screen is one of the SendScreen words.
	Screen string
	// Written is true once the input was written to the pane by this call.
	Written bool
	// Replayed is true when this operation had already been sent, so nothing
	// was written again.
	Replayed bool
}

var sendOperationID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidSendOperationID reports whether id can name a send.
func ValidSendOperationID(id string) bool { return sendOperationID.MatchString(id) }

// Send writes req.Input to the session's pane, once per operation ID. An
// operation is recorded on the pane before its input is written and again
// after, so a repeat of one that finished is answered without writing, and a
// repeat of one that did not is refused rather than risk a second copy.
func Send(term SendTerminal, req SendRequest) (SendOutcome, error) {
	if err := req.Input.Validate(); err != nil {
		return SendOutcome{}, err
	}
	if !ValidSendOperationID(req.OperationID) {
		return SendOutcome{}, fmt.Errorf("%w: operation ID must be 1 to 64 letters, digits, '.', '_' or '-'", backend.ErrInvalidInput)
	}
	if req.Record.Target == nil {
		return SendOutcome{}, &SendRefusal{Code: SendRefusedGone, Message: fmt.Sprintf("runtime session %s has no terminal", req.Record.ID)}
	}
	var outcome SendOutcome
	err := cachefile.WithExclusive(req.LockPath, func() error {
		var err error
		outcome, err = send(term, req)
		return err
	})
	return outcome, err
}

func send(term SendTerminal, req SendRequest) (SendOutcome, error) {
	ref := backend.PaneID(req.Record.Target.Ref)
	panes, err := term.Discover()
	if err != nil {
		return SendOutcome{}, err
	}
	// The pane is read again here, under the lock, so the tag checked is the
	// one in force when the input is written.
	pane, ok := backend.FindPane(panes, ref)
	if !ok || pane.Exit != nil || pane.Vars[RuntimeSessionVar] != string(req.Record.ID) {
		return SendOutcome{}, &SendRefusal{Code: SendRefusedGone, Message: fmt.Sprintf("no running agent of runtime session %s", req.Record.ID)}
	}
	switch pane.Vars[SendOperationVar] {
	case req.OperationID + ":" + sendSent:
		return SendOutcome{Screen: SendScreenUnchecked, Replayed: true}, nil
	case req.OperationID + ":" + sendPending:
		return SendOutcome{}, &SendRefusal{Code: SendRefusedOutcomeUnknown, Message: fmt.Sprintf(
			"operation %s was started and never seen to finish; look at the agent before sending again under a new operation ID", req.OperationID)}
	}
	outcome := SendOutcome{Screen: SendScreenUnchecked}
	if !req.AnyScreen {
		if outcome.Screen, err = sendScreen(term, req, pane); err != nil {
			return SendOutcome{}, err
		}
	}
	if err := term.SetMetadata(ref, map[string]string{SendOperationVar: req.OperationID + ":" + sendPending}); err != nil {
		return SendOutcome{}, fmt.Errorf("recording operation %s: %w", req.OperationID, err)
	}
	if err := term.Send(ref, req.Record.Target.Instance.Generation, req.Input); err != nil {
		if notWritten(err) {
			// Nothing reached the pane, so the operation is not unknown: the
			// mark goes back to what it was and a repeat may write it.
			_ = term.SetMetadata(ref, map[string]string{SendOperationVar: pane.Vars[SendOperationVar]})
		}
		return outcome, err
	}
	outcome.Written = true
	if err := term.SetMetadata(ref, map[string]string{SendOperationVar: req.OperationID + ":" + sendSent}); err != nil {
		return outcome, fmt.Errorf("the input was written, but recording operation %s as sent failed: %w", req.OperationID, err)
	}
	return outcome, nil
}

// notWritten reports a failed write that certainly wrote nothing: the input
// was refused before reaching the pane, or the client that would have written
// it was never started.
func notWritten(err error) bool {
	return errors.Is(err, backend.ErrStaleGeneration) || errors.Is(err, backend.ErrInvalidInput) ||
		errors.Is(err, backend.ErrInvalidRef) || errors.Is(err, tmux.ErrClientQueueTimeout)
}

// SendLook is what an agent's screen shows to someone about to send to it:
// the word a send would report for it, and its last rows.
type SendLook struct {
	// Screen is one of the SendScreen words, never SendScreenUnchecked.
	Screen string
	// Lines is the tail of the screen, without the blank rows below the
	// program's last one.
	Lines []string
}

// SendLookLines is how many rows of a screen Look returns.
const SendLookLines = 24

// Look reads the screen a send to req.Record would be judged against, and
// writes nothing. It is what lets a surface put a prompt in front of the
// operator before a key is pressed. Only Record, Now, and Classify are read.
//
// A screen that cannot be read is refused as a send to it would be; one no
// adapter knows is returned as SendScreenUnknown, since it can still be
// looked at.
func Look(term SendTerminal, req SendRequest) (SendLook, error) {
	if req.Record.Target == nil {
		return SendLook{}, &SendRefusal{Code: SendRefusedGone, Message: fmt.Sprintf("runtime session %s has no terminal", req.Record.ID)}
	}
	panes, err := term.Discover()
	if err != nil {
		return SendLook{}, err
	}
	pane, ok := backend.FindPane(panes, backend.PaneID(req.Record.Target.Ref))
	if !ok || pane.Exit != nil || pane.Vars[RuntimeSessionVar] != string(req.Record.ID) {
		return SendLook{}, &SendRefusal{Code: SendRefusedGone, Message: fmt.Sprintf("no running agent of runtime session %s", req.Record.ID)}
	}
	text, state, recognized, err := readSendScreen(term, req, pane)
	if err != nil {
		return SendLook{}, err
	}
	if !recognized {
		state = screenstate.Unknown
	}
	lines := strings.Split(strings.TrimRight(text, " \n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	if len(lines) > SendLookLines {
		lines = lines[len(lines)-SendLookLines:]
	}
	return SendLook{Screen: sendScreenWord(state), Lines: lines}, nil
}

// readSendScreen reads the pane's screen fresh and classifies it. The last
// result is false when the session's harness has no screen adapter.
func readSendScreen(term SendTerminal, req SendRequest, pane backend.Pane) (string, screenstate.State, bool, error) {
	screen, err := term.Observe(pane.Ref)
	text, readErr := probes.FreshScreenText(screen, err, probes.ExpectScreenOf(*req.Record.Target), req.Now)
	if readErr != nil {
		var withheld *probes.ScreenError
		verdict := probes.ScreenUnavailable
		if errors.As(readErr, &withheld) {
			verdict = withheld.Verdict
		}
		return "", screenstate.Unknown, false, &SendRefusal{Code: SendRefusedUnreadable, Message: fmt.Sprintf(
			"the agent's screen could not be read (%s), so there is no telling what the input would land on; --any-screen sends anyway", verdict)}
	}
	harnessName := ""
	if sessions := RunningSessionsFromPanes(req.Record.Target.Instance.Kind, []backend.Pane{pane}, nil); len(sessions) == 1 {
		harnessName = sessions[0].Harness
	}
	classify := req.Classify
	if classify == nil {
		classify = classifySendScreen
	}
	state, recognized := classify(harnessName, text)
	return text, state, recognized, nil
}

func sendScreenWord(state screenstate.State) string {
	switch state {
	case screenstate.Blocked:
		return SendScreenBlocked
	case screenstate.Working:
		return SendScreenWorking
	case screenstate.Terminal:
		return SendScreenIdle
	default:
		return SendScreenUnknown
	}
}

// sendScreen reads the pane's screen and applies the screen rules, returning
// the word for what it saw.
func sendScreen(term SendTerminal, req SendRequest, pane backend.Pane) (string, error) {
	_, state, recognized, err := readSendScreen(term, req, pane)
	if err != nil {
		return "", err
	}
	isText := req.Input.Text != ""
	switch {
	case !recognized:
		return "", &SendRefusal{Code: SendRefusedUnreadable, Message: "nothing here knows what a prompt looks like on this agent's screen, so there is no telling what the input would land on; --any-screen sends anyway"}
	case isText && state == screenstate.Blocked:
		return "", &SendRefusal{Code: SendRefusedPromptShowing, Message: "the agent is asking something, and text typed there would be read as its answer; answer with --key, or pass --any-screen"}
	case !isText && state != screenstate.Blocked:
		return "", &SendRefusal{Code: SendRefusedNoPrompt, Message: "the agent is not asking anything, so the keys would be typed into its prompt box; pass --any-screen to press them anyway"}
	}
	return sendScreenWord(state), nil
}

func classifySendScreen(harnessName, screen string) (screenstate.State, bool) {
	adapter, ok := screenstate.For(harnessName)
	if !ok {
		return screenstate.Unknown, false
	}
	return adapter.Classify(screen).State, true
}
