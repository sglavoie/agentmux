package debug

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/aslexport"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/spf13/cobra"
)

var debugDoctorSnapshotMu sync.Mutex

var (
	debugCaptureDuration  time.Duration
	debugCaptureInclude   bool
	debugCaptureYes       bool
	debugCaptureJSON      bool
	debugBundleMode       string
	debugBundleOutput     string
	debugBundleJSON       bool
	debugBundleIncludeASL bool
	debugInputInteractive = func(reader io.Reader) bool {
		file, ok := reader.(*os.File)
		return ok && isTerminalInput(file)
	}
	// debugBundleASLFetch is the subprocess seam for the ASL side. Bundle
	// creation must stay exercisable with no ASL installed, and every degraded
	// path is a documented outcome rather than an error, so the fetch is
	// replaceable in tests.
	debugBundleASLFetch   = aslexport.Fetch
	debugBundleASLResolve = func(command string) (string, error) {
		resolution, err := aslbridge.ResolveCommand(command)
		if err != nil {
			return "", err
		}
		return resolution.Executable, nil
	}
)

var debugCaptureCmd = &cobra.Command{
	Use:   "capture",
	Short: "Manage an explicitly authorized temporary raw hook-payload capture",
}

var debugCaptureStartCmd = &cobra.Command{
	Use:          "start",
	Short:        "Start a scoped, quota-bound raw hook-payload capture",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugCaptureStart,
}

var debugCaptureStopCmd = &cobra.Command{
	Use:          "stop",
	Short:        "Stop the current raw hook-payload capture",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugCaptureStop,
}

var debugCaptureStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Show capture scope, expiry, quota, and payload counts",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugCaptureStatus,
}

var debugBundleCmd = &cobra.Command{
	Use:          "bundle",
	Short:        "Create an atomic always-redacted diagnostic archive",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugBundle,
}

func init() {
	debugCaptureStartCmd.Flags().DurationVar(&debugCaptureDuration, "duration", diagnostics.CaptureDefaultTTL, "capture lifetime (maximum 60m)")
	debugCaptureStartCmd.Flags().BoolVar(&debugCaptureInclude, "include-hook-payloads", false, "authorize temporary raw hook-payload storage")
	debugCaptureStartCmd.Flags().BoolVar(&debugCaptureYes, "yes", false, "confirm raw capture without an interactive prompt (required non-interactively)")
	debugCaptureStartCmd.Flags().BoolVar(&debugCaptureJSON, "json", false, "write schema-versioned JSON")
	addDebugRuntimeSessionFlag(debugCaptureStartCmd)
	debugCaptureStopCmd.Flags().BoolVar(&debugCaptureJSON, "json", false, "write schema-versioned JSON")
	debugCaptureStatusCmd.Flags().BoolVar(&debugCaptureJSON, "json", false, "write schema-versioned JSON")
	debugCaptureCmd.AddCommand(debugCaptureStartCmd, debugCaptureStopCmd, debugCaptureStatusCmd)

	debugBundleCmd.Flags().StringVar(&debugBundleMode, "mode", string(debugger.BundleModeShare), "redaction mode: share or forensic")
	debugBundleCmd.Flags().StringVarP(&debugBundleOutput, "output", "o", "", "output .tar.gz path")
	debugBundleCmd.Flags().BoolVar(&debugBundleJSON, "json", false, "write schema-versioned JSON")
	debugBundleCmd.Flags().BoolVar(&debugBundleIncludeASL, "include-asl", false,
		"include agent-session-loop's public sanitized diagnostics export and correlate it with this bundle's journal window")
	debugCmd.AddCommand(debugCaptureCmd, debugBundleCmd)
}

func captureService() (*debugger.CaptureService, error) {
	paths, err := debugPaths()
	if err != nil {
		return nil, err
	}
	return debugger.NewCaptureService(paths, debugNow)
}

