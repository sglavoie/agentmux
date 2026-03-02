#!/usr/bin/env bash
# shellcheck shell=bash
# shellcheck disable=SC2154

[[ -n "${_CC_AUDIT_REPORT_LOADED:-}" ]] && return 0
_CC_AUDIT_REPORT_LOADED=1

# Format a single AI findings section.
# Args: section_label result_file
# Appends to the $output variable (inherited from caller).
_format_ai_section() {
	local label="$1"
	local result_file="$2"
	local nl=$'\n'

	if [[ ! -f "$result_file" ]]; then
		if [[ "$opt_quiet" == "false" ]]; then
			output+="  ${_C_DIM}$label: analysis not available${_C_RESET}${nl}"
		fi
		return
	fi

	local new_count dep_count chg_count notes
	new_count="$(jq '.new_items | length' "$result_file")"
	dep_count="$(jq '.deprecated_items | length' "$result_file")"
	chg_count="$(jq '.changed_items | length' "$result_file")"
	notes="$(jq -r '.notes // ""' "$result_file")"

	if ((dep_count > 0)); then
		output+="  ${_C_RED}$label${_C_RESET}${nl}"
	elif ((new_count > 0 || chg_count > 0)); then
		output+="  ${_C_YELLOW}$label${_C_RESET}${nl}"
	else
		if [[ "$opt_quiet" == "true" ]]; then
			return
		fi
		output+="  ${_C_GREEN}$label: up to date${_C_RESET}${nl}"
		return
	fi

	if ((new_count > 0)); then
		output+="    ${_C_YELLOW}New upstream (${new_count}):${_C_RESET}${nl}"
		local i item
		for ((i = 0; i < new_count; i++)); do
			item="$(jq -r --argjson i "$i" '.new_items[$i]' "$result_file")"
			output+="      + $item${nl}"
		done
	fi

	if ((dep_count > 0)); then
		output+="    ${_C_RED}Deprecated (${dep_count}):${_C_RESET}${nl}"
		local i item
		for ((i = 0; i < dep_count; i++)); do
			item="$(jq -r --argjson i "$i" '.deprecated_items[$i]' "$result_file")"
			output+="      - $item${nl}"
		done
	fi

	if ((chg_count > 0)); then
		output+="    ${_C_CYAN}Changed (${chg_count}):${_C_RESET}${nl}"
		local i item change
		for ((i = 0; i < chg_count; i++)); do
			item="$(jq -r --argjson i "$i" '.changed_items[$i].item' "$result_file")"
			change="$(jq -r --argjson i "$i" '.changed_items[$i].change' "$result_file")"
			output+="      ~ $item: $change${nl}"
		done
	fi

	if [[ -n "$notes" ]]; then
		output+="    ${_C_DIM}$notes${_C_RESET}${nl}"
	fi
}

