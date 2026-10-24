package plans

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/spf13/pflag"
)

// recordingKitty is a Kitty that grows a tab per launch, as a live one would.
type recordingKitty struct {
	windows  []kitty.OSWindow
	launched []kitty.LaunchOpts
}

func (c *recordingKitty) ListWindows() ([]kitty.OSWindow, error) { return c.windows, nil }
func (c *recordingKitty) LaunchTab(opts kitty.LaunchOpts) error {
	c.launched = append(c.launched, opts)
	if len(c.windows) == 0 {
		c.windows = []kitty.OSWindow{{ID: 1, IsFocused: true}}
	}
	id := 500 + len(c.launched)
	c.windows[0].Tabs = append(c.windows[0].Tabs, kitty.Tab{ID: id, Title: opts.Title, Windows: []kitty.Window{{ID: id + 400}}})
	return nil
}
func (*recordingKitty) LaunchOverlay(kitty.LaunchOpts) error                     { return nil }
func (*recordingKitty) CloseTab(string) error                                    { return nil }
func (*recordingKitty) MoveTab(string, int) error                                { return nil }
func (*recordingKitty) SetTabColor(string, string, string, string, string) error { return nil }
func (*recordingKitty) SetTabTitle(string, string) error                         { return nil }
func (*recordingKitty) SetUserVars(string, map[string]string) error              { return nil }
func (*recordingKitty) FocusTab(string) error                                    { return nil }
func (*recordingKitty) FocusWindow(string) error                                 { return nil }
func (*recordingKitty) GetText(string, string) (string, error)                   { return "", nil }
func (*recordingKitty) LaunchOverlayPager(string) error                          { return nil }

// scriptedASL answers each asl subcommand with a fixed text.
type scriptedASL struct {
	status, preflight string
	asked             []string
}

func (a *scriptedASL) Output(_ context.Context, args ...string) ([]byte, error) {
	a.asked = append(a.asked, args[0])
	switch args[0] {
	case "status":
		return []byte(a.status), nil
	case "preflight":
		if strings.Contains(a.preflight, `"ok":false`) {
			return []byte(a.preflight), errors.New("exit status 1")
		}
		return []byte(a.preflight), nil
	case "supervisor-guide":
		return []byte(`{"skills":[{"invocations":{"` + args[2] + `":"/asl asl run PLAN_DIR"}}]}`), nil
	}
	return nil, errors.New("unexpected asl command")
}

const (
	pendingStatus = `{"ok":true,"plan_title":"Fix things","counts":{"pending":2,"complete":1},"lock":{"active":false},"next_command":{"complete":false}}`
	greenReport   = `{"ok":true,"findings":[]}`
	yellowReport  = `{"ok":false,"findings":[{"severity":"yellow","category":"manifest-readiness","message":"plan readiness state is 'reviewed'","detail":"run preflight"}]}`
)

// planWorld is a plans root with two plans that share a prefix, and a
// configuration whose one project is the directory both plans record.
type planWorld struct {
	cfg     *config.Config
	kitty   *recordingKitty
	asl     *scriptedASL
	workDir string
	first   string
}

func newPlanWorld(t *testing.T) *planWorld {
	t.Helper()
	root, work := t.TempDir(), t.TempDir()
	for _, name := range []string{"alpha-one", "alpha-two"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.md"), []byte("# manifest\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	world := &planWorld{
		cfg: &config.Config{
			Defaults: config.Defaults{PlansDirectory: root},
			Profiles: []config.Account{{Name: "work", Harness: "claude"}},
			Projects: []config.Session{{Title: "API", Directory: work, Profile: "work"}},
		},
		kitty:   &recordingKitty{},
		asl:     &scriptedASL{status: pendingStatus, preflight: greenReport},
		workDir: work,
		first:   filepath.Join(root, "alpha-one"),
	}
	oldLoad, oldKitty, oldDescribe, oldASL := planLoadConfig, planNewKitty, planDescribe, planASL
	planLoadConfig = func() (*config.Config, error) { return world.cfg, nil }
	planNewKitty = func() kitty.KittyClient { return world.kitty }
	planDescribe = func(dir string) (plans.PlanMeta, error) {
		return plans.PlanMeta{Name: filepath.Base(dir), DirPath: dir, CWD: work}, nil
	}
	planASL = world.asl
	t.Cleanup(func() { planLoadConfig, planNewKitty, planDescribe, planASL = oldLoad, oldKitty, oldDescribe, oldASL })
	return world
}

// runPlan runs the real `plans run` RunE with args, restoring its flags.
func runPlan(t *testing.T, args ...string) (string, error) {
	t.Helper()
	flags := planRunCmd.Flags()
	t.Cleanup(func() {
		flags.VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	})
	if err := planRunCmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	planRunCmd.SetOut(&out)
	planRunCmd.SetErr(&out)
	t.Cleanup(func() { planRunCmd.SetOut(nil); planRunCmd.SetErr(nil) })
	err := planRunCmd.RunE(planRunCmd, flags.Args())
	return out.String(), err
}

