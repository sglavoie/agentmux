package session

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// RuntimeSessionVar is the pane metadata key naming the runtime session a
// registered launch created, so discovery can tie a pane back to its record.
const RuntimeSessionVar = "amx_runtime_session_id"

// ColorVar is the metadata key holding the color an operator picked for one
// session (`amx color-picker`). Session lists and terminal frames draw their
// color from this metadata (RunningSession.Color). A launch starts with its
// profile color.
const ColorVar = "cc_color"

// NameVar is the metadata key holding the name an operator gave one session
// (`amx name-session`), empty for none. The daemon publishes it to companions,
// where the desktop workspace shows it above the session's profile.
const NameVar = "cc_name"

// NameMax is the longest name a session is given, in runes.
const NameMax = 64

// CleanName is name as it is kept: on one line, with no control characters,
// its runs of whitespace collapsed, and no longer than NameMax. Empty is no
// name.
func CleanName(name string) string {
	fields := strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	runes := []rune(strings.Join(fields, " "))
	if len(runes) > NameMax {
		runes = runes[:NameMax]
	}
	return strings.TrimSpace(string(runes))
}

// pendingLaunchTimeout is how old a pending registration must be before a
// later launch treats it as left behind by a launcher that died between
// registering and committing. Launches finish in seconds.
const pendingLaunchTimeout = 2 * time.Minute

// ErrAgentMayBeRunning marks a failed launch whose agent could not be
// stopped: the launch is reported as failed, yet its process may still be
// running in whatever directory it was given. A caller that would clean up
// after a failed launch (an isolated fork removing its worktree) must check
// for it with errors.Is and keep what the agent may be using.
var ErrAgentMayBeRunning = errors.New("the agent may still be running")

// Runtime launches agents on the private tmux server and records each launch
// as a runtime session in Registry.
type Runtime struct {
	// Tmux is the private tmux server.
	Tmux backend.TargetLauncher
	// Registry records tmux launches. Nil means the default registry file.
	Registry *runtimesession.Store
}

// Kind reports the selected backend.
func (r Runtime) Kind() runtimesession.BackendKind {
	return runtimesession.BackendTmux
}

// Terminal is the selected backend's terminal seam, or nil when none is set.
func (r Runtime) Terminal() backend.Backend {
	return r.Tmux
}

// Owns reports whether running is hosted by the selected backend. Actions on
// an existing session follow its recorded backend, never the default.
func (r Runtime) Owns(running RunningSession) error {
	if running.BackendKind() != r.Kind() {
		return fmt.Errorf("session %q runs on %s, not %s", running.EffectiveProject(), running.BackendKind(), r.Kind())
	}
	return nil
}

func (r Runtime) registry() (*runtimesession.Store, error) {
	if r.Registry != nil {
		return r.Registry, nil
	}
	return runtimesession.NewStore(runtimesession.DefaultRegistryPath())
}

// LaunchResult is what one launch did.
type LaunchResult struct {
	// LaunchID identifies an invocation before its conversation is reported.
	LaunchID string
	Backend  runtimesession.BackendKind
	// Deduplicated is true when the project/profile key was already running
	// and nothing was launched; Group, Pane, and RuntimeSessionID then name
	// the existing session when the backend reports them.
	Deduplicated bool
	// RuntimeSessionID is set for launches that registered a runtime session.
	RuntimeSessionID runtimesession.ID
	Group            backend.Ref
	Pane             backend.Ref
}

func (r LaunchResult) deduplicated(existing RunningSession) LaunchResult {
	r.Deduplicated = true
	if existing.Backend != "" {
		r.Group, r.Pane = existing.Group, existing.Pane
		r.RuntimeSessionID = existing.RuntimeSessionID
	}
	return r
}

// lineage is how a launch relates to what came before it.
type lineage struct {
	operation string
	// parent is the runtime session a restart replaces.
	parent runtimesession.ID
	// conversation is the conversation a resume continues.
	conversation *conversation.Identity
}

