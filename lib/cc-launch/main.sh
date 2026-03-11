#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_MAIN_LOADED:-}" ]] && return 0
_CC_LAUNCH_MAIN_LOADED=1

cmd_help() {
    cat <<'USAGE'
cc-launch: Manage Claude Code sessions as Kitty terminal tabs.

Usage:
	cc-launch [--socket SOCKET] [COMMAND]

Commands:
	(none)                          Interactively select sessions to launch (requires python3)
	--all                           Launch all sessions defined in the config file.
	                                Accepts --group NAME to launch only sessions in that group.
	--add TITLE DIR [ACCOUNT [ENV]]  Launch a single ad-hoc session tab (account picker if omitted)
	--close                         Close a tab by stable selector (requires --instance and --tab-id),
	                                or close all tabs in a group (with --group NAME, no --instance/--tab-id).
	                                Use --dry-run to preview without closing.
	--restart                       Restart a session by stable selector (requires --instance and --tab-id)
	--clone                         Clone a session by stable selector (requires --instance and --tab-id)
	--move                          Move a session to a different Kitty instance
									(requires --instance, --tab-id, and --target-instance)
	--list                          List open tabs running Claude
	--save [FILE]                   Save current Claude sessions to config file
	--cleanup                       Find and close stale sessions (dead processes)
	--sockets                       List available Kitty sockets
	--help                          Show this help message

Global options:
	--socket SOCKET     Target a specific Kitty socket (e.g. unix:/tmp/kitty-12345)
	--instance LABEL    Target a specific Kitty instance by label (e.g. K:12345).
						Use --sockets to list available instances.
	--tab-id ID         Target tab id within the selected instance.
	--group NAME        Filter by session group (for --all and --close).
						Mutually exclusive with --tab-id.
	--dry-run           Preview what would be closed (for --close).
	--target-instance LABEL
						Destination instance label for --move.

Config file (default: ~/.config/cc-manager/sessions.toml):
	Override with CC_MANAGER_CONFIG env var.
	TOML format with [[sessions]] entries. Required keys: title, directory, account.
	Optional keys: flags, project_window, env.
	Use "-" to disable global flags/project_window for a specific session.

Accounts (defined in ~/.config/cc-manager/accounts.toml):
	TOML format with [[accounts]] entries. Keys: name, config_dir, color.
	Colors: green, blue, red, orange, purple, teal, pink, yellow, gray, cyan
	If accounts.toml is missing, defaults to work (green) and personal (blue).

Defaults (defined in ~/.config/cc-manager/defaults.toml):
	TOML format with [defaults] and [notifications] sections.
	[defaults] keys: claude_flags, project_window, sort_sessions, dashboard_refresh.
	[notifications] keys: error, permission, stop, idle (booleans),
		idle_alert_minutes, min_secs (integers), ignore_patterns (string array).

Environment:
	KITTY_LISTEN_ON      Kitty remote-control socket (auto-detected if unset)
	CC_MANAGER_CONFIG    Path to sessions config file
	CC_MANAGER_ACCOUNTS  Path to accounts config file
	CC_MANAGER_DEFAULTS  Path to defaults config file
	CCM_DEBUG            Set to 1 for debug output

Optional dependency:
	gum (charmbracelet/gum) — enables interactive session picker and confirmation
	dialogs. Install with: brew install gum. Falls back to non-interactive
	behavior when gum is not installed.
USAGE
}

