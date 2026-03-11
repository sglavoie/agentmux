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
	for candidate in "${TMPDIR:-/tmp}"/kitty-*; do
		[[ -e "$candidate" ]] || continue
		if ! mtime="$(_file_mtime "$candidate")"; then
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
	done < <(find "${TMPDIR:-/tmp}" -maxdepth 1 -name 'kitty-*' 2>/dev/null | while IFS= read -r p; do m="$(_file_mtime "$p")" && printf '%s %s\n' "$m" "$p"; done | sort -rn | while IFS=' ' read -r _ p; do printf '%s\0' "$p"; done)

	local candidate
	for candidate in "${candidates[@]+"${candidates[@]}"}"; do
		local probe_err
		if probe_err="$(kitty @ --to "$candidate" ls 2>&1 >/dev/null)"; then
			_INSTANCE_SOCKETS+=("$candidate")
			local bare="${candidate#unix:}"
			local base
			base="$(basename "$bare")"
			local label="$candidate"
			if [[ "$base" =~ ^kitty-([0-9]+) ]]; then
				label="K:${BASH_REMATCH[1]}"
			fi
			_INSTANCE_LABELS+=("$label")
		else
			ccm_debug "socket probe failed for $candidate: $probe_err"
		fi
	done

	if [[ ${#_INSTANCE_SOCKETS[@]} -eq 0 ]]; then
		if [[ ${#candidates[@]} -gt 0 ]]; then
			echo "Warning: Found ${#candidates[@]} kitty socket(s) but none responded. Check that remote control is enabled in kitty.conf (allow_remote_control socket-only)." >&2
		else
			echo "Warning: No kitty sockets found. Set KITTY_LISTEN_ON or check that Kitty is running." >&2
		fi
	fi
	ccm_debug "Discovered ${#_INSTANCE_SOCKETS[@]} active socket(s)"
}
