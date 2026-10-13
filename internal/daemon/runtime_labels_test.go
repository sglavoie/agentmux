package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// tmuxSessionIn returns the v2 snapshot's session for runtime.
func tmuxSessionIn(t *testing.T, h *Handler, runtime runtimesession.ID) protocol.CompanionSession {
	t.Helper()
	snap := h.buildCompanionSnapshots(time.Now())[protocol.CompanionSnapshotVersionRuntime]
	for _, s := range snap.Sessions {
		if s.Runtime != nil && s.Runtime.SessionID == string(runtime) {
			return s
		}
	}
	t.Fatalf("runtime session %s is not in the v2 snapshot: %+v", runtime, snap.Sessions)
	return protocol.CompanionSession{}
}

// TestRuntimeSessionsAreLabeledForCompanions: a tmux session is grouped under
// its registry project from its first hook, gains the profile its pane
// metadata holds on the next pass, and is not read again once it has it. A
// failed read keeps the project and is retried later; an ended session's label
// is forgotten. A v1 subscriber never sees any of it.
func TestRuntimeSessionsAreLabeledForCompanions(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	directory.mu.Lock()
	record := directory.records[onTmux]
	record.Project = "api"
	directory.records[onTmux] = record
	directory.mu.Unlock()

	reads := 0
	var failure error
	h.SetRuntimeSessionLabeler(func(record runtimesession.Record) (RuntimeSessionLabel, error) {
		reads++
		if failure != nil {
			return RuntimeSessionLabel{}, failure
		}
		return RuntimeSessionLabel{Project: "API", Profile: "work"}, nil
	})

	requireAccepted(t, h.Handle(runtimeHook(t, onTmux, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	if s := tmuxSessionIn(t, h, onTmux); s.Project != "api" || s.Profile != "" || reads != 0 {
		t.Fatalf("after the first hook: project %q profile %q, %d backend reads; want the registry project and no read on the hook path", s.Project, s.Profile, reads)
	}

	now := time.Now()
	failure = errors.New("server unreachable")
	h.labelRuntimeSessions(now)
	if s := tmuxSessionIn(t, h, onTmux); s.Project != "api" || s.Profile != "" || reads != 1 {
		t.Fatalf("after a failed read: project %q profile %q, %d reads", s.Project, s.Profile, reads)
	}
	h.labelRuntimeSessions(now.Add(time.Second))
	if reads != 1 {
		t.Fatalf("a failed read was retried at once: %d reads", reads)
	}

	failure = nil
	h.labelRuntimeSessions(now.Add(runtimeLabelRetry))
	if s := tmuxSessionIn(t, h, onTmux); s.Project != "API" || s.Profile != "work" || reads != 2 {
		t.Fatalf("after the read: project %q profile %q, %d reads", s.Project, s.Profile, reads)
	}
	h.labelRuntimeSessions(now.Add(10 * runtimeLabelRetry))
	if reads != 2 {
		t.Fatalf("a labeled session was read again: %d reads", reads)
	}

	v1 := h.buildCompanionSnapshots(time.Now())[protocol.CompanionSnapshotVersion]
	for _, s := range v1.Sessions {
		if s.WindowID == 0 || s.Runtime != nil || s.Profile == "work" {
			t.Fatalf("v1 lists a tmux session: %+v", s)
		}
	}

	directory.put(onTmux, runtimesession.StateEnded, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	h.maintainRuntimeSessions(now.Add(11 * runtimeLabelRetry))
	h.stateMu.Lock()
	left := len(h.runtimeLabels)
	h.stateMu.Unlock()
	if left != 0 {
		t.Fatalf("an ended session kept its label: %d left", left)
	}
}

// TestTmuxRuntimeSessionLabelReadsPaneMetadata: against a real private tmux
// server, the label is the project and profile the launch wrote on the pane,
// and a record naming another server generation, another session's pane, or a
// pane that is gone reads nothing.
func TestTmuxRuntimeSessionLabelReadsPaneMetadata(t *testing.T) {
	fixture := tmuxtest.New(t)
	id := newRuntimeID(t)
	directory := t.TempDir()
	launched, err := fixture.Backend.LaunchTarget(backend.LaunchSpec{
		Title:     "api",
		Directory: directory,
		Vars: map[string]string{
			"amx_project":            "api",
			"amx_profile":            "work",
			"amx_directory":          directory,
			"amx_runtime_session_id": string(id),
		},
		Argv: []string{"/bin/sh", "-c", "exec sleep 600"},
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	record := runtimesession.Record{
		ID:      id,
		Project: "registry-project",
		State:   runtimesession.StateLive,
		Target:  &runtimesession.Target{Instance: launched.Instance, Ref: launched.Pane.ID},
	}

	label, err := TmuxRuntimeSessionLabel(record)
	if err != nil || label != (RuntimeSessionLabel{Project: "api", Profile: "work", Directory: directory}) {
		t.Fatalf("label = %+v, %v", label, err)
	}

	other := record
	other.ID = newRuntimeID(t)
	if _, err := TmuxRuntimeSessionLabel(other); err == nil {
		t.Fatal("read the label of a pane another runtime session holds")
	}
	stale := record
	target := *record.Target
	target.Instance.Generation = "0-0"
	stale.Target = &target
	if _, err := TmuxRuntimeSessionLabel(stale); err == nil {
		t.Fatal("read a label from another server generation")
	}
	gone := record
	missing := *record.Target
	missing.Ref = "%999"
	gone.Target = &missing
	if _, err := TmuxRuntimeSessionLabel(gone); err == nil {
		t.Fatal("read a label for a pane that is not there")
	}
	kitty := record
	onKitty := *record.Target
	onKitty.Instance.Kind = runtimesession.BackendKitty
	kitty.Target = &onKitty
	if _, err := TmuxRuntimeSessionLabel(kitty); !errors.Is(err, errRuntimeLabelUnsupported) {
		t.Fatalf("a Kitty record: %v", err)
	}
}

// TestRuntimeSessionColorFollowsThePickAndTheProfile: a tmux session is
// published on its profile's color, in the palette's bright background. A color picked for it replaces the profile's as
// soon as a reconciliation pass reads the pick, survives a later labeling
// pass, and gives way to the profile's again when the pick is removed. A
// Kitty slot never takes a color from the tmux observation.
func TestRuntimeSessionColorFollowsThePickAndTheProfile(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	onKitty, onTmux := newRuntimeID(t), newRuntimeID(t)
	directory.put(onKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	h.SetProfileColors(map[string]string{"Work": "blue"})
	h.SetRuntimeSessionLabeler(func(runtimesession.Record) (RuntimeSessionLabel, error) {
		return RuntimeSessionLabel{Project: "API", Profile: "work"}, nil
	})
	for _, runtime := range []runtimesession.ID{onKitty, onTmux} {
		requireAccepted(t, h.Handle(runtimeHook(t, runtime, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-" + string(runtime)})))
	}
	color := func(name string) string {
		t.Helper()
		c, err := palette.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		return c.BrightBg
	}

	now := time.Now()
	h.labelRuntimeSessions(now)
	if got := tmuxSessionIn(t, h, onTmux).TabColor; got != color("blue") {
		t.Fatalf("tab color with no pick = %q; want the profile's, %q", got, color("blue"))
	}

	h.observeRuntimeColors([]tmux.Colored{{ID: onTmux, Color: "red"}, {ID: onKitty, Color: "red"}, {ID: newRuntimeID(t), Color: "red"}})
	if got := tmuxSessionIn(t, h, onTmux).TabColor; got != color("red") {
		t.Fatalf("tab color after a pick = %q; want %q", got, color("red"))
	}
	h.stateMu.Lock()
	_, kittyLabeled := h.runtimeLabels[string(onKitty)]
	h.stateMu.Unlock()
	if kittyLabeled {
		t.Fatal("a Kitty slot took a color from the tmux observation")
	}
	h.labelRuntimeSessions(now.Add(10 * runtimeLabelRetry))
	if got := tmuxSessionIn(t, h, onTmux).TabColor; got != color("red") {
		t.Fatalf("tab color after a labeling pass = %q; want the pick kept", got)
	}

	h.observeRuntimeColors([]tmux.Colored{{ID: onTmux, Color: "no-such-color"}})
	if got := tmuxSessionIn(t, h, onTmux).TabColor; got != "" {
		t.Fatalf("tab color for a name the palette lacks = %q; want none", got)
	}
	h.observeRuntimeColors([]tmux.Colored{{ID: onTmux}})
	if got := tmuxSessionIn(t, h, onTmux).TabColor; got != color("blue") {
		t.Fatalf("tab color after the pick was removed = %q; want the profile's again", got)
	}
}

// TestRuntimeSessionNameFollowsThePane: a tmux session is published under the
// name a reconciliation pass read from its pane, keeps it across a later
// labeling pass, and loses it when the name is removed. A Kitty slot never
// takes a name from the tmux observation.
func TestRuntimeSessionNameFollowsThePane(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	onKitty, onTmux := newRuntimeID(t), newRuntimeID(t)
	directory.put(onKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	h.SetRuntimeSessionLabeler(func(runtimesession.Record) (RuntimeSessionLabel, error) {
		return RuntimeSessionLabel{Project: "API", Profile: "work"}, nil
	})
	for _, runtime := range []runtimesession.ID{onKitty, onTmux} {
		requireAccepted(t, h.Handle(runtimeHook(t, runtime, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-" + string(runtime)})))
	}

	now := time.Now()
	h.labelRuntimeSessions(now)
	if got := tmuxSessionIn(t, h, onTmux).Name; got != "" {
		t.Fatalf("name before any was given = %q", got)
	}
	h.observeRuntimeNames([]tmux.Named{{ID: onTmux, Name: "billing fix"}, {ID: onKitty, Name: "billing fix"}, {ID: newRuntimeID(t), Name: "billing fix"}})
	if got := tmuxSessionIn(t, h, onTmux).Name; got != "billing fix" {
		t.Fatalf("name after naming = %q", got)
	}
	h.stateMu.Lock()
	_, kittyLabeled := h.runtimeLabels[string(onKitty)]
	h.stateMu.Unlock()
	if kittyLabeled {
		t.Fatal("a Kitty slot took a name from the tmux observation")
	}
	h.labelRuntimeSessions(now.Add(10 * runtimeLabelRetry))
	if got := tmuxSessionIn(t, h, onTmux).Name; got != "billing fix" {
		t.Fatalf("name after a labeling pass = %q; want it kept", got)
	}
	h.observeRuntimeNames([]tmux.Named{{ID: onTmux}})
	if got := tmuxSessionIn(t, h, onTmux).Name; got != "" {
		t.Fatalf("name after it was removed = %q", got)
	}
}

// TestRuntimeSessionHarnessIconFollowsTheTitle: a tmux session is published
// with the harness icon its title carries, the one its launch put there, and
// follows the title when the icon picker replaces or clears it. Markers ahead
// of the icon do not hide it, and an emoji that is no configured harness icon
// is part of the title, not an icon.
func TestRuntimeSessionHarnessIconFollowsTheTitle(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	h.SetHarnessIcons((&config.Config{HarnessEmojis: map[string]string{"claude": "🦊", "codex": "📟"}}).HarnessIcons())
	h.SetRuntimeSessionLabeler(func(runtimesession.Record) (RuntimeSessionLabel, error) {
		return RuntimeSessionLabel{Project: "API", Profile: "work"}, nil
	})
	requireAccepted(t, h.Handle(runtimeHook(t, onTmux, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-" + string(onTmux)})))

	now := time.Now()
	h.labelRuntimeSessions(now)
	if got := tmuxSessionIn(t, h, onTmux).HarnessIcon; got != "" {
		t.Fatalf("icon before the title was read = %q", got)
	}
	for _, step := range []struct{ title, want string }{
		{"🦊API", "🦊"},
		{palette.BuildTitleParts(palette.TitleParts{Base: "📟API", Starred: true}), "📟"},
		{"🚁API", ""},
		{"API", ""},
	} {
		h.observeRuntimeTitles([]tmux.Titled{{ID: onTmux, Title: step.title}, {ID: newRuntimeID(t), Title: "🦊stray"}})
		if got := tmuxSessionIn(t, h, onTmux).HarnessIcon; got != step.want {
			t.Fatalf("icon for title %q = %q; want %q", step.title, got, step.want)
		}
	}
	h.observeRuntimeTitles([]tmux.Titled{{ID: onTmux, Title: "🦊API"}})
	h.labelRuntimeSessions(now.Add(10 * runtimeLabelRetry))
	if got := tmuxSessionIn(t, h, onTmux).HarnessIcon; got != "🦊" {
		t.Fatalf("icon after a labeling pass = %q; want it kept", got)
	}
}
