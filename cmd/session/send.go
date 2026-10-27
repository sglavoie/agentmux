package session

// `amx send` answers or prompts a running agent without attaching to it. It
// is the at-the-desk way to clear a permission prompt or hand an idle agent
// its next prompt, and it works on what the agent's own terminal shows.
//
// It is not a way to answer an agent-session-loop supervisor question. That
// answer is the run's, travels by its mailbox, and is given from the Runs
// view, the app's answer panel, or the Workbench; a session the daemon
// reports as having such a question open is refused here whatever flags are
// passed. Only the daemon can say so: when it does not answer, the send is
// refused unless --yes is passed, and with --yes it goes ahead without that
// check.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
)

var (
	sendJSON          bool
	sendStdin         bool
	sendKeys          []string
	sendNoEnter       bool
	sendAnyScreen     bool
	sendYes           bool
	sendOperationID   string
	sendWait          time.Duration
	sendLook          bool
	sendSearchContext bool
)

// The seams the tests replace: the screen classifier, and the pause between
// two looks at the daemon while waiting.
var (
	sendClassify func(harnessName, screen string) (screenstate.State, bool)
	sendPause    = time.Sleep
)

var sendCmd = &cobra.Command{
	Use:               "send RUNTIME_SESSION_ID [TEXT]",
	ValidArgsFunction: cli.CompleteRuntimeSessionFirstArg,
	Short:             "Answer or prompt a running agent without attaching",
	Long: `Writes input to the agent a runtime session runs in Agent Mux's private
tmux server, with no terminal attached and without taking the session from
whoever has it.

TEXT (or standard input, with --stdin) is pasted as one prompt and entered;
--no-enter leaves it typed and unentered. --key presses keys instead, in the
order given: ` + strings.Join(backend.KeyNames(), ", ") + `.

What is sent has to fit what the agent's screen shows, read just before:

  text  is refused while the agent is asking something (prompt_showing),
        since a sentence typed into a menu answers it;
  keys  are refused unless the agent is asking something (no_prompt_showing);
  both  are refused when the screen cannot be read, or its harness has no
        screen adapter to look for a prompt with (screen_unreadable).

--any-screen sends without reading the screen.

A session running an agent-session-loop run needs --yes, since what is typed
there reaches the run's supervisor. One with a supervisor question open is
always refused (asl_question_open): answer it from the Runs view, the app, or
the Workbench, so the run records the reply. When the daemon cannot be asked,
--yes is needed too, and sends without knowing whether a question is open.

--operation-id names the send so that asking twice writes once: a repeat of a
finished operation reports delivery "replayed", and a repeat of one that was
never seen to finish is refused (operation_outcome_unknown). Only a session's
latest operation is remembered.

Delivery is "accepted" once the input is written. With --wait, a session that
was at rest or asking is watched for that long, and delivery is "consumed" if
its state moves; an agent that was already working gives no such evidence and
stays "accepted". The text itself is never logged or recorded.

--look sends nothing: it prints the last rows of the agent's screen, and with
--json reports {runtime_session_id, backend, project, screen, lines}, where
screen is the word a send would report. It is what a surface shows before a
key is pressed, and it is refused where a send would be for a session that is
gone or a screen that cannot be read.`,
	Example: `  amx send rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f "run the tests again"
  git diff | amx send rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --stdin --no-enter
  amx send rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --key down --key enter
  amx send rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --look --json`,
	Args: cobra.MaximumNArgs(2),
	RunE: runSend,
}

func init() {
	cli.AddJSONFlag(sendCmd, &sendJSON)
	sendCmd.Flags().BoolVar(&sendStdin, "stdin", false, "read the text from standard input")
	sendCmd.Flags().StringArrayVar(&sendKeys, "key", nil, "press this key instead of sending text (repeatable)")
	_ = sendCmd.RegisterFlagCompletionFunc("key", cli.FixedCompletions(backend.KeyNames()...))
	sendCmd.Flags().BoolVar(&sendNoEnter, "no-enter", false, "leave the text typed without entering it")
	sendCmd.Flags().BoolVar(&sendAnyScreen, "any-screen", false, "send without reading the agent's screen first")
	sendCmd.Flags().BoolVar(&sendYes, "yes", false, "confirm sending into a session that runs an agent-session-loop run")
	sendCmd.Flags().StringVar(&sendOperationID, "operation-id", "", "name this send, so a repeat of it writes nothing twice")
	sendCmd.Flags().DurationVar(&sendWait, "wait", 0, "how long to watch for the agent taking the input")
	sendCmd.Flags().BoolVar(&sendLook, "look", false, "send nothing; show what the agent's screen shows")
	sendCmd.Flags().BoolVar(&sendSearchContext, "search-context", false, "with --look, include the active terminal search for conversation search")
}