func (l lineage) beginRequest(project, launchID string, relation runtimesession.Relation) runtimesession.BeginRequest {
	request := runtimesession.BeginRequest{Project: project, LaunchID: launchID, Relation: runtimesession.RelationFresh, Conversation: l.conversation}
	// The registry requires a parent for every non-fresh relation; a resume
	// or restart of something never registered is recorded as fresh and keeps
	// its conversation.
	if l.parent != "" {
		request.Relation, request.Parent = relation, l.parent
	}
	return request
}

// processStartToken reads a process's start time as an opaque token that,
// with the PID, survives PID reuse. It is replaceable in tests.
var processStartToken = psStartToken

func psStartToken(pid int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// #nosec G204 -- the only argument is a decimal PID formatted here.
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "TZ=UTC")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("reading start time of pid %d: %w", pid, err)
	}
	token := strings.Join(strings.Fields(string(out)), " ")
	if token == "" {
		return "", fmt.Errorf("reading start time of pid %d: no such process", pid)
	}
	return token, nil
}

// launchTmux launches on the private tmux server. Deduplication, launch, and
// registration happen under one launch lock beside the registry, so two
// launchers can never both decide a key is free, and every launch either
// ends live in the registry or leaves nothing running: a failed launch ends
// its pending record, and a launch whose registration cannot be committed is
// closed again (and its record ended only once the close succeeded).
func launchTmux(rt Runtime, sess config.Session, acc config.Account, cfg *config.Config, formattedTitle string, build func() (harness.Invocation, error), policy LaunchPolicy, lin lineage) (LaunchResult, error) {
	result := LaunchResult{Backend: runtimesession.BackendTmux}
	if rt.Tmux == nil {
		return result, errors.New("launching session: no tmux server")
	}
	store, err := rt.registry()
	if err != nil {
		return result, fmt.Errorf("launching session %q: %w", formattedTitle, err)
	}
	err = cachefile.WithExclusive(store.Path()+".launch", func() error {
		panes, err := rt.Tmux.Discover()
		if err != nil {
			return fmt.Errorf("listing running sessions: %w", err)
		}
		if policy != LaunchPolicyAlwaysNewInstance {
			key := SessionKey(effectiveSessionProfile(sess, acc), sess.Title)
			if existing, ok := FindByKey(RunningSessionsFromPanes(runtimesession.BackendTmux, panes, cfg), key); ok {
				launchDebug.Printf("Launch: tmux dedup hit %s", existing.Group)
				result = result.deduplicated(existing)
				return nil
			}
		}
		invocation, err := build()
		if err != nil {
			return err
		}
		launched, err := startTmux(rt, store, sess, acc, cfg, formattedTitle, invocation, lin, activePaneLaunchIDs(panes))
		result.RuntimeSessionID, result.Group, result.Pane = launched.id, launched.Group, launched.Pane
		return err
	})
	return result, err
}

type tmuxLaunch struct {
	backend.Launched
	id runtimesession.ID
}

