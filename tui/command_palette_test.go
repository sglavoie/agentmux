package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
)

func TestPaletteSubsequenceScorePrefersExactPrefix(t *testing.T) {
	prefix, ok := paletteSubsequenceScore("OPEN", "open in editor")
	if !ok {
		t.Fatal("case-insensitive prefix did not match")
	}
	substring, ok := paletteSubsequenceScore("open", "project open")
	if !ok {
		t.Fatal("substring did not match")
	}
	sparse, ok := paletteSubsequenceScore("opie", "open in editor")
	if !ok {
		t.Fatal("subsequence did not match")
	}
	if prefix <= substring || substring <= sparse {
		t.Fatalf("scores prefix=%d substring=%d sparse=%d, want strict preference", prefix, substring, sparse)
	}
	if _, ok := paletteSubsequenceScore("missing", "open in editor"); ok {
		t.Fatal("non-subsequence unexpectedly matched")
	}
}

func TestPaletteDiscoversEveryRegisteredActionAndNavigationKind(t *testing.T) {
	fixture := workspaceAttentionFixture()
	driver, _ := NewUITestFixtureDriver(fixture, fixture.Size)
	m := driver.Model().enterPalette()
	entries := paletteEntries(m)

	foundActions := make(map[actions.ID]bool)
	foundKinds := make(map[paletteEntryKind]bool)
	for _, entry := range entries {
		foundKinds[entry.kind] = true
		if entry.kind == paletteEntryAction {
			foundActions[entry.actionID] = true
		}
	}
	for _, definition := range sharedActionRegistry.Definitions() {
		if !foundActions[definition.ID] {
			t.Errorf("registered action %q is not discoverable", definition.ID)
		}
	}
	for _, kind := range []paletteEntryKind{paletteEntryWorkspace, paletteEntryLive, paletteEntryConversation, paletteEntryPlan, paletteEntrySearch} {
		if !foundKinds[kind] {
			t.Errorf("navigation kind %q is not discoverable", kind.label())
		}
	}
}

func TestPaletteUnavailableActionShowsReasonAndDoesNotExit(t *testing.T) {
	fixture := workspaceAttentionFixture()
	driver, _ := NewUITestFixtureDriver(fixture, fixture.Size)
	if err := driver.Key(keyPalette); err != nil {
		t.Fatal(err)
	}
	if err := driver.Text("close"); err != nil {
		t.Fatal(err)
	}
	if err := driver.Key(keyEnter); err != nil {
		t.Fatal(err)
	}
	m := driver.Model()
	if m.mode != ModePalette {
		t.Fatalf("mode = %v, want palette", m.mode)
	}
	if !strings.Contains(m.paletteMessage, "running session") {
		t.Fatalf("palette message = %q, want missing running-session reason", m.paletteMessage)
	}
}

func TestPaletteWorkspaceItemContextAndReturnFocus(t *testing.T) {
	fixture := workspaceAttentionFixture()
	driver, _ := NewUITestFixtureDriver(fixture, fixture.Size)
	if err := driver.Key(keyEnter); err != nil {
		t.Fatal(err)
	}
	before := driver.Model()
	if before.workspaceFocus != workspaceFocusDetail {
		t.Fatalf("workspace focus = %v, want detail", before.workspaceFocus)
	}
	if err := driver.Key(keyPalette); err != nil {
		t.Fatal(err)
	}
	request := paletteContextRequest(driver.Model())
	if request.Session == nil || request.Session.WindowID != 11 {
		t.Fatalf("palette session context = %+v, want window 11", request.Session)
	}
	if request.Project == nil || request.Project.Title != "api" {
		t.Fatalf("palette project context = %+v, want api", request.Project)
	}
	if err := driver.Key(keyEsc); err != nil {
		t.Fatal(err)
	}
	after := driver.Model()
	if after.mode != ModeWorkspace || after.workspaceFocus != before.workspaceFocus || after.workspaceDetailCursor != before.workspaceDetailCursor {
		t.Fatalf("return state = mode %v focus %v cursor %d; want workspace focus %v cursor %d", after.mode, after.workspaceFocus, after.workspaceDetailCursor, before.workspaceFocus, before.workspaceDetailCursor)
	}
}

func TestPaletteLaunchReusesProfilePicker(t *testing.T) {
	fixture := workspaceProfilesFixture()
	driver, _ := NewUITestFixtureDriver(fixture, fixture.Size)
	if err := driver.Key(keyPalette); err != nil {
		t.Fatal(err)
	}
	if err := driver.Text("launch new"); err != nil {
		t.Fatal(err)
	}
	if err := driver.Key(keyEnter); err != nil {
		t.Fatal(err)
	}
	m := driver.Model()
	if m.mode != ModeConfigPicker {
		t.Fatalf("mode = %v, want config picker", m.mode)
	}
	if m.configPickerPurpose != ConfigPickerDashboardLaunch {
		t.Fatalf("picker purpose = %v, want dashboard launch", m.configPickerPurpose)
	}
	if m.configPickerReturnMode != ModeWorkspace {
		t.Fatalf("picker return mode = %v, want workspace", m.configPickerReturnMode)
	}
	if m.targetSession.EffectiveProject() != "platform" {
		t.Fatalf("picker target = %+v, want platform", m.targetSession)
	}
}

func TestPaletteActionExecutesThroughSharedHandler(t *testing.T) {
	fixture := workspaceAttentionFixture()
	driver, client := NewUITestFixtureDriver(fixture, fixture.Size)
	for _, key := range []string{keyEnter, keyPalette} {
		if err := driver.Key(key); err != nil {
			t.Fatal(err)
		}
	}
	if err := driver.Text("lock/unlock"); err != nil {
		t.Fatal(err)
	}
	if err := driver.Key(keyEnter); err != nil {
		t.Fatal(err)
	}
	if got := len(client.CallsFor("SetUserVars")); got != 1 {
		t.Fatalf("SetUserVars calls = %d, want 1 registry-backed lock action", got)
	}
}
