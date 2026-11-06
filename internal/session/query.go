package session

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// RunningSession is a managed harness running in a tmux pane.
type RunningSession struct {
	Review                protocol.SessionReview
	TabID                 int
	TabNumber             int // legacy positional metadata
	WindowID              int
	Key                   string
	Title                 string                      // terminal title (may include icons)
	Base                  string                      // title with all icons stripped (e.g., "api"; older tabs may still be "W:api")
	State                 string                      // "bolt", "stop", "key", "warning", "error", "subagent", "compact", "question", or ""
	Looping               bool                        // daemon-owned loop marker; title-derived only when the daemon is unreachable
	AgentTurn             *protocol.AgentTurnSnapshot // agent work independently of ASL; nil with older/unavailable daemons
	ASLPhases             []string                    // distinct live run phases, independent of AgentTurn
	BusySince             time.Time                   // daemon-owned start of the current active-class stretch; zero when the row is not busy
	PresentationChangedAt time.Time                   // daemon-owned last visible icon/unseen transition; zero when unknown
	Unseen                bool                        // daemon-owned: the row came to rest on a turn the user has not looked at; false (seen) with no daemon
	Reason                string                      // daemon-owned harness prompt kind naming WHY a blocked row is blocked; empty when nothing is blocking it, when no screen probe labelled the prompt, and with no daemon
	Branch                string                      // daemon-owned short Git branch of the row's checkout; empty for a non-repository, a detached HEAD, a disabled/failed probe, and with no daemon
	Dirty                 bool                        // daemon-owned: the row's checkout has modified tracked files; independent of Branch, so a detached HEAD still reports it
	Starred               bool
	Locked                bool
	Muted                 bool
	// Repository-family identity, resolved during config-aware discovery from
	// the configured project that owns Directory. All four stay empty when no
	// configured project matches the row's directory and when the projects that
	// do match disagree about which family they are, so a row never groups on a
	// guess. Discovery without a config resolves no family at all.
	//
	// FamilyKey is an internal grouping token built out of filesystem paths and
	// must never be rendered; FamilyLabel is the one of these meant to be read.
	FamilyKey          string
	FamilyLabel        string
	FamilyIsRepository bool     // the family is a Git repository rather than a lone configured project
	FamilyPrimaryRoot  string   // main checkout root, or the configured directory for a non-repository
	Project            string   // configured project title
	DisplayProject     string   // current config title; presentation only, never a launch identity
	Profile            string   // configured launch profile
	Account            string   // compatibility alias for Profile
	Harness            string   // configured agent harness
	HomeDir            string   // agent home directory
	Directory          string   // session working directory
	Args               string   // launch arguments (shell-quoted)
	Flags              string   // compatibility alias for Args
	ExplicitFlags      string   // explicitly supplied launch arguments (shell-quoted)
	ExplicitFlagsSet   bool     // whether launch arguments were supplied explicitly
	Env                string   // serialized launch environment
	Groups             []string // session groups
	LaunchID           string
	NativeCoverage     string
	NativeLogPath      string
	NativeLogLevel     string
	NativeOverride     string
	HarnessSessionID   string
	ASLRunID           string
	// Backend is the terminal backend hosting the session. Empty means tmux.
	Backend runtimesession.BackendKind
	// Group and Pane address the session inside its backend.
	Group backend.Ref
	Pane  backend.Ref
	// RuntimeSessionID is the runtime session the launch registered, when it
	// registered one.
	RuntimeSessionID runtimesession.ID
	// Color is the palette color the operator picked for this session.
	// Empty means the profile's color.
	Color string
}

// BackendKind is the backend hosting the session; tmux when unrecorded.
func (rs RunningSession) BackendKind() runtimesession.BackendKind {
	if rs.Backend == "" {
		return runtimesession.BackendTmux
	}
	return rs.Backend
}

func (rs RunningSession) EffectiveProfile() string {
	if rs.Profile != "" {
		return rs.Profile
	}
	return rs.Account
}

func (rs RunningSession) EffectiveProject() string {
	if rs.Project != "" {
		return rs.Project
	}
	return projectNameForProfile(rs.Base, rs.EffectiveProfile())
}

func (rs RunningSession) EffectiveArgs() string {
	if rs.Args != "" {
		return rs.Args
	}
	return rs.Flags
}

// TitleParts returns the title components for the remaining non-daemon title
// writers (lock/mute/star). State and looping come from first-class fields so a
// stale tab title cannot overwrite the daemon-owned values.
func (rs RunningSession) TitleParts() palette.TitleParts {
	return palette.TitleParts{
		Base:    rs.Base,
		State:   rs.State,
		Starred: rs.Starred,
		Locked:  rs.Locked,
		Muted:   rs.Muted,
		Looping: rs.Looping,
	}
}