func startTmux(rt Runtime, store *runtimesession.Store, sess config.Session, acc config.Account, cfg *config.Config, formattedTitle string, invocation harness.Invocation, lin lineage, active map[string]struct{}) (tmuxLaunch, error) {
	launchID := invocation.LaunchID
	if launchID == "" {
		launchID = "unlogged-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	relation := runtimesession.RelationRestart
	if lin.conversation != nil {
		relation = runtimesession.RelationResume
	}
	var record runtimesession.Record
	err := store.Update(func(registry *runtimesession.Registry) error {
		now := time.Now()
		registry.AbandonInterrupted(now.Add(-pendingLaunchTimeout), now)
		var err error
		record, err = registry.Begin(lin.beginRequest(sess.Title, launchID, relation), now)
		return err
	})
	if err != nil {
		if errors.Is(err, runtimesession.ErrConversationInUse) {
			return tmuxLaunch{}, fmt.Errorf("launching session %q: conversation is already running: %w", formattedTitle, err)
		}
		return tmuxLaunch{}, fmt.Errorf("launching session %q: registering runtime session: %w", formattedTitle, err)
	}
	id := record.ID
	end := func() error {
		return store.Update(func(registry *runtimesession.Registry) error {
			_, err := registry.End(id, time.Now())
			return err
		})
	}

	env := maps.Clone(invocation.Environment)
	if env == nil {
		env = map[string]string{}
	}
	env[runtimesession.EnvVar] = string(id)
	vars := buildLaunchUserVars(sess, acc, cfg.Defaults, invocation)
	vars[RuntimeSessionVar] = string(id)
	// Record the profile color for session lists and terminal frames.
	if acc.Color != "" {
		vars[ColorVar] = acc.Color
	}
	nativeReport := maintainNativeForActive(cfg, active, invocation.LaunchID)
	spec := backend.LaunchSpec{
		Title:     formattedTitle,
		Directory: invocation.CWD,
		Env:       env,
		Vars:      vars,
		Argv:      []string{loginShell(), "-lic", RenderInvocation(invocation)},
	}
	launchDebug.Printf("Launch: tmux LaunchTarget: runtime=%s title=%q dir=%q", id, spec.Title, spec.Directory)
	launched, err := rt.Tmux.LaunchTarget(spec)
	if err != nil {
		recordHarnessLaunch(cfg, sess, acc, invocation, "error", tmuxLaunchFailureReason(err), nativeReport)
		failure := fmt.Errorf("launching session %q: %w", formattedTitle, err)
		if errors.Is(err, tmux.ErrPartialLaunchRemains) {
			// The server may have started the agent and it could not be
			// killed, so the record stays pending, as for an uncommitted
			// launch that cannot be closed below.
			return tmuxLaunch{}, fmt.Errorf("%w; %w", failure, ErrAgentMayBeRunning)
		}
		return tmuxLaunch{}, errors.Join(failure, end())
	}
	committed := func() error {
		process, err := ProcessFingerprint(launched.PID)
		if err != nil {
			return err
		}
		return store.Update(func(registry *runtimesession.Registry) error {
			_, err := registry.Commit(id,
				runtimesession.Target{Instance: launched.Instance, Ref: launched.Pane.ID}, process, time.Now())
			return err
		})
	}()
	if committed != nil {
		// An agent the registry does not know about could be launched again
		// by the next attempt, so a launch that cannot be committed is undone.
		recordHarnessLaunch(cfg, sess, acc, invocation, "error", launchReasonCommitFailed, nativeReport)
		failure := fmt.Errorf("launching session %q: committing runtime session %s: %w", formattedTitle, id, committed)
		if closeErr := rt.Tmux.Close(launched.Group); closeErr != nil {
			// The agent may still be running, so its record stays pending:
			// ending it would leave a running agent the registry calls
			// finished. The next launch abandons the interrupted
			// registration (AbandonInterrupted), and until then the pane
			// still deduplicates its key.
			return tmuxLaunch{}, errors.Join(failure,
				fmt.Errorf("closing uncommitted session %s: %w; %w", id, closeErr, ErrAgentMayBeRunning))
		}
		return tmuxLaunch{}, errors.Join(failure, end())
	}
	recordHarnessLaunch(cfg, sess, acc, invocation, "success", "launched", nativeReport, launchFlightIdentity{runtimeID: string(id), operation: lin.operation})
	return tmuxLaunch{Launched: launched, id: id}, nil
}

// Journal reasons for a launch that failed. A reason is a word from a fixed
// vocabulary, never an error's text: a backend error can carry the launch's
// argument list, which holds the project environment and the initial prompt.
const (
	launchReasonLaunchFailed = "launch_failed"
	launchReasonCommitFailed = "commit_failed"
)

// tmuxLaunchFailureReason names a failed tmux launch by its failure class.
func tmuxLaunchFailureReason(err error) string {
	return launchReasonLaunchFailed + ":" + tmux.FailureClass(err)
}

func loginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/zsh"
}

func activePaneLaunchIDs(panes []backend.Pane) map[string]struct{} {
	active := make(map[string]struct{})
	for _, pane := range panes {
		if id := pane.Vars["amx_launch_id"]; id != "" && pane.Exit == nil {
			active[id] = struct{}{}
		}
	}
	return active
}

