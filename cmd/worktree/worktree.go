// Package worktree holds the `amx worktree` command family: listing managed
// Agent Mux Git worktrees, showing one, reopening it as a live session,
// opening a shell or the configured editor in it, reviewing what it changed
// since its source branch and how that would land (`diff`, diff.go, which
// writes nothing), bringing that work back by fast-forward (`land`, land.go),
// and the bounded destructive
// leaves — `remove` a clean inactive worktree, `forget` stale metadata, and
// `prune` (prune.go), which selects every worktree `remove` would accept and
// removes each through `remove` itself, previewing unless --yes. The bounded
// creation and cleanup themselves stay in internal/worktree; this package is
// only the command surface over them.
//
// A file in this package may name the imported internal/worktree package
// unqualified as `worktree`: a package's own name is not in scope inside its
// files.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	worktreeCommandsLoadConfig   = cli.LoadRunnableConfig
	worktreeCommandsNewManager   = func() actions.WorktreeLifecycle { return worktree.NewManager() }
	worktreeCommandsNewKitty     = func() kitty.KittyClient { return cli.NewKittyClient() }
	worktreeCommandsLiveSessions = func(client kitty.KittyClient, cfg *config.Config) ([]session.RunningSession, error) {
		running, _, err := session.ListWithConfig(client, cfg)
		return running, err
	}
	// worktreeCommandsTmuxSessions lists what runs on the private tmux
	// server, for the commands that must know whether a worktree is in use.
	worktreeCommandsTmuxSessions = func(server backend.TargetLauncher, cfg *config.Config) ([]session.RunningSession, error) {
		panes, err := server.Discover()
		if err != nil {
			return nil, err
		}
		return session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, cfg), nil
	}
	// worktreeCommandsObservedTmux opens the private tmux server only to read
	// what runs on it, when launches go to Kitty. Listing a server that is
	// not running starts nothing.
	worktreeCommandsObservedTmux = func() (backend.TargetLauncher, error) { return cli.NewTmuxBackend() }
	// worktreeCommandsInsideKitty says the command was typed in a Kitty
	// window, where Kitty's sessions count even for a tmux launch.
	worktreeCommandsInsideKitty = func() bool {
		return os.Getenv("KITTY_LISTEN_ON") != "" || cli.WindowIDFromEnv() > 0
	}
	// worktreeCommandsWorkspace asks the desktop workspace for a utility
	// view. It refuses unless the workspace hosts this terminal, so a shell
	// is never opened in an app the operator is not looking at.
	worktreeCommandsWorkspace = func(view protocol.WorkspaceViewRequest) error {
		if _, hosted := os.LookupEnv(tmux.WorkspaceHostEnv); !hosted {
			return errors.New("not hosted by the workspace")
		}
		_, err := ipc.NewClient().WorkspaceView(view)
		return err
	}
	// worktreeCommandsRunHere runs a shell or an editor in the terminal the
	// command was typed in and returns when it ends.
	worktreeCommandsRunHere = func(cmd *cobra.Command, dir string, argv []string) error {
		// #nosec G204 -- the operator's own shell or configured editor.
		program := exec.CommandContext(cli.CommandContext(cmd), argv[0], argv[1:]...)
		program.Dir = dir
		program.Stdin, program.Stdout, program.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		return program.Run()
	}
	worktreeCommandsActiveLoops = func() ([]agentloop.Candidate, error) {
		return agentloop.Discover(agentloop.DiscoverOptions{})
	}
	worktreeCommandsTranscriptIndex = transcript.LoadIndex
	worktreeCommandsReaders         = cli.TranscriptReaders
	worktreeListIsTerminal          = cli.IsTerminal
)

