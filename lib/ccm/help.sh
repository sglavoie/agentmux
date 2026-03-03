#!/usr/bin/env bash

[[ -n "${_CCM_HELP_LOADED:-}" ]] && return 0
_CCM_HELP_LOADED=1

cmd_help() {
	local subcmd="${1:-}"
	case "$subcmd" in
	launch)
		exec cc-launch ${SOCKET_ARGS[@]+"${SOCKET_ARGS[@]}"} --help
		;;
	status)
		exec cc-status --help
		;;
	config)
		cat <<'HELP'
Usage: ccm config <subcommand> [FILE]

Subcommands:
    edit [FILE]     Open config file(s) in $VISUAL/$EDITOR/vi
    show [FILE]     Print config file contents to stdout
    path [FILE]     Print resolved config file path(s)
    init            Copy example configs to ~/.config/cc-manager/ if missing
    sync            Update documentation preambles in existing config files

FILE can be: accounts, sessions, defaults, hooks
When FILE is omitted, edit/show/path operate on all four config files.

The sync subcommand replaces the documentation header (above the marker line)
in each user config file with the latest version from the example files, while
preserving all user data below the marker. This runs automatically during
'make install' but can also be invoked standalone.
HELP
		;;
	notify)
		cat <<'HELP'
Usage: ccm notify [--title TEXT] [--subtitle TEXT] [--message TEXT] [--timeout SECS]

Send a test macOS notification via alerter. Useful for verifying that
notifications are working correctly.

Options:
    --title TEXT       Notification title (default: "cc-manager")
    --subtitle TEXT    Notification subtitle (default: "Test notification")
    --message TEXT     Notification body (default: "If you see this, notifications are working.")
    --timeout SECS    Auto-dismiss after SECS seconds (default: 5)

Requires: brew install vjeantet/tap/alerter
HELP
		;;
	hooks)
		cat <<'HELP'
Usage: ccm hooks <subcommand> [ACCOUNT]

Subcommands:
    sync [ACCOUNT]    Sync hooks into each account's settings.json
                      Optionally sync a single account by name.
                      Backs up settings.json before modifying.
                      User-defined hook entries are preserved.
    status            Show drift status for all accounts.
                      Reports "in_sync", "drifted", or "missing" for each.

The hooks system is configured via ~/.config/cc-manager/hooks.json, which
defines global hook events and optional per-account overrides (disabled_events,
extra_hooks). Use the dashboard (H key) for interactive management.
HELP
		;;
	logs)
		cat <<'HELP'
Usage: ccm logs <subcommand> [args]

Subcommands:
    list              List all session log files (sorted by last-modified)
    show SESSION_ID   Show raw JSONL entries for a session
                        --pretty    Format output with jq
                        --tail N    Show only the last N entries
    summary SESSION_ID  Print an aggregate summary (event counts, top tools,
                        duration, whether the session ended)
    clean             Delete old log files
                        --older-than DAYS  Age threshold (default: 30)
                        --dry-run          Preview without deleting
    dir               Print the resolved log directory path

Log files are stored in ${CC_MANAGER_LOGS:-~/.local/state/cc-manager/activity/}
as per-session JSONL files written by the cc-activity-log hook.
HELP
		;;
	audit)
		exec cc-audit --help
		;;
	idle-watch)
		exec cc-idle-watch --help
		;;
	switch)
		cat <<'HELP'
Usage: ccm switch

Launch a lightweight fuzzy-finder overlay listing all running Claude sessions.
Type to filter by title, account, or working directory. Use arrow keys or
ctrl+n/ctrl+p to navigate, Enter to jump to the selected session, Escape to
dismiss without switching.

Keyboard shortcut: ctrl+shift+p (in Kitty with cc-manager.conf included).
HELP
		;;
	"")
		cat <<HELP
ccm $CCM_VERSION — Claude Code Manager CLI

Usage: ccm <command> [args...]

Commands:
    launch [args]       Manage and launch Claude Code sessions (delegates to cc-launch)
    status [args]       Show active session status table (delegates to cc-status)
    star [args]         Toggle star on active tab (delegates to cc-toggle-star)
    config <subcmd>     Manage config files (edit, show, path, init)
    hooks <subcmd>      Manage hooks (sync, status)
    logs <subcmd>       Query and manage activity logs
    dashboard           Launch the curses dashboard kitten
    switch              Quick-switch: fuzzy-find and jump to a session
    notify [flags]      Send a test notification via alerter
    audit [flags]       Check upstream feature compatibility
    idle-watch <cmd>    Manage background idle notification watcher
	help [COMMAND]      Show help for ccm or a specific command
    --version           Print version

Global options:
    --socket SOCKET     Target a specific Kitty socket

Run 'ccm help <command>' for details on a specific command.
Tip: Run 'ccm help launch' for selector-based destructive launch commands
	using --instance LABEL and --tab-id ID.
HELP
		;;
	*)
		echo "No help available for '$subcmd'. Run 'ccm help' to see all commands." >&2
		return 1
		;;
	esac
}
