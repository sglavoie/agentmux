// Package planrun answers the two questions starting an agent-session-loop
// run from AgentMux asks of ASL: may this split plan be started now, and what
// does a supervising agent type to start it.
//
// AgentMux starts a run by launching an ordinary managed session whose first
// prompt invokes ASL's own supervision skill. Nothing here launches anything:
// the package reads `asl status`, `asl preflight`, and `asl supervisor-guide`
// through the ASL seam and returns values. Launching stays with
// internal/session, and deciding which project and profile the session runs
// under stays with internal/actions.
//
// It holds no opinion of its own about readiness. A plan is ready when ASL's
// preflight says so, running when ASL's lock is held, and complete when ASL
// has no session left to run; a finding is shown the way ASL worded it. The
// skill invocation is likewise ASL's, read per harness, so a harness whose
// skills are spelled differently needs no change here.
//
// What ASL chooses to run a plan with is ASL's business. Options carries the
// three choices an operator makes at launch (worker harness, review harness,
// commit mode) and each one left empty is left to ASL.
package planrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/harness"
)

// CommitMode is when a run commits what its sessions wrote.
type CommitMode string

const (
	// CommitDefault leaves the choice to ASL.
	CommitDefault    CommitMode = ""
	CommitPerSession CommitMode = "per-session"
	CommitAtEnd      CommitMode = "at-end"
)

// CommitModes are the modes an operator can name, in the order they are offered.
func CommitModes() []CommitMode { return []CommitMode{CommitPerSession, CommitAtEnd} }

// Options are the choices made for one run. An empty field is ASL's default.
type Options struct {
	// Harness is the harness that implements the plan's sessions.
	Harness string
	// ReviewHarness, when set, appends a final review run by that harness.
	ReviewHarness string
	CommitMode    CommitMode
}

// Validate refuses options ASL would refuse, before anything is asked of it.
func (o Options) Validate() error {
	for _, named := range []struct{ flag, value string }{{"harness", o.Harness}, {"review harness", o.ReviewHarness}} {
		if named.value != "" && !harness.Valid(named.value) {
			return fmt.Errorf("unknown %s %q (%s)", named.flag, named.value, strings.Join(harness.Names(), ", "))
		}
	}
	if o.Harness != "" && harness.Normalize(o.Harness) == harness.Normalize(o.ReviewHarness) {
		return fmt.Errorf("the review harness must differ from the harness (%s)", o.Harness)
	}
	switch o.CommitMode {
	case CommitDefault, CommitPerSession, CommitAtEnd:
	default:
		return fmt.Errorf("unknown commit mode %q (%s, %s)", o.CommitMode, CommitPerSession, CommitAtEnd)
	}
	return nil
}

// ASL runs one asl command and returns what it printed. A command that
// answered and exited nonzero returns its output beside the error: preflight
// reports a plan that is not ready exactly that way.
type ASL interface {
	Output(ctx context.Context, args ...string) ([]byte, error)
}

// Exec is the installed ASL: the configured command, resolved the way the
// daemon resolves its bridge.
type Exec struct{ Command string }

// Output implements ASL.
func (e Exec) Output(ctx context.Context, args ...string) ([]byte, error) {
	resolved, err := aslbridge.ResolveCommand(e.Command)
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, resolved.Executable, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if detail := lastLine(stderr.String()); detail != "" {
			err = fmt.Errorf("%w: %s", err, detail)
		}
	}
	return out, err
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// State is where a plan stands with respect to being started.
type State string

const (
	// StateReady: preflight passed and nothing is running or finished.
	StateReady State = "ready"
	// StateNotReady: preflight has a finding that is not green.
	StateNotReady State = "not_ready"
	// StateRunning: a run of this plan holds ASL's lock.
	StateRunning State = "running"
	// StateComplete: every session of the plan is implemented.
	StateComplete State = "complete"
)

