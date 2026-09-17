package tui

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// This file is where the two dashboard entry points are held to one vocabulary.
// The inventory itself is asserted in tui/sessionactions; what is checked here
// is that neither surface adds to it, drops from it, or reworded part of it on
// the way through — and that the menu's mnemonics are spelled the way the key
// constants in keys.go spell the same keystrokes.

// TestOverlayOffersEveryMenuDefinition checks the per-row overlay against the
// shared menu inventory: same actions, same order, same wording, and a runner
// for each. A definition whose runner went missing would be dropped by
// actionsFor rather than rendered as a dead row, so this is what reports it.
func TestOverlayOffersEveryMenuDefinition(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10},
	})
	m = openActionsOverlay(t, m)

	definitions := sessionactions.MenuDefinitions()
	actions := actionsFor(m)
	if len(actions) != len(definitions) {
		t.Fatalf("overlay offers %d actions, the vocabulary declares %d menu entries", len(actions), len(definitions))
	}
	for i, definition := range definitions {
		action := actions[i]
		if action.ID != definition.ID {
			t.Errorf("action[%d] id = %q, want %q", i, action.ID, definition.ID)
		}
		if action.KeyMatch != definition.Mnemonic.Key || action.KeyDisplay != definition.Mnemonic.Display {
			t.Errorf("%q key = %q/%q, want %q/%q", definition.ID, action.KeyDisplay, action.KeyMatch, definition.Mnemonic.Display, definition.Mnemonic.Key)
		}
		if action.Label != definition.MenuLabel {
			t.Errorf("%q label = %q, want %q", definition.ID, action.Label, definition.MenuLabel)
		}
		if action.Description != definition.MenuDescription {
			t.Errorf("%q description = %q, want %q", definition.ID, action.Description, definition.MenuDescription)
		}
		if action.Run == nil {
			t.Errorf("%q has no runner", definition.ID)
		}
		if action.Availability == nil {
			t.Errorf("%q has no availability closure", definition.ID)
		}
		if action.Band != definition.MenuGroup.String() {
			t.Errorf("%q band = %q, want %q", definition.ID, action.Band, definition.MenuGroup.String())
		}
	}
}

