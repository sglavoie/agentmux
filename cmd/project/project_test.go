package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/layout"
)

// recordingKittyClient is a Kitty that holds a window tree and grows it: a
// LaunchTab adds a tab carrying the launched title, so the real launch path's
// colour retry and the command's new-tab lookup both see what a live Kitty
// would show them.
type recordingKittyClient struct {
	windows  []kitty.OSWindow
	launched []kitty.LaunchOpts
	nextID   int
}

func (c *recordingKittyClient) ListWindows() ([]kitty.OSWindow, error) { return c.windows, nil }
func (c *recordingKittyClient) LaunchTab(opts kitty.LaunchOpts) error {
	c.launched = append(c.launched, opts)
	c.nextID++
	tab := kitty.Tab{ID: 500 + c.nextID, Title: opts.Title, Windows: []kitty.Window{{ID: 900 + c.nextID}}}
	if len(c.windows) == 0 {
		c.windows = []kitty.OSWindow{{ID: 1, IsFocused: true}}
	}
	c.windows[0].Tabs = append(c.windows[0].Tabs, tab)
	return nil
}
func (*recordingKittyClient) LaunchOverlay(kitty.LaunchOpts) error { return nil }
func (*recordingKittyClient) CloseTab(string) error                { return nil }
func (*recordingKittyClient) MoveTab(string, int) error            { return nil }
func (*recordingKittyClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (*recordingKittyClient) SetTabTitle(string, string) error            { return nil }
func (*recordingKittyClient) SetUserVars(string, map[string]string) error { return nil }
func (*recordingKittyClient) FocusTab(string) error                       { return nil }
func (*recordingKittyClient) FocusWindow(string) error                    { return nil }
func (*recordingKittyClient) GetText(string, string) (string, error)      { return "", nil }
func (*recordingKittyClient) LaunchOverlayPager(string) error             { return nil }

func testConfig() *config.Config {
	return &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", Color: "purple"},
			{Name: "codex-work", Harness: "codex", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "API", Directory: "/tmp/api", Profile: "work", Groups: []string{"backend"}},
			{Title: "API", Directory: "/tmp/api", Profile: "codex-work"},
			{Title: "Web", Directory: "/tmp/web", Profile: "work"},
		},
	}
}

// runProject runs one leaf's real RunE against cfg and client with the given
// flags, restoring every seam and flag afterwards.
func runProject(t *testing.T, leaf string, cfg *config.Config, client kitty.KittyClient, jsonOut bool, profile, title string) (string, error) {
	t.Helper()
	return runProjectArgs(t, leaf, cfg, client, jsonOut, profile, title, nil)
}

// runProjectArgs is runProject with positional arguments.
func runProjectArgs(t *testing.T, leaf string, cfg *config.Config, client kitty.KittyClient, jsonOut bool, profile, title string, args []string) (string, error) {
	t.Helper()
	oldLoad, oldKitty := projectLoadConfig, projectNewKitty
	oldListJSON, oldLaunchJSON, oldProfile, oldTitle := projectListJSON, projectLaunchJSON, projectLaunchProfile, projectLaunchTitle
	t.Cleanup(func() {
		projectLoadConfig, projectNewKitty = oldLoad, oldKitty
		projectListJSON, projectLaunchJSON, projectLaunchProfile, projectLaunchTitle = oldListJSON, oldLaunchJSON, oldProfile, oldTitle
	})
	projectLoadConfig = func() (*config.Config, error) { return cfg, nil }
	projectNewKitty = func() kitty.KittyClient { return client }
	projectListJSON, projectLaunchJSON = jsonOut, jsonOut
	projectLaunchProfile, projectLaunchTitle = profile, title

	command := projectListCmd
	if leaf == "launch" {
		command = projectLaunchCmd
	}
	var out bytes.Buffer
	command.SetOut(&out)
	t.Cleanup(func() { command.SetOut(nil) })
	command.SetContext(context.Background())
	err := command.RunE(command, args)
	return out.String(), err
}

