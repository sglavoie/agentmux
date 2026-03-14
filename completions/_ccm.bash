# Bash completion for ccm — Claude Code Manager CLI.
# Installed to ~/.local/share/bash-completion/completions/ccm by `just install`.
#
# Source manually:  source completions/_ccm.bash

_ccm_commands="launch status star config hooks logs audit dashboard switch idle-watch notify help"
_ccm_config_subs="edit show path init sync validate"
_ccm_config_files="accounts sessions defaults hooks"
_ccm_logs_subs="list show summary clean dir"
_ccm_idle_watch_opts="--start --stop --status"

_ccm_launch_flags="--all --add --close --restart --clone --move --list --save --cleanup --sockets --socket --instance --tab-id --target-instance --group --dry-run --help"

_ccm_audit_opts="--no-cache --skip-ai --section --json --quiet --update-manifest"
_ccm_audit_sections="claude-hooks claude-cli kitty-remote kitty-launch kitty-match"

_ccm_notify_opts="--title --subtitle --message --timeout"

# Check if cursor is at the account-name position after: launch --add TITLE DIR
_ccm_prev_is_add_dir() {
    local i found_add=0 pos_after_add=0
    for (( i=1; i < COMP_CWORD; i++ )); do
        if [[ "${COMP_WORDS[$i]}" == "--add" ]]; then
            found_add=1
            pos_after_add=0
        elif [[ $found_add -eq 1 ]]; then
            (( pos_after_add++ ))
        fi
    done
    [[ $found_add -eq 1 && $pos_after_add -eq 2 ]]
}