// Command returns the `worktree` command tree so the root command can register
// it. Root assembly is the only consumer.
func Command() *cobra.Command { return worktreeCmd }

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Inspect and manage Agent Mux Git worktrees",
	Long: `Inspects and manages the Git worktrees Agent Mux creates for sessions
(` + "`amx session fork-worktree`" + `, or the dashboard). Every command that takes a
<selector> names one managed worktree by:

` + worktreeSelectorHelp + `

An ambiguous selector is refused with the candidates it matched; nothing is
picked on your behalf.`,
}

// worktreeSelectorHelp is what a <selector> may be, as
// worktree.ResolveSelector reads it, for the help of every command taking one.
const worktreeSelectorHelp = `  - its stable ID (the ID column of ` + "`amx worktree list`" + `), or a unique
    prefix of it, in any case;
  - its branch name, exactly;
  - the absolute path of its checkout.`

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed worktrees",
	Long: `Lists every managed worktree with its ID, project, branch, state, and path.
STATE is active (a live session or an agent-session-loop run uses it), dirty
(uncommitted changes), unavailable (Git could not inspect it), or clean.

In a terminal the list is an aligned table, and an empty one says so. Piped,
it is one headerless tab-separated row per worktree (id, project, branch,
state, path), and an empty list prints nothing. Warnings follow the rows.

With --json it reports {worktrees, warnings}.`,
	Example: `  amx worktree list
  amx worktree list | cut -f1,3
  amx worktree list --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if len(args) != 0 {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("worktree list accepts no arguments"))
		}
		result, err := executeWorktreeAction(cmd, actions.WorktreeList, actions.Request{})
		if err != nil {
			return worktreeCommandError(cmd, jsonOutput, result, err)
		}
		views, _ := result.Data["worktrees"].([]actions.WorktreeView)
		warnings, _ := result.Data["warnings"].([]string)
		if jsonOutput {
			return cli.WriteJSON(cmd, worktreeListData{Worktrees: nonNilWorktreeViews(views), Warnings: nonNilStrings(warnings)})
		}
		out := cmd.OutOrStdout()
		if worktreeListIsTerminal(out) {
			if err := writeWorktreeTable(out, views); err != nil {
				return err
			}
			writeWorktreeWarnings(cmd, warnings)
			return nil
		}
		// Off a terminal an empty list stays empty: a script reading rows must
		// not mistake a sentence for a worktree.
		for _, view := range views {
			_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", view.ID, displayWorktreeProject(view), view.Branch, worktreeState(view), view.Path)
		}
		writeWorktreeWarnings(cmd, warnings)
		return nil
	},
}

// writeWorktreeTable is the worktree list for a person. An empty list says so,
// because printing nothing reads the same as a command that failed silently.
func writeWorktreeTable(out io.Writer, views []actions.WorktreeView) error {
	if len(views) == 0 {
		_, err := fmt.Fprintln(out, "No managed worktrees. Create one with `amx session fork-worktree` or from the dashboard.")
		return err
	}
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		rows = append(rows, []string{view.ID, displayWorktreeProject(view), view.Branch, worktreeState(view), cli.DisplayPath(view.Path)})
	}
	return cli.WriteTable(out, []string{"ID", "PROJECT", "BRANCH", "STATE", "PATH"}, rows)
}

var worktreeShowCmd = newWorktreeSelectorCommand("show", actions.WorktreeShow, "Show one managed worktree",
	`Shows one managed worktree: its ID, project, path, branch, classification,
whether it is dirty or locked, and how many live sessions and
agent-session-loop runs use it. The <selector> is one of:

`+worktreeSelectorHelp+`

With --json it reports {worktree, warnings}.`,
	`  amx worktree show 3f9c2a1e
  amx worktree show amx/fix-login
  amx worktree show 3f9c2a1e --json`)

var worktreeShellCmd = newWorktreeSelectorCommand("shell", actions.WorktreeShell, "Open a shell in a managed worktree",
	`Opens $SHELL (else /bin/sh) in a managed worktree. On the Kitty backend it
opens in a new Kitty tab; hosted by the Agent Mux workspace it opens as a
view beside the session; otherwise it runs in this terminal and returns when
the shell exits. The <selector> is one of:

`+worktreeSelectorHelp+`

--backend picks where it opens as it would for a launch. With --json nothing
is run here: the result says where the shell went, or that the caller should
run it itself (continuation "here").`,
	`  amx worktree shell 3f9c2a1e
  amx worktree shell amx/fix-login --backend tmux`)

var worktreeEditCmd = newWorktreeSelectorCommand("edit", actions.WorktreeEditor, "Open a managed worktree in the configured editor",
	`Opens a managed worktree in the configured editor. Where it opens follows
the same rules as `+"`amx worktree shell`"+`: a Kitty tab, a view in the Agent
Mux workspace, or this terminal. The <selector> is one of:

`+worktreeSelectorHelp+`

With --json nothing is run here; the result says where the editor went.`,
	`  amx worktree edit 3f9c2a1e
  amx worktree edit ~/projects/api-worktrees/fix-login`)

var worktreeReopenCmd = &cobra.Command{
	Use:   "reopen <selector>",
	Short: "Focus, resume, or freshly launch a managed worktree",
	Long: `Gets you back to work in a managed worktree, in the first way that applies:

  1. a live session already running in it is focused (a tmux session is
     reported for you to attach to instead); several are refused with their
     candidate IDs, and --live-session picks one;
  2. otherwise the newest indexed conversation started in it is resumed;
  3. otherwise a fresh agent is launched there with the worktree's recorded
     profile, or --profile.

The <selector> is one of:

` + worktreeSelectorHelp + `

With --json it reports {action, changed, message, data}.`,
	Example: `  amx worktree reopen 3f9c2a1e
  amx worktree reopen 3f9c2a1e --live-session 12
  amx worktree reopen amx/fix-login --profile claude-work --backend tmux`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, "reopen", args)
		if err != nil {
			return err
		}
		parameters := make(map[string]string)
		if value := worktreeReopenLiveSession(cmd); value != "" {
			parameters["session"] = value
		}
		if value, _ := cmd.Flags().GetString("profile"); strings.TrimSpace(value) != "" {
			parameters["profile"] = value
		}
		return runWorktreeMutationCommand(cmd, jsonOutput, actions.WorktreeReopen, actions.Request{Selector: selector, Parameters: parameters})
	},
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove <selector>",
	Short: "Safely remove a clean inactive worktree",
	Long: `Removes a managed worktree's checkout with ` + "`git worktree remove`" + `, and its
Agent Mux metadata. It asks for --yes, and it refuses a worktree that is
dirty, locked, or used by a live session or an agent-session-loop run; each
of those is checked again at the moment of removal. --delete-branch also
deletes the worktree's branch with ` + "`git branch -d`" + `, which Git refuses
unless the branch is merged. A checkout that is already gone is cleaned up
only with --delete-branch; otherwise use ` + "`amx worktree forget`" + `.

The <selector> is one of:

` + worktreeSelectorHelp + `

With --json it reports {action, changed, message, data}.`,
	Example: `  amx worktree remove 3f9c2a1e --yes
  amx worktree remove amx/fix-login --yes --delete-branch`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, "remove", args)
		if err != nil {
			return err
		}
		confirmed, _ := cmd.Flags().GetBool("yes")
		deleteBranch, _ := cmd.Flags().GetBool("delete-branch")
		parameters := make(map[string]string)
		if deleteBranch {
			parameters["delete_branch"] = "true"
		}
		return runWorktreeMutationCommand(cmd, jsonOutput, actions.WorktreeRemove, actions.Request{Selector: selector, Parameters: parameters, Confirmed: confirmed})
	},
}

var worktreeForgetCmd = &cobra.Command{
	Use:   "forget <selector>",
	Short: "Forget stale or invalid worktree metadata",
	Long: `Deletes the Agent Mux metadata of a managed worktree whose checkout is gone or