func TestProjectListJSONContract(t *testing.T) {
	out, err := runProject(t, "list", testConfig(), &recordingKittyClient{}, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":1,"data":[` +
		`{"profile":"work","title":"API","harness":"claude","directory":"/tmp/api","groups":["backend"]},` +
		`{"profile":"codex-work","title":"API","harness":"codex","directory":"/tmp/api","groups":[]},` +
		`{"profile":"work","title":"Web","harness":"claude","directory":"/tmp/web","groups":[]}]}`
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("json = %s\nwant   %s", got, want)
	}
}

func TestProjectListJSONEmptyIsAnArray(t *testing.T) {
	out, err := runProject(t, "list", &config.Config{}, &recordingKittyClient{}, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != `{"schema_version":1,"data":[]}` {
		t.Fatalf("json = %s, want an empty array", got)
	}
}

func TestProjectListText(t *testing.T) {
	out, err := runProject(t, "list", testConfig(), &recordingKittyClient{}, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PROFILE", "codex-work", "/tmp/web", "backend"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text output lacks %q:\n%s", want, out)
		}
	}
}

// Emoji titles are two cells wide in a terminal; the harness column must still
// start at the same cell on every row.
func TestProjectListTextAlignsEmojiTitles(t *testing.T) {
	cfg := testConfig()
	cfg.Projects[0].Title = "🐱API"
	cfg.Projects[2].Title = "🗣️Web"
	out, err := runProject(t, "list", cfg, &recordingKittyClient{}, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := -1
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		col := strings.Index(line, "claude")
		if col < 0 {
			col = strings.Index(line, "codex ")
		}
		if col < 0 {
			col = strings.Index(line, "HARNESS")
		}
		cell := layout.Width(line[:col])
		if want < 0 {
			want = cell
		}
		if cell != want {
			t.Errorf("harness column at cell %d in %q, want %d", cell, line, want)
		}
	}
}

// The launch goes through the real DefaultRegistry and the real session launch
// path; only Kitty is recorded.
func TestProjectLaunchJSONGoesThroughTheLaunchAction(t *testing.T) {
	client := &recordingKittyClient{windows: []kitty.OSWindow{{ID: 1, IsFocused: true, Tabs: []kitty.Tab{{ID: 10, Title: "shell", Windows: []kitty.Window{{ID: 100}}}}}}}
	out, err := runProject(t, "launch", testConfig(), client, true, "codex-work", "API")
	if err != nil {
		t.Fatalf("launch: %v\n%s", err, out)
	}
	if len(client.launched) != 1 {
		t.Fatalf("launched %d tabs, want 1", len(client.launched))
	}
	if opts := client.launched[0]; opts.Directory != "/tmp/api" || !strings.Contains(opts.Title, "API") {
		t.Fatalf("launch opts = %+v, want the codex-work API project", opts)
	}
	var envelope struct {
		SchemaVersion int               `json:"schema_version"`
		Data          projectLaunchData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	data := envelope.Data
	if envelope.SchemaVersion != 1 || data.Action != actions.Launch || !data.Changed {
		t.Fatalf("envelope = %+v", envelope)
	}
	if data.Project.Profile != "codex-work" || data.Project.Harness != "codex" || data.Project.Title != "API" {
		t.Fatalf("project = %+v, want codex-work API", data.Project)
	}
	if data.TabID != 501 || data.WindowID != 901 {
		t.Fatalf("tab/window = %d/%d, want the new tab 501/901", data.TabID, data.WindowID)
	}
}

func TestProjectLaunchAmbiguousTitleReturnsCandidates(t *testing.T) {
	client := &recordingKittyClient{}
	out, err := runProject(t, "launch", testConfig(), client, true, "", "API")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !exit.Quiet || exit.Code == 0 {
		t.Fatalf("err = %v, want a quiet nonzero exit", err)
	}
	if len(client.launched) != 0 {
		t.Fatal("an ambiguous selector launched a tab")
	}
	want := `{"schema_version":1,"error":{"code":"ambiguous_project","message":"2 configured projects are titled \"API\"; choose one with --profile",` +
		`"candidates":[{"id":"codex-work/API","display_name":"API","canonical_path":"/tmp/api"},{"id":"work/API","display_name":"API","canonical_path":"/tmp/api"}],` +
		`"parameters":[{"name":"profile","description":"configured profile the project launches with","required":true}]}}`
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("json = %s\nwant   %s", got, want)
	}
}