_ccm() {
    local cur prev
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    # Determine the subcommand (skip --socket ARG if present)
    local subcmd="" idx=1
    if [[ "${COMP_WORDS[1]}" == "--socket" ]]; then
        idx=3
    fi
    subcmd="${COMP_WORDS[$idx]:-}"

    # Complete --socket argument with kitty sockets
    if [[ "$prev" == "--socket" ]]; then
        local socks
        socks=$(compgen -G '/tmp/kitty-*' 2>/dev/null)
        COMPREPLY=( $(compgen -W "$socks" -- "$cur") )
        return
    fi

    # Top-level completion
    if [[ $COMP_CWORD -eq 1 ]] || { [[ "${COMP_WORDS[1]}" == "--socket" ]] && [[ $COMP_CWORD -eq 3 ]]; }; then
        COMPREPLY=( $(compgen -W "$_ccm_commands --socket --version" -- "$cur") )
        return
    fi

    case "$subcmd" in
        config)
            local config_sub="${COMP_WORDS[$((idx+1))]:-}"
            if [[ $COMP_CWORD -eq $((idx+1)) ]]; then
                COMPREPLY=( $(compgen -W "$_ccm_config_subs" -- "$cur") )
            elif [[ "$config_sub" == "edit" || "$config_sub" == "show" || "$config_sub" == "path" ]]; then
                COMPREPLY=( $(compgen -W "$_ccm_config_files" -- "$cur") )
            fi
            ;;
        launch)
            if [[ "$prev" == "--group" ]]; then
                # Try to get groups from sessions.toml
                local sessions_toml="${CC_MANAGER_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager}/sessions.toml"
                local helper="$HOME/.local/lib/cc-manager/toml-helper.py"
                if [[ -f "$sessions_toml" && -f "$helper" ]]; then
                    local groups
                    groups=$(python3 "$helper" read-sessions "$sessions_toml" 2>/dev/null | awk -F'\t' '$7 != "" {print $7}' | sort -u)
                    COMPREPLY=( $(compgen -W "$groups" -- "$cur") )
                elif [[ -f "$sessions_toml" ]]; then
                    # Fallback: extract group values with basic grep
                    local groups
                    groups=$(grep -oP '^\s*group\s*=\s*"\K[^"]+' "$sessions_toml" 2>/dev/null | sort -u)
                    COMPREPLY=( $(compgen -W "$groups" -- "$cur") )
                fi
            elif [[ "$prev" == "--save" ]]; then
                COMPREPLY=( $(compgen -f -- "$cur") )
            elif [[ "$prev" == "--socket" ]]; then
                local socks
                socks=$(compgen -G '/tmp/kitty-*' 2>/dev/null)
                COMPREPLY=( $(compgen -W "$socks" -- "$cur") )
            elif [[ "$prev" == "--add" ]]; then
                # Title argument — no completion
                :
            elif _ccm_prev_is_add_dir; then
                # Third positional arg after --add TITLE DIR → account name
                local accounts_toml="${CC_MANAGER_ACCOUNTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/accounts.toml}"
                local helper="$HOME/.local/lib/cc-manager/toml-helper.py"
                if [[ -f "$accounts_toml" && -f "$helper" ]]; then
                    local accts
                    accts=$(python3 "$helper" read-accounts "$accounts_toml" 2>/dev/null | cut -f1)
                    COMPREPLY=( $(compgen -W "$accts" -- "$cur") )
                else
                    COMPREPLY=( $(compgen -W "work personal" -- "$cur") )
                fi
            else
                COMPREPLY=( $(compgen -W "$_ccm_launch_flags" -- "$cur") )
            fi
            ;;
        status)
            COMPREPLY=( $(compgen -W "--socket --json --help" -- "$cur") )
            ;;
        hooks)
            local hooks_sub="${COMP_WORDS[$((idx+1))]:-}"
            if [[ $COMP_CWORD -eq $((idx+1)) ]]; then
                COMPREPLY=( $(compgen -W "sync status" -- "$cur") )
            elif [[ "$hooks_sub" == "sync" && $COMP_CWORD -eq $((idx+2)) ]]; then
                # Complete account names for hooks sync
                local accounts_toml="${CC_MANAGER_ACCOUNTS:-${XDG_CONFIG_HOME:-$HOME/.config}/cc-manager/accounts.toml}"
                local helper="$HOME/.local/lib/cc-manager/toml-helper.py"
                if [[ -f "$accounts_toml" && -f "$helper" ]]; then
                    local accts
                    accts=$(python3 "$helper" read-accounts "$accounts_toml" 2>/dev/null | cut -f1)
                    COMPREPLY=( $(compgen -W "$accts" -- "$cur") )
                else
                    COMPREPLY=( $(compgen -W "work personal" -- "$cur") )
                fi
            fi
            ;;
        logs)
            local logs_sub="${COMP_WORDS[$((idx+1))]:-}"
            if [[ $COMP_CWORD -eq $((idx+1)) ]]; then
                COMPREPLY=( $(compgen -W "$_ccm_logs_subs" -- "$cur") )
            elif [[ "$logs_sub" == "show" ]]; then
                if [[ "$cur" == -* ]]; then
                    COMPREPLY=( $(compgen -W "--pretty --tail" -- "$cur") )
                elif [[ "$prev" != "--tail" ]]; then
                    # Complete session IDs from log filenames
                    local log_dir="${CC_MANAGER_LOGS:-${XDG_STATE_HOME:-$HOME/.local/state}/cc-manager/activity}"
                    if [[ -d "$log_dir" ]]; then
                        local sessions
                        sessions=$(ls "$log_dir"/*.jsonl 2>/dev/null | xargs -I{} basename {} .jsonl)
                        COMPREPLY=( $(compgen -W "$sessions" -- "$cur") )
                    fi
                fi
            elif [[ "$logs_sub" == "summary" && $COMP_CWORD -eq $((idx+2)) ]]; then
                # Complete session IDs from log filenames
                local log_dir="${CC_MANAGER_LOGS:-${XDG_STATE_HOME:-$HOME/.local/state}/cc-manager/activity}"
                if [[ -d "$log_dir" ]]; then
                    local sessions
                    sessions=$(ls "$log_dir"/*.jsonl 2>/dev/null | xargs -I{} basename {} .jsonl)
                    COMPREPLY=( $(compgen -W "$sessions" -- "$cur") )
                fi
            elif [[ "$logs_sub" == "clean" ]]; then
                COMPREPLY=( $(compgen -W "--older-than --dry-run" -- "$cur") )
            fi
            ;;
        audit)
            if [[ "$prev" == "--section" ]]; then
                COMPREPLY=( $(compgen -W "$_ccm_audit_sections" -- "$cur") )
            else
                COMPREPLY=( $(compgen -W "$_ccm_audit_opts" -- "$cur") )
            fi
            ;;
        idle-watch)
            COMPREPLY=( $(compgen -W "$_ccm_idle_watch_opts" -- "$cur") )
            ;;
        notify)
            COMPREPLY=( $(compgen -W "$_ccm_notify_opts" -- "$cur") )
            ;;
        help)
            COMPREPLY=( $(compgen -W "launch status star config hooks logs audit dashboard switch idle-watch notify" -- "$cur") )
            ;;
    esac
}

complete -F _ccm ccm
