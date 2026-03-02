#!/usr/bin/env bash
# shellcheck shell=bash
# shellcheck disable=SC2154

[[ -n "${_CC_AUDIT_FETCH_LOADED:-}" ]] && return 0
_CC_AUDIT_FETCH_LOADED=1

# ---------------------------------------------------------------------------
# Fetch and cache upstream docs
# ---------------------------------------------------------------------------
fetch_docs() {
	mkdir -p "$CACHE_DIR"

	local url_keys url_count key url cache_file file_age now
	url_keys="$(jq -r '.urls | keys[]' "$MANIFEST")"
	url_count="$(echo "$url_keys" | wc -l | tr -d ' ')"

	echo "${_C_BOLD}Fetching upstream documentation ($url_count sources)...${_C_RESET}"

	now="$(date +%s)"

	local fetch_keys=""
	while IFS= read -r key; do
		cache_file="$CACHE_DIR/$key.txt"

		if [[ "$opt_no_cache" == "false" && -f "$cache_file" ]]; then
			file_age="$(stat -f %m "$cache_file" 2>/dev/null || echo 0)"
			if ((now - file_age < 86400)); then
				local file_size
				file_size="$(wc -c <"$cache_file" | tr -d ' ')"
				if ((file_size >= 100)); then
					echo "  ${_C_DIM}cached${_C_RESET}  $key"
					continue
				fi
			fi
		fi

		fetch_keys="$fetch_keys $key"
	done <<<"$url_keys"

	local pids="" failures=0
	for key in $fetch_keys; do
		(
			url="$(jq -r --arg k "$key" '.urls[$k]' "$MANIFEST")"
			cache_file="$CACHE_DIR/$key.txt"

			echo "  ${_C_CYAN}fetch${_C_RESET}   $key"
			local strip_ok=false
			if [[ "$has_lynx" == "true" ]]; then
				if curl -sSfL --max-time 30 "$url" 2>/dev/null |
					lynx -stdin -dump -nolist -width=120 2>/dev/null >"$cache_file.tmp"; then
					strip_ok=true
				fi
			else
				if curl -sSfL --max-time 30 "$url" 2>/dev/null |
					sed 's/<[^>]*>//g' |
					sed '/^[[:space:]]*$/d' >"$cache_file.tmp"; then
					strip_ok=true
				fi
			fi
			if [[ "$strip_ok" == "true" ]]; then
				local tmp_size
				tmp_size="$(wc -c <"$cache_file.tmp" | tr -d ' ')"
				if ((tmp_size >= 100)); then
					mv "$cache_file.tmp" "$cache_file"
				else
					rm -f "$cache_file.tmp"
					echo "  ${_C_YELLOW}warn${_C_RESET}    $key: response too small, skipping"
					exit 1
				fi
			else
				rm -f "$cache_file.tmp"
				echo "  ${_C_YELLOW}warn${_C_RESET}    $key: fetch failed"
				exit 1
			fi
		) &
		pids="$pids $!"
	done

	for pid in $pids; do
		if ! wait "$pid"; then
			failures=$((failures + 1))
		fi
	done

	if ((failures > 0)); then
		echo "  ${_C_YELLOW}warn${_C_RESET}    $failures fetch(es) failed or produced warnings"
	fi

	echo ""
}
