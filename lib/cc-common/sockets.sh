#!/usr/bin/env bash

[[ -n "${_CC_COMMON_SOCKETS_LOADED:-}" ]] && return 0
_CC_COMMON_SOCKETS_LOADED=1

discover_socket() {
	if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
		ccm_debug "Socket from KITTY_LISTEN_ON: $KITTY_LISTEN_ON"
		echo "$KITTY_LISTEN_ON"
		return
	fi

	local latest=""
	local candidate mtime newest_mtime=0
	for candidate in /tmp/kitty-*; do
		[[ -e "$candidate" ]] || continue
		if mtime="$(stat -f %m "$candidate" 2>/dev/null)"; then
			:
		elif mtime="$(stat -c %Y "$candidate" 2>/dev/null)"; then
			:
		else
			continue
		fi
		if ((mtime > newest_mtime)); then
			newest_mtime=$mtime
			latest="$candidate"
		fi
	done

	if [[ -n "$latest" ]]; then
		ccm_debug "Socket from file: unix:$latest"
		echo "unix:$latest"
		return
	fi

	echo "Error: No kitty socket found. Set KITTY_LISTEN_ON or ensure kitty is running with remote control enabled." >&2
	exit 1
}

_INSTANCE_SOCKETS=()
_INSTANCE_LABELS=()

discover_all_sockets() {
	_INSTANCE_SOCKETS=()
	_INSTANCE_LABELS=()
	local -a candidates=()
	local -a seen=()

	if [[ -n "${KITTY_LISTEN_ON:-}" ]]; then
		candidates+=("$KITTY_LISTEN_ON")
		seen+=("$KITTY_LISTEN_ON")
	fi

	local sock_path
	while IFS= read -r -d '' sock_path; do
		local sock="unix:$sock_path"
		local already=false
		local seen_sock
		for seen_sock in "${seen[@]+"${seen[@]}"}"; do
			if [[ "$seen_sock" == "$sock" ]]; then
				already=true
				break
			fi
		done
		if [[ "$already" == "false" ]]; then
			candidates+=("$sock")
			seen+=("$sock")
		fi
	done < <(find /tmp -maxdepth 1 -name 'kitty-*' -print0 2>/dev/null | xargs -0 -r stat -f '%m %N' 2>/dev/null | sort -rn | while IFS=' ' read -r _ p; do printf '%s\0' "$p"; done)

	local candidate
	for candidate in "${candidates[@]+"${candidates[@]}"}"; do
		if kitty @ --to "$candidate" ls >/dev/null 2>&1; then
			_INSTANCE_SOCKETS+=("$candidate")
			local bare="${candidate#unix:}"
			local base
			base="$(basename "$bare")"
			local label="$candidate"
			if [[ "$base" =~ ^kitty-([0-9]+) ]]; then
				label="K:${BASH_REMATCH[1]}"
			fi
			_INSTANCE_LABELS+=("$label")
		fi
	done
	ccm_debug "Discovered ${#_INSTANCE_SOCKETS[@]} active socket(s)"
}
