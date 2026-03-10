#!/usr/bin/env bash
# shellcheck shell=bash
# shellcheck disable=SC2154,SC2034

[[ -n "${_CC_AUDIT_ANALYSIS_LOADED:-}" ]] && return 0
_CC_AUDIT_ANALYSIS_LOADED=1

# Run a single claude -p analysis call with timeout.
# Args: section_name prompt_text [output_mode]
# output_mode: "json" (default) validates JSON structure; "text" accepts any non-empty output.
# Writes result to $AI_RESULTS_DIR/$section_name.json (json mode) or .txt (text mode).
_run_ai_analysis() {
	local section="$1"
	local prompt="$2"
	local output_mode="${3:-json}"
	local result_ext="json"
	[[ "$output_mode" == "text" ]] && result_ext="txt"
	local result_file="$AI_RESULTS_DIR/$section.$result_ext"
	local debug_file="${TMPDIR:-/tmp}/ccm-audit-debug-$section.txt"
	local raw_file="$AI_RESULTS_DIR/$section.raw"

	local raw_output="" parsed=""
	(printf '%s' "$prompt" | claude -p --max-turns 1 >"$raw_file" 2>/dev/null) &
	local pid=$!
	(sleep "$AI_TIMEOUT" && kill "$pid" 2>/dev/null) &
	local watchdog=$!
	if wait "$pid" 2>/dev/null; then
		{ kill "$watchdog" && wait "$watchdog"; } 2>/dev/null || true
		raw_output="$(cat "$raw_file" 2>/dev/null)"
	else
		{ kill "$watchdog" && wait "$watchdog"; } 2>/dev/null || true
		echo "  ${_C_YELLOW}timeout${_C_RESET}   $section (exceeded ${AI_TIMEOUT}s)"
		raw_output="$(cat "$raw_file" 2>/dev/null)"
	fi
	rm -f "$raw_file"

	if [[ "$output_mode" == "text" ]]; then
		if [[ -n "$raw_output" ]]; then
			printf '%s\n' "$raw_output" >"$result_file"
			return 0
		fi
		echo "  ${_C_RED}failed${_C_RESET}    $section (empty response)"
		return 1
	fi

	if [[ -n "$raw_output" ]]; then
		parsed="$(printf '%s\n' "$raw_output" | _extract_json)"
		if [[ -n "$parsed" ]] && printf '%s\n' "$parsed" | jq -e 'has("new_items") and has("deprecated_items") and has("changed_items") and has("notes")' >/dev/null 2>&1; then
			printf '%s\n' "$parsed" >"$result_file"
			echo "  ${_C_GREEN}done${_C_RESET}      $section"
			return 0
		fi
	fi

	printf '%s\n' "$raw_output" >"$debug_file"

	echo "  ${_C_YELLOW}retry${_C_RESET}     $section (malformed output, retrying)..."

	local retry_suffix
	retry_suffix="

CRITICAL: Your response must be ONLY a valid JSON object with no other text, no markdown fences, no explanation. The JSON must have exactly these keys: \"new_items\" (array of strings), \"deprecated_items\" (array of strings), \"changed_items\" (array of objects with \"item\" and \"change\" keys), \"notes\" (string)."

	(printf '%s%s' "$prompt" "$retry_suffix" | claude -p --max-turns 1 >"$raw_file" 2>/dev/null) &
	pid=$!
	(sleep "$AI_TIMEOUT" && kill "$pid" 2>/dev/null) &
	watchdog=$!
	if wait "$pid" 2>/dev/null; then
		{ kill "$watchdog" && wait "$watchdog"; } 2>/dev/null || true
		raw_output="$(cat "$raw_file" 2>/dev/null)"
	else
		{ kill "$watchdog" && wait "$watchdog"; } 2>/dev/null || true
		raw_output="$(cat "$raw_file" 2>/dev/null)"
	fi
	rm -f "$raw_file"

	if [[ -n "$raw_output" ]]; then
		parsed="$(printf '%s\n' "$raw_output" | _extract_json)"
		if [[ -n "$parsed" ]] && printf '%s\n' "$parsed" | jq -e 'has("new_items") and has("deprecated_items") and has("changed_items") and has("notes")' >/dev/null 2>&1; then
			printf '%s\n' "$parsed" >"$result_file"
			echo "  ${_C_GREEN}done${_C_RESET}      $section (retry succeeded)"
			return 0
		fi
	fi

	printf '%s\n' "$raw_output" >>"$debug_file"
	echo "  ${_C_RED}failed${_C_RESET}    $section (debug saved to $debug_file)"
	return 1
}

# Print URL keys to research for a given --section filter (or all if empty).
_research_url_keys_for_section() {
	local filter="$1"
	case "$filter" in
	"")
		echo "claude_hooks_docs claude_cli_reference claude_settings_docs kitty_remote_control kitty_launch kitty_changelog"
		;;
	claude-hooks)
		echo "claude_hooks_docs"
		;;
	claude-cli)
		echo "claude_cli_reference"
		;;
	kitty-remote | kitty-match)
		echo "kitty_remote_control"
		;;
	kitty-launch)
		echo "kitty_launch"
		;;
	*)
		echo "claude_hooks_docs claude_cli_reference claude_settings_docs kitty_remote_control kitty_launch kitty_changelog"
		;;
	esac
}