whose metadata is invalid, so it stops being listed. It asks for --yes. No
checkout, branch, or Git state is touched, and a healthy worktree whose
checkout is present is refused: remove that one with ` + "`amx worktree remove`" + `.

The <selector> is one of:

` + worktreeSelectorHelp + `

With --json it reports {action, changed, message, data}.`,
	Example: `  amx worktree forget 3f9c2a1e --yes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, "forget", args)
		if err != nil {
			return err
		}
		confirmed, _ := cmd.Flags().GetBool("yes")
		return runWorktreeMutationCommand(cmd, jsonOutput, actions.WorktreeForget, actions.Request{Selector: selector, Confirmed: confirmed})
	},
}

func newWorktreeSelectorCommand(name string, actionID actions.ID, short, long, example string) *cobra.Command {
	return &cobra.Command{
		Use: name + " <selector>", Short: short, Long: long, Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, name, args)
			if err != nil {
				return err
			}
			result, actionErr := executeWorktreeAction(cmd, actionID, actions.Request{Selector: selector})
			if actionErr != nil {
				return worktreeCommandError(cmd, jsonOutput, result, actionErr)
			}
			if view, here := actions.UtilityHere(result); here && !jsonOutput {
				return runWorktreeViewHere(cmd, view)
			}
			if actionID == actions.WorktreeShow {
				view, _ := result.Data["worktree"].(actions.WorktreeView)
				warnings, _ := result.Data["warnings"].([]string)
				if jsonOutput {
					return cli.WriteJSON(cmd, worktreeShowData{Worktree: view, Warnings: nonNilStrings(warnings)})
				}
				printWorktree(cmd, view)
				writeWorktreeWarnings(cmd, warnings)
				return nil
			}
			return writeWorktreeActionResult(cmd, jsonOutput, result)
		},
	}
}

// runWorktreeViewHere runs the shell or editor an open left to this
// terminal: outside Kitty and outside the workspace there is no tab or view
// to open, so the worktree is opened where the command was typed. A --json
// caller is told so instead (continuation "here") and runs it itself.
func runWorktreeViewHere(cmd *cobra.Command, view actions.UtilityView) error {
	action := actions.WorktreeShell
	argv := []string{"/bin/sh"}
	if shell := os.Getenv("SHELL"); shell != "" {
		argv = []string{shell}
	}
	dir := view.Path
	if view.Editor {
		action = actions.WorktreeEditor
		cfg, _ := worktreeCommandsLoadConfig()
		editor, err := session.EditorCommand(cfg)
		if err != nil {
			return cli.ActionErrorText(&actions.Error{Code: "launch_failed", Action: action, Message: err.Error()})
		}
		argv = append(editor, view.Path)
	}
	if err := worktreeCommandsRunHere(cmd, dir, argv); err != nil {
		// A shell that ends with a failing status reports its last command,
		// not a failure to open it.
		var exit *exec.ExitError
		if !view.Editor && errors.As(err, &exit) {
			return nil
		}
		return cli.ActionErrorText(&actions.Error{Code: "launch_failed", Action: action, Message: err.Error()})
	}
	return nil
}

