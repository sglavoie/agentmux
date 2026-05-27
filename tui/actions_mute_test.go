package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestToggleMuteCmdSetsUserVarAndPushesTitle verifies that toggleMuteCmd both
// flips the cc_mute user var on the anchor window and pushes a refreshed tab
// title that contains the 🔇 glyph. Without the title push, downstream
// rewrite paths only preserve a 🔇 that is already present, so the glyph
// never appears on the tab.
func TestToggleMuteCmdSetsUserVarAndPushesTitle(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")
	rs := session.RunningSession{
		WindowID: 7,
		Base:     "api",
		State:    "bolt",
	}

	cmd := toggleMuteCmd(client, rs)
	msg := cmd()

	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("expected actionDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("toggleMuteCmd returned error: %v", done.err)
	}
	if len(runner.Calls) != 2 {
		t.Fatalf("expected 2 kitten calls (set-user-vars, set-tab-title), got %d: %+v", len(runner.Calls), runner.Calls)
	}

	wantMute := []string{"@", "set-user-vars", "--match=id:7", "cc_mute=1"}
	gotMute := runner.Calls[0].Args
	if len(gotMute) != len(wantMute) {
		t.Fatalf("mute argv length: got %v, want %v", gotMute, wantMute)
	}
	for i := range wantMute {
		if gotMute[i] != wantMute[i] {
			t.Errorf("mute argv[%d]: got %q, want %q (full: %v)", i, gotMute[i], wantMute[i], gotMute)
		}
	}

	gotTitle := runner.Calls[1].Args
	wantTitlePrefix := []string{"@", "set-tab-title", "--match=window_id:7", "--"}
	if len(gotTitle) != len(wantTitlePrefix)+1 {
		t.Fatalf("set-tab-title argv: got %v, want prefix %v + title", gotTitle, wantTitlePrefix)
	}
	for i := range wantTitlePrefix {
		if gotTitle[i] != wantTitlePrefix[i] {
			t.Errorf("set-tab-title argv[%d]: got %q, want %q (full: %v)", i, gotTitle[i], wantTitlePrefix[i], gotTitle)
		}
	}
	gotTitleValue := gotTitle[len(wantTitlePrefix)]
	wantTitleValue := palette.BuildTitleFull("api", "bolt", false, true)
	if gotTitleValue != wantTitleValue {
		t.Errorf("set-tab-title value: got %q, want %q", gotTitleValue, wantTitleValue)
	}
	if !strings.Contains(gotTitleValue, palette.MuteIcon()) {
		t.Errorf("set-tab-title value %q should contain mute icon %q", gotTitleValue, palette.MuteIcon())
	}
}

// TestToggleMuteCmdUnmuteStripsGlyph verifies that toggling a currently-muted
// session writes an empty cc_mute and rebuilds the title without the 🔇 glyph,
// so the tab returns to its un-muted appearance immediately.
func TestToggleMuteCmdUnmuteStripsGlyph(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")
	rs := session.RunningSession{
		WindowID: 9,
		Base:     "api",
		State:    "bolt",
		Muted:    true,
	}

	cmd := toggleMuteCmd(client, rs)
	msg := cmd()

	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("expected actionDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("toggleMuteCmd returned error: %v", done.err)
	}
	if len(runner.Calls) != 2 {
		t.Fatalf("expected 2 kitten calls, got %d: %+v", len(runner.Calls), runner.Calls)
	}

	gotMute := runner.Calls[0].Args
	wantMute := []string{"@", "set-user-vars", "--match=id:9", "cc_mute="}
	for i := range wantMute {
		if i >= len(gotMute) || gotMute[i] != wantMute[i] {
			t.Errorf("unmute argv[%d]: got %v, want %v", i, gotMute, wantMute)
			break
		}
	}

	gotTitleValue := runner.Calls[1].Args[len(runner.Calls[1].Args)-1]
	if strings.Contains(gotTitleValue, palette.MuteIcon()) {
		t.Errorf("unmuted tab title %q must not contain mute icon %q", gotTitleValue, palette.MuteIcon())
	}
}

// TestFindAccountByNameCaseInsensitive verifies that account lookup folds
// case when matching a running session's profile user var against the
// configured profile list. Without this fold, a session launched as
// "Work" (from a prior config) cannot be resolved after the config is
// renamed to "work", and every per-row action that resolves an account
// (close-with-undo, restart, open dir, …) silently degrades — the close
// fires but the undo affordance is dropped because hasAcc is false.
// Mirrors the contract enforced by session.findAccount.
func TestFindAccountByNameCaseInsensitive(t *testing.T) {
	accounts := []config.Account{
		{Name: "work"},
		{Name: "personal"},
	}
	for _, name := range []string{"work", "Work", "WORK"} {
		got, ok := findAccountByName(accounts, name)
		if !ok {
			t.Errorf("findAccountByName(%q): not found", name)
			continue
		}
		if got.Name != "work" {
			t.Errorf("findAccountByName(%q): resolved to %q, want %q", name, got.Name, "work")
		}
	}
	if _, ok := findAccountByName(accounts, "unknown"); ok {
		t.Error("findAccountByName(unknown): unexpectedly found")
	}
}