func TestProjectLaunchAmbiguousTitleTextListsCandidates(t *testing.T) {
	_, err := runProject(t, "launch", testConfig(), &recordingKittyClient{}, false, "", "API")
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"choose one with --profile", "candidates:", "codex-work/API", "work/API", "missing --profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("text error lacks %q:\n%s", want, err)
		}
	}
}

func TestProjectLaunchNotFoundOffersNearMisses(t *testing.T) {
	out, err := runProject(t, "launch", testConfig(), &recordingKittyClient{}, true, "missing", "web")
	if err == nil {
		t.Fatal("want an error")
	}
	var envelope cli.JSONErrorEnvelope
	if decodeErr := json.Unmarshal([]byte(out), &envelope); decodeErr != nil {
		t.Fatalf("decode %q: %v", out, decodeErr)
	}
	if envelope.Error.Code != "not_found" || len(envelope.Error.Candidates) != 1 || envelope.Error.Candidates[0].ID != "work/Web" {
		t.Fatalf("error = %+v, want not_found with the differently cased Web", envelope.Error)
	}
}

func TestProjectLaunchMissingTitle(t *testing.T) {
	out, err := runProject(t, "launch", testConfig(), &recordingKittyClient{}, true, "work", "")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(out, `"code":"missing_parameter"`) || !strings.Contains(out, `"name":"title"`) {
		t.Fatalf("json = %s, want missing_parameter title", out)
	}
}

func TestProjectLaunchFailureUsesTheErrorEnvelope(t *testing.T) {
	old := projectExecute
	t.Cleanup(func() { projectExecute = old })
	projectExecute = func(context.Context, actions.Dependencies, actions.Request) (actions.Result, error) {
		return actions.Result{}, errors.New("kitty refused")
	}
	out, err := runProject(t, "launch", testConfig(), &recordingKittyClient{}, true, "work", "Web")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(out, `"code":"action_failed"`) || !strings.Contains(out, "kitty refused") {
		t.Fatalf("json = %s, want action_failed", out)
	}
}

// emojiConfig titles its projects the way operators do, behind an emoji or a
// bullet, with one project whose bare title is another's typed form.
func emojiConfig() *config.Config {
	cfg := testConfig()
	cfg.Projects = []config.Session{
		{Title: "🐱amx", Directory: "/tmp/amx", Profile: "work"},
		{Title: "•dotfiles", Directory: "/tmp/dotfiles", Profile: "work"},
		{Title: "🐱pets", Directory: "/tmp/cats", Profile: "work"},
		{Title: "🐶Pets", Directory: "/tmp/dogs", Profile: "work"},
		{Title: "🗣️coach", Directory: "/tmp/coach", Profile: "codex-work"},
	}
	return cfg
}

func TestProjectLaunchMatchesATitleWithoutItsLeadingEmoji(t *testing.T) {
	for _, typed := range []string{"amx", "AMX", " amx "} {
		client := &recordingKittyClient{}
		out, err := runProject(t, "launch", emojiConfig(), client, false, "", typed)
		if err != nil {
			t.Fatalf("launch %q: %v", typed, err)
		}
		if len(client.launched) != 1 || client.launched[0].Directory != "/tmp/amx" {
			t.Fatalf("launch %q = %+v, want the 🐱amx project", typed, client.launched)
		}
		if want := "Launched 🐱amx (work).\n"; out != want {
			t.Fatalf("out = %q, want %q", out, want)
		}
	}
	client := &recordingKittyClient{}
	if _, err := runProject(t, "launch", emojiConfig(), client, false, "codex-work", "coach"); err != nil || len(client.launched) != 1 {
		t.Fatalf("launch coach = %v, %d tabs; want the 🗣️coach project, variation selector and all", err, len(client.launched))
	}
}

