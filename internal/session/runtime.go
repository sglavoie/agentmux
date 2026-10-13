package session

import (
	"context"
	"encoding/json"
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
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// RuntimeSessionVar is the pane metadata key naming the runtime session a
// registered launch created, so discovery can tie a pane back to its record.
const RuntimeSessionVar = "amx_runtime_session_id"

// ColorVar is the metadata key holding the color an operator picked for one
// session (`amx color-picker`). On Kitty the pick is drawn by the tab bar and
// this key is only its record; a session on another backend has no tab bar,
// and the key is what its rows are drawn from (RunningSession.Color). A tmux
// launch starts it at the profile's color, which is what a new Kitty tab is
// painted in.
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

var errNoKittyClient = errors.New("launching session: no Kitty client")

// Runtime selects the terminal backend new agents launch on. Kitty stays the
// default; a Runtime with Tmux set launches on the private tmux server and
// records every launch as a runtime session in Registry.
type Runtime struct {
	// Kitty is the Kitty client, the backend when Tmux is nil.
	Kitty kitty.KittyClient
	// Tmux is the private tmux server. Setting it selects tmux.
	Tmux backend.TargetLauncher
	// Registry records tmux launches. Nil means the default registry file.
	Registry *runtimesession.Store
}

// KittyRuntime is the Kitty backend over client.
func KittyRuntime(client kitty.KittyClient) Runtime { return Runtime{Kitty: client} }

// Kind reports the selected backend.
func (r Runtime) Kind() runtimesession.BackendKind {
	if r.Tmux != nil {
		return runtimesession.BackendTmux
	}
	return runtimesession.BackendKitty
}

// Terminal is the selected backend's terminal seam, or nil when none is set.
func (r Runtime) Terminal() backend.Backend {
	if r.Tmux != nil {
		return r.Tmux
	}
	if r.Kitty == nil {
		return nil
	}
	return kitty.NewBackend(r.Kitty)
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
	Backend runtimesession.BackendKind
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
// closed again.
func launchTmux(rt Runtime, sess config.Session, acc config.Account, cfg *config.Config, formattedTitle string, build func() (harness.Invocation, error), policy LaunchPolicy, lin lineage) (LaunchResult, error) {
	result := LaunchResult{Backend: runtimesession.BackendTmux}
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
	// Kitty paints a new tab in its profile's color. A tmux session has no
	// tab, so the color is recorded where the picker records a pick, and
	// whatever lists or frames the session draws it from there.
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
		recordHarnessLaunch(cfg, sess, acc, invocation, "error", err.Error(), nativeReport)
		return tmuxLaunch{}, errors.Join(fmt.Errorf("launching session %q: %w", formattedTitle, err), end())
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
		recordHarnessLaunch(cfg, sess, acc, invocation, "error", committed.Error(), nativeReport)
		return tmuxLaunch{}, errors.Join(
			fmt.Errorf("launching session %q: committing runtime session %s: %w", formattedTitle, id, committed),
			rt.Tmux.Close(launched.Group), end())
	}
	recordHarnessLaunch(cfg, sess, acc, invocation, "success", "launched", nativeReport)
	return tmuxLaunch{Launched: launched, id: id}, nil
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

// RunningSessionsFromPanes converts the panes of a kind backend (not Kitty,
// whose tab topology Discover flattens) into
// running sessions. Only panes AgentMux launched (those carrying a profile)
// whose program is still running count; an exited pane holds no agent.
func RunningSessionsFromPanes(kind runtimesession.BackendKind, panes []backend.Pane, cfg *config.Config) []RunningSession {
	ctx := newProjectMatchContext(cfg)
	var sessions []RunningSession
	for _, pane := range panes {
		if pane.Exit != nil || len(pane.Vars) == 0 {
			continue
		}
		data, err := json.Marshal(pane.Vars)
		if err != nil {
			continue
		}
		var vars kitty.UserVars
		if err := json.Unmarshal(data, &vars); err != nil || vars.EffectiveProfile() == "" {
			continue
		}
		window := kitty.Window{UserVars: vars, CWD: pane.CWD}
		rs := runningSessionFromWindow(kitty.Tab{Title: pane.GroupTitle, Windows: []kitty.Window{window}}, window)
		rs.Backend = kind
		rs.Group, rs.Pane = pane.Group, pane.Ref
		if id, err := runtimesession.ParseID(pane.Vars[RuntimeSessionVar]); err == nil {
			rs.RuntimeSessionID = id
		}
		rs.Color = pane.Vars[ColorVar]
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