func executeWorktreeAction(cmd *cobra.Command, actionID actions.ID, request actions.Request) (actions.Result, error) {
	cfg, err := worktreeCommandsLoadConfig()
	if err != nil {
		return actions.Result{}, &actions.Error{Code: "config_error", Action: actionID, Message: err.Error()}
	}
	deps := actions.Dependencies{
		Config: cfg, WorktreeManager: worktreeCommandsNewManager(), Readers: worktreeCommandsReaders(),
		ActiveLoops:     worktreeCommandsActiveLoops,
		TranscriptIndex: worktreeCommandsTranscriptIndex,
	}
	// A tmux that cannot be used stops a reopen, which launches on it. The
	// other commands still run: what is running on it is then unknown, which
	// the usage probe reports and a removal refuses on.
	var tmuxErr error
	if err := cli.SelectBackend(cmd, &deps); err != nil {
		var actionErr *actions.Error
		if !errors.As(err, &actionErr) {
			return actions.Result{}, err
		}
		if actionErr.Code != "backend_unavailable" || actionID == actions.WorktreeReopen {
			tagged := *actionErr
			tagged.Action = actionID
			return actions.Result{}, &tagged
		}
		tmuxErr = err
	}
	// Only the Kitty backend opens Kitty tabs: on tmux a reopen launches on
	// the private server, and a shell or an editor goes to the workspace or
	// this terminal. Kitty is still read for what is running in it when the
	// command was typed in a Kitty window.
	var client kitty.KittyClient
	if deps.Backend != runtimesession.BackendTmux {
		client = worktreeCommandsNewKitty()
		deps.Kitty = client
	} else if worktreeCommandsInsideKitty() {
		client = worktreeCommandsNewKitty()
	}
	server := deps.Tmux
	if server == nil && tmuxErr == nil {
		// Launches go to Kitty, but agents may still run on the private tmux
		// server (started from the workspace or with --backend tmux). They
		// count toward a worktree's use; a tmux that cannot be opened has
		// none to count.
		if observed, err := worktreeCommandsObservedTmux(); err == nil {
			server = observed
		}
	}
	// Each backend is read on its own, so a Kitty that does not answer still
	// leaves the tmux sessions counted; the error says what is missing, and
	// land and remove refuse on it.
	deps.LiveSessions = func(current *config.Config) ([]session.RunningSession, error) {
		var running []session.RunningSession
		var problems []error
		if client != nil {
			listed, err := worktreeCommandsLiveSessions(client, current)
			if err != nil {
				problems = append(problems, fmt.Errorf("sessions in Kitty are not counted: %w", cli.PlainKittyError(err)))
			}
			running = listed
		}
		if tmuxErr != nil {
			problems = append(problems, tmuxErr)
		} else if server != nil {
			onTmux, err := worktreeCommandsTmuxSessions(server, current)
			if err != nil {
				problems = append(problems, fmt.Errorf("sessions on tmux are not counted: %w", err))
			}
			running = append(running, onTmux...)
		}
		return running, joinProblems(problems)
	}
	deps.Utility = func(view actions.UtilityView) error {
		request := protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewShell, Directory: view.Path, Title: filepath.Base(view.Path)}
		if view.Editor {
			request = protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewEditor, Path: view.Path, Title: filepath.Base(view.Path)}
		}
		return worktreeCommandsWorkspace(request)
	}
	return actions.DefaultRegistry().Execute(cli.CommandContext(cmd), deps, actionID, request)
}

func runWorktreeMutationCommand(cmd *cobra.Command, jsonOutput bool, actionID actions.ID, request actions.Request) error {
	result, err := executeWorktreeAction(cmd, actionID, request)
	if err != nil {
		return worktreeCommandError(cmd, jsonOutput, result, err)
	}
	return writeWorktreeActionResult(cmd, jsonOutput, result)
}

func writeWorktreeActionResult(cmd *cobra.Command, jsonOutput bool, result actions.Result) error {
	data := worktreeActionData{Action: result.Action, Changed: result.Changed, Message: result.Message, Data: result.Data}
	if jsonOutput {
		return cli.WriteJSON(cmd, data)
	}
	if result.Message != "" {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Message)
	}
	return nil
}

// worktreeListHint ends an error about a selector that named nothing, so
// the operator learns where the selectable worktrees are listed.
const worktreeListHint = "`amx worktree list` shows every managed worktree"