// SendCommand returns the top-level `amx send` command.
func SendCommand() *cobra.Command { return sendCmd }

// Delivery words, weakest claim first.
const (
	sendAccepted = "accepted"
	sendConsumed = "consumed"
	sendReplayed = "replayed"
)

// sendData is the `send --json` payload. It never carries what was sent.
type sendData struct {
	RuntimeSessionID string `json:"runtime_session_id"`
	Backend          string `json:"backend"`
	Project          string `json:"project"`
	OperationID      string `json:"operation_id"`
	// Input is "text" or "keys".
	Input string `json:"input"`
	// Screen is what the agent's screen said before it was written to:
	// blocked, working, idle, unknown, or unchecked.
	Screen string `json:"screen"`
	// Delivery is accepted, consumed, or replayed.
	Delivery string `json:"delivery"`
}

// sendLookData is the `send --look --json` payload.
type sendLookData struct {
	Search           *tmux.SearchContext `json:"search,omitempty"`
	RuntimeSessionID string              `json:"runtime_session_id"`
	Backend          string              `json:"backend"`
	Project          string              `json:"project"`
	// Screen is blocked, working, idle, or unknown.
	Screen string   `json:"screen"`
	Lines  []string `json:"lines"`
}

// sendTerminal is what sending needs from the tmux backend.
type sendTerminal interface {
	tmuxAttacher
	session.SendTerminal
}

