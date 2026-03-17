#!/usr/bin/env bash

[[ -n "${_CC_COMMON_DIM_LOADED:-}" ]] && return 0
_CC_COMMON_DIM_LOADED=1

# Retain 70% of each RGB component of a hex color string.
# Usage: _dim_hex "#2d7d46" => "#1f5731"
_dim_hex() {
	local hex="$1"
	hex="${hex#\#}"
	local r g b
	r=$((16#${hex:0:2} * 7 / 10))
	g=$((16#${hex:2:2} * 7 / 10))
	b=$((16#${hex:4:2} * 7 / 10))
	printf '#%02x%02x%02x' "$r" "$g" "$b"
}

# Dim a foreground color to 70% brightness.
_dim_fg() {
	local hex="$1"
	hex="${hex#\#}"
	local r g b
	r=$((16#${hex:0:2} * 7 / 10))
	g=$((16#${hex:2:2} * 7 / 10))
	b=$((16#${hex:4:2} * 7 / 10))
	printf '#%02x%02x%02x' "$r" "$g" "$b"
}

# Encode space-separated color args to +-separated format for user var storage.
# "active_bg=#2d7d46 active_fg=#fff" => "active_bg=#2d7d46+active_fg=#fff"
_encode_color_args() {
	local args="$1"
	printf '%s' "${args// /+}"
}

# Decode +-separated format back to space-separated color args.
# "active_bg=#2d7d46+active_fg=#fff" => "active_bg=#2d7d46 active_fg=#fff"
_decode_color_args() {
	local encoded="$1"
	printf '%s' "${encoded//+/ }"
}

# Get kitty default tab colors from kitty @ get-colors.
# Returns color args string for tabs with no per-tab overrides.
_get_kitty_default_tab_colors() {
	local socket="$1"
	local colors_output
	colors_output="$(kitty @ --to "$socket" get-colors 2>/dev/null)" || return 1

	local abg afg ibg ifg
	abg="$(printf '%s' "$colors_output" | awk '/^active_tab_background/ {print $2}')"
	afg="$(printf '%s' "$colors_output" | awk '/^active_tab_foreground/ {print $2}')"
	ibg="$(printf '%s' "$colors_output" | awk '/^inactive_tab_background/ {print $2}')"
	ifg="$(printf '%s' "$colors_output" | awk '/^inactive_tab_foreground/ {print $2}')"

	if [[ -n "$abg" && -n "$afg" && -n "$ibg" && -n "$ifg" ]]; then
		printf 'active_bg=%s active_fg=%s inactive_bg=%s inactive_fg=%s' "$abg" "$afg" "$ibg" "$ifg"
	fi
}

# Check if a tab is dimmed by reading cc_dim_type from user vars in ls JSON.
# Echoes the dim type ("manual", "auto", or "focus") and returns 0 if dimmed, 1 if not.
# Usage: is_dimmed TAB_ID LS_JSON
is_dimmed() {
	local tab_id="$1" ls_json="$2"
	local dim_type
	dim_type="$(printf '%s' "$ls_json" | jq -r --argjson tid "$tab_id" '
		[.[] | .tabs[] | select(.id == $tid) | .windows[0].user_vars.cc_dim_type // ""] | first // ""
	' 2>/dev/null)"
	if [[ -n "$dim_type" ]]; then
		echo "$dim_type"
		return 0
	fi
	return 1
}

# Apply dim (darkened colors) to a tab.
# Usage: apply_dim SOCKET TAB_ID WIN_ID DIM_TYPE [LS_JSON]
#   SOCKET     — kitty socket path
#   TAB_ID     — kitty tab id
#   WIN_ID     — kitty window id (used for set-user-vars; resolved from ls if empty)
#   DIM_TYPE   — "manual", "auto", or "focus"
#   LS_JSON    — (optional) pre-fetched kitty @ ls output
apply_dim() {
	local socket="$1" tab_id="$2" win_id="$3" dim_type="$4"
	local ls_json="${5:-}"

	if [[ -z "$ls_json" ]]; then
		ls_json="$(kitty @ --to "$socket" ls 2>/dev/null)" || true
	fi

	# Resolve win_id if not provided
	if [[ -z "$win_id" && -n "$ls_json" ]]; then
		win_id="$(printf '%s' "$ls_json" | jq -r --argjson tid "$tab_id" '
			[.[] | .tabs[] | select(.id == $tid) | .windows[0].id] | first // ""
		' 2>/dev/null)" || win_id=""
	fi

	if [[ -z "$win_id" ]]; then
		ccm_debug "apply_dim: cannot resolve window id for tab $tab_id"
		return 1
	fi

	# Resolve original colors from cc_color/cc_account user vars.
	# Kitty does not expose per-tab color overrides in `kitty @ ls`, so we
	# rely on the user vars that cc-manager sets at launch time.
	local color_args="" color_source="none"
	local uv_json cc_color="" account=""
	if [[ -n "$ls_json" ]]; then
		uv_json="$(printf '%s' "$ls_json" | jq --argjson wid "$win_id" '
			[.[] | .tabs[] | .windows[] | select(.id == $wid) | .user_vars] | first // {}
		' 2>/dev/null)" || uv_json="{}"
		cc_color="$(printf '%s' "$uv_json" | jq -r '.cc_color // ""')"
		account="$(printf '%s' "$uv_json" | jq -r '.cc_account // ""')"
	fi

	if [[ -n "$cc_color" ]]; then
		color_args="$(resolve_palette_color "$cc_color")"
		color_source="managed"
	elif [[ -n "$account" ]]; then
		# Resolve account color: try load_accounts first (works in interactive
		# shells), then fall back to awk parsing (needed in --type=background
		# contexts where python3/fd redirection may fail).
		load_accounts 2>/dev/null || true
		local color_name=""
		color_name="$(_account_color "$account" 2>/dev/null)" || color_name=""
		if [[ -z "$color_name" && -f "$ACCOUNTS_CONFIG" ]]; then
			color_name="$(awk -v name="$account" '
				/^\[\[accounts\]\]/ { n=""; c="" }
				/^name[[:space:]]*=/ { gsub(/[" ]/, ""); split($0,a,"="); n=a[2] }
				/^color[[:space:]]*=/ { gsub(/[" ]/, ""); split($0,a,"="); c=a[2] }
				n == name && c != "" { print c; exit }
			' "$ACCOUNTS_CONFIG" 2>/dev/null)" || color_name=""
		fi
		if [[ -n "$color_name" ]]; then
			color_args="$(resolve_palette_color "$color_name")"
		fi
		color_source="managed"
	fi

	# Last resort for unmanaged tabs: read kitty.conf defaults
	if [[ -z "$color_args" ]]; then
		color_args="$(_get_kitty_default_tab_colors "$socket")"
		color_source="none"
	fi

	if [[ -z "$color_args" ]]; then
		ccm_debug "apply_dim: cannot resolve colors for tab $tab_id"
		return 1
	fi

	# Store the resolved colors so undim can restore exactly.
	# For managed tabs, store the palette color args.
	# For unmanaged tabs (kitty defaults), store "none" so undim resets
	# via set-tab-color active_bg=none (returning to kitty.conf defaults).
	local orig_for_storage="$color_args"
	if [[ "$color_source" == "none" ]]; then
		orig_for_storage="none"
	fi

	# Parse color args into individual values
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
	kitty @ --to "$socket" set-tab-color --match "id:$tab_id" \
		"active_bg=$dim_abg" "active_fg=$dim_afg" \
		"inactive_bg=$dim_ibg" "inactive_fg=$dim_ifg" || {
		ccm_debug "apply_dim: set-tab-color failed for tab $tab_id"
		return 1
	}

	# Store dim state in user vars
	local encoded_orig
	encoded_orig="$(_encode_color_args "$orig_for_storage")"
	kitty @ --to "$socket" set-user-vars --match "id:$win_id" \
		"cc_dim_type=$dim_type" "cc_dim_orig=$encoded_orig" 2>/dev/null || true
}

# Restore original colors on a dimmed tab.
# Usage: apply_undim SOCKET TAB_ID WIN_ID [LS_JSON]
apply_undim() {
	local socket="$1" tab_id="$2" win_id="$3"
	local ls_json="${4:-}"

	if [[ -z "$ls_json" ]]; then
		ls_json="$(kitty @ --to "$socket" ls 2>/dev/null)" || true
	fi

	# Resolve win_id if not provided
	if [[ -z "$win_id" && -n "$ls_json" ]]; then
		win_id="$(printf '%s' "$ls_json" | jq -r --argjson tid "$tab_id" '
			[.[] | .tabs[] | select(.id == $tid) | .windows[0].id] | first // ""
		' 2>/dev/null)" || win_id=""
	fi

	# Read cc_dim_orig from user vars
	local encoded_orig=""
	if [[ -n "$ls_json" && -n "$win_id" ]]; then
		encoded_orig="$(printf '%s' "$ls_json" | jq -r --argjson wid "$win_id" '
			[.[] | .tabs[] | .windows[] | select(.id == $wid) | .user_vars.cc_dim_orig // ""] | first // ""
		' 2>/dev/null)" || encoded_orig=""
	fi

	if [[ -z "$encoded_orig" ]]; then
		ccm_debug "apply_undim: no dim state for tab $tab_id"
		return 1
	fi

	if [[ "$encoded_orig" == "none" ]]; then
		# Tab had no managed colors before dim — reset to kitty.conf defaults
		kitty @ --to "$socket" set-tab-color --match "id:$tab_id" \
			active_bg=none active_fg=none inactive_bg=none inactive_fg=none || {
			ccm_debug "apply_undim: set-tab-color reset failed for tab $tab_id"
			return 1
		}
	else
		# Restore saved original colors
		local color_args
		color_args="$(_decode_color_args "$encoded_orig")"
		# shellcheck disable=SC2086
		kitty @ --to "$socket" set-tab-color --match "id:$tab_id" $color_args || {
			ccm_debug "apply_undim: set-tab-color failed for tab $tab_id"
			return 1
		}
	fi

	# Clear dim user vars
	if [[ -n "$win_id" ]]; then
		kitty @ --to "$socket" set-user-vars --match "id:$win_id" \
			"cc_dim_type=" "cc_dim_orig=" 2>/dev/null || true
	fi
}
