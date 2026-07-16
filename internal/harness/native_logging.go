package harness

import (
	"path/filepath"
	"strings"
)

func applyNativeLogging(name string, input *InvocationInput, args []string) []string {
	request := input.NativeLogging
	if !request.Enabled {
		input.nativeLogging = NativeLogging{Coverage: NativeLogDisabled}
		return args
	}

	switch Normalize(name) {
	case Codex:
		return applyCodexNativeLogging(input, args, request)
	case Copilot:
		return applyCopilotNativeLogging(input, args, request)
	default:
		return applyClaudeNativeLogging(input, args, request)
	}
}

func applyClaudeNativeLogging(input *InvocationInput, args []string, request NativeLoggingRequest) []string {
	path, found, valid := longOption(args, "--debug-file")
	if found {
		coverage := NativeLogExternal
		if !valid {
			coverage = NativeLogDegraded
		}
		input.nativeLogging = NativeLogging{
			Coverage: coverage,
			Path:     path,
			Level:    "debug",
			Override: "--debug-file",
		}
		return args
	}

	path = filepath.Join(request.Directory, "claude-debug.log")
	input.nativeLogging = NativeLogging{
		Coverage: NativeLogManaged, Path: path, Level: "debug", ManagedDirectory: true,
	}
	return append(args, "--debug-file", path)
}

func applyCodexNativeLogging(input *InvocationInput, args []string, request NativeLoggingRequest) []string {
	environment := cloneEnvironment(input.Environment)
	level, levelOverride := environment["RUST_LOG"]
	if !levelOverride {
		level = "debug"
		environment["RUST_LOG"] = level
	}
	input.Environment = environment

	path, pathOverride, pathValid := codexLogDir(args)
	managedDirectory := false
	if !pathOverride {
		path = request.Directory
		pathValid = path != ""
		managedDirectory = true
		args = append(args, "-c", "log_dir="+path)
	}

	overrides := make([]string, 0, 2)
	if levelOverride {
		overrides = append(overrides, "RUST_LOG")
	}
	if pathOverride {
		overrides = append(overrides, "log_dir")
	}
	coverage := NativeLogManaged
	if len(overrides) > 0 {
		coverage = NativeLogExternal
	}
	if !pathValid || !debugLevel(level) {
		coverage = NativeLogDegraded
	}
	input.nativeLogging = NativeLogging{
		Coverage: coverage, Path: path, Level: level,
		Override: strings.Join(overrides, ","), ManagedDirectory: managedDirectory,
	}
	return args
}

func applyCopilotNativeLogging(input *InvocationInput, args []string, request NativeLoggingRequest) []string {
	path, pathOverride, pathValid := longOption(args, "--log-dir")
	level, levelOverride, levelValid := longOption(args, "--log-level")
	managedDirectory := false
	if !pathOverride {
		path = request.Directory
		pathValid = path != ""
		managedDirectory = true
		args = append(args, "--log-dir", path)
	}
	if !levelOverride {
		level = "debug"
		levelValid = true
		args = append(args, "--log-level", level)
	}

	overrides := make([]string, 0, 2)
	if pathOverride {
		overrides = append(overrides, "--log-dir")
	}
	if levelOverride {
		overrides = append(overrides, "--log-level")
	}
	coverage := NativeLogManaged
	if len(overrides) > 0 {
		coverage = NativeLogExternal
	}
	if !pathValid || !levelValid || !debugLevel(level) {
		coverage = NativeLogDegraded
	}
	input.nativeLogging = NativeLogging{
		Coverage: coverage, Path: path, Level: level,
		Override: strings.Join(overrides, ","), ManagedDirectory: managedDirectory,
	}
	return args
}

// longOption returns the last occurrence because all three supported CLIs use
// last-write-wins option parsing for repeated global flags.
func longOption(args []string, name string) (value string, found, valid bool) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == name:
			found = true
			if index+1 < len(args) && args[index+1] != "" && !strings.HasPrefix(args[index+1], "-") {
				value, valid = args[index+1], true
				index++
			} else {
				value, valid = "", false
			}
		case strings.HasPrefix(arg, name+"="):
			found = true
			value = strings.TrimPrefix(arg, name+"=")
			valid = value != ""
		}
	}
	return value, found, valid
}

func codexLogDir(args []string) (value string, found, valid bool) {
	if direct, directFound, directValid := longOption(args, "--log-dir"); directFound {
		return direct, true, directValid
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		var configValue string
		switch {
		case arg == "-c" || arg == "--config":
			if index+1 >= len(args) {
				continue
			}
			configValue = args[index+1]
			index++
		case strings.HasPrefix(arg, "-c="):
			configValue = strings.TrimPrefix(arg, "-c=")
		case strings.HasPrefix(arg, "--config="):
			configValue = strings.TrimPrefix(arg, "--config=")
		default:
			continue
		}
		key, configured, ok := strings.Cut(configValue, "=")
		if strings.TrimSpace(key) != "log_dir" {
			continue
		}
		found = true
		value = strings.Trim(strings.TrimSpace(configured), `"'`)
		valid = ok && value != ""
	}
	return value, found, valid
}

func debugLevel(level string) bool {
	lower := strings.ToLower(strings.TrimSpace(level))
	return lower == "debug" || lower == "trace" || strings.Contains(lower, "=debug") || strings.Contains(lower, "=trace")
}
