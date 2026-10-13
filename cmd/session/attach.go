package session

// `amx attach` connects the current terminal to a running agent by its
// runtime-session ID. Attaching never relaunches the agent: it transfers
// interactive ownership (input and size) to this terminal and, when the
// terminal detaches, the agent keeps running.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// The seams the tests replace: the registry file, the backends, the terminal
// check, and running the interactive client.
var (
	attachRegistryPath = runtimesession.DefaultRegistryPath
	attachNewTmux      = func() (backend.TargetLauncher, error) { return cli.NewTmuxBackend() }
	attachNewKitty     = func() kitty.KittyClient { return cli.NewKittyClient() }
	attachIsTerminal   = func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
	attachRunClient    = runAttachedClient
	attachFingerprint  = session.ProcessFingerprint
	attachNow          = time.Now
	attachBinary       = cli.AmxBinary
)

// runAttachedClient runs the interactive client on this terminal.
func runAttachedClient(c *exec.Cmd) error {
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// outliveHangup makes amx survive its terminal closing until the returned
// function is called. A terminal that closes (a desktop view dismissed, a
// window shut, an app that crashed) hangs up its whole foreground group, and
// an embedded terminal keeps hanging it up until the group is gone: the tmux
// client detaches on the first, and amx has to outlive every one of them to
// release the ownership it recorded.
func outliveHangup() (restore func()) {
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)
	return func() { signal.Stop(hangup) }
}

var attachJSON bool