// RunningSessionsFromPanes converts discovered tmux panes into running
// sessions. Only panes AgentMux launched (those carrying a profile)
// whose program is still running count; an exited pane holds no agent.
func RunningSessionsFromPanes(kind runtimesession.BackendKind, panes []backend.Pane, cfg *config.Config) []RunningSession {
	ctx := newProjectMatchContext(cfg)
	var sessions []RunningSession
	for _, pane := range panes {
		if pane.Exit != nil || len(pane.Vars) == 0 {
			continue
		}
		vars := pane.Vars
		profile := vars["amx_profile"]
		if profile == "" {
			profile = vars["cc_account"]
		}
		if profile == "" {
			continue
		}
		parts := palette.ParseTitleParts(pane.GroupTitle)
		project := vars["amx_project"]
		if project == "" {
			project = projectNameForProfile(parts.Base, profile)
		}
		directory := vars["amx_directory"]
		if directory == "" {
			directory = vars["cc_cwd"]
		}
		if directory == "" {
			directory = pane.CWD
		}
		args := vars["amx_args"]
		if args == "" {
			args = vars["cc_flags"]
		}
		harnessName := ""
		if vars["amx_harness"] != "" {
			harnessName = harness.Normalize(vars["amx_harness"])
		} else if vars["cc_account"] != "" {
			harnessName = harness.Claude
		}
		homeDir := vars["amx_home_dir"]
		if homeDir == "" {
			homeDir = vars["cc_config_dir"]
		}
		env := vars["amx_env"]
		if env == "" {
			env = vars["cc_env"]
		}
		groups := vars["amx_groups"]
		if groups == "" {
			groups = vars["cc_groups"]
		}
		_, explicitFlagsSet := vars["cc_explicit_flags"]
		rs := RunningSession{
			Key: SessionKey(profile, project), Title: pane.GroupTitle, Base: parts.Base, State: parts.State,
			Looping: parts.Looping, Starred: parts.Starred, Locked: vars["amx_locked"] == "true", Muted: parts.Muted || vars["cc_mute"] != "",
			Project: project, Profile: profile, Account: profile, Harness: harnessName, HomeDir: homeDir, Directory: directory,
			Args: args, Flags: args, ExplicitFlags: vars["cc_explicit_flags"], ExplicitFlagsSet: explicitFlagsSet,
			Env: env, Groups: decodeGroups(groups), LaunchID: vars["amx_launch_id"],
			NativeCoverage: vars["amx_native_coverage"], NativeLogPath: vars["amx_native_log_path"],
			NativeLogLevel: vars["amx_native_log_level"], NativeOverride: vars["amx_native_override"],
			HarnessSessionID: vars["amx_harness_session"], ASLRunID: vars["amx_asl_run_id"],
		}
		rs.Backend = kind
		rs.Group, rs.Pane = pane.Group, pane.Ref
		if id, err := runtimesession.ParseID(vars[RuntimeSessionVar]); err == nil {
			rs.RuntimeSessionID = id
		}
		rs.Color = vars[ColorVar]
		sessions = append(sessions, ctx.stampFamily(rs))
	}
	return sessions
}

// EndRuntimeSession marks a closed session's runtime session ended. A record
// already gone or no longer active needs nothing.
func EndRuntimeSession(rt Runtime, id runtimesession.ID) error {
	if id == "" {
		return nil
	}
	store, err := rt.registry()
	if err != nil {
		return err
	}
	return store.Update(func(registry *runtimesession.Registry) error {
		_, err := registry.End(id, time.Now())
		if errors.Is(err, runtimesession.ErrNotFound) || errors.Is(err, runtimesession.ErrInvalidState) {
			return nil
		}
		return err
	})
}

// ProcessFingerprint fingerprints a running process the way launches record
// it, so an observer can tell the registered agent from a reused PID.
func ProcessFingerprint(pid int) (runtimesession.ProcessFingerprint, error) {
	token, err := processStartToken(pid)
	if err != nil {
		return runtimesession.ProcessFingerprint{}, err
	}
	return runtimesession.ProcessFingerprint{PID: pid, StartToken: token}, nil
}
