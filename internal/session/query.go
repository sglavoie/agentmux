package session

import (
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/projectresolve"
)

// RunningSession is a currently open managed harness tab in Kitty.
type RunningSession struct {
	TabID            int
	TabNumber        int // 1-based physical position within the containing Kitty OS window
	WindowID         int
	Key              string
	Title            string    // raw Kitty tab title (may include icons)
	Base             string    // title with all icons stripped (e.g., "api"; older tabs may still be "W:api")
	State            string    // "bolt", "stop", "key", "warning", "error", "subagent", "compact", "question", or ""
	Looping          bool      // daemon-owned loop marker; title-derived only when the daemon is unreachable
	BusySince        time.Time // daemon-owned start of the current active-class stretch; zero when the row is not busy
	Unseen           bool      // daemon-owned: the row came to rest on a turn the user has not looked at; false (seen) with no daemon
	Reason           string    // daemon-owned harness prompt kind naming WHY a blocked row is blocked; empty when nothing is blocking it, when no screen probe labelled the prompt, and with no daemon
	Starred          bool
	Locked           bool
	Muted            bool
	Project          string   // configured project title
	Profile          string   // from amx_profile / legacy cc_account user vars
	Account          string   // compatibility alias for Profile
	Harness          string   // from amx_harness or inferred from legacy vars
	HomeDir          string   // from amx_home_dir / legacy cc_config_dir
	Directory        string   // from amx_directory / legacy cc_cwd user var, or the selected managed window's CWD
	Args             string   // from amx_args / legacy cc_flags (shell-quoted)
	Flags            string   // compatibility alias for Args
	ExplicitFlags    string   // from cc_explicit_flags (shell-quoted); empty when inherited or legacy
	ExplicitFlagsSet bool     // true when cc_explicit_flags was present in Kitty user vars
	Env              string   // from amx_env / legacy cc_env (raw serialized)
	Groups           []string // decoded from cc_groups user var (\x1f-delimited)
	LaunchID         string
	NativeCoverage   string
	NativeLogPath    string
	NativeLogLevel   string
	NativeOverride   string
	HarnessSessionID string
	ASLRunID         string
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
// writers (mute/star). State and looping come from first-class fields so a
// stale tab title cannot overwrite the daemon-owned values.
func (rs RunningSession) TitleParts() palette.TitleParts {
	return palette.TitleParts{
		Base:    rs.Base,
		State:   rs.State,
		Starred: rs.Starred,
		Muted:   rs.Muted,
		Looping: rs.Looping,
	}
}

// GhostTab is a managed tab whose anchor window (the one bearing amx user vars)
// no longer exists, but whose surviving live harness window still matches a
// unique configured project by title or working directory. Callers can use the
// WindowID to re-stamp user vars onto that harness window and restore normal
// detection.
type GhostTab struct {
	TabID    int
	WindowID int
	Profile  string
	Project  string
	Account  config.Account
	Session  config.Session
}

type titleFallbackMatch struct {
	acc  config.Account
	sess config.Session
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

// List returns all managed running sessions from Kitty.
// A tab is "managed" if any of its windows has cc_account set (non-empty).
func List(client kitty.KittyClient) ([]RunningSession, error) {
	sessions, _, err := ListWithConfig(client, nil)
	return sessions, err
}

// ListFromOSWindows returns all managed running sessions from an already-fetched
// kitten-@-ls payload. This avoids a second Kitty roundtrip for callers that
// already have the raw window list and only need session reconstruction.
func ListFromOSWindows(osWindows []kitty.OSWindow) []RunningSession {
	sessions, _ := runningSessionsFromOSWindows(osWindows, nil)
	return sessions
}

// ListFromOSWindowsWithConfig returns managed sessions and recoverable ghost
// tabs from an already-fetched kitten-@-ls payload. It is the config-aware
// counterpart to ListFromOSWindows and lets callers retain the raw Kitty
// topology for related lookups without issuing a second remote-control query.
func ListFromOSWindowsWithConfig(osWindows []kitty.OSWindow, cfg *config.Config) ([]RunningSession, []GhostTab) {
	return runningSessionsFromOSWindows(osWindows, cfg)
}

// ListWithConfig returns running sessions plus any "ghost" tabs whose anchor
// window is gone but whose surviving live harness window resolves
// unambiguously to a configured project. Title matching is tried first,
// including legacy "<initial>:<project>" titles. If that fails, the harness
// window's CWD can recover a tab whose title no longer identifies the project.
// When cfg is nil, no fallback occurs and ghosts is always empty — behavior is
// identical to List.
func ListWithConfig(client kitty.KittyClient, cfg *config.Config) (sessions []RunningSession, ghosts []GhostTab, err error) {
	osWindows, err := client.ListWindows()
	if err != nil {
		return nil, nil, err
	}
	sessions, ghosts = ListFromOSWindowsWithConfig(osWindows, cfg)
	return sessions, ghosts, nil
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