report_manifest() {
	local output=""
	local nl=$'\n'

	output+="═══════════════════════════════════════════════════════════${nl}"
	output+="${_C_BOLD}  cc-manager audit report${_C_RESET}${nl}"
	output+="═══════════════════════════════════════════════════════════${nl}"
	output+="${nl}"

	output+="${_C_BOLD}${_C_CYAN}Claude Code${_C_RESET}${nl}"
	output+="───────────────────────────────────────────────────────────${nl}"

	local hook_total hook_active
	hook_total="$(jq '.claude_code.hook_events.known | length' "$MANIFEST")"
	hook_active="$(jq '.claude_code.hook_events.actively_hooked | length' "$MANIFEST")"
	output+="  Hook events:       ${_C_BOLD}${hook_total}${_C_RESET} known, ${_C_BOLD}${hook_active}${_C_RESET} actively hooked${nl}"

	local flags_used flags_known
	flags_used="$(jq '.claude_code.cli_flags.used | length' "$MANIFEST")"
	flags_known="$(jq '.claude_code.cli_flags.known | length' "$MANIFEST")"
	output+="  CLI flags:         ${_C_BOLD}${flags_used}${_C_RESET} used, ${_C_BOLD}${flags_known}${_C_RESET} known${nl}"

	local subcmds
	subcmds="$(jq '.claude_code.cli_subcommands.known | length' "$MANIFEST")"
	output+="  CLI subcommands:   ${_C_BOLD}${subcmds}${_C_RESET} known${nl}"

	local hook_fields
	hook_fields="$(jq '.claude_code.hook_config_format.known | length' "$MANIFEST")"
	output+="  Hook config fields: ${_C_BOLD}${hook_fields}${_C_RESET} known${nl}"

	local settings_sections
	settings_sections="$(jq '.claude_code.settings_json.known | length' "$MANIFEST")"
	output+="  Settings sections: ${_C_BOLD}${settings_sections}${_C_RESET} known${nl}"

	local capabilities
	capabilities="$(jq '.claude_code.capabilities.known | length' "$MANIFEST")"
	output+="  Capabilities:      ${_C_BOLD}${capabilities}${_C_RESET} tracked${nl}"

	if [[ "$opt_skip_ai" == "false" ]]; then
		output+="${nl}"
		_format_ai_section "Claude Code features" "$AI_RESULTS_DIR/claude_code.json"
	fi

	output+="${nl}"

	output+="${_C_BOLD}${_C_CYAN}Kitty${_C_RESET}${nl}"
	output+="───────────────────────────────────────────────────────────${nl}"

	local remote_used remote_known_unused
	remote_used="$(jq '.kitty.remote_commands.used | length' "$MANIFEST")"
	remote_known_unused="$(jq '.kitty.remote_commands.known_unused | length' "$MANIFEST")"
	output+="  Remote commands:   ${_C_BOLD}${remote_used}${_C_RESET} used, ${_C_BOLD}${remote_known_unused}${_C_RESET} known unused${nl}"

	local launch_opts
	launch_opts="$(jq '.kitty.launch_options.used | length' "$MANIFEST")"
	output+="  Launch options:    ${_C_BOLD}${launch_opts}${_C_RESET} used${nl}"

	local match_used match_known_unused
	match_used="$(jq '.kitty.match_fields.used | length' "$MANIFEST")"
	match_known_unused="$(jq '.kitty.match_fields.known_unused | length' "$MANIFEST")"
	output+="  Match fields:      ${_C_BOLD}${match_used}${_C_RESET} used, ${_C_BOLD}${match_known_unused}${_C_RESET} known unused${nl}"

	if [[ "$opt_skip_ai" == "false" ]]; then
		output+="${nl}"
		_format_ai_section "Kitty features" "$AI_RESULTS_DIR/kitty.json"
	fi

	output+="${nl}"

	if [[ "$opt_skip_ai" == "false" && -f "$AI_RESULTS_DIR/opportunities.json" ]]; then
		output+="${_C_BOLD}${_C_CYAN}Opportunities${_C_RESET}${nl}"
		output+="───────────────────────────────────────────────────────────${nl}"
		_format_ai_section "Enhancement opportunities" "$AI_RESULTS_DIR/opportunities.json"
		output+="${nl}"
	fi

	local last_audit
	last_audit="$(jq -r '.last_audit // "never"' "$MANIFEST")"
	local url_count
	url_count="$(jq '.urls | length' "$MANIFEST")"
	output+="${_C_DIM}Last audit: ${last_audit} | Documentation sources: ${url_count}${_C_RESET}${nl}"

	printf '%s' "$output"

	printf '%s' "$output" | _strip_ansi >"$REPORT_FILE"
	echo "${_C_DIM}Report saved to $REPORT_FILE${_C_RESET}"
}

report_manifest_json() {
	local manifest_json ai_json="{}"

	manifest_json="$(jq '{
		claude_code: {
			hook_events: {
				total: (.claude_code.hook_events.known | length),
				actively_hooked: (.claude_code.hook_events.actively_hooked | length)
			},
			cli_flags: {
				used: (.claude_code.cli_flags.used | length),
				known: (.claude_code.cli_flags.known | length)
			},
			cli_subcommands: (.claude_code.cli_subcommands.known | length),
			hook_config_fields: (.claude_code.hook_config_format.known | length),
			settings_sections: (.claude_code.settings_json.known | length),
			capabilities: (.claude_code.capabilities.known | length)
		},
		kitty: {
			remote_commands: {
				used: (.kitty.remote_commands.used | length),
				known_unused: (.kitty.remote_commands.known_unused | length)
			},
			launch_options: (.kitty.launch_options.used | length),
			match_fields: {
				used: (.kitty.match_fields.used | length),
				known_unused: (.kitty.match_fields.known_unused | length)
			}
		},
		metadata: {
			last_audit: (.last_audit // "never"),
			documentation_sources: (.urls | length),
			manifest_version: .version
		}
	}' "$MANIFEST")"

	if [[ "$opt_skip_ai" == "false" ]]; then
		local findings="{}"
		local section file
		for section in claude_code kitty opportunities; do
			file="$AI_RESULTS_DIR/$section.json"
			if [[ -f "$file" ]]; then
				findings="$(printf '%s\n' "$findings" | jq --arg s "$section" --slurpfile f "$file" '.[$s] = $f[0]')"
			fi
		done
		ai_json="$findings"
	fi

	printf '%s\n' "$manifest_json" | jq --argjson ai "$ai_json" '. + {ai_findings: $ai}'
}

_update_last_audit() {
	local timestamp
	timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	local tmp="$MANIFEST.tmp"
	jq --arg ts "$timestamp" '.last_audit = $ts' "$MANIFEST" >"$tmp" && mv "$tmp" "$MANIFEST"
}