func runDebugCaptureStart(cmd *cobra.Command, _ []string) error {
	if !debugCaptureInclude {
		return fmt.Errorf("raw capture requires --include-hook-payloads")
	}
	selector, err := debugSelector(debugNow().UTC())
	if err != nil {
		return err
	}
	if selector.Profile != "" || selector.SessionID != "" || selector.ASLRunID != "" || selector.Event != "" ||
		selector.TraceID != "" || selector.RequestID != "" || selector.SourceEventID != "" || len(selector.Outcomes) != 0 ||
		len(selector.Stages) != 0 || !selector.After.IsZero() || !selector.Before.IsZero() {
		return fmt.Errorf("capture scope accepts only --window, --%s, and/or --harness selectors", debugRuntimeSessionFlag)
	}
	// A capture scoped by harness alone is machine-wide for that harness; the
	// current session is the default only when nothing else scopes it.
	windowID, runtimeSession := selector.WindowID, ""
	if windowID != 0 || strings.TrimSpace(debugRuntimeSession) != "" || selector.Harness == "" {
		windowID, runtimeSession, err = debugSessionTarget(selector.WindowID)
		if err != nil {
			return err
		}
	}
	authorized := debugCaptureYes
	if !authorized {
		if !debugInputInteractive(cmd.InOrStdin()) {
			return fmt.Errorf("raw capture requires --yes in non-interactive use")
		}
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Raw hook payloads may contain prompts, tool arguments, credentials, and other sensitive text."); err != nil {
			return err
		}
		if _, err := fmt.Fprint(cmd.ErrOrStderr(), "Type 'yes' to start this temporary private capture: "); err != nil {
			return err
		}
		answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && len(answer) == 0 {
			return err
		}
		authorized = strings.EqualFold(strings.TrimSpace(answer), "yes")
		if !authorized {
			return fmt.Errorf("raw capture was not confirmed")
		}
	}
	service, err := captureService()
	if err != nil {
		return err
	}
	report, err := service.Start(cmd.Context(), debugger.CaptureStartOptions{
		WindowID: windowID, RuntimeSessionID: runtimeSession, Harness: selector.Harness, Duration: debugCaptureDuration,
		IncludeHookPayloads: true, Authorized: authorized,
	})
	if err != nil {
		return err
	}
	return writeCaptureReport(cmd.OutOrStdout(), report)
}

func runDebugCaptureStop(cmd *cobra.Command, _ []string) error {
	service, err := captureService()
	if err != nil {
		return err
	}
	report, err := service.Stop(cmd.Context())
	if err != nil {
		return err
	}
	return writeCaptureReport(cmd.OutOrStdout(), report)
}

func runDebugCaptureStatus(cmd *cobra.Command, _ []string) error {
	service, err := captureService()
	if err != nil {
		return err
	}
	report, err := service.Status(cmd.Context())
	if err != nil {
		return err
	}
	return writeCaptureReport(cmd.OutOrStdout(), report)
}

func writeCaptureReport(out io.Writer, report debugger.CaptureReport) error {
	if debugCaptureJSON {
		return writeIndentedJSON(out, report)
	}
	if report.Descriptor == nil {
		_, err := fmt.Fprintf(out, "Agent Mux debug capture (schema %d)\nstatus: inactive\n", report.SchemaVersion)
		return err
	}
	descriptor := report.Descriptor
	scope := fmt.Sprintf("window=%d", descriptor.Scope.WindowID)
	if descriptor.Scope.RuntimeSessionID != "" {
		scope = "runtime_session=" + descriptor.Scope.RuntimeSessionID
	}
	_, err := fmt.Fprintf(out,
		"Agent Mux debug capture (schema %d)\nstatus: %s\nid: %s\nscope: %s harness=%s\nstarted: %s\nexpires: %s\npayloads: %d\nbytes: %d/%d\n",
		report.SchemaVersion, descriptor.Status, descriptor.ID, scope, emptyDash(descriptor.Scope.Harness),
		descriptor.StartedAt.Format(time.RFC3339), descriptor.ExpiresAt.Format(time.RFC3339),
		descriptor.CapturedPayloads, descriptor.CapturedBytes, descriptor.QuotaBytes)
	return err
}