func worktreeCommandError(cmd *cobra.Command, jsonOutput bool, result actions.Result, err error) error {
	var partial *actions.PartialResultError
	if errors.As(err, &partial) {
		data := worktreeActionData{Action: result.Action, Changed: result.Changed, Message: result.Message, Data: result.Data}
		if jsonOutput {
			return cli.WriteJSONPartial(cmd, data, partial)
		}
		if result.Message != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Message)
		}
		return partial
	}
	var actionErr *actions.Error
	if errors.As(err, &actionErr) {
		// Both output modes carry the hint: a JSON caller relaying the
		// message to a person needs the next step as much as a terminal does.
		var selectorErr *worktree.SelectorError
		if errors.As(actionErr, &selectorErr) && errors.Is(selectorErr, worktree.ErrSelectorNotFound) {
			hinted := *actionErr
			hinted.Message += "; " + worktreeListHint
			actionErr = &hinted
		}
		if !jsonOutput {
			return cli.ActionErrorText(actionErr)
		}
		candidates := make([]cli.Candidate, 0, len(actionErr.Candidates))
		for _, candidate := range actionErr.Candidates {
			candidates = append(candidates, cli.Candidate{ID: candidate.ID, DisplayName: candidate.DisplayName, CanonicalPath: candidate.CanonicalPath})
		}
		parameters := make([]cli.ParameterHelp, 0, len(actionErr.MissingParameters))
		for _, parameter := range actionErr.MissingParameters {
			parameters = append(parameters, cli.ParameterHelp{Name: parameter.Name, Description: parameter.Description, Required: parameter.Required})
		}
		return cli.WriteJSONError(cmd, cli.JSONError{Code: actionErr.Code, Message: actionErr.Message, Candidates: candidates, Parameters: parameters})
	}
	return cli.CommandFailure(cmd, jsonOutput, "action_failed", err)
}

// worktreeReopenLiveSession is the live session --live-session, or its
// deprecated alias --session, names; the canonical flag wins when both are
// given.
func worktreeReopenLiveSession(cmd *cobra.Command) string {
	if value, _ := cmd.Flags().GetString("live-session"); strings.TrimSpace(value) != "" {
		return value
	}
	value, _ := cmd.Flags().GetString("session")
	return strings.TrimSpace(value)
}

func worktreeSelectorFromArgs(cmd *cobra.Command, jsonOutput bool, name string, args []string) (string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "", cli.MissingParameter(cmd, jsonOutput, "selector", "stable ID, unique ID prefix, branch, or canonical path (see `amx worktree list`)")
	}
	if len(args) != 1 {
		return "", cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("worktree %s accepts exactly one selector", name))
	}
	return args[0], nil
}

type worktreeListData struct {
	Worktrees []actions.WorktreeView `json:"worktrees"`
	Warnings  []string               `json:"warnings"`
}

type worktreeShowData struct {
	Worktree actions.WorktreeView `json:"worktree"`
	Warnings []string             `json:"warnings"`
}

