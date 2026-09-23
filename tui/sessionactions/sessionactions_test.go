package sessionactions

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// testConfig matches the row testRow produces, so the directory- and
// editor-opening rules resolve a configured project.
func testConfig() *config.Config {
	return &config.Config{
		Accounts: []config.Account{{Name: "work"}},
		Sessions: []config.Session{{Title: "api", Directory: "/home/user/api", Account: "work"}},
	}
}

func testEnv() Env {
	return Env{Config: testConfig()}
}

func testRow() session.RunningSession {
	return session.RunningSession{TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work"}
}

// TestDefinitionsInventory states the whole vocabulary as data. It is the one
// place the inventory is asserted: both surfaces build from this list, so an
// action added, retired, renamed, regrouped, or reordered shows up here.
func TestDefinitionsInventory(t *testing.T) {
	type expected struct {
		id       commands.ID
		group    MenuGroup
		mnemonic Mnemonic
		label    string
		title    string
		aliases  []string
	}
	want := []expected{
		{commands.FocusSession, MenuGroupOpen, Mnemonic{"⏎", "enter"}, "Open session", "Focus Selected Session", []string{"jump", "tab"}},
		{commands.PeekScrollback, MenuGroupOpen, Mnemonic{"e", "e"}, "Peek scrollback", "Peek Scrollback", []string{"pager", "scrollback"}},
		{commands.RestartSession, MenuGroupLifecycle, Mnemonic{"r", "r"}, "Restart", "Restart Session", []string{"relaunch"}},
		{commands.OverrideProfile, MenuGroupLifecycle, Mnemonic{"c", "c"}, "Restart with another profile", "Override Profile", []string{"account", "profile"}},
		{commands.LaunchAnother, MenuGroupOpen, Mnemonic{"C", "C"}, "Launch another session", "Launch Another Session", []string{"duplicate", "new"}},
		{commands.ClearStatusIcon, MenuGroupStatus, Mnemonic{"x", "x"}, "Clear status", "Clear Status Icon", []string{"attention", "badge", "icon"}},
		{commands.MarkActiveIcon, MenuGroupStatus, Mnemonic{"b", "b"}, "Mark active", "Mark Session Active", []string{"bolt", "busy", "working"}},
		{commands.CloseSession, MenuGroupLifecycle, Mnemonic{"X", "X"}, "Close", "Close Session", []string{"exit", "kill"}},
		{commands.OpenDirectoryID, MenuGroupOpen, Mnemonic{"o", "o"}, "Open shell", "Open Directory in Shell", []string{"directory", "shell", "terminal"}},
		{commands.OpenEditorID, MenuGroupOpen, Mnemonic{"O", "O"}, "Open editor", "Open in Editor", []string{"directory", "edit"}},
		{commands.CopyTitleID, MenuGroupOpen, Mnemonic{"y", "y"}, "Copy title", "Copy Session Title", []string{"clipboard", "yank"}},
		{commands.ToggleLockID, MenuGroupStatus, Mnemonic{"L", "L"}, "Lock/Unlock", "Lock Session", []string{"protect", "unlock"}},
		{commands.ToggleMuteID, MenuGroupStatus, Mnemonic{"m", "m"}, "Mute/Unmute", "Mute Session", []string{"notifications", "unmute"}},
		{commands.SearchProject, MenuGroupNone, Mnemonic{}, "", "Search This Project's Past Sessions", []string{"history", "project", "transcripts"}},
		{commands.ShowDetails, MenuGroupNone, Mnemonic{}, "", "Show Session Details", []string{"events", "hooks"}},
		{commands.ShowTimeline, MenuGroupNone, Mnemonic{}, "", "Show Session Timeline", []string{"events", "phases"}},
	}

	got := Definitions()
	if len(got) != len(want) {
		t.Fatalf("vocabulary has %d definitions, want %d", len(got), len(want))
	}
	target := Capture(testRow())
	seen := make(map[commands.ID]bool, len(got))
	keys := make(map[string]commands.ID, len(got))
	for i, definition := range got {
		expect := want[i]
		if definition.ID != expect.id {
			t.Errorf("definition[%d] id = %q, want %q", i, definition.ID, expect.id)
		}
		if definition.MenuGroup != expect.group {
			t.Errorf("%q menu group = %v, want %v", definition.ID, definition.MenuGroup, expect.group)
		}
		if definition.Mnemonic != expect.mnemonic {
			t.Errorf("%q mnemonic = %+v, want %+v", definition.ID, definition.Mnemonic, expect.mnemonic)
		}
		if definition.MenuLabel != expect.label {
			t.Errorf("%q menu label = %q, want %q", definition.ID, definition.MenuLabel, expect.label)
		}
		if got := definition.Title(target); got != expect.title {
			t.Errorf("%q palette title = %q, want %q", definition.ID, got, expect.title)
		}
		if !reflect.DeepEqual(definition.PaletteAliases, expect.aliases) {
			t.Errorf("%q aliases = %v, want %v", definition.ID, definition.PaletteAliases, expect.aliases)
		}
		if definition.PaletteDescription == "" {
			t.Errorf("%q has no palette description", definition.ID)
		}
		if definition.Availability == nil {
			t.Errorf("%q has no availability rule", definition.ID)
		}
		if seen[definition.ID] {
			t.Errorf("duplicate definition ID %q", definition.ID)
		}
		seen[definition.ID] = true
		if definition.Mnemonic.Bound() {
			if other, clash := keys[definition.Mnemonic.Key]; clash {
				t.Errorf("mnemonic %q is claimed by both %q and %q", definition.Mnemonic.Key, other, definition.ID)
			}
			keys[definition.Mnemonic.Key] = definition.ID
		}
	}
}

// TestMenuMembershipIsOneFact checks that the two facts describing a menu entry
// agree: an action in a band has a key and wording, and one in no band has
// neither. MenuGroup is the predicate InMenu reads, so a definition that
// declared a key but forgot its band would otherwise be silently palette-only.
func TestMenuMembershipIsOneFact(t *testing.T) {
	for _, definition := range Definitions() {
		if definition.InMenu() {
			if !definition.Mnemonic.Bound() {
				t.Errorf("%q is in menu band %v but answers no key", definition.ID, definition.MenuGroup)
			}
			if definition.MenuLabel == "" || definition.MenuDescription == "" {
				t.Errorf("%q is in the menu with label %q and description %q", definition.ID, definition.MenuLabel, definition.MenuDescription)
			}
			continue
		}
		if definition.Mnemonic.Bound() {
			t.Errorf("%q is not in the menu but claims key %q", definition.ID, definition.Mnemonic.Key)
		}
		if definition.MenuLabel != "" || definition.MenuDescription != "" {
			t.Errorf("%q is not in the menu but carries menu wording", definition.ID)
		}
	}
}

// TestMenuDefinitionsAreTheRowActionsInMenuOrder states the menu's order as
// data. It is banded — every Open entry, then every Status entry, then every
// Lifecycle entry — and within a band the declaration order in Definitions()
// stands, which is why MenuDefinitions sorts stably by band rather than keeping
// a second hand-written list that could drift from the first.
func TestMenuDefinitionsAreTheRowActionsInMenuOrder(t *testing.T) {
	want := []commands.ID{
		commands.FocusSession, commands.PeekScrollback, commands.LaunchAnother,
		commands.OpenDirectoryID, commands.OpenEditorID, commands.CopyTitleID,
		commands.ClearStatusIcon, commands.MarkActiveIcon, commands.ToggleLockID, commands.ToggleMuteID,
		commands.RestartSession, commands.OverrideProfile, commands.CloseSession,
	}
	var got []commands.ID
	for _, definition := range MenuDefinitions() {
		got = append(got, definition.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("menu definitions = %v, want %v", got, want)
	}
}

// TestMenuDefinitionsAreBanded is the property the order above is an instance
// of: bands never interleave, so the rule the menu draws at a band change is
// drawn exactly twice rather than wherever a declaration happened to land.
func TestMenuDefinitionsAreBanded(t *testing.T) {
	previous := MenuGroup(0)
	for _, definition := range MenuDefinitions() {
		if definition.MenuGroup < previous {
			t.Fatalf("%q is in band %v after band %v", definition.ID, definition.MenuGroup, previous)
		}
		previous = definition.MenuGroup
	}
}

// TestInitialSelectionOpensTheMenuOnOpenSession pins what a freshly opened menu
// has under its cursor. It is the first entry of the first band, which is also
// what enter did before the menu had a cursor at all: pressing `a` then enter
// still focuses the session.
func TestInitialSelectionOpensTheMenuOnOpenSession(t *testing.T) {
	if got := InitialSelection(); got != commands.FocusSession {
		t.Fatalf("InitialSelection() = %q, want %q", got, commands.FocusSession)
	}
	if got := MenuIndexOf(InitialSelection()); got != 0 {
		t.Fatalf("MenuIndexOf(InitialSelection()) = %d, want 0", got)
	}
	definition, ok := Lookup(InitialSelection())
	if !ok {
		t.Fatal("the initial selection is not in the vocabulary")
	}
	if !definition.InMenu() {
		t.Fatalf("the initial selection %q is not offered in the menu", definition.ID)
	}
}

// TestMenuIndexOf covers the whole menu and the two ways an index can be asked
// for something the menu does not offer. Both answer 0 rather than -1, because
// the caller is positioning a cursor: an out-of-range index would be a menu that
// opens with nothing selected.
func TestMenuIndexOf(t *testing.T) {
	menu := MenuDefinitions()
	for i, definition := range menu {
		if got := MenuIndexOf(definition.ID); got != i {
			t.Errorf("MenuIndexOf(%q) = %d, want %d", definition.ID, got, i)
		}
	}
	for _, id := range []commands.ID{commands.ShowDetails, commands.Quit, commands.ID("nonesuch")} {
		if got := MenuIndexOf(id); got != 0 {
			t.Errorf("MenuIndexOf(%q) = %d, want 0 for an action the menu does not offer", id, got)
		}
	}
}

// TestDefinitionsAreIndependentValues guards the fresh-slice contract: two
// surfaces read this vocabulary, and one mutating its copy must not be visible
// to the other.
func TestDefinitionsAreIndependentValues(t *testing.T) {
	first := Definitions()
	first[0].MenuLabel = "tampered"
	if second := Definitions(); second[0].MenuLabel != "Open session" {
		t.Fatalf("second read saw %q, want the declared label", second[0].MenuLabel)
	}
	// The alias slices are handed straight to the palette, so they are part of
	// the same contract: a cached backing array would let one caller's sort or
	// append reach the other's rows.
	before := Definitions()
	aliases := append([]string(nil), before[0].PaletteAliases...)
	before[0].PaletteAliases[0] = "tampered"
	after := Definitions()
	if !reflect.DeepEqual(after[0].PaletteAliases, aliases) {
		t.Fatalf("second read saw aliases %v, want %v", after[0].PaletteAliases, aliases)
	}
}

func TestLookup(t *testing.T) {
	definition, ok := Lookup(commands.CloseSession)
	if !ok {
		t.Fatal("close session is not in the vocabulary")
	}
	if definition.MenuLabel != "Close" {
		t.Fatalf("close session label = %q", definition.MenuLabel)
	}
	if _, ok := Lookup(commands.Quit); ok {
		t.Fatal("quit is an app command, not a session action")
	}
}

func TestMenuGroupString(t *testing.T) {
	for group, want := range map[MenuGroup]string{
		MenuGroupNone:      "",
		MenuGroupOpen:      "Open",
		MenuGroupStatus:    "Status",
		MenuGroupLifecycle: "Lifecycle",
		MenuGroup(99):      "",
	} {
		if got := group.String(); got != want {
			t.Errorf("MenuGroup(%d).String() = %q, want %q", group, got, want)
		}
	}
}

// TestAvailabilityWithoutATarget is the fail-closed reading: with nothing
// captured, every session action refuses and says so.
func TestAvailabilityWithoutATarget(t *testing.T) {
	for _, definition := range Definitions() {
		got := definition.AvailabilityFor(testEnv(), Target{})
		if got != commands.Unavailable(commands.ReasonNoSession) {
			t.Errorf("%q availability with no target = %+v", definition.ID, got)
		}
	}
}

func TestAvailabilityMatrix(t *testing.T) {
	withState := func(state string) session.RunningSession {
		row := testRow()
		row.State = state
		return row
	}
	withLock := func() session.RunningSession {
		row := testRow()
		row.Locked = true
		return row
	}
	withProject := func(project string) session.RunningSession {
		row := testRow()
		row.Project = project
		return row
	}

	cases := []struct {
		name string
		id   commands.ID
		row  session.RunningSession
		env  Env
		want commands.Availability
	}{
		{"focus an active row", commands.FocusSession, withState("bolt"), testEnv(), commands.Available()},
		{"peek an active row", commands.PeekScrollback, withState("bolt"), testEnv(), commands.Available()},
		{"peek an idle row", commands.PeekScrollback, testRow(), testEnv(), commands.Unavailable(commands.ReasonPeekInactive)},
		{"peek an attention row", commands.PeekScrollback, withState("key"), testEnv(), commands.Unavailable(commands.ReasonPeekInactive)},
		{"restart a locked row", commands.RestartSession, withLock(), testEnv(), commands.Unavailable(commands.ReasonLocked)},
		{"override a locked row", commands.OverrideProfile, withLock(), testEnv(), commands.Unavailable(commands.ReasonLocked)},
		{"close a locked row", commands.CloseSession, withLock(), testEnv(), commands.Unavailable(commands.ReasonLocked)},
		{"restart an unlocked row", commands.RestartSession, withState("bolt"), testEnv(), commands.Available()},
		// Lock and mute stay available on a locked row: unlocking and quieting a
		// runaway tab are exactly what a locked row still has to allow.
		{"lock a locked row", commands.ToggleLockID, withLock(), testEnv(), commands.Available()},
		{"mute a locked row", commands.ToggleMuteID, withLock(), testEnv(), commands.Available()},
		{"launch from a locked row", commands.LaunchAnother, withLock(), testEnv(), commands.Available()},
		{"clear an attention row", commands.ClearStatusIcon, withState("key"), testEnv(), commands.Available()},
		{"clear an active row", commands.ClearStatusIcon, withState("bolt"), testEnv(), commands.Unavailable(commands.ReasonStatusNotClear)},
		{"clear an idle row", commands.ClearStatusIcon, testRow(), Env{
			Config:        testConfig(),
			LastActivity:  map[int]time.Time{101: time.Now().Add(-time.Hour)},
			IdleThreshold: time.Minute,
		}, commands.Unavailable(commands.ReasonStatusNotClear)},
		{"open a configured directory", commands.OpenDirectoryID, testRow(), testEnv(), commands.Available()},
		{"open a configured editor", commands.OpenEditorID, testRow(), testEnv(), commands.Available()},
		{"open an unconfigured directory", commands.OpenDirectoryID, withProject("orphan"), testEnv(), commands.Unavailable(commands.ReasonNoProject)},
		{"open an unconfigured editor", commands.OpenEditorID, withProject("orphan"), testEnv(), commands.Unavailable(commands.ReasonNoProject)},
		// A host that has not loaded configuration yet reads as "no project
		// here" rather than panicking on a nil pointer.
		{"open a directory with no configuration", commands.OpenDirectoryID, testRow(), Env{}, commands.Unavailable(commands.ReasonNoProject)},
		{"copy a title", commands.CopyTitleID, testRow(), testEnv(), commands.Available()},
		{"search project history", commands.SearchProject, testRow(), testEnv(), commands.Available()},
		{"show details", commands.ShowDetails, testRow(), testEnv(), commands.Available()},
		{"show a timeline", commands.ShowTimeline, testRow(), testEnv(), commands.Available()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			definition, ok := Lookup(tc.id)
			if !ok {
				t.Fatalf("%q is not in the vocabulary", tc.id)
			}
			if got := definition.AvailabilityFor(tc.env, Capture(tc.row)); got != tc.want {
				t.Fatalf("availability = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDynamicPaletteTitles(t *testing.T) {
	locked := testRow()
	locked.Locked = true
	muted := testRow()
	muted.Muted = true

	cases := []struct {
		id     commands.ID
		target Target
		want   string
	}{
		{commands.ToggleLockID, Capture(testRow()), "Lock Session"},
		{commands.ToggleLockID, Capture(locked), "Unlock Session"},
		{commands.ToggleMuteID, Capture(testRow()), "Mute Session"},
		{commands.ToggleMuteID, Capture(muted), "Unmute Session"},
		// With nothing captured the title names the direction an operator would
		// be offered once a row is selected, not a blank.
		{commands.ToggleLockID, Target{}, "Lock Session"},
		{commands.ToggleMuteID, Target{}, "Mute Session"},
	}
	for _, tc := range cases {
		definition, ok := Lookup(tc.id)
		if !ok {
			t.Fatalf("%q is not in the vocabulary", tc.id)
		}
		if got := definition.Title(tc.target); got != tc.want {
			t.Errorf("%q title = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// TestZeroDefinitionIsPermissive covers the two safe accessors: a definition
// that declares no rule is offered rather than silently dimmed, and one that
// declares no title reads as empty rather than panicking.
func TestZeroDefinitionIsPermissive(t *testing.T) {
	var definition Definition
	if got := definition.AvailabilityFor(Env{}, Target{}); got != commands.Available() {
		t.Errorf("zero definition availability = %+v, want available", got)
	}
	if got := definition.Title(Target{}); got != "" {
		t.Errorf("zero definition title = %q, want empty", got)
	}
	if definition.InMenu() {
		t.Error("zero definition should not be in the menu")
	}
}