func TestProjectLaunchPrefersAnExactTitleOverAnEmojiOne(t *testing.T) {
	cfg := emojiConfig()
	cfg.Projects = append(cfg.Projects, config.Session{Title: "amx", Directory: "/tmp/plain-amx", Profile: "work"})
	client := &recordingKittyClient{}
	if _, err := runProject(t, "launch", cfg, client, false, "", "amx"); err != nil {
		t.Fatal(err)
	}
	if len(client.launched) != 1 || client.launched[0].Directory != "/tmp/plain-amx" {
		t.Fatalf("launched = %+v, want the project titled exactly amx", client.launched)
	}
}

func TestProjectLaunchRefusesATypedTitleSeveralProjectsShare(t *testing.T) {
	client := &recordingKittyClient{}
	out, err := runProject(t, "launch", emojiConfig(), client, true, "", "pets")
	if err == nil || len(client.launched) != 0 {
		t.Fatalf("err = %v, launched %d; want a refusal and no tab", err, len(client.launched))
	}
	want := `{"schema_version":1,"error":{"code":"ambiguous_project","message":"2 configured projects match title \"pets\"; give the exact title",` +
		`"candidates":[{"id":"work/🐱pets","display_name":"🐱pets","canonical_path":"/tmp/cats"},{"id":"work/🐶Pets","display_name":"🐶Pets","canonical_path":"/tmp/dogs"}]}}`
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("json = %s\nwant   %s", got, want)
	}
}

// A typed title that reaches one title under several profiles is the ordinary
// shared-title case: --profile, not a longer title, tells them apart.
func TestProjectLaunchTypedSharedTitleAsksForProfile(t *testing.T) {
	_, err := runProject(t, "launch", testConfig(), &recordingKittyClient{}, false, "", "api")
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`2 configured projects are titled "API"; choose one with --profile`, "codex-work/API", "work/API", "missing --profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("text error lacks %q:\n%s", want, err)
		}
	}
}

func TestProjectLaunchNotFoundOffersOnlyTitlesContainingTheQuery(t *testing.T) {
	_, err := runProject(t, "launch", emojiConfig(), &recordingKittyClient{}, false, "", "dot")
	if err == nil {
		t.Fatal("want an error")
	}
	want := "no configured project matches title \"dot\"\ncandidates:\n  work/•dotfiles  •dotfiles  /tmp/dotfiles"
	if err.Error() != want {
		t.Fatalf("err = %q\nwant  %q", err, want)
	}
}

func TestProjectLaunchTakesThePositionalTitle(t *testing.T) {
	client := &recordingKittyClient{}
	if _, err := runProjectArgs(t, "launch", emojiConfig(), client, false, "", "", []string{"amx"}); err != nil {
		t.Fatal(err)
	}
	if len(client.launched) != 1 || client.launched[0].Directory != "/tmp/amx" {
		t.Fatalf("launched = %+v, want the 🐱amx project", client.launched)
	}
	if _, err := runProjectArgs(t, "launch", emojiConfig(), client, false, "", "amx", []string{"amx"}); err != nil {
		t.Fatalf("the same title as argument and flag: %v", err)
	}
}

func TestProjectLaunchRefusesTwoDifferentTitles(t *testing.T) {
	client := &recordingKittyClient{}
	out, err := runProjectArgs(t, "launch", emojiConfig(), client, true, "", "dotfiles", []string{"amx"})
	if err == nil || len(client.launched) != 0 {
		t.Fatalf("err = %v, launched %d; want a refusal and no tab", err, len(client.launched))
	}
	if !strings.Contains(out, `"code":"invalid_arguments"`) {
		t.Fatalf("json = %s, want invalid_arguments", out)
	}
}