# Build a research prompt for a single URL key.
_build_research_prompt() {
	local key="$1"
	local cache_file="$CACHE_DIR/$key.txt"
	local doc_content manifest_subset jq_filter topic

	if [[ ! -f "$cache_file" ]]; then
		return
	fi
	doc_content="$(cat "$cache_file")"

	case "$key" in
	claude_hooks_docs)
		jq_filter='{hook_events: .claude_code.hook_events, hook_config_format: .claude_code.hook_config_format}'
		topic="Claude Code hooks (events, configuration format)"
		;;
	claude_cli_reference)
		jq_filter='{cli_flags: .claude_code.cli_flags, cli_subcommands: .claude_code.cli_subcommands}'
		topic="Claude Code CLI (flags, subcommands)"
		;;
	claude_settings_docs)
		jq_filter='{settings_json: .claude_code.settings_json, capabilities: .claude_code.capabilities}'
		topic="Claude Code settings and capabilities"
		;;
	kitty_remote_control)
		jq_filter='{remote_commands: .kitty.remote_commands, match_fields: .kitty.match_fields}'
		topic="Kitty remote control (commands, match syntax)"
		;;
	kitty_launch)
		jq_filter='{launch_options: .kitty.launch_options}'
		topic="Kitty launch options"
		;;
	kitty_changelog)
		jq_filter='.kitty'
		topic="Kitty changelog and recent changes"
		;;
	*)
		return
		;;
	esac

	manifest_subset="$(jq "$jq_filter" "$MANIFEST")"

	cat <<EOF
You are analyzing upstream documentation for: $topic.

Here is what the project currently tracks in its manifest:
$manifest_subset

Here is the full upstream documentation:
$doc_content

Extract a concise inventory of every feature, command, flag, option, event, or configuration field mentioned in this documentation. Include version numbers or deprecation notices where present. Output plain text, not JSON.
EOF
}

# Run the research phase: iterate URL keys, build prompts, call claude -p in text mode.
_run_research_phase() {
	local keys key prompt
	keys="$(_research_url_keys_for_section "${opt_section:-}")"

	mkdir -p "$AI_RESULTS_DIR/prompts"
	local valid_keys=""
	for key in $keys; do
		if [[ ! -f "$CACHE_DIR/$key.txt" ]]; then
			echo "  ${_C_YELLOW}skip${_C_RESET}      $key (no cached doc)"
			continue
		fi

		prompt="$(_build_research_prompt "$key")"
		if [[ -z "$prompt" ]]; then
			echo "  ${_C_YELLOW}skip${_C_RESET}      $key (could not build prompt)"
			continue
		fi

		printf '%s' "$prompt" >"$AI_RESULTS_DIR/prompts/$key.prompt"
		valid_keys="$valid_keys $key"
	done

	local pids="" failures=0
	for key in $valid_keys; do
		(
			echo "  ${_C_CYAN}research${_C_RESET}  $key..."
			prompt="$(cat "$AI_RESULTS_DIR/prompts/$key.prompt")"
			if _run_ai_analysis "research-$key" "$prompt" text; then
				echo "  ${_C_GREEN}done${_C_RESET}      $key"
			else
				echo "  ${_C_RED}failed${_C_RESET}    $key"
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

	rm -rf "$AI_RESULTS_DIR/prompts"

	if ((failures > 0)); then
		echo "  ${_C_YELLOW}warn${_C_RESET}      $failures research job(s) failed"
	fi
}

# Print synthesis section names for a given --section filter (or all if empty).
_synthesis_sections_for_filter() {
	local filter="$1"
	case "$filter" in
	"")
		echo "claude_code kitty opportunities"
		;;
	claude-hooks | claude-cli)
		echo "claude_code"
		;;
	kitty-remote | kitty-launch | kitty-match)
		echo "kitty"
		;;
	*)
		echo "claude_code kitty opportunities"
		;;
	esac
}

# Print the research URL keys relevant to a synthesis section.
_research_keys_for_synthesis() {
	local section="$1"
	case "$section" in
	claude_code)
		echo "claude_hooks_docs claude_cli_reference claude_settings_docs"
		;;
	kitty)
		echo "kitty_remote_control kitty_launch kitty_changelog"
		;;
	opportunities)
		echo "claude_hooks_docs claude_cli_reference claude_settings_docs kitty_remote_control kitty_launch kitty_changelog"
		;;
	esac
}

# Read and concatenate research findings for a list of URL keys.
_read_research_findings() {
	local keys="$1"
	local combined="" key findings_file
	for key in $keys; do
		findings_file="$AI_RESULTS_DIR/research-$key.txt"
		if [[ -f "$findings_file" ]]; then
			combined+="
--- $key ---
$(cat "$findings_file")
"
		fi
	done
	printf '%s' "$combined"
}

