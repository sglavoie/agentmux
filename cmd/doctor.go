package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/hooks"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/spf13/cobra"
)

const doctorSuppressionWindow = 15 * time.Minute

var (
	newDoctorDaemonClient = func() daemonExportClient { return daemon.NewClient() }
	newDoctorKitty        = func() kittyWindowLister { return kitty.NewClient(kitty.NewRealRunner()) }
	doctorLoadConfig      = loadRunnableConfig
	doctorExecutable      = os.Executable
	doctorSocketPath      = daemon.SocketPath
	doctorStat            = os.Stat
	doctorNow             = time.Now
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose daemon, Kitty, hook, and renderer health",
	Args:  cobra.NoArgs,
	RunE:  runDoctor,
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	issues := make([]string, 0)
	reportIssue := func(format string, args ...any) {
		issues = append(issues, fmt.Sprintf(format, args...))
	}

	if _, err := fmt.Fprintln(out, "Agent Mux doctor"); err != nil {
		return err
	}

	client := newDoctorDaemonClient()
	health := client.Health()
	if health.Alive {
		if _, err := fmt.Fprintf(out, "daemon: running (pid %d, uptime %s)\n", health.PID, health.Uptime.Round(time.Second)); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(out, "daemon: unreachable"); err != nil {
			return err
		}
		reportIssue("daemon is unreachable")
	}

	if err := writeDoctorSocketStatus(out, reportIssue); err != nil {
		return err
	}

	var (
		snapshot daemon.EventsExport
		windows  []kitty.OSWindow
		kittyOK  bool
	)
	if health.Alive {
		var err error
		snapshot, err = client.ExportEvents(0)
		if err != nil {
			if _, writeErr := fmt.Fprintf(out, "daemon export: unavailable (%s)\n", err); writeErr != nil {
				return writeErr
			}
			reportIssue("daemon export is unavailable: %s", err)
		} else if _, err := fmt.Fprintln(out, "daemon export: ok"); err != nil {
			return err
		}
	}

	var kittyErr error
	windows, kittyErr = newDoctorKitty().ListWindows()
	if kittyErr != nil {
		if _, err := fmt.Fprintf(out, "kitty: unavailable (%s)\n", kittyErr); err != nil {
			return err
		}
		reportIssue("kitten @ ls failed: %s", kittyErr)
	} else {
		kittyOK = true
		if _, err := fmt.Fprintf(out, "kitty: ok (%d OS windows)\n", len(windows)); err != nil {
			return err
		}
	}

	if err := writeDoctorHooks(out, reportIssue); err != nil {
		return err
	}
	if err := writeDoctorRenderer(out, snapshot, windows, kittyOK, reportIssue); err != nil {
		return err
	}
	if err := writeDoctorSuppressions(out, snapshot, doctorNow()); err != nil {
		return err
	}

	if len(issues) == 0 {
		_, err := fmt.Fprintln(out, "overall: clean")
		return err
	}
	if _, err := fmt.Fprintln(out, "overall: issues detected"); err != nil {
		return err
	}
	for _, issue := range issues {
		if _, err := fmt.Fprintf(out, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

func writeDoctorSocketStatus(out interface{ Write([]byte) (int, error) }, reportIssue func(string, ...any)) error {
	socketPath := doctorSocketPath()
	socketInfo, socketErr := doctorStat(socketPath)
	if socketErr != nil {
		_, err := fmt.Fprintf(out, "socket: unavailable (%s)\n", socketErr)
		reportIssue("daemon socket is unavailable: %s", socketErr)
		return err
	}

	binary, binaryErr := doctorExecutable()
	if binaryErr != nil {
		_, err := fmt.Fprintf(out, "socket: %s (binary mtime unavailable: %s)\n", socketPath, binaryErr)
		reportIssue("daemon binary mtime is unavailable: %s", binaryErr)
		return err
	}
	binaryInfo, binaryStatErr := doctorStat(binary)
	if binaryStatErr != nil {
		_, err := fmt.Fprintf(out, "socket: %s (binary mtime unavailable: %s)\n", socketPath, binaryStatErr)
		reportIssue("daemon binary mtime is unavailable: %s", binaryStatErr)
		return err
	}
	state := "current"
	if binaryInfo.ModTime().After(socketInfo.ModTime()) {
		state = "older than binary; restart daemon"
		reportIssue("daemon socket is older than the installed binary")
	}
	_, err := fmt.Fprintf(out, "socket: %s (%s)\n", socketPath, state)
	return err
}

func writeDoctorHooks(out interface{ Write([]byte) (int, error) }, reportIssue func(string, ...any)) error {
	cfg, err := doctorLoadConfig()
	if err != nil {
		if _, writeErr := fmt.Fprintf(out, "hooks: config unavailable (%s)\n", err); writeErr != nil {
			return writeErr
		}
		reportIssue("hook status unavailable: %s", err)
		return nil
	}
	binary, err := doctorExecutable()
	if err != nil {
		if _, writeErr := fmt.Fprintf(out, "hooks: binary unavailable (%s)\n", err); writeErr != nil {
			return writeErr
		}
		reportIssue("hook status binary unavailable: %s", err)
		return nil
	}
	statuses, statusErr := hooks.StatusAll(cfg, binary)
	if len(statuses) == 0 {
		if _, err := fmt.Fprintln(out, "hooks: no configured profiles"); err != nil {
			return err
		}
	}
	for _, status := range statuses {
		if status.Err != nil {
			if _, err := fmt.Fprintf(out, "hooks: %s (%s): error (%s)\n", status.Account, status.Harness, status.Err); err != nil {
				return err
			}
			reportIssue("hooks for %s cannot be checked: %s", status.Account, status.Err)
			continue
		}
		if _, err := fmt.Fprintf(out, "hooks: %s (%s): %s\n", status.Account, status.Harness, status.Status); err != nil {
			return err
		}
		if status.Status != hooks.SyncInSync {
			reportIssue("hooks for %s are %s", status.Account, status.Status)
		}
	}
	if statusErr != nil {
		reportIssue("hook status errors: %s", statusErr)
	}
	return nil
}

func writeDoctorRenderer(out interface{ Write([]byte) (int, error) }, snapshot daemon.EventsExport, windows []kitty.OSWindow, kittyOK bool, reportIssue func(string, ...any)) error {
	sockets := make(map[string]struct{})
	ids := daemonExportWindowIDs(snapshot, 0)
	for _, id := range ids {
		if socket := strings.TrimSpace(snapshot.WindowMetadata[id].Renderer.KittyListenOn); socket != "" {
			sockets[socket] = struct{}{}
		}
	}
	if len(sockets) == 0 {
		if _, err := fmt.Fprintln(out, "kitty sockets: none reported"); err != nil {
			return err
		}
	} else {
		values := make([]string, 0, len(sockets))
		for socket := range sockets {
			values = append(values, socket)
		}
		sort.Strings(values)
		if _, err := fmt.Fprintf(out, "kitty sockets: %s\n", strings.Join(values, ", ")); err != nil {
			return err
		}
	}

	if !kittyOK {
		_, err := fmt.Fprintln(out, "renderer drift: unavailable (Kitty is unreachable)")
		return err
	}
	liveByID := daemonExportLiveMetadata(windows, snapshot, 0)
	drift := make([]string, 0)
	for _, id := range ids {
		renderer := snapshot.WindowMetadata[id].Renderer
		live := liveByID[id]
		if !live.Found {
			if (renderer.StateSet && renderer.DesiredIcon != "") || (renderer.LoopingSet && renderer.Looping) {
				drift = append(drift, fmt.Sprintf("window %d is no longer live", id))
			}
			continue
		}
		if (renderer.StateSet && renderer.DesiredIcon != live.IconState) || (renderer.LoopingSet && renderer.Looping != live.Looping) {
			drift = append(drift, fmt.Sprintf("window %d wants icon=%q looping=%t; has icon=%q looping=%t", id, renderer.DesiredIcon, renderer.Looping, live.IconState, live.Looping))
		}
		if renderer.RenderFailures > 0 {
			detail := fmt.Sprintf("window %d has %d consecutive render failures", id, renderer.RenderFailures)
			if renderer.LastRenderError != "" {
				detail += ": " + renderer.LastRenderError
			}
			drift = append(drift, detail)
		}
	}
	if len(drift) == 0 {
		if _, err := fmt.Fprintln(out, "renderer drift: none"); err != nil {
			return err
		}
		return nil
	}
	if _, err := fmt.Fprintln(out, "renderer drift:"); err != nil {
		return err
	}
	for _, detail := range drift {
		if _, err := fmt.Fprintf(out, "- %s\n", detail); err != nil {
			return err
		}
		reportIssue("renderer drift: %s", detail)
	}
	return nil
}

func writeDoctorSuppressions(out interface{ Write([]byte) (int, error) }, snapshot daemon.EventsExport, now time.Time) error {
	cutoff := now.Add(-doctorSuppressionWindow).Unix()
	counts := make(map[string]int)
	for _, events := range snapshot.EventsByWindow {
		for _, event := range events {
			if event.At >= cutoff && event.Suppressed && event.SuppressionReason != "" {
				counts[event.SuppressionReason]++
			}
		}
	}
	if len(counts) == 0 {
		_, err := fmt.Fprintf(out, "recent suppressions (last %s): none\n", doctorSuppressionWindow)
		return err
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	if _, err := fmt.Fprintf(out, "recent suppressions (last %s):\n", doctorSuppressionWindow); err != nil {
		return err
	}
	for _, reason := range reasons {
		if _, err := fmt.Fprintf(out, "- %s: %d\n", reason, counts[reason]); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