var attachCmd = &cobra.Command{
	Use:   "attach RUNTIME_SESSION_ID",
	Short: "Attach this terminal to a running agent",
	Long: `Attaches this terminal to the agent a runtime session runs, without
restarting it. Attaching takes interactive ownership: any other terminal
attached to the same agent is detached, and the agent's size follows this
terminal. Detaching (closing the terminal, or another attach taking over)
leaves the agent running; ` + "`amx attach`" + ` again picks it back up.

A tmux runtime session is attached through AgentMux's private tmux server. A
Kitty runtime session has its window focused instead.

--json does not attach, since its output is the JSON document: it checks that
the runtime session is live and reports the command a terminal runs to attach.
Runtime-session IDs come from ` + "`amx project launch --backend tmux --json`" + `.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAttach,
}

func init() {
	attachCmd.Flags().BoolVar(&attachJSON, "json", false, "check the session and emit schema-v1 JSON instead of attaching")
}

// AttachCommand returns the top-level `amx attach` command.
func AttachCommand() *cobra.Command { return attachCmd }

// attachData is the `attach --json` payload.
type attachData struct {
	RuntimeSessionID string `json:"runtime_session_id"`
	Backend          string `json:"backend"`
	Project          string `json:"project"`
	State            string `json:"state"`
	Pane             string `json:"pane"`
	PID              int    `json:"pid"`
	// Clients counts the terminals attached to the session right now. One
	// means the session has an interactive owner, which attaching would
	// detach; none means attaching takes nothing from anyone.
	Clients int      `json:"clients"`
	Command []string `json:"command,omitempty"`
}

// attachView is what this terminal shows around the pane. A plain terminal
// has nothing else to say which session it is on, so it gets the session's
// status line; the desktop workspace lists every session beside the pane and
// marks the terminals it runs.
//
// The keys bound in the tmux configuration run amx, and the server's PATH is
// that of whatever started it, so this terminal says where its own amx is. A
// binary `go run` built for itself is gone by the time a key is pressed, and
// is not offered.
func attachView() tmux.AttachView {
	view := tmux.AttachView{StatusLine: !attachHosted()}
	if bin, err := attachBinary(); err == nil && !cli.TransientBinary(bin) {
		view.Amx = bin
	}
	return view
}

// attachHosted reports whether the desktop workspace runs this attach in one
// of its own terminals.
func attachHosted() bool {
	_, hosted := os.LookupEnv(tmux.WorkspaceHostEnv)
	return hosted
}

// tmuxAttacher is what attaching needs from the tmux backend.
type tmuxAttacher interface {
	Survey() (tmux.Survey, error)
	AttachCommand(pane backend.Ref, view tmux.AttachView) (*exec.Cmd, error)
}

func runAttach(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return cli.MissingParameter(cmd, attachJSON, "runtime-session-id", "the runtime session to attach (rs-...)")
	}
	id, err := runtimesession.ParseID(strings.TrimSpace(args[0]))
	if err != nil {
		return cli.CommandFailure(cmd, attachJSON, "invalid_arguments", err)
	}
	store, err := runtimesession.NewStore(attachRegistryPath())
	if err != nil {
		return cli.CommandFailure(cmd, attachJSON, "registry_unavailable", err)
	}
	registry, err := store.Load()
	if err != nil {
		return cli.CommandFailure(cmd, attachJSON, "registry_unavailable", err)
	}
	record, err := registry.Get(id)
	if err != nil {
		return cli.CommandFailure(cmd, attachJSON, "not_found", fmt.Errorf("no runtime session %s", id))
	}
	if record.State != runtimesession.StateLive {
		return cli.CommandFailure(cmd, attachJSON, notLiveCode(record), notLiveError(record))
	}
	data := attachData{
		RuntimeSessionID: string(id), Backend: string(record.Target.Instance.Kind), Project: record.Project,
		State: string(record.State), Pane: record.Target.Ref, PID: record.Process.PID,
	}
	switch record.Target.Instance.Kind {
	case runtimesession.BackendTmux:
		return attachTmux(cmd, store, record, data)
	case runtimesession.BackendKitty:
		return attachKitty(cmd, record, data)
	default:
		return cli.CommandFailure(cmd, attachJSON, "unsupported_backend", fmt.Errorf("runtime session %s runs on unknown backend %q", id, record.Target.Instance.Kind))
	}
}

func attachTmux(cmd *cobra.Command, store *runtimesession.Store, record runtimesession.Record, data attachData) error {
	opened, err := attachNewTmux()
	if err != nil {
		return cli.CommandFailure(cmd, attachJSON, "backend_unavailable", fmt.Errorf("tmux backend unavailable: %w", err))
	}
	server, ok := opened.(tmuxAttacher)
	if !ok {
		return cli.CommandFailure(cmd, attachJSON, "backend_unavailable", errors.New("tmux backend cannot attach terminals"))
	}
	survey, stale, err := validateTmux(store, server, record)
	if err != nil {
		return cli.CommandFailure(cmd, attachJSON, "backend_unavailable", err)
	}
	if stale.State != runtimesession.StateLive {
		return cli.CommandFailure(cmd, attachJSON, notLiveCode(stale), notLiveError(stale))
	}
	data.Clients = len(survey.ClientsOf(backend.PaneID(record.Target.Ref)))
	client, err := server.AttachCommand(backend.PaneID(record.Target.Ref), attachView())
	if err != nil {
		return cli.CommandFailure(cmd, attachJSON, "invalid_target", err)
	}
	if attachJSON {
		data.Command = client.Args
		return cli.WriteJSON(cmd, data)
	}
	if !attachIsTerminal() {
		return errors.New("attach needs a terminal on stdin and stdout; use --json to get the attach command")
	}
	owner, err := newAttachment()
	if err != nil {
		return err
	}
	defer outliveHangup()()
	// Recording ownership first means a view that attached earlier, and
	// detaches late, cannot release this one.
	if err := store.Update(func(registry *runtimesession.Registry) error {
		_, err := registry.Attach(record.ID, owner, attachNow())
		return err
	}); err != nil {
		return fmt.Errorf("taking ownership of %s: %w", record.ID, err)
	}
	runErr := attachRunClient(client)
	detachErr := store.Update(func(registry *runtimesession.Registry) error {
		_, err := registry.Detach(record.ID, owner.ID, attachNow())
		// Another view took ownership, or the session ended, while attached:
		// there is nothing of ours left to release.
		if errors.Is(err, runtimesession.ErrAttachmentMismatch) || errors.Is(err, runtimesession.ErrNotFound) {
			return nil
		}
		return err
	})
	if runErr != nil {
		return fmt.Errorf("tmux client: %w", runErr)
	}
	return detachErr
}

// notLiveCode names why a session cannot be attached. An agent that exited
// and an agent that was lost are different answers: the first ended where it
// ran and left its exit status, the second's terminal is gone and what became
// of it was not seen. Neither is ever relaunched from here.
func notLiveCode(record runtimesession.Record) string {
	if record.State == runtimesession.StatePending {
		return "runtime_session_pending"
	}
	return "runtime_session_" + string(record.Outcome())
}

func notLiveError(record runtimesession.Record) error {
	switch record.Outcome() {
	case runtimesession.OutcomeExited:
		return fmt.Errorf("the agent of runtime session %s is no longer running: %s", record.ID, record.StaleReason)
	case runtimesession.OutcomeLost:
		return fmt.Errorf("runtime session %s was lost (%s); its agent is not running where it was started", record.ID, record.StaleReason)
	}
	return fmt.Errorf("runtime session %s is %s", record.ID, record.State)
}

// validateTmux confirms the registered pane still holds the registered
// process on the same server generation, marking the session stale in the
// registry when it does not, so nothing ever attaches to a reused pane ID. It
// returns the record as it stands afterwards: still live, or stale with why.
func validateTmux(store *runtimesession.Store, server tmuxAttacher, record runtimesession.Record) (tmux.Survey, runtimesession.Record, error) {
	// A server that is not running is surveyed as having no generation, which
	// marks every session registered on it stale.
	survey, err := server.Survey()
	if err != nil {
		return tmux.Survey{}, record, fmt.Errorf("reading tmux server: %w", err)
	}
	if survey.Instance.Address != record.Target.Instance.Address {
		return tmux.Survey{}, record, fmt.Errorf("runtime session %s belongs to tmux server %s, not %s", record.ID, record.Target.Instance.Address, survey.Instance.Address)
	}
	observation := survey.Observation(record, attachFingerprint)
	current := record
	err = store.Update(func(registry *runtimesession.Registry) error {
		validated, err := registry.Validate(record.ID, observation, attachNow())
		if err == nil || errors.Is(err, runtimesession.ErrStaleTarget) || errors.Is(err, runtimesession.ErrInvalidState) {
			current = validated
			return nil
		}
		return err
	})
	return survey, current, err
}

func attachKitty(cmd *cobra.Command, record runtimesession.Record, data attachData) error {
	if attachJSON {
		return cli.WriteJSON(cmd, data)
	}
	if err := kitty.NewBackend(attachNewKitty()).Focus(backend.PaneID(record.Target.Ref)); err != nil {
		return fmt.Errorf("focusing Kitty window %s: %w", record.Target.Ref, err)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Focused %s.\n", record.Project)
	return err
}

func newAttachment() (runtimesession.Attachment, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return runtimesession.Attachment{}, fmt.Errorf("naming attachment: %w", err)
	}
	// The ID says whether the workspace hosts this terminal: the agent's own
	// environment never learns who is viewing it, so a command typed in the
	// pane reads it from here (tmux.Switcher.Hosted).
	return runtimesession.Attachment{ID: tmux.AttachmentID(attachHosted(), hex.EncodeToString(b[:])), Client: fmt.Sprintf("amx attach (pid %d)", os.Getpid())}, nil
}
