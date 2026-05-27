package session_test

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestTitleRoundTrip_FormatPalettePalette verifies that a config.Session's
// FormatTitle output, wrapped with palette icons via BuildTitle, parses back
// to the same base title via palette.ParseTitle. This pins the contract that
// session.FormatTitle and palette.BuildTitle/ParseTitle remain interoperable
// even when session metadata includes profile/account combinations and unicode
// values that no longer appear in the visible Kitty tab base title.
func TestTitleRoundTrip_FormatPalettePalette(t *testing.T) {
	cases := []struct {
		name    string
		sess    config.Session
		state   string
		starred bool
	}{
		{"ascii plain", config.Session{Title: "api", Account: "work"}, "", false},
		{"ascii bolt", config.Session{Title: "api", Account: "work"}, "bolt", false},
		{"ascii stop", config.Session{Title: "api", Account: "work"}, "stop", false},
		{"ascii key", config.Session{Title: "api", Account: "work"}, "key", false},
		{"ascii warning", config.Session{Title: "api", Account: "work"}, "warning", false},
		{"ascii error", config.Session{Title: "api", Account: "work"}, "error", false},
		{"ascii starred no state", config.Session{Title: "api", Account: "work"}, "", true},
		{"ascii starred bolt", config.Session{Title: "api", Account: "work"}, "bolt", true},
		{"latin extended initial", config.Session{Title: "scriba", Profile: "éscriba"}, "bolt", false},
		{"cjk initial", config.Session{Title: "project", Profile: "中文"}, "stop", true},
		{"profile beats account", config.Session{Title: "x", Profile: "alpha", Account: "beta"}, "", false},
		{"no profile no account", config.Session{Title: "myproject"}, "", false},
		{"title contains colon", config.Session{Title: "a:b:c", Account: "work"}, "", false},
		{"title contains spaces", config.Session{Title: "with spaces", Account: "work"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			formatted := session.FormatTitle(tc.sess)
			withIcons := palette.BuildTitle(formatted, tc.state, tc.starred)

			base, state, starred := palette.ParseTitle(withIcons)
			if base != formatted {
				t.Errorf("ParseTitle.base = %q, want %q (built %q)", base, formatted, withIcons)
			}
			if state != tc.state {
				t.Errorf("ParseTitle.state = %q, want %q", state, tc.state)
			}
			if starred != tc.starred {
				t.Errorf("ParseTitle.starred = %v, want %v", starred, tc.starred)
			}
			if got := palette.StripIcons(withIcons); got != formatted {
				t.Errorf("StripIcons = %q, want %q", got, formatted)
			}
		})
	}
}

// TestTitleRoundTrip_FormatPaletteFull verifies that wrapping FormatTitle
// output with BuildTitleFull/ParseTitleFull preserves the muted flag through
// the full round-trip, complementing the BuildTitle/ParseTitle wrapper cases.
func TestTitleRoundTrip_FormatPaletteFull(t *testing.T) {
	cases := []struct {
		name    string
		sess    config.Session
		state   string
		starred bool
		muted   bool
	}{
		{"muted plain", config.Session{Title: "api", Account: "work"}, "", false, true},
		{"muted bolt", config.Session{Title: "api", Account: "work"}, "bolt", false, true},
		{"muted starred", config.Session{Title: "api", Account: "work"}, "", true, true},
		{"muted starred bolt", config.Session{Title: "api", Account: "work"}, "bolt", true, true},
		{"muted unicode profile", config.Session{Title: "scriba", Profile: "éscriba"}, "key", true, true},
		{"muted colon title", config.Session{Title: "a:b:c", Account: "work"}, "error", true, true},
		{"unmuted via Full", config.Session{Title: "api", Account: "work"}, "warning", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			formatted := session.FormatTitle(tc.sess)
			withIcons := palette.BuildTitleFull(formatted, tc.state, tc.starred, tc.muted)

			base, state, starred, muted := palette.ParseTitleFull(withIcons)
			if base != formatted {
				t.Errorf("ParseTitleFull.base = %q, want %q (built %q)", base, formatted, withIcons)
			}
			if state != tc.state {
				t.Errorf("ParseTitleFull.state = %q, want %q", state, tc.state)
			}
			if starred != tc.starred {
				t.Errorf("ParseTitleFull.starred = %v, want %v", starred, tc.starred)
			}
			if muted != tc.muted {
				t.Errorf("ParseTitleFull.muted = %v, want %v", muted, tc.muted)
			}
			if got := palette.StripIcons(withIcons); got != formatted {
				t.Errorf("StripIcons = %q, want %q", got, formatted)
			}
		})
	}
}