func runSend(cmd *cobra.Command, args []string) error {
	if sendSearchContext && !sendLook {
		return cli.CommandFailure(cmd, sendJSON, "invalid_arguments", errors.New("--search-context requires --look"))
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return cli.MissingParameter(cmd, sendJSON, "runtime-session-id", "the runtime session to send to (rs-...)")
	}
	id, err := cli.ParseRuntimeSessionID(strings.TrimSpace(args[0]))
	if err != nil {
		return cli.CommandFailure(cmd, sendJSON, "invalid_arguments", err)
	}
	var input backend.Input
	if sendLook {
		if len(args) > 1 || sendStdin || len(sendKeys) > 0 {
			return cli.CommandFailure(cmd, sendJSON, "invalid_arguments", errors.New("--look sends nothing; leave out TEXT, --stdin, and --key"))
		}
	} else if input, err = sendInput(cmd, args[1:]); err != nil {
		return cli.CommandFailure(cmd, sendJSON, "invalid_arguments", err)
	}
	operation := strings.TrimSpace(sendOperationID)
	switch {
	case sendLook:
		// A look writes nothing, so there is no operation to name.
	case operation == "":
		if operation, err = newSendOperationID(); err != nil {
			return cli.CommandFailure(cmd, sendJSON, "send_failed", err)
		}
	case !session.ValidSendOperationID(operation):
		return cli.CommandFailure(cmd, sendJSON, "invalid_arguments", errors.New("--operation-id must be 1 to 64 letters, digits, '.', '_' or '-'"))
	}
	store, err := runtimesession.NewStore(attachRegistryPath())
	if err != nil {
		return cli.CommandFailure(cmd, sendJSON, "registry_unavailable", err)
	}
	registry, err := store.Load()
	if err != nil {
		return cli.CommandFailure(cmd, sendJSON, "registry_unavailable", err)
	}
	record, err := registry.Get(id)
	if err != nil {
		return cli.CommandFailure(cmd, sendJSON, "not_found", unknownRuntimeSession(id))
	}
	if record.State != runtimesession.StateLive {
		return cli.CommandFailure(cmd, sendJSON, notLiveCode(record), notLiveError(record))
	}
	if record.Target.Instance.Kind != runtimesession.BackendTmux {
		return cli.CommandFailure(cmd, sendJSON, "unsupported_backend", fmt.Errorf(
			"runtime session %s runs on %s; type in its tab there", id, record.Target.Instance.Kind))
	}
	opened, err := attachNewTmux()
	if err != nil {
		return cli.CommandFailure(cmd, sendJSON, "backend_unavailable", fmt.Errorf("tmux backend unavailable: %w", err))
	}
	term, ok := opened.(sendTerminal)
	if !ok {
		return cli.CommandFailure(cmd, sendJSON, "backend_unavailable", errors.New("tmux backend cannot send input"))
	}
	if _, record, err = validateTmux(store, term, record); err != nil {
		return cli.CommandFailure(cmd, sendJSON, "backend_unavailable", err)
	}
	if record.State != runtimesession.StateLive {
		return cli.CommandFailure(cmd, sendJSON, notLiveCode(record), notLiveError(record))
	}
	if sendLook {
		return runSendLook(cmd, term, record)
	}
	before, known := sendSlotState(id)
	if code, refusal := sendASLGuard(record, before, known, sendYes); refusal != nil {
		return cli.CommandFailure(cmd, sendJSON, code, refusal)
	}
	data := sendData{
		RuntimeSessionID: string(id), Backend: string(record.Target.Instance.Kind), Project: record.Project,
		OperationID: operation, Input: "text", Delivery: sendAccepted,
	}
	if input.Text == "" {
		data.Input = "keys"
	}
	outcome, err := session.Send(term, session.SendRequest{
		Record: record, Input: input, OperationID: operation, AnyScreen: sendAnyScreen,
		LockPath: store.Path() + ".send", Now: attachNow(), Classify: sendClassify,
	})
	data.Screen = outcome.Screen
	var refusal *session.SendRefusal
	switch {
	case errors.As(err, &refusal):
		return cli.CommandFailure(cmd, sendJSON, refusal.Code, refusal)
	case errors.Is(err, backend.ErrInvalidInput):
		return cli.CommandFailure(cmd, sendJSON, "invalid_arguments", err)
	case err != nil && outcome.Written && sendJSON:
		// The input reached the agent; only the record of it did not land.
		return cli.WriteJSONPartial(cmd, data, err)
	case err != nil:
		return cli.CommandFailure(cmd, sendJSON, "send_failed", fmt.Errorf("sending to %s: %w", record.Project, err))
	}
	switch {
	case outcome.Replayed:
		data.Delivery = sendReplayed
	case sendWait > 0 && sendWatchable(before) && sendMoved(id, before.state.IconState, sendWait):
		data.Delivery = sendConsumed
	}
	if sendJSON {
		return cli.WriteJSON(cmd, data)
	}
	message := fmt.Sprintf("Sent to %s.", record.Project)
	switch data.Delivery {
	case sendReplayed:
		message = fmt.Sprintf("Operation %s was already sent to %s; nothing was written again.", operation, record.Project)
	case sendConsumed:
		message = fmt.Sprintf("Sent to %s, and its agent took it.", record.Project)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), message)
	return err
}

// runSendLook reports what the agent's screen shows and writes nothing.
func runSendLook(cmd *cobra.Command, term sendTerminal, record runtimesession.Record) error {
	look, err := session.Look(term, session.SendRequest{Record: record, Now: attachNow(), Classify: sendClassify})
	var refusal *session.SendRefusal
	switch {
	case errors.As(err, &refusal):
		return cli.CommandFailure(cmd, sendJSON, refusal.Code, refusal)
	case err != nil:
		return cli.CommandFailure(cmd, sendJSON, "send_failed", fmt.Errorf("reading the screen of %s: %w", record.Project, err))
	}
	if sendJSON {
		var search *tmux.SearchContext
		if sendSearchContext {
			if reader, ok := term.(interface {
				ReadSearchContext(runtimesession.Record) (tmux.SearchContext, error)
			}); ok {
				value, err := reader.ReadSearchContext(record)
				if err != nil {
					return cli.CommandFailure(cmd, sendJSON, "search_unavailable", err)
				}
				search = &value
			}
		}
		return cli.WriteJSON(cmd, sendLookData{
			Search:           search,
			RuntimeSessionID: string(record.ID), Backend: string(record.Target.Instance.Kind), Project: record.Project,
			Screen: look.Screen, Lines: look.Lines,
		})
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.Join(look.Lines, "\n"))
	return err
}

