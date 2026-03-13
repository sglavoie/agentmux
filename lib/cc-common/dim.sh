#!/usr/bin/env bash

[[ -n "${_CC_COMMON_DIM_LOADED:-}" ]] && return 0
_CC_COMMON_DIM_LOADED=1

# Halve the RGB components of a hex color string.
# Usage: _dim_hex "#2d7d46" => "#163e23"
_dim_hex() {
	local hex="$1"
	hex="${hex#\#}"
	local r g b
	r=$((16#${hex:0:2} / 2))
	g=$((16#${hex:2:2} / 2))
	b=$((16#${hex:4:2} / 2))
	printf '#%02x%02x%02x' "$r" "$g" "$b"
}

# Dim a foreground color string (e.g., "#ffffff" -> "#666666").
_dim_fg() {
	local hex="$1"
	hex="${hex#\#}"
	local r g b
	r=$((16#${hex:0:2}))
	g=$((16#${hex:2:2}))
	b=$((16#${hex:4:2}))
	# Map to ~40% brightness
	r=$((r * 2 / 5))
	g=$((g * 2 / 5))
	b=$((b * 2 / 5))
	printf '#%02x%02x%02x' "$r" "$g" "$b"
}

# Check if a tab is dimmed.
# Echoes the dim type ("manual" or "auto") and returns 0 if dimmed, 1 if not.
is_dimmed() {
	local tab_id="$1"
	local dim_file="${_CCM_TMPDIR}/ccm-dim-${tab_id}"
	if [[ -f "$dim_file" ]]; then
		head -n1 "$dim_file"
		return 0
	fi
	return 1
}

# Apply dim (darkened colors) to a tab.
# Usage: apply_dim SOCKET TAB_ID WINDOW_ID DIM_TYPE COLOR_ARGS
#   SOCKET     — kitty socket path
#   TAB_ID     — kitty tab id
#   WINDOW_ID  — kitty window id (can be empty; used to read cc_color user var)
#   DIM_TYPE   — "manual" or "auto"
#   COLOR_ARGS — (optional) original color args string like "active_bg=#2d7d46 ..."
#                If omitted, resolved from cc_color user var or account default.
apply_dim() {
	local socket="$1" tab_id="$2" win_id="$3" dim_type="$4"
	local color_args="${5:-}"

	if [[ -z "$color_args" ]]; then
		# Try to resolve original colors from the tab's user vars
		local account="" cc_color=""
		local ls_json
		ls_json="$(kitty @ --to "$socket" ls 2>/dev/null)" || true
		if [[ -n "$ls_json" ]]; then
			local uv_json
			if [[ -n "$win_id" ]]; then
				# Look up user vars by window id
				uv_json="$(printf '%s' "$ls_json" | jq --argjson wid "$win_id" '
					[.[] | .tabs[] | .windows[] | select(.id == $wid) | .user_vars] | first // {}
				' 2>/dev/null)" || uv_json="{}"
			else
				# Look up user vars by tab id (first window in the tab)
				uv_json="$(printf '%s' "$ls_json" | jq --argjson tid "$tab_id" '
					[.[] | .tabs[] | select(.id == $tid) | .windows[0].user_vars] | first // {}
				' 2>/dev/null)" || uv_json="{}"
			fi
			account="$(printf '%s' "$uv_json" | jq -r '.cc_account // ""')"
			cc_color="$(printf '%s' "$uv_json" | jq -r '.cc_color // ""')"
		fi

		if [[ -n "$cc_color" ]]; then
			color_args="$(resolve_palette_color "$cc_color")"
		elif [[ -n "$account" ]]; then
			color_args="$(tab_colors_for_account "$account")"
		else
			ccm_debug "apply_dim: cannot resolve colors for tab $tab_id"
			return 1
		fi
	fi

	# Parse original color args into individual values
	local active_bg="" active_fg="" inactive_bg="" inactive_fg=""
	local pair
	for pair in $color_args; do
		case "$pair" in
		active_bg=*) active_bg="${pair#active_bg=}" ;;
		active_fg=*) active_fg="${pair#active_fg=}" ;;
		inactive_bg=*) inactive_bg="${pair#inactive_bg=}" ;;
		inactive_fg=*) inactive_fg="${pair#inactive_fg=}" ;;
		esac
	done

	# Compute dimmed colors
	local dim_abg dim_afg dim_ibg dim_ifg
	dim_abg="$(_dim_hex "$active_bg")"
	dim_afg="$(_dim_fg "$active_fg")"
	dim_ibg="$(_dim_hex "$inactive_bg")"
	dim_ifg="$(_dim_fg "$inactive_fg")"

	# Apply dimmed colors to the tab
	# shellcheck disable=SC2086
	kitty @ --to "$socket" set-tab-color --match "id:$tab_id" \
		"active_bg=$dim_abg" "active_fg=$dim_afg" \
		"inactive_bg=$dim_ibg" "inactive_fg=$dim_ifg" || {
		ccm_debug "apply_dim: set-tab-color failed for tab $tab_id"
		return 1
	}

	# Write dim state file with original colors for restore
	local dim_file="${_CCM_TMPDIR}/ccm-dim-${tab_id}"
	printf '%s\n%s\n' "$dim_type" "$color_args" >"$dim_file"
}

# Restore original colors on a dimmed tab.
# Usage: apply_undim SOCKET TAB_ID WINDOW_ID
apply_undim() {
	local socket="$1" tab_id="$2" win_id="$3"
	local dim_file="${_CCM_TMPDIR}/ccm-dim-${tab_id}"

	if [[ ! -f "$dim_file" ]]; then
		ccm_debug "apply_undim: no dim file for tab $tab_id"
		return 1
	fi

	# Read original color args from line 2 of the dim file
	local color_args
	color_args="$(sed -n '2p' "$dim_file")"

	if [[ -z "$color_args" ]]; then
		ccm_debug "apply_undim: no saved colors in dim file for tab $tab_id"
		rm -f "$dim_file"
		return 1
	fi

	# Restore original colors
	# shellcheck disable=SC2086
	kitty @ --to "$socket" set-tab-color --match "id:$tab_id" $color_args || {
		ccm_debug "apply_undim: set-tab-color failed for tab $tab_id"
		return 1
	}

	rm -f "$dim_file"
}
