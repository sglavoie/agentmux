package cli

import (
	"log/slog"
	"os"
	"strings"
)

// DebugFlag carries the root command's persistent --debug flag. It is the one
// piece of process-wide state this package keeps: the flag is bound once during
// root command assembly, and command families that run long enough to log
// (notably `amx daemon start`) read the resulting verbosity through
// DebugEnabled rather than reaching back into the root package for the flag.
var DebugFlag bool

// DebugEnabled reports whether verbose debug output was requested, by the
// persistent flag or by the environment. Both env spellings are honored because
// the historical lowercase form is still set by existing operator shells.
func DebugEnabled() bool {
	if DebugFlag {
		return true
	}
	for _, key := range []string{"amx_DEBUG", "AMX_DEBUG"} {
		if value, ok := os.LookupEnv(key); ok {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "1", "true", "yes", "on":
				return true
			}
		}
	}
	return false
}

// ConfigureLogging installs the process-wide structured logger. Debug runs get
// level Debug and source positions; ordinary runs get level Info without them,
// so a normal invocation's stderr stays readable.
func ConfigureLogging(enabled bool) {
	level := slog.LevelInfo
	if enabled {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     level,
		AddSource: enabled,
	})
	slog.SetDefault(slog.New(handler))
}