// TestFormatTitle_IgnoresProfileAndAccount verifies that managed Kitty tab
// base titles come entirely from the project title.
func TestFormatTitle_IgnoresProfileAndAccount(t *testing.T) {
	sess := config.Session{Title: "api", Profile: "alpha", Account: "beta"}
	if got := session.FormatTitle(sess); got != "api" {
		t.Errorf("FormatTitle(Profile=alpha, Account=beta) = %q, want %q", got, "api")
	}
}

// TestFind_CaseSensitive_Base verifies that Find on Base title is case-sensitive
// (sessions store the original case from the tab title). Callers that want
// case-insensitive matching should use FindByKey instead, since SessionKey
// lowercases its inputs.
func TestFind_CaseSensitive_Base(t *testing.T) {
	sessions := []session.RunningSession{{Base: "W:api"}}
	if _, ok := session.Find(sessions, "w:api"); ok {
		t.Error("Find should be case-sensitive on Base; got match for w:api against W:api")
	}
	if _, ok := session.Find(sessions, "W:api"); !ok {
		t.Error("Find should match exact Base; missed W:api")
	}
}

// TestFindByKey_CaseInsensitive verifies that FindByKey matches keys built
// via SessionKey, which lowercases both profile and project. This is the
// canonical lookup path used by Launch's dedup check.
func TestFindByKey_CaseInsensitive(t *testing.T) {
	sessions := []session.RunningSession{
		{Key: session.SessionKey("work", "api"), Base: "W:api"},
	}
	// Querying with mixed case must hit the same key.
	if _, ok := session.FindByKey(sessions, session.SessionKey("WORK", "API")); !ok {
		t.Error("FindByKey should case-fold via SessionKey; missed WORK/API")
	}
}

// TestSessionKey_UnicodeProfileCaseInsensitive verifies SessionKey lowercases
// unicode profile names (Turkish dotted I excluded — Go's strings.ToLower uses
// simple case folding which matches the validation rule at config load time).
func TestSessionKey_UnicodeProfileCaseInsensitive(t *testing.T) {
	a := session.SessionKey("Éscriba", "Api")
	b := session.SessionKey("éscriba", "api")
	if a != b {
		t.Errorf("SessionKey unicode case sensitivity: %q != %q", a, b)
	}
}

// TestSessionFromRunning_RoundTrip verifies that a RunningSession reconstructed
// from user vars produces a config.Session whose ResolveArgs output (with the
// matching account/defaults applied) equals the original effective args
// string. This guards Restart's invariant that args, env, and groups survive
// the user-var serialization round-trip.
func TestSessionFromRunning_RoundTrip(t *testing.T) {
	original := session.RunningSession{
		Base:             "W:api",
		Account:          "work",
		Profile:          "work",
		Directory:        "/tmp/api",
		Args:             "--prompt 'hello world' '--literal=$HOME'",
		ExplicitFlags:    "--prompt 'hello world' '--literal=$HOME'",
		ExplicitFlagsSet: true,
		Env:              "DEBUG=1\x1fNODE_ENV=development",
		Groups:           []string{"backend", "infra"},
		Project:          "api",
	}

	sess := session.SessionFromRunning(original)

	if sess.Title != "api" {
		t.Errorf("Title: got %q, want %q", sess.Title, "api")
	}
	if sess.Directory != "/tmp/api" {
		t.Errorf("Directory: got %q, want %q", sess.Directory, "/tmp/api")
	}
	if sess.EffectiveProfile() != "work" {
		t.Errorf("EffectiveProfile: got %q, want %q", sess.EffectiveProfile(), "work")
	}
	if !reflect.DeepEqual(sess.Args, []string{"--prompt", "hello world", "--literal=$HOME"}) {
		t.Errorf("Args parsed: got %#v", sess.Args)
	}
	if !reflect.DeepEqual(sess.Env, map[string]string{"DEBUG": "1", "NODE_ENV": "development"}) {
		t.Errorf("Env parsed: got %#v", sess.Env)
	}
	if !reflect.DeepEqual(sess.Groups, []string{"backend", "infra"}) {
		t.Errorf("Groups: got %#v", sess.Groups)
	}

	// Re-serialize through ResolveArgs and SerializeSessionEnv; should match
	// the original strings byte-for-byte (modulo the Env which sorts keys).
	acc := config.Account{Name: "work"}
	defaults := config.Defaults{}
	if got := session.ResolveArgs(sess, acc, defaults); got != original.Args {
		t.Errorf("ResolveArgs round-trip: got %q, want %q", got, original.Args)
	}
	if got := session.SerializeSessionEnv(sess.Env); got != original.Env {
		t.Errorf("SerializeSessionEnv round-trip: got %q, want %q", got, original.Env)
	}
}