func runDebugBundle(cmd *cobra.Command, _ []string) error {
	now := debugNow().UTC()
	selector, err := debugSelector(now)
	if err != nil {
		return err
	}
	paths, err := debugPaths()
	if err != nil {
		return err
	}
	cfg, configErr := debugLoadConfig()
	policy := config.DiagnosticsConfig{}
	if cfg != nil {
		policy = cfg.Defaults.Diagnostics
	}
	journal, err := debugNewJournal(paths, diagnostics.JournalOptions{
		KeepDays: policy.JournalKeepDays, MaxBytes: int64(policy.JournalMaxMiB) << 20,
	})
	if err != nil {
		return err
	}
	bin, executableErr := debugExecutable()
	if executableErr != nil && configErr == nil {
		configErr = executableErr
	}
	status, err := debugger.CollectStatus(cmd.Context(), debugger.StatusOptions{
		Config: cfg, ConfigError: configErr, Paths: paths, Journal: journal,
		Daemon: debugDaemon(), Kitty: debugKitty(), AMXBin: bin,
		CLIBuild: buildinfo.Current(), Now: now, Selector: selector,
		Backend: doctorBackendStatus(),
	})
	if err != nil {
		return err
	}
	doctor := collectDoctorSnapshotForBundle(cmd)
	result, err := debugger.CreateBundle(cmd.Context(), debugger.BundleOptions{
		Paths: paths, OutputPath: debugBundleOutput, Mode: debugger.BundleMode(strings.ToLower(strings.TrimSpace(debugBundleMode))),
		Selector: selector, Now: now, Journal: journal, Config: cfg, ConfigError: configErr,
		Status: &status, Doctor: doctor, Build: buildinfo.Current(), NativeSessions: status.Native.Sessions,
		ASL:              fetchASLExportForBundle(cmd.Context(), cfg, selector),
		ASLRequested:     debugBundleIncludeASL,
		ASLContractSetID: aslcontract.ContractSetID,
	})
	if err != nil {
		return err
	}
	if debugBundleJSON {
		return writeIndentedJSON(cmd.OutOrStdout(), result)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Agent Mux debug bundle (schema %d)\nmode: %s\npath: %s\nbytes: %d\n",
		result.SchemaVersion, result.Mode, result.Path, result.Bytes); err != nil {
		return err
	}
	if result.Manifest.LocalOnly {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Manifest.Warning)
	}
	return err
}

// fetchASLExportForBundle obtains the ASL half of the causal path.
//
// It never returns an error. A bundle is most valuable exactly when the other
// half of the pairing is broken, so every way this can fail — not requested,
// turned off, not installed, too old, hung, unreadable — resolves to an
// availability the bundle records and explains. The only thing that would make
// a bundle worse is refusing to write one.
//
// The export is read through ASL's public sanitized command. AgentMux does not
// parse state.json, events.jsonl or mailbox.jsonl for diagnostics, so no bundle
// can carry a question body, a reply, or a prompt that ASL declined to publish.
func fetchASLExportForBundle(ctx context.Context, cfg *config.Config, selector debugger.Selector) aslexport.Result {
	if !debugBundleIncludeASL {
		return aslexport.Result{Availability: aslexport.Availability{Reason: aslexport.ReasonNotRequested}}
	}
	settings := config.ASLConfig{}
	if cfg != nil {
		settings = cfg.Defaults.ASL
	}
	if settings.ASLModeValue() == "disabled" {
		return aslexport.Result{Availability: aslexport.Availability{
			Reason: aslexport.ReasonDisabled,
			Detail: "[defaults.asl] enabled = false",
		}}
	}
	command := strings.TrimSpace(settings.Command)
	if command == "" {
		command = "asl"
	}
	executable, err := debugBundleASLResolve(command)
	if err != nil {
		return aslexport.Result{Availability: aslexport.Availability{
			Reason: aslexport.ReasonNotFound,
			Detail: err.Error(),
		}}
	}
	stateRoot := strings.TrimSpace(settings.StateRoot)
	if stateRoot == "" {
		// The unpaired default. Passing it explicitly rather than letting ASL
		// choose keeps the bundle's evidence tied to the root AgentMux itself
		// watches, which is the root the rest of the bundle describes.
		stateRoot = agentloop.DefaultStateRoot()
	}
	result, err := debugBundleASLFetch(ctx, aslexport.Options{
		Executable:    executable,
		StateRoot:     stateRoot,
		RunID:         strings.TrimSpace(selector.ASLRunID),
		ContractSetID: aslcontract.ContractSetID,
	})
	if err != nil {
		return aslexport.Result{Availability: aslexport.Availability{
			Reason: aslexport.ReasonFailed,
			Detail: err.Error(),
		}}
	}
	return result
}

func collectDoctorSnapshotForBundle(command *cobra.Command) any {
	debugDoctorSnapshotMu.Lock()
	defer debugDoctorSnapshotMu.Unlock()
	oldJSON, oldStrict := doctorJSON, doctorStrict
	doctorJSON, doctorStrict = true, false
	defer func() { doctorJSON, doctorStrict = oldJSON, oldStrict }()
	var output bytes.Buffer
	doctorCommand := &cobra.Command{}
	doctorCommand.SetContext(command.Context())
	doctorCommand.SetOut(&output)
	if err := runDoctor(doctorCommand, nil); err != nil {
		return map[string]any{"schema_version": 1, "availability": "unavailable", "error": err.Error()}
	}
	var snapshot any
	if err := json.Unmarshal(output.Bytes(), &snapshot); err != nil {
		return map[string]any{"schema_version": 1, "availability": "unavailable", "error": "doctor output could not be decoded"}
	}
	return snapshot
}