type worktreeActionData struct {
	Action  actions.ID     `json:"action"`
	Changed bool           `json:"changed"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

func printWorktree(cmd *cobra.Command, view actions.WorktreeView) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "id: %s\n", view.ID)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "project: %s\n", displayWorktreeProject(view))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "path: %s\n", view.Path)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "branch: %s\n", view.Branch)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "classification: %s\n", view.Classification)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "dirty: %t\n", view.Dirty)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "locked: %t\n", view.Locked)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "live_sessions: %d\n", len(view.LiveSessions))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "active_loops: %d\n", view.LoopCount)
	writeWorktreeWarnings(cmd, view.Warnings)
}

func writeWorktreeWarnings(cmd *cobra.Command, warnings []string) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", warning)
	}
}

func displayWorktreeProject(view actions.WorktreeView) string {
	if view.ProjectLabel != "" {
		return view.ProjectLabel
	}
	return "-"
}

func worktreeState(view actions.WorktreeView) string {
	switch {
	case len(view.LiveSessions) > 0 || view.LoopCount > 0:
		return "active"
	case view.Dirty:
		return "dirty"
	case !view.GitAvailable:
		return "unavailable"
	default:
		return "clean"
	}
}

// worktreeCompletionTimeout bounds the inventory a <Tab> waits for. Listing
// probes each worktree with git, and a shell prompt that hangs is worse than
// one that suggests nothing.
const worktreeCompletionTimeout = 2 * time.Second

// completeWorktreeSelectors suggests managed worktree IDs for the selector
// argument, each described by its branch and project, and nothing after it.
// It reads the inventory alone — no configuration, Kitty, or transcript
// index — and degrades to no suggestions on any failure.
func completeWorktreeSelectors(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeCompletionTimeout)
	defer cancel()
	entries, _ := worktreeCommandsNewManager().List(ctx)
	var selectors []string
	for _, entry := range entries {
		if entry.ID == "" || !strings.HasPrefix(entry.ID, toComplete) {
			continue
		}
		description := strings.TrimSpace(strings.Join([]string{entry.Branch, entry.ProjectLabel}, " "))
		if description == "" {
			selectors = append(selectors, entry.ID)
			continue
		}
		selectors = append(selectors, entry.ID+"\t"+description)
	}
	return selectors, cobra.ShellCompDirectiveNoFileComp
}

func nonNilWorktreeViews(values []actions.WorktreeView) []actions.WorktreeView {
	if values == nil {
		return []actions.WorktreeView{}
	}
	return values
}

// joinProblems is the probe failures as one warning line, or nil when there
// are none.
func joinProblems(problems []error) error {
	if len(problems) == 0 {
		return nil
	}
	messages := make([]string, 0, len(problems))
	for _, problem := range problems {
		messages = append(messages, problem.Error())
	}
	return errors.New(strings.Join(messages, "; "))
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func init() {
	for _, command := range []*cobra.Command{worktreeListCmd, worktreeShowCmd, worktreeShellCmd, worktreeEditCmd} {
		cli.AddJSONFlag(command, nil)
	}
	// Where a reopen launches, and so whether a shell or an editor opens a
	// Kitty tab or runs outside Kitty.
	cli.AddBackendFlag(worktreeReopenCmd)
	for _, command := range []*cobra.Command{worktreeShellCmd, worktreeEditCmd} {
		cli.AddBackendFlagUsage(command, "where it opens: kitty for a new Kitty tab, tmux for the workspace or this terminal (default: [defaults] launch_backend, else kitty)")
	}
	worktreeReopenCmd.Flags().String("live-session", "", "live session to focus when several run in the worktree: a candidate ID from the ambiguous_live_sessions error (a Kitty tab ID)")
	// --session was the flag's first name; it stays as a hidden alias so
	// scripts that pass it keep working.
	worktreeReopenCmd.Flags().String("session", "", "deprecated alias of --live-session")
	_ = worktreeReopenCmd.Flags().MarkDeprecated("session", "use --live-session")
	worktreeReopenCmd.Flags().String("profile", "", "replacement profile for a fresh launch")
	_ = worktreeReopenCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	cli.AddJSONFlag(worktreeReopenCmd, nil)
	worktreeRemoveCmd.Flags().Bool("yes", false, "confirm worktree removal")
	worktreeRemoveCmd.Flags().Bool("delete-branch", false, "also safely delete the merged worktree branch")
	cli.AddJSONFlag(worktreeRemoveCmd, nil)
	worktreeForgetCmd.Flags().Bool("yes", false, "confirm deleting the worktree's Agent Mux metadata")
	cli.AddJSONFlag(worktreeForgetCmd, nil)
	for _, command := range []*cobra.Command{worktreeShowCmd, worktreeShellCmd, worktreeEditCmd, worktreeReopenCmd, worktreeRemoveCmd, worktreeForgetCmd} {
		command.ValidArgsFunction = completeWorktreeSelectors
	}
	worktreeCmd.AddCommand(worktreeListCmd, worktreeShowCmd, worktreeReopenCmd, worktreeShellCmd, worktreeEditCmd, worktreeRemoveCmd, worktreeForgetCmd)
}
