package session

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
)

// BuildInvocation resolves a configured project's effective arguments,
// profile home environment, and working directory before delegating CLI
// ordering to its harness adapter. It is the single session-level seam for
// fresh, resume, and fork launches.
func BuildInvocation(
	sess config.Session,
	acc config.Account,
	defaults config.Defaults,
	kind harness.InvocationKind,
	sessionID string,
	initialPrompt string,
) (harness.Invocation, error) {
	return buildInvocation(sess, acc, defaults, kind, sessionID, initialPrompt, false)
}

func buildLaunchInvocation(
	sess config.Session,
	acc config.Account,
	defaults config.Defaults,
	kind harness.InvocationKind,
	sessionID string,
	initialPrompt string,
) (harness.Invocation, error) {
	return buildInvocation(sess, acc, defaults, kind, sessionID, initialPrompt, true)
}

func buildInvocation(
	sess config.Session,
	acc config.Account,
	defaults config.Defaults,
	kind harness.InvocationKind,
	sessionID string,
	initialPrompt string,
	controlledLaunch bool,
) (harness.Invocation, error) {
	if !controlledLaunch {
		return harness.BuildInvocation(acc.EffectiveHarness(), kind, harness.InvocationInput{
			ProfileArgs:   resolveArgv(sess, acc, defaults),
			Environment:   ResolveEnv(sess, acc),
			CWD:           config.ExpandTilde(sess.Directory),
			SessionID:     sessionID,
			InitialPrompt: initialPrompt,
		})
	}

	launchID, err := diagnostics.NewLaunchID()
	if err != nil {
		return harness.Invocation{}, err
	}
	nativeRequest := harness.NativeLoggingRequest{
		Enabled:  defaults.Diagnostics.NativeLoggingValue(),
		LaunchID: launchID,
	}
	var paths diagnostics.Paths
	if nativeRequest.Enabled {
		paths, err = diagnostics.DefaultPaths()
		if err != nil {
			return harness.Invocation{}, fmt.Errorf("preparing native harness logging: %w", err)
		}
		nativeRequest.Directory, err = diagnostics.NativeLaunchDir(paths, launchID)
		if err != nil {
			return harness.Invocation{}, err
		}
	}

	invocation, err := harness.BuildInvocation(acc.EffectiveHarness(), kind, harness.InvocationInput{
		ProfileArgs:   resolveArgv(sess, acc, defaults),
		Environment:   ResolveEnv(sess, acc),
		CWD:           config.ExpandTilde(sess.Directory),
		SessionID:     sessionID,
		InitialPrompt: initialPrompt,
		NativeLogging: nativeRequest,
	})
	if err != nil {
		return harness.Invocation{}, err
	}
	if invocation.NativeLogging.ManagedDirectory {
		if _, err := diagnostics.EnsureNativeLaunch(paths, launchID); err != nil {
			return harness.Invocation{}, fmt.Errorf("preparing native harness log directory: %w", err)
		}
	}
	return invocation, nil
}

// RenderInvocation turns a structured invocation into the shell command that
// Kitty launches. Arguments are quoted here, after each harness has finalized
// their ordering. Environment values that determine the selected profile or
// native logging coverage are also pinned here: an interactive login shell can
// otherwise overwrite the environment passed to Kitty before the harness
// starts.
func RenderInvocation(invocation harness.Invocation) string {
	command := renderInvocation(invocation)
	if !isolateCodexLaunch(invocation) {
		return command
	}
	// Probe in the launch shell, whose PATH selects the actual CLI. Versions
	// without this option must not receive an unknown flag. Do not probe in
	// the app process: its PATH can select another installation. Both branches
	// still exec the terminal's own process.
	probe := invocation
	probe.Args = []string{"--help"}
	probe.NativeLogging.ManagedDirectory = false
	help := strings.TrimPrefix(renderInvocation(probe), "exec ")
	local := invocation
	local.Args = append([]string{"--no-daemon"}, invocation.Args...)
	return "amx_codex_help=$(" + help + " 2>/dev/null) || amx_codex_help=; " +
		"case \"$amx_codex_help\" in *--no-daemon*) " + renderInvocation(local) +
		" ;; *) " + command + " ;; esac"
}

// A launch ID marks a managed interactive launch. Pure command previews keep
// their stable argv, and explicit transport choices remain the user's choice.
func isolateCodexLaunch(invocation harness.Invocation) bool {
	if invocation.LaunchID == "" || harness.Normalize(invocation.Harness) != harness.Codex {
		return false
	}
	args := invocation.Args
	if invocation.InitialPrompt != "" && len(args) > 0 {
		args = args[:len(args)-1]
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--no-daemon" || arg == "--remote" || strings.HasPrefix(arg, "--remote=") {
			return false
		}
	}
	return true
}

func renderInvocation(invocation harness.Invocation) string {
	parts := make([]string, 0, len(invocation.Args)+5)
	if invocation.NativeLogging.ManagedDirectory {
		parts = append(parts, "umask", "077", "&&")
	}
	parts = append(parts, "exec")
	environment := make([]string, 0, 2)
	if envVar := harness.EnvVar(invocation.Harness); envVar != "" {
		if envValue, ok := invocation.Environment[envVar]; ok {
			environment = append(environment, shellQuoteWord(envVar+"="+envValue))
		}
	}
	if harness.Normalize(invocation.Harness) == harness.Codex {
		if rustLog, ok := invocation.Environment["RUST_LOG"]; ok {
			environment = append(environment, shellQuoteWord("RUST_LOG="+rustLog))
		}
	}
	if len(environment) > 0 {
		parts = append(parts, "env")
		parts = append(parts, environment...)
	}
	parts = append(parts, shellQuoteWord(invocation.Executable))
	for _, arg := range invocation.Args {
		parts = append(parts, shellQuoteWord(arg))
	}
	return strings.Join(parts, " ")
}