// sendInput builds the input from the text argument, standard input, or the
// --key flags: exactly one of them.
func sendInput(cmd *cobra.Command, rest []string) (backend.Input, error) {
	sources := 0
	for _, given := range []bool{len(rest) > 0, sendStdin, len(sendKeys) > 0} {
		if given {
			sources++
		}
	}
	if sources != 1 {
		return backend.Input{}, errors.New("give exactly one of TEXT, --stdin, or --key")
	}
	var input backend.Input
	switch {
	case len(sendKeys) > 0:
		if sendNoEnter {
			return backend.Input{}, errors.New("--no-enter applies to text; leave enter out of the keys instead")
		}
		for _, name := range sendKeys {
			key, err := backend.ParseKey(name)
			if err != nil {
				return backend.Input{}, err
			}
			input.Keys = append(input.Keys, key)
		}
	case sendStdin:
		// Room for the limit, a trailing CRLF, and one byte more: input that
		// fills it was cut short, and trimming the newline it was cut at
		// must not let the cut prompt through as a whole one.
		limit := int64(backend.InputMaxBytes + len("\r\n") + 1)
		text, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), limit))
		if err != nil {
			return backend.Input{}, fmt.Errorf("reading standard input: %w", err)
		}
		if int64(len(text)) == limit {
			return backend.Input{}, fmt.Errorf("%w: standard input is more than %d bytes", backend.ErrInvalidInput, backend.InputMaxBytes)
		}
		// What a pipe adds at the end is not part of the prompt.
		input.Text, input.Submit = strings.TrimRight(string(text), "\r\n"), !sendNoEnter
	default:
		input.Text, input.Submit = rest[0], !sendNoEnter
	}
	return input, input.Validate()
}

func newSendOperationID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("naming the operation: %w", err)
	}
	return "send-" + hex.EncodeToString(b[:]), nil
}

// sendSlot is the daemon's state for a runtime session, when it holds any.
type sendSlot struct {
	found bool
	state protocol.WindowStateSnapshot
}

// sendSlotState asks the daemon for the session's state. The second result is
// false when the daemon could not be asked; the daemon is never started for
// this. A session the daemon has not heard from yet has no slot.
func sendSlotState(id runtimesession.ID) (sendSlot, bool) {
	response, err := sessionCommandsDaemon(protocol.Request{Type: protocol.TypeStateQuery})
	if err != nil || !response.OK {
		return sendSlot{}, false
	}
	for slot, bound := range response.RuntimeSlots {
		if bound.RuntimeSessionID == string(id) {
			state, ok := response.States[slot]
			return sendSlot{found: ok, state: state}, true
		}
	}
	return sendSlot{}, true
}

// sendASLGuard keeps typed input out of agent-session-loop's lane. A run
// reaches its session's slot only through that session's own hooks, so a
// session with no slot runs none. With the daemon unreachable there is no
// slot to read, and --yes is the operator taking that on: the send goes ahead
// without the open-question check.
func sendASLGuard(record runtimesession.Record, slot sendSlot, known, yes bool) (string, error) {
	switch {
	case !known && !yes:
		return "daemon_unavailable", fmt.Errorf(
			"the daemon did not answer, so there is no telling whether %s runs an agent-session-loop run; `amx doctor` says why, and --yes sends anyway", record.Project)
	case !known || len(slot.state.ASLRunIDs) == 0:
		return "", nil
	case slot.state.IconState == "question":
		return "asl_question_open", fmt.Errorf(
			"%s has an agent-session-loop question open; answer it from the Runs view, Agent Mux.app, or the Workbench, so the run records the reply", record.Project)
	case !yes:
		return "confirmation_required", fmt.Errorf(
			"%s runs an agent-session-loop run, and what is typed there reaches its supervisor; pass --yes to confirm", record.Project)
	}
	return "", nil
}

// sendWatchable reports whether the session was somewhere it only leaves
// because of input: asking, or at rest. An agent already working moves on its
// own, which proves nothing about what was sent.
func sendWatchable(slot sendSlot) bool {
	if !slot.found {
		return false
	}
	switch slot.state.IconState {
	case "stop", "key", "question", "warning", "error":
		return true
	}
	return false
}

// sendMoved watches the session's state for up to wait and reports whether it
// left the state it was in when the input was written.
func sendMoved(id runtimesession.ID, from string, wait time.Duration) bool {
	const step = 200 * time.Millisecond
	for waited := time.Duration(0); waited < wait; waited += step {
		sendPause(step)
		if slot, known := sendSlotState(id); known && slot.found && slot.state.IconState != from {
			return true
		}
	}
	return false
}
