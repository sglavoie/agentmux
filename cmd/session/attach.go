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
)

// runAttachedClient runs the interactive client on this terminal. A terminal
// that closes (a desktop view dismissed, a window shut) hangs up its whole
// foreground group: the tmux client detaches on that, and amx has to outlive it
// to release the ownership it recorded, so the hangup is taken here instead
// of ending the process.
func runAttachedClient(c *exec.Cmd) error {
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)
	defer signal.Stop(hangup)
	return c.Run()
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
	RuntimeSessionID string   `json:"runtime_session_id"`
	Backend          string   `json:"backend"`
	Project          string   `json:"project"`
	State            string   `json:"state"`
	Pane             string   `json:"pane"`
	PID              int      `json:"pid"`
	Command          []string `json:"command,omitempty"`
}

// tmuxAttacher is what attaching needs from the tmux backend.
type tmuxAttacher interface {
	backend.Discoverer
	Instance() (runtimesession.BackendInstance, error)
	AttachCommand(pane backend.Ref) (*exec.Cmd, error)
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
		code := "runtime_session_ended"
		if record.State == runtimesession.StatePending {
			code = "runtime_session_pending"
		}
		return cli.CommandFailure(cmd, attachJSON, code, fmt.Errorf("runtime session %s is %s", id, record.State))
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
	if err := validateTmux(store, server, record); err != nil {
		return cli.CommandFailure(cmd, attachJSON, "runtime_session_stale", err)
	}
	client, err := server.AttachCommand(backend.PaneID(record.Target.Ref))
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

// validateTmux confirms the registered pane still holds the registered
// process on the same server generation, marking the session stale in the
// registry when it does not, so nothing ever attaches to a reused pane ID.
func validateTmux(store *runtimesession.Store, server tmuxAttacher, record runtimesession.Record) error {
	observation := runtimesession.Observation{Instance: runtimesession.BackendInstance{
		Kind: runtimesession.BackendTmux, Address: record.Target.Instance.Address,
	}}
	// A server that is not running is observed as having no generation, which
	// marks every session registered on it stale.
	if instance, err := server.Instance(); err == nil {
		observation.Instance = instance
	} else if !errors.Is(err, tmux.ErrServerNotRunning) {
		return fmt.Errorf("reading tmux server: %w", err)
	}
	if observation.Instance.Address != record.Target.Instance.Address {
		return fmt.Errorf("runtime session %s belongs to tmux server %s, not %s", record.ID, record.Target.Instance.Address, observation.Instance.Address)
	}
	panes, err := server.Discover()
	if err != nil {
		return fmt.Errorf("listing tmux panes: %w", err)
	}
	if pane, ok := backend.FindPane(panes, backend.PaneID(record.Target.Ref)); ok {
		observation.Present = true
		if pane.Exit == nil && len(pane.Foreground) > 0 {
			if process, err := attachFingerprint(pane.Foreground[0].PID); err == nil {
				observation.Process = &process
			}
		}
	}
	var stale error
	err = store.Update(func(registry *runtimesession.Registry) error {
		_, stale = registry.Validate(record.ID, observation, attachNow())
		if stale != nil && !errors.Is(stale, runtimesession.ErrStaleTarget) {
			return stale
		}
		return nil
	})
	if err != nil {
		return err
	}
	return stale
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
	return runtimesession.Attachment{ID: "amx-attach-" + hex.EncodeToString(b[:]), Client: fmt.Sprintf("amx attach (pid %d)", os.Getpid())}, nil
}