// Finding is one preflight finding that is not green, as ASL worded it.
type Finding struct {
	Severity  string `json:"severity"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Detail    string `json:"detail,omitempty"`
	Path      string `json:"path,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// Readiness is ASL's answer about one plan.
type Readiness struct {
	PlanDir string `json:"plan_dir"`
	// Title is the plan's own title, when ASL has a status for the plan.
	Title string `json:"title,omitempty"`
	State State  `json:"state"`
	// Pending and Complete count the plan's sessions; both are zero when ASL
	// could not report the plan's status.
	Pending  int `json:"pending_sessions"`
	Complete int `json:"complete_sessions"`
	// Findings are the preflight findings that are not green. It is empty for
	// a ready plan and for one refused as running or complete.
	Findings []Finding `json:"findings"`
}

// Ready reports whether the plan may be started.
func (r Readiness) Ready() bool { return r.State == StateReady }

// Summary is one sentence saying why the plan may not be started.
func (r Readiness) Summary() string {
	switch r.State {
	case StateReady:
		return "the plan is ready to run"
	case StateRunning:
		return "a run of this plan is already in progress"
	case StateComplete:
		return "every session of this plan is already implemented"
	}
	red, yellow := 0, 0
	for _, finding := range r.Findings {
		if finding.Severity == "red" {
			red++
		} else {
			yellow++
		}
	}
	return fmt.Sprintf("the plan is not ready to run: preflight reported %d red and %d yellow", red, yellow)
}

type statusReport struct {
	OK     bool   `json:"ok"`
	Title  string `json:"plan_title"`
	Counts struct {
		Pending  int `json:"pending"`
		Complete int `json:"complete"`
	} `json:"counts"`
	Lock struct {
		Active bool `json:"active"`
	} `json:"lock"`
	NextCommand struct {
		Complete bool `json:"complete"`
	} `json:"next_command"`
}

type preflightReport struct {
	OK       *bool     `json:"ok"`
	Error    string    `json:"error"`
	Findings []Finding `json:"findings"`
}

// Check asks ASL whether the plan in planDir may be started with opts from
// the working directory cwd. An empty cwd skips ASL's working-directory
// checks. An error means ASL gave no answer; a plan that may not be started
// is an answer, returned as a Readiness.
func Check(ctx context.Context, asl ASL, planDir, cwd string, opts Options) (Readiness, error) {
	readiness := Readiness{PlanDir: planDir, Findings: []Finding{}}

	// A plan with no run state yet has no status to report, and one that does
	// not exist is preflight's to describe, so only an answer is acted on.
	var status statusReport
	statusOut, _ := asl.Output(ctx, "status", planDir, "--json")
	if json.Unmarshal(statusOut, &status) == nil && status.OK {
		readiness.Title = status.Title
		readiness.Pending, readiness.Complete = status.Counts.Pending, status.Counts.Complete
		switch {
		case status.Lock.Active:
			readiness.State = StateRunning
			return readiness, nil
		case status.NextCommand.Complete && status.Counts.Pending == 0:
			readiness.State = StateComplete
			return readiness, nil
		}
	}

	args := []string{"preflight", planDir, "--json"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if opts.Harness != "" {
		args = append(args, "--harness", opts.Harness)
	}
	if flag := commitFlag(opts.CommitMode); flag != "" {
		args = append(args, flag)
	}
	out, runErr := asl.Output(ctx, args...)
	var report preflightReport
	if err := json.Unmarshal(out, &report); err != nil || report.OK == nil {
		if runErr == nil {
			runErr = errors.New("its answer could not be read")
		}
		return Readiness{}, fmt.Errorf("asl preflight: %w", runErr)
	}
	for _, finding := range report.Findings {
		if finding.Severity != "green" {
			readiness.Findings = append(readiness.Findings, finding)
		}
	}
	if *report.OK {
		readiness.State = StateReady
		return readiness, nil
	}
	readiness.State = StateNotReady
	if len(readiness.Findings) == 0 {
		// ASL refused without a finding to show; its own sentence is the reason.
		readiness.Findings = append(readiness.Findings, Finding{Severity: "red", Category: "preflight", Message: report.Error})
	}
	return readiness, nil
}

func commitFlag(mode CommitMode) string {
	switch mode {
	case CommitPerSession:
		return "--commit-per-session"
	case CommitAtEnd:
		return "--commit-at-end"
	}
	return ""
}

// planDirPlaceholder is how ASL's supervisor guide spells the plan directory
// in a skill invocation.
const planDirPlaceholder = "PLAN_DIR"

// RunArguments is what follows `asl run` for this plan and these options,
// quoted for a shell.
func RunArguments(planDir string, opts Options) string {
	args := []string{shellQuote(planDir)}
	if opts.Harness != "" {
		args = append(args, "--harness", opts.Harness)
	}
	if opts.ReviewHarness != "" {
		args = append(args, "--review-harness", opts.ReviewHarness)
	}
	if flag := commitFlag(opts.CommitMode); flag != "" {
		args = append(args, flag)
	}
	return strings.Join(args, " ")
}

// Command is the `asl run` command line a run started with opts amounts to,
// for showing to the operator.
func Command(planDir string, opts Options) string {
	return "asl run " + RunArguments(planDir, opts)
}

type guideReport struct {
	Skills []struct {
		Invocations map[string]string `json:"invocations"`
	} `json:"skills"`
}

// Prompt is the first prompt of a supervising agent on supervisorHarness:
// ASL's own skill invocation for that harness, naming this plan and these
// options.
func Prompt(ctx context.Context, asl ASL, supervisorHarness, planDir string, opts Options) (string, error) {
	name := harness.Normalize(supervisorHarness)
	out, runErr := asl.Output(ctx, "supervisor-guide", "--harness", name, "--json")
	var guide guideReport
	if err := json.Unmarshal(out, &guide); err != nil {
		if runErr == nil {
			runErr = errors.New("its answer could not be read")
		}
		return "", fmt.Errorf("asl supervisor-guide: %w", runErr)
	}
	for _, skill := range guide.Skills {
		invocation := skill.Invocations[name]
		if strings.Count(invocation, planDirPlaceholder) != 1 {
			continue
		}
		return strings.Replace(invocation, planDirPlaceholder, RunArguments(planDir, opts), 1), nil
	}
	return "", fmt.Errorf("asl supervisor-guide names no supervision skill for %s", name)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(value string) string {
	if shellSafe.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