type projectMatchContext struct {
	cfg      *config.Config
	resolver projectresolve.Resolver
}

func newProjectMatchContext(cfg *config.Config) projectMatchContext {
	ctx := projectMatchContext{cfg: cfg}
	if cfg != nil {
		ctx.resolver = projectresolve.NewWithGit(configuredProjects(cfg))
	}
	return ctx
}

// stampFamily attaches current project presentation and repository-family
// identity to a session that discovery has already settled. It reuses the
// resolver built for ghost recovery, so no second Git probe pass runs. Family
// fields stay empty when the resolver declines to name a family.
func (ctx projectMatchContext) stampFamily(rs RunningSession) RunningSession {
	if ctx.cfg == nil {
		return rs
	}
	rs.DisplayProject = ctx.displayProject(rs)
	family, ok := ctx.resolver.ResolveFamily(rs.Directory)
	if !ok {
		return rs
	}
	rs.FamilyKey = family.Key
	rs.FamilyLabel = family.Label
	rs.FamilyIsRepository = family.IsRepository
	rs.FamilyPrimaryRoot = family.PrimaryRoot
	return rs
}

// Find returns the first running session whose Base title equals baseTitle.
func Find(sessions []RunningSession, baseTitle string) (RunningSession, bool) {
	for _, s := range sessions {
		if s.Base == baseTitle {
			return s, true
		}
	}
	return RunningSession{}, false
}

// FindByKey returns the first running session whose logical key equals key.
func FindByKey(sessions []RunningSession, key string) (RunningSession, bool) {
	for _, s := range sessions {
		if s.Key == key {
			return s, true
		}
	}
	return RunningSession{}, false
}

// FindByWindowID returns the running session anchored to windowID.
func FindByWindowID(sessions []RunningSession, windowID int) (RunningSession, bool) {
	for _, s := range sessions {
		if s.WindowID == windowID {
			return s, true
		}
	}
	return RunningSession{}, false
}

// GroupRef is the tmux window hosting the session.
func (rs RunningSession) GroupRef() backend.Ref {
	if !rs.Group.IsZero() {
		return rs.Group
	}
	return backend.Ref{}
}

// PaneRef is the tmux pane the session is anchored to.
func (rs RunningSession) PaneRef() backend.Ref {
	if !rs.Pane.IsZero() {
		return rs.Pane
	}
	return backend.Ref{}
}

// PaneTarget names one session pane without saying how to reach it: the
// backend that hosts it and the pane's reference there. It is comparable, so a
// reader can key its schedule and its buffers on it, and two backends that
// happen to use the same pane ID never collide.
type PaneTarget struct {
	Backend runtimesession.BackendKind
	Pane    backend.Ref
}

// PaneTarget is the session's pane in its recorded backend. A session with no
// addressable pane yields the zero target.
func (rs RunningSession) PaneTarget() PaneTarget {
	if rs.Pane.Scope != backend.ScopePane || rs.Pane.ID == "" {
		return PaneTarget{}
	}
	return PaneTarget{Backend: rs.BackendKind(), Pane: rs.Pane}
}

// IsZero reports a target that names no pane.
func (t PaneTarget) IsZero() bool { return t == PaneTarget{} }

// String renders a target for diagnostics.
func (t PaneTarget) String() string { return string(t.Backend) + "/" + t.Pane.String() }

// configuredProjects returns the current project entries, including older config aliases.
func configuredProjects(cfg *config.Config) []config.Session {
	if cfg == nil {
		return nil
	}
	if len(cfg.Sessions) > 0 {
		return cfg.Sessions
	}
	return cfg.Projects
}

// displayProject follows config renames without rewriting launch metadata.
func (ctx projectMatchContext) displayProject(rs RunningSession) string {
	matches := ctx.resolver.Matches(rs.Directory)
	for _, match := range matches {
		if match.Title == rs.EffectiveProject() {
			return match.Title
		}
	}
	var profileMatches []projectresolve.Match
	for _, match := range matches {
		if strings.EqualFold(match.Profile, rs.EffectiveProfile()) {
			profileMatches = append(profileMatches, match)
		}
	}
	if len(profileMatches) > 0 {
		matches = profileMatches
	}
	title := ""
	for i, match := range matches {
		if i > 0 && match.Title != title {
			return ""
		}
		title = match.Title
	}
	return title
}

// SessionKey builds a case-insensitive identity for a configured session.
func SessionKey(profileName, projectTitle string) string {
	return strings.ToLower(profileName) + "\x1f" + strings.ToLower(projectTitle)
}

func decodeGroups(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, group := range strings.Split(raw, "\x1f") {
		if group != "" {
			out = append(out, group)
		}
	}
	return out
}
