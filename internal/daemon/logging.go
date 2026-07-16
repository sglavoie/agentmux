package daemon

import (
	"io"
	"log/slog"
	"os"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const (
	daemonDetachedEnv  = "AMX_DAEMON_DETACHED"
	daemonLaunchIDEnv  = "AMX_DAEMON_LAUNCH_ID"
	daemonBootstrapEnv = "AMX_DAEMON_BOOTSTRAP_LOG"
)

// NewOperationalLogger returns the durable structured daemon logger. A
// foreground daemon mirrors the same JSON records to stderr; a detached daemon
// writes only to the private state directory inherited from bootstrap.
func NewOperationalLogger(cfg config.DiagnosticsConfig, debug, foreground bool) (*slog.Logger, io.Closer, string, error) {
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return nil, nil, "", err
	}
	writer, err := diagnostics.NewDaemonLogWriter(paths, diagnostics.DaemonLogOptions{
		KeepDays: cfg.JournalKeepDays,
		MaxBytes: int64(cfg.JournalMaxMiB) << 20,
	})
	if err != nil {
		return nil, nil, "", err
	}
	var output io.Writer = writer
	if foreground {
		output = io.MultiWriter(writer, os.Stderr)
	}
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level, AddSource: debug})
	logger := slog.New(handler).With(
		"component", "daemon",
		"launch_id", os.Getenv(daemonLaunchIDEnv),
		"bootstrap_log", os.Getenv(daemonBootstrapEnv),
		"build", buildinfo.Current(),
	)
	return logger, writer, paths.DaemonDir, nil
}