// TestSessionFromRunning_ExplicitFlagsSet_EmptyDoesNotInheritFromArgs
// verifies the explicit-empty-args semantics: when ExplicitFlagsSet=true
// and both ExplicitFlags and Args/Flags are empty, the reconstructed session
// has no project-level args (so the next launch will pick up account/defaults
// args), rather than pinning the previous effective args.
func TestSessionFromRunning_ExplicitFlagsSet_EmptyDoesNotInheritFromArgs(t *testing.T) {
	rs := session.RunningSession{
		Base:             "W:bare",
		Account:          "work",
		Args:             "--account-flag", // legacy effective args
		ExplicitFlags:    "",
		ExplicitFlagsSet: true,
	}
	sess := session.SessionFromRunning(rs)
	if len(sess.Args) != 0 {
		t.Errorf("expected Args nil/empty when ExplicitFlagsSet but ExplicitFlags empty, got %#v", sess.Args)
	}
	if len(sess.ClaudeFlags) != 0 {
		t.Errorf("expected ClaudeFlags nil/empty when ExplicitFlagsSet but ExplicitFlags empty, got %#v", sess.ClaudeFlags)
	}
}

// TestSessionFromRunning_LegacyNoExplicitFlagsPin verifies the legacy path:
// when ExplicitFlagsSet=false and ExplicitFlags is empty, the reconstructed
// session captures the previous effective args as project args (Args).
// This is the documented "legacy effective args preserved" behavior; without
// this branch, restarting an old tab would silently swap to the current
// account defaults.
func TestSessionFromRunning_LegacyNoExplicitFlagsPin(t *testing.T) {
	rs := session.RunningSession{
		Base:             "W:legacy",
		Account:          "work",
		Args:             "--legacy-flag",
		ExplicitFlags:    "",
		ExplicitFlagsSet: false,
	}
	sess := session.SessionFromRunning(rs)
	if !reflect.DeepEqual(sess.Args, []string{"--legacy-flag"}) {
		t.Errorf("expected legacy Args pinned, got %#v", sess.Args)
	}
}

// TestRunningSessionEffectiveProject_UsesProjectOnlyBase verifies that when
// Project is empty in user vars, EffectiveProject treats the stripped Kitty
// base title as the project title under the new project-only contract.
func TestRunningSessionEffectiveProject_UsesProjectOnlyBase(t *testing.T) {
	rs := session.RunningSession{Base: "api", Project: ""}
	if got := rs.EffectiveProject(); got != "api" {
		t.Errorf("EffectiveProject base: got %q, want %q", got, "api")
	}
}

// TestRunningSessionEffectiveProject_LegacyPrefixFallbackUsesProfile keeps the
// old prefix-stripping behavior as a compatibility path when the profile is
// known but the explicit amx_project user var is missing.
func TestRunningSessionEffectiveProject_LegacyPrefixFallbackUsesProfile(t *testing.T) {
	rs := session.RunningSession{Base: "W:api", Profile: "work", Project: ""}
	if got := rs.EffectiveProject(); got != "api" {
		t.Errorf("EffectiveProject legacy fallback: got %q, want %q", got, "api")
	}
}

// TestRunningSessionEffectiveProject_ProjectOnlyColonTitleNotStripped verifies
// that a project-only title that happens to contain a colon is preserved when
// it does not match the current profile's legacy prefix.
func TestRunningSessionEffectiveProject_ProjectOnlyColonTitleNotStripped(t *testing.T) {
	rs := session.RunningSession{Base: "A:b:c", Profile: "work", Project: ""}
	if got := rs.EffectiveProject(); got != "A:b:c" {
		t.Errorf("EffectiveProject colon title: got %q, want %q", got, "A:b:c")
	}
}

// TestRunningSessionEffectiveProject_PrefersUserVar verifies the override
// path: when user vars include amx_project, it wins over the base fallback
// (so a session whose tab title was renamed by the user still reports the
// configured project name).
func TestRunningSessionEffectiveProject_PrefersUserVar(t *testing.T) {
	rs := session.RunningSession{Base: "other", Project: "api"}
	if got := rs.EffectiveProject(); got != "api" {
		t.Errorf("EffectiveProject user-var: got %q, want %q", got, "api")
	}
}

// TestRunningSessionEffectiveProfile_ProfileBeatsAccount verifies the
// modern Profile field is preferred over the legacy Account alias.
func TestRunningSessionEffectiveProfile_ProfileBeatsAccount(t *testing.T) {
	rs := session.RunningSession{Profile: "modern", Account: "legacy"}
	if got := rs.EffectiveProfile(); got != "modern" {
		t.Errorf("EffectiveProfile: got %q, want %q", got, "modern")
	}
}

// TestRunningSessionEffectiveArgs_ArgsBeatsFlags verifies the modern Args
// field is preferred over the legacy Flags alias.
func TestRunningSessionEffectiveArgs_ArgsBeatsFlags(t *testing.T) {
	rs := session.RunningSession{Args: "--new", Flags: "--old"}
	if got := rs.EffectiveArgs(); got != "--new" {
		t.Errorf("EffectiveArgs: got %q, want %q", got, "--new")
	}
}