main() {
    _require_jq
    local instance_label=""
    local tab_id=""
    local target_instance=""
    _MOVE_TARGET_INSTANCE=""
    _TARGET_INSTANCE_LABEL=""
    _TARGET_TAB_ID=""
    _TARGET_GROUP=""
    _DRY_RUN=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
        --socket)
            if [[ -z "${2:-}" ]]; then
                echo "Error: --socket requires a SOCKET argument." >&2
                exit 1
            fi
            SOCKET="$2"
            shift 2
            ;;
        --instance)
            if [[ -z "${2:-}" ]]; then
                echo "Error: --instance requires a LABEL argument (e.g. K:12345)." >&2
                exit 1
            fi
            instance_label="$2"
            shift 2
            ;;
        --tab-id)
            if [[ -z "${2:-}" ]]; then
                echo "Error: --tab-id requires an ID argument." >&2
                exit 1
            fi
            tab_id="$2"
            shift 2
            ;;
        --target-instance)
            if [[ -z "${2:-}" ]]; then
                echo "Error: --target-instance requires a LABEL argument (e.g. K:12345)." >&2
                exit 1
            fi
            target_instance="$2"
            shift 2
            ;;
        --group)
            if [[ -z "${2:-}" ]]; then
                echo "Error: --group requires a NAME argument." >&2
                exit 1
            fi
            _TARGET_GROUP="$2"
            shift 2
            ;;
        --dry-run)
            _DRY_RUN="true"
            shift
            ;;
        *) break ;;
        esac
    done

    if [[ -n "$tab_id" && ! "$tab_id" =~ ^[0-9]+$ ]]; then
        echo "Error: --tab-id must be a numeric Kitty tab id." >&2
        exit 1
    fi

    if [[ -n "$_TARGET_GROUP" && -n "$tab_id" ]]; then
        echo "Error: --group and --tab-id are mutually exclusive." >&2
        exit 1
    fi

    _TARGET_INSTANCE_LABEL="$instance_label"
    _TARGET_TAB_ID="$tab_id"
    _MOVE_TARGET_INSTANCE="$target_instance"

    case "${1:-}" in
    --sockets)
        cmd_sockets
        return
        ;;
    --help | -h)
        cmd_help
        return
        ;;
    esac

    if [[ -n "$instance_label" ]]; then
        discover_all_sockets
        local idx found=false
        for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
            if [[ "${_INSTANCE_LABELS[$idx]}" == "$instance_label" ]]; then
                SOCKET="${_INSTANCE_SOCKETS[$idx]}"
                found=true
                break
            fi
        done
        if [[ "$found" == "false" ]]; then
            echo "Error: No active Kitty instance found with label '$instance_label'." >&2
            echo "Available instances:" >&2
            for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
                echo "  ${_INSTANCE_LABELS[$idx]}  (${_INSTANCE_SOCKETS[$idx]})" >&2
            done
            exit 1
        fi
    fi

    if [[ -z "$SOCKET" ]]; then
        SOCKET="$(discover_socket)"
    fi

    if [[ -z "$SOCKET" ]]; then
        echo "Error: No Kitty socket found. Is Kitty running with remote control enabled?" >&2
        exit 1
    fi

    load_accounts
    load_defaults
    _load_icons
    _STRIP_RE="$(_icon_strip_regex)"
    _MANAGED_RE="${_STRIP_RE}[A-Z]:"

    case "${1:-}" in
    --all)
        cmd_launch_all
        ;;
    --add)
        shift
        cmd_add "$@"
        ;;
    --close)
        shift
        if [[ -n "$_TARGET_GROUP" ]]; then
            cmd_close_group "$@"
        else
            cmd_close "$@"
        fi
        ;;
    --restart)
        shift
        cmd_restart "$@"
        ;;
    --clone)
        shift
        cmd_clone "$@"
        ;;
    --move)
        shift
        cmd_move "$@"
        ;;
    --list)
        cmd_list
        ;;
    --cleanup)
        cmd_cleanup
        ;;
    --save)
        shift
        cmd_save "$@"
        ;;
    "")
        if command -v python3 >/dev/null 2>&1; then
            cmd_launch_interactive
        else
            cmd_launch_all
        fi
        ;;
    *)
        echo "Error: Unknown option '$1'. See --help for usage." >&2
        exit 1
        ;;
    esac
}