// TestPaletteOffersEverySessionDefinition checks the palette side of the same
// declaration: the catalog's session block is the session actions, in
// vocabulary order, carrying the vocabulary's own title, description, and
// aliases.
//
// The block is located by group rather than assumed to open the catalog. It is
// the second of three now — the destinations come first, because a palette
// opened on a cold dashboard is most often a way to go somewhere — and the
// property being checked was never about position.
func TestPaletteOffersEverySessionDefinition(t *testing.T) {
	target := session.RunningSession{TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work", State: "bolt"}
	m, ctx, _ := commandTestModel(target)

	definitions := sessionactions.Definitions()
	catalog := commandCatalog()
	var block []commands.Command[Model]
	for _, candidate := range catalog {
		if candidate.Group == sessionactions.PaletteGroup {
			block = append(block, candidate)
		}
	}
	if len(block) != len(definitions) {
		t.Fatalf("catalog's session group has %d commands, the vocabulary declares %d", len(block), len(definitions))
	}
	for i, definition := range definitions {
		candidate := block[i]
		if candidate.ID != definition.ID {
			t.Errorf("command[%d] id = %q, want %q", i, candidate.ID, definition.ID)
		}
		if candidate.Group != sessionactions.PaletteGroup {
			t.Errorf("%q group = %q, want %q", definition.ID, candidate.Group, sessionactions.PaletteGroup)
		}
		if got, want := candidate.DisplayTitle(m, ctx), definition.Title(sessionactions.FromContext(ctx)); got != want {
			t.Errorf("%q title = %q, want %q", definition.ID, got, want)
		}
		if candidate.Description != definition.PaletteDescription {
			t.Errorf("%q description = %q, want %q", definition.ID, candidate.Description, definition.PaletteDescription)
		}
		if !reflect.DeepEqual(candidate.Aliases, definition.PaletteAliases) {
			t.Errorf("%q aliases = %v, want %v", definition.ID, candidate.Aliases, definition.PaletteAliases)
		}
		if candidate.Execute == nil {
			t.Errorf("%q has no executor", definition.ID)
		}
	}
	// The block is contiguous, so it is one heading in the palette rather than a
	// scattering of session rows through the other two groups.
	first, last := -1, -1
	for i, candidate := range catalog {
		if candidate.Group != sessionactions.PaletteGroup {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if last-first+1 != len(definitions) {
		t.Errorf("session group spans catalog[%d:%d] for %d commands; it is not contiguous", first, last+1, len(definitions))
	}
}

// TestMenuMnemonicsMatchDashboardKeyConstants is the one place the shared
// vocabulary and keys.go are held to the same spelling. The vocabulary names no
// host, so it cannot read the root's unexported key constants; this is what
// stops the two from drifting.
func TestMenuMnemonicsMatchDashboardKeyConstants(t *testing.T) {
	want := map[commands.ID]string{
		commands.FocusSession:    keyEnter,
		commands.PeekScrollback:  keyE,
		commands.RestartSession:  keyR,
		commands.OverrideProfile: keyC,
		commands.LaunchAnother:   keyDashboardLaunch,
		commands.ClearStatusIcon: keyClearStatus,
		commands.CloseSession:    keyClose,
		commands.OpenDirectoryID: keyO,
		commands.OpenEditorID:    keyOpenEditor,
		commands.CopyTitleID:     keyY,
		commands.ToggleLockID:    keyLock,
		commands.ToggleMuteID:    keyM,
	}
	definitions := sessionactions.MenuDefinitions()
	if len(definitions) != len(want) {
		t.Fatalf("the menu declares %d entries, this map names %d", len(definitions), len(want))
	}
	for _, definition := range definitions {
		key, ok := want[definition.ID]
		if !ok {
			t.Errorf("menu entry %q has no key-constant mapping", definition.ID)
			continue
		}
		if definition.Mnemonic.Key != key {
			t.Errorf("%q mnemonic = %q, want the key constant %q", definition.ID, definition.Mnemonic.Key, key)
		}
	}
}

// TestMenuNavigationKeysShadowNoMnemonic is the mnemonic-collision guard the
// selectable menu made necessary. handleActionsKey answers navigation before it
// scans the mnemonics, so any key in both sets belongs to navigation and its
// action becomes unreachable by letter — silently, because the menu would still
// draw the letter against the row. Enter is the one deliberate overlap and is
// excluded here: the menu takes it, ImmediateKey hides it, and enter on the
// initial selection still runs focus-session.
func TestMenuNavigationKeysShadowNoMnemonic(t *testing.T) {
	navigation := []string{
		keyEsc, keyQ, keyCtrlC,
		keyJ, keyK, keyDown, keyUp,
		keyPgDown, keyPgUp, keyCtrlF, keyCtrlB, keyCtrlD, keyCtrlU,
		keyG, keyGG,
	}
	taken := make(map[string]bool, len(navigation))
	for _, key := range navigation {
		taken[key] = true
	}
	for _, definition := range sessionactions.MenuDefinitions() {
		if definition.Mnemonic.Key == commands.MenuExecuteKey {
			continue
		}
		if taken[definition.Mnemonic.Key] {
			t.Errorf("menu entry %q claims %q, which the menu's navigation answers first",
				definition.ID, definition.Mnemonic.Key)
		}
	}
}

// TestBothSurfacesAgreeOnAvailability is the point of the shared declaration:
// for the same row, the overlay entry and the palette row reach the same
// verdict. Before the vocabulary was one file, these were two rules that
// happened to agree.
func TestBothSurfacesAgreeOnAvailability(t *testing.T) {
	base := session.RunningSession{TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work"}
	withState := func(state string) session.RunningSession {
		row := base
		row.State = state
		return row
	}
	locked := base
	locked.Locked = true
	muted := base
	muted.Muted = true
	orphan := base
	orphan.Project = "orphan"

	cases := []struct {
		name  string
		row   session.RunningSession
		setup func(*Model)
	}{
		{"active", withState("bolt"), nil},
		{"attention", withState("key"), nil},
		{"stopped", withState("stop"), nil},
		{"locked", locked, nil},
		{"muted", muted, nil},
		{"unconfigured project", orphan, nil},
		{"idle", base, func(m *Model) {
			m.idleThreshold = time.Minute
			m.lastActivity = map[int]time.Time{101: time.Now().Add(-time.Hour)}
		}},
	}
	refusals := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := loadSessions(testModelWithSessions(), []session.RunningSession{tc.row})
			if tc.setup != nil {
				tc.setup(&m)
			}
			ctx := captureCommandContext(m)
			overlay := openActionsOverlay(t, m)
			for _, action := range actionsFor(overlay) {
				candidate := commandByID(t, action.ID)
				want := candidate.AvailabilityFor(m, ctx)
				if !want.Available {
					refusals++
				}
				if got := action.Available(overlay); got != want.Available {
					t.Errorf("%q: overlay availability = %v, palette = %+v", action.ID, got, want)
				}
			}
		})
	}
	// Agreement is cheap to satisfy if nothing ever refuses, so the matrix has to
	// have exercised at least one unavailable verdict to mean anything.
	if refusals == 0 {
		t.Fatal("no row in the matrix made any action unavailable; the agreement is vacuous")
	}
}