# Build a synthesis prompt for a section (claude_code/kitty/opportunities).
_build_synthesis_prompt() {
	local section="$1"
	local keys findings jq_filter

	keys="$(_research_keys_for_synthesis "$section")"
	findings="$(_read_research_findings "$keys")"

	if [[ -z "$findings" ]]; then
		return
	fi

	case "$section" in
	claude_code)
		jq_filter='.claude_code'
		cat <<EOF
You are analyzing Claude Code features for a project called cc-manager-kitty. Compare the project's known features (from its manifest) against research findings extracted from upstream documentation.

MANIFEST (what cc-manager currently knows about):
$(jq "$jq_filter" "$MANIFEST")

RESEARCH FINDINGS:
$findings

Analyze the findings and compare against the manifest. Return a JSON object with:
- "new_items": array of strings describing features/flags/events found in findings but NOT in the manifest
- "deprecated_items": array of strings describing features in the manifest that appear removed or deprecated
- "changed_items": array of objects with "item" (string) and "change" (string) keys for features that have been modified
- "notes": a brief string summarizing the overall state

Return ONLY valid JSON with no surrounding text or markdown fences.
EOF
		;;
	kitty)
		jq_filter='.kitty'
		cat <<EOF
You are analyzing Kitty terminal features for a project called cc-manager-kitty. Compare the project's known features (from its manifest) against research findings extracted from upstream documentation.

MANIFEST (what cc-manager currently knows about):
$(jq "$jq_filter" "$MANIFEST")

RESEARCH FINDINGS:
$findings

Analyze the findings and compare against the manifest. Return a JSON object with:
- "new_items": array of strings describing remote commands, launch options, or match fields found in findings but NOT in the manifest
- "deprecated_items": array of strings describing features in the manifest that appear removed or deprecated
- "changed_items": array of objects with "item" (string) and "change" (string) keys for features that have been modified
- "notes": a brief string summarizing the overall state

Return ONLY valid JSON with no surrounding text or markdown fences.
EOF
		;;
	opportunities)
		jq_filter='{claude_code: .claude_code, kitty: .kitty}'
		cat <<EOF
You are analyzing potential enhancement opportunities for cc-manager-kitty, a Claude Code session manager for the Kitty terminal. Look at upstream features that the project doesn't currently use but could benefit from.

MANIFEST (what cc-manager currently tracks):
$(jq "$jq_filter" "$MANIFEST")

RESEARCH FINDINGS:
$findings

Identify features, commands, options, or capabilities from the research findings that cc-manager does not currently use but could potentially leverage to improve functionality. Return a JSON object with:
- "new_items": array of strings describing potential opportunities (features cc-manager could adopt)
- "deprecated_items": empty array (not applicable for opportunities)
- "changed_items": empty array (not applicable for opportunities)
- "notes": a brief string summarizing the most promising opportunities

Return ONLY valid JSON with no surrounding text or markdown fences.
EOF
		;;
	*)
		return
		;;
	esac
}

# Run the synthesis phase: iterate sections, build prompts, call claude -p in json mode.
_run_synthesis_phase() {
	local sections section prompt
	sections="$(_synthesis_sections_for_filter "$opt_section")"

	mkdir -p "$AI_RESULTS_DIR/prompts"
	local valid_sections=""
	for section in $sections; do
		prompt="$(_build_synthesis_prompt "$section")"
		if [[ -z "$prompt" ]]; then
			echo "  ${_C_YELLOW}skip${_C_RESET}      $section (no research findings available)"
			continue
		fi

		printf '%s' "$prompt" >"$AI_RESULTS_DIR/prompts/$section.prompt"
		valid_sections="$valid_sections $section"
	done

	local pids="" failures=0
	for section in $valid_sections; do
		(
			echo "  ${_C_CYAN}analyzing${_C_RESET} $section..."
			prompt="$(cat "$AI_RESULTS_DIR/prompts/$section.prompt")"
			_run_ai_analysis "$section" "$prompt" json || exit 1
		) &
		pids="$pids $!"
	done

	for pid in $pids; do
		if ! wait "$pid"; then
			failures=$((failures + 1))
		fi
	done

	rm -rf "$AI_RESULTS_DIR/prompts"

	if ((failures > 0)); then
		echo "  ${_C_YELLOW}warn${_C_RESET}      $failures synthesis job(s) failed"
	fi
}

analyze_with_ai() {
	if [[ "${has_claude:-false}" != "true" ]]; then
		echo "${_C_YELLOW}Note: claude not found in PATH, falling back to --skip-ai mode.${_C_RESET}"
		echo ""
		opt_skip_ai=true
		return 0
	fi

	mkdir -p "$AI_RESULTS_DIR"

	echo "${_C_BOLD}Phase 1: Researching upstream documentation...${_C_RESET}"
	echo ""
	_run_research_phase
	echo ""

	echo "${_C_BOLD}Phase 2: Synthesizing findings...${_C_RESET}"
	echo ""
	_run_synthesis_phase
	echo ""
}