func decodeData(t *testing.T, output string) planRunData {
	t.Helper()
	var envelope struct {
		SchemaVersion int         `json:"schema_version"`
		Data          planRunData `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil || envelope.SchemaVersion != 1 {
		t.Fatalf("output is not a schema-v1 data envelope: %v\n%s", err, output)
	}
	return envelope.Data
}

func decodeError(t *testing.T, output string) cli.JSONError {
	t.Helper()
	var envelope cli.JSONErrorEnvelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil || envelope.Error.Code == "" {
		t.Fatalf("output is not an error envelope: %v\n%s", err, output)
	}
	return envelope.Error
}

func TestPlanRunStartsAReadyPlan(t *testing.T) {
	world := newPlanWorld(t)
	output, err := runPlan(t, "alpha-one", "--json", "--harness", "codex", "--review-harness", "claude", "--commit-at-end")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, output)
	}
	data := decodeData(t, output)
	wantCommand := "asl run " + world.first + " --harness codex --review-harness claude --commit-at-end"
	if !data.Started || !data.Changed || data.Command != wantCommand || data.Project != "API" || data.Profile != "work" || data.Backend != "kitty" {
		t.Fatalf("data = %+v", data)
	}
	if data.Readiness.State != "ready" || data.Readiness.Title != "Fix things" || data.Plan.Directory != world.first {
		t.Fatalf("data = %+v", data)
	}
	if len(world.kitty.launched) != 1 || world.kitty.launched[0].Directory != world.workDir ||
		!strings.Contains(strings.Join(world.kitty.launched[0].Cmdline, " "), "/asl "+wantCommand) {
		t.Fatalf("launched = %+v", world.kitty.launched)
	}
}

// TestPlanRunReportsAPlanThatMayNotBeStartedAsData: the refusal is the data
// envelope with the readiness in it and a quiet nonzero exit, and in text
// the findings are listed before the error.
func TestPlanRunReportsAPlanThatMayNotBeStartedAsData(t *testing.T) {
	world := newPlanWorld(t)
	world.asl.preflight = yellowReport

	output, err := runPlan(t, "alpha-one", "--json")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !exit.Quiet || exit.Code != 1 {
		t.Fatalf("err = %#v, want a quiet exit 1", err)
	}
	data := decodeData(t, output)
	if data.Started || data.Readiness.State != "not_ready" || len(data.Readiness.Findings) != 1 || data.Readiness.Findings[0].Category != "manifest-readiness" {
		t.Fatalf("data = %+v", data)
	}

	output, err = runPlan(t, "alpha-one")
	if err == nil || !strings.Contains(err.Error(), "not ready") || !strings.Contains(output, "manifest-readiness") || !strings.Contains(output, "plan readiness state is 'reviewed'") {
		t.Fatalf("err = %v, output:\n%s", err, output)
	}
	if len(world.kitty.launched) != 0 {
		t.Fatal("a plan that is not ready was launched")
	}
}

func TestPlanRunCheckStartsNothingAndNeedsNoBackend(t *testing.T) {
	world := newPlanWorld(t)
	planNewKitty = func() kitty.KittyClient { t.Fatal("a check opened Kitty"); return nil }
	// No project covers the plan: readiness is still an answer.
	world.cfg.Projects = nil
	output, err := runPlan(t, "alpha-one", "--check", "--json", "--backend", "screen")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, output)
	}
	if data := decodeData(t, output); data.Started || data.Readiness.State != "ready" || data.Project != "" {
		t.Fatalf("data = %+v", data)
	}
	for _, asked := range world.asl.asked {
		if asked == "supervisor-guide" {
			t.Fatal("a check asked for the supervision prompt")
		}
	}
}

func TestPlanRunSelectsOnePlanOrListsCandidates(t *testing.T) {
	world := newPlanWorld(t)

	output, err := runPlan(t, "alpha", "--json", "--check")
	if err == nil {
		t.Fatalf("an ambiguous name ran:\n%s", output)
	}
	if failure := decodeError(t, output); failure.Code != "ambiguous_selector" || len(failure.Candidates) != 2 || failure.Candidates[0].ID != "alpha-one" {
		t.Fatalf("failure = %+v", failure)
	}

	for _, selector := range []string{"alpha-o", "alpha-one", world.first} {
		output, err = runPlan(t, selector, "--json", "--check")
		if err != nil {
			t.Fatalf("%s: %v\n%s", selector, err, output)
		}
		if data := decodeData(t, output); data.Plan.Directory != world.first {
			t.Fatalf("%s selected %s", selector, data.Plan.Directory)
		}
	}

	output, _ = runPlan(t, "beta", "--json", "--check")
	if failure := decodeError(t, output); failure.Code != "not_found" || !strings.Contains(failure.Message, "<Tab> completes the plan names") {
		t.Fatalf("failure = %+v", failure)
	}
	output, _ = runPlan(t, "--json")
	if failure := decodeError(t, output); failure.Code != "missing_parameter" {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestPlanRunNamesTheProjectItNeeds(t *testing.T) {
	world := newPlanWorld(t)
	world.cfg.Projects = append(world.cfg.Projects, config.Session{Title: "Other", Directory: t.TempDir(), Profile: "work"})
	world.cfg.Projects[0].Directory = t.TempDir() // nothing covers the plan now

	output, _ := runPlan(t, "alpha-one", "--json")
	if failure := decodeError(t, output); failure.Code != "missing_parameter" || len(failure.Parameters) != 1 || failure.Parameters[0].Name != "project" {
		t.Fatalf("failure = %+v", failure)
	}
	output, err := runPlan(t, "alpha-one", "--json", "--project", "Other")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, output)
	}
	if data := decodeData(t, output); !data.Started || data.Project != "Other" || data.Directory != world.workDir {
		t.Fatalf("data = %+v", data)
	}
}
