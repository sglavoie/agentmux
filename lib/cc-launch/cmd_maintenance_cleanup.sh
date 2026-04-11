#!/usr/bin/env bash

[[ -n "${_CC_LAUNCH_CMD_MAINTENANCE_CLEANUP_LOADED:-}" ]] && return 0
_CC_LAUNCH_CMD_MAINTENANCE_CLEANUP_LOADED=1

cmd_sockets() {
    _load_icons
    local strip_re
    strip_re="$(_icon_strip_regex)"
    local emoji_re
    emoji_re="$(_override_emoji_re)"
    local managed_re="${strip_re}${emoji_re}?[A-Z]:"

    local found=0
    for sock in "${TMPDIR:-/tmp}"/kitty-*; do
        [[ -e "$sock" ]] || continue
        found=1
        local pid="${sock#"${TMPDIR:-/tmp}"/kitty-}"
        local status="stale"
        local managed_count=""
        local ls_out
        if ls_out="$(kitty @ --to "unix:$sock" ls 2>/dev/null)"; then
            status="active"
            managed_count="$(printf '%s' "$ls_out" | jq -r --arg managed_re "$managed_re" '
				[.[] | .tabs[] | . as $tab |
					select(
						([$tab.windows[] | .user_vars.cc_account // empty | select(. != "")] | length > 0) or
						($tab.title | test($managed_re))
					)
				] | length
			' 2>/dev/null)" || managed_count="?"
        fi
        if [[ -n "$managed_count" && "$managed_count" != "0" ]]; then
            echo "unix:$sock  ($status, pid=$pid, $managed_count managed tab(s))"
        else
            echo "unix:$sock  ($status, pid=$pid)"
        fi
    done

    if [[ $found -eq 0 ]]; then
        echo "No kitty sockets found in ${TMPDIR:-/tmp}/" >&2
    fi

    if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
        echo "" >&2
        echo "KITTY_LISTEN_ON=$KITTY_LISTEN_ON" >&2
    fi
}

cmd_cleanup() {
    discover_all_sockets
    if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
        err "No active Kitty instances found."
        return 1
    fi

    local multi_instance=false
    if [[ ${#_INSTANCE_SOCKETS[@]} -gt 1 ]]; then
        multi_instance=true
    fi

    local initials=""
    local idx
    for ((idx = 0; idx < ${#_ACCOUNT_NAMES[@]}; idx++)); do
        local ch="${_ACCOUNT_NAMES[$idx]:0:1}"
        ch="$(echo "$ch" | tr '[:lower:]' '[:upper:]')"
        if [[ "$initials" != *"$ch"* ]]; then
            initials+="$ch"
        fi
    done
    if [[ -z "$initials" ]]; then
        local defaults=("work" "personal")
        local d ch_d
        for d in "${defaults[@]}"; do
            ch_d="$(echo "${d:0:1}" | tr '[:lower:]' '[:upper:]')"
            if [[ "$initials" != *"$ch_d"* ]]; then
                initials+="$ch_d"
            fi
        done
    fi
    local title_pattern="${_STRIP_RE}${_OVERRIDE_EMOJI_RE}[$initials]:"

    local stale=""
    local live_ids=""
    for ((idx = 0; idx < ${#_INSTANCE_SOCKETS[@]}; idx++)); do
        local inst_socket="${_INSTANCE_SOCKETS[$idx]}"
        local inst_label="${_INSTANCE_LABELS[$idx]}"
        local ls_json
        ls_json="$(kitty @ --to "$inst_socket" ls 2>/dev/null)" || continue

        local inst_stale
        inst_stale="$(echo "$ls_json" | jq -r --arg pat "$title_pattern" '
			[.[] | .tabs[] |
				select(.title | test($pat)) |
				select(
					[.windows[] | .foreground_processes[]?.cmdline[]? | test("claude")] | any | not
				) |
				{ title: .title, id: .id }
			] | .[] | [.title, (.id | tostring)] | @tsv
		')" || continue

        if [[ -n "$inst_stale" ]]; then
            while IFS=$'\t' read -r title tab_id; do
                if [[ -n "$stale" ]]; then
                    stale+=$'\n'
                fi
                stale+="${title}"$'\t'"${tab_id}"$'\t'"${inst_socket}"$'\t'"${inst_label}"
            done <<<"$inst_stale"
        fi

        local inst_live
        inst_live="$(echo "$ls_json" | jq -r --arg pat "$title_pattern" '
			[.[] | .tabs[] |
				select(
					(.title | test($pat)) or
					([.windows[] | .user_vars.cc_account // empty | select(. != "")] | length > 0)
				) | .id
			] | .[]
		')" || true
        if [[ -n "$inst_live" ]]; then
            if [[ -n "$live_ids" ]]; then
                live_ids+=$'\n'
            fi
            live_ids+="$inst_live"
        fi
    done

    if [[ -z "$stale" ]]; then
        echo "No stale sessions found." >&2
    else
        echo "Stale sessions (no Claude process running):" >&2
        echo "" >&2
        while IFS=$'\t' read -r title tab_id sock inst_label; do
            if [[ "$multi_instance" == "true" ]]; then
                echo "  $title (tab id: $tab_id, instance: $inst_label)" >&2
            else
                echo "  $title (tab id: $tab_id)" >&2
            fi
        done <<<"$stale"
        echo "" >&2

        if confirm "Close all stale sessions?" "Yes, close them"; then
            while IFS=$'\t' read -r title tab_id sock inst_label; do
                if kitty @ --to "$sock" close-tab --match "id:$tab_id"; then
                    echo "Closed: $title" >&2
                else
                    warn "failed to close '$title' (tab id: $tab_id)."
                fi
            done <<<"$stale"
        else
            echo "No sessions closed." >&2
        fi
    fi

    local orphaned=0
    for f in "${_CCM_TMPDIR}"/ccm-activity-*.counts "${_CCM_TMPDIR}"/ccm-notify-*; do
        [[ -e "$f" ]] || continue
        local fname="${f##*/}"
        local file_id=""
        case "$fname" in
        ccm-activity-*.counts)
            file_id="${fname#ccm-activity-}"
            file_id="${file_id%.counts}"
            ;;
        ccm-notify-*) file_id="${fname##*-}" ;;
        esac
        [[ -z "$file_id" ]] && continue
        if ! echo "$live_ids" | grep -qx "$file_id"; then
            rm -f "$f"
            orphaned=$((orphaned + 1))
        fi
    done
    if [[ $orphaned -gt 0 ]]; then
        echo "Removed $orphaned orphaned activity/notification file(s)." >&2
    fi
}
