package debugger

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

type BundleMode string

const (
	BundleModeShare    BundleMode = "share"
	BundleModeForensic BundleMode = "forensic"
)

var (
	secretFieldPattern  = regexp.MustCompile(`(?i)(authorization|cookie|credential|password|passwd|secret|token|api[_ -]?key|private[_ -]?key|prompt|tool[_ -]?(input|output|content|arguments?)|command[_ -]?line|cmdline|argv|message|transcript|payload|content)`)
	identifierPattern   = regexp.MustCompile(`(?i)(^|_)(id|ids|profile|window|session|run|launch|trace|request|source_event|binary_hash)$`)
	safeTokenPattern    = regexp.MustCompile(`^[A-Za-z0-9_.:/@+-]{1,160}$`)
	absolutePathPattern = regexp.MustCompile(`(^|[\s"'(=])(/(?:Users|home|tmp|private|var|etc|opt|Volumes)/[^\s"',;)\]}]+)`)
	secretPatterns      = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`),
		regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9+/=]+`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{12,}\b`),
		regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`),
		regexp.MustCompile(`\bnpm_[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\bpypi-[A-Za-z0-9_-]{20,}\b`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
		regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_ -]?key|authorization|cookie)\s*[:=]\s*[^\s,;]+`),
		regexp.MustCompile(`(?i)https?://[^/@:\s]+:[^/@\s]+@`),
		regexp.MustCompile(`(?i)(prompt|tool[_ -]?(input|output|content|arguments?)|message|transcript|payload|content)\s*[:=].*`),
		regexp.MustCompile(`(?i)(command[_ -]?line|cmdline|argv)\s*[:=].*`),
		regexp.MustCompile(`(?s)-----BEGIN [^-]+-----.*?-----END [^-]+-----`),
	}
	shareAllowedKeys = allowedShareKeys(`
		action activation after alive asl_run_id attempt availability before binary_hash build build_matches build_time bytes
		bytes_scanned cause certainty check check_error classification cli cli_build cli_version code corrupt_lines count coverage daemon
		daemon_sequence decision degraded degraded_reason delivery diagnostics dirty disabled edges enabled ended_at entries error
		event evidence evidence_graph evidence_ids expected_sequences expires_at external files files_read findings finished_at
		first_failed_stage first_missing_stage from generated_at go_version graph_depth harness harness_counts harness_session_id health healthy
		hook_silence_seconds hook_status hooks id identity issues journal journal_enabled journal_keep_days journal_max_mib journal_snapshot journal_snapshot_scope kind legacy_claim_files
		kitty_available last_seen last_seen_age_seconds last_seen_events latency_ms launch_id launch_max_bytes level local_only
		limits managed max_bytes max_edges max_nodes message missing_steps mode module native native_keep_days native_logging native_max_mib
		native_session_max_mib nodes normalized_source_id normalized_source_sequence observed_at observed_schemas observed_steps oldest_spool_mod_time outcome outcomes
		override parent_trace_id path payload_hmac payload_size pid producer_source_id producer_source_sequence profile profile_count
		profiles project_count pruned_edge_candidates pruned_node_candidates quota_bytes raw_sources ready reason reasons recent record records records_matched records_scanned relation render_generation request_id revision
		route_decision run_id schema_version scope selector session_id sessions silent source source_event_id source_event_ids source_id
		source_key source_kind source_sequence span_id stage stages started_at status storage success suggested_commands summary sync timestamp to tool_use_id
		trace_id transform truncated truncation turn_id unmanaged uptime_seconds value verdict verified_cli_version version warning warnings
		window_id reducer_action reducer_reason normalization_outcome used_fallback_identity segment_files_read spool_files spool_files_read spool_bytes duplicates_omitted quarantined_files latest_observed_at
		process_harness process_pid process_start_token process_observation process_mode process_sources withdrawn_facts cleared_subagents synthesized_stop terminal_hook_evidence
		process_exits last_process_exit_reason last_process_exit_observation last_process_exit_at attempted_processes unknown_processes pipe_mode
		before_icon before_looping before_source_kind before_source_key after_icon after_looping after_source_kind after_source_key icon looping origin_window_id
		effective_before effective_after result_icon_state config_dir routed_profile producer_config_dir
		trace_ids request_ids journal_detail
		earliest_observed_at promoted_spool_files_read delivery_failures latest_at
		acknowledged agent_mux agent_mux_event agrees artifact_count asl asl_acknowledged asl_attempts asl_limits
		asl_omissions asl_status assurance assurance_state attempt_count available bridge_protocol builds
		by_action by_kind by_name by_status closure_id closure_ids command_id completed_at contract_set
		contract_set_id control control_records counts created_at deliveries deliveries_matched deliveries_total
		deliveries_unmatched delivery_records detail digest duration_seconds eligible error_classification events
		export_digest exported field final_status hook_schema_version issue_count issue_ids journal_outcomes
		journal_records journal_records_joined journal_stages lifecycle_source_sequence matched max_control_records
		max_delivery_records max_export_bytes max_log_lines max_receipt_records max_runs new_issue_count note_chars
		notes ok omissions operation_ids phase plan_digest plans_scanned product projection projection_generation
		projection_schema_version projection_status question_id queue_command reason_code receipt_records receipts
		recurring_issue_count redaction_policy rejected runs runs_correlated runs_exported runs_matched
		runs_without_journal scanned section seq session sha256 state state_available state_reason state_root_present
		state_schema_version stop_after_current text_chars total total_sessions turn_index updated_at
		runtime reconcile capture tmux_client interval_seconds passes failures consecutive_failures age_seconds
		last_duration_ms max_duration_ms failure_class ended released detached known live tmux servers attached
		unattached reads limit commands timeouts in_flight max_in_flight queued max_queue_ms
		backend launch_backend server running agents min_version problems severity candidates verified
		versions amx go kitty notifier notifier_version companion_app tmux_source tmux_min
		duration_ms live_sessions attached_sessions
	`)
)

type bundleRedactor struct {
	mode    BundleMode
	hash    func(string) string
	aliases []pathAlias
}

type pathAlias struct {
	path  string
	alias string
}

func newBundleRedactor(mode BundleMode, hash func(string) string, aliases map[string]string) *bundleRedactor {
	values := make([]pathAlias, 0, len(aliases))
	for path, alias := range aliases {
		path = strings.TrimSpace(path)
		if path != "" {
			values = append(values, pathAlias{path: path, alias: alias})
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i].path) > len(values[j].path) })
	return &bundleRedactor{mode: mode, hash: hash, aliases: values}
}

func (r *bundleRedactor) JSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	sanitized := r.value("", decoded)
	data, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (r *bundleRedactor) value(key string, value any) any {
	lower := strings.ToLower(strings.TrimSpace(key))
	if isSecretFieldKey(lower) && lower != "process_start_token" {
		return "[redacted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			lowerChild := strings.ToLower(strings.TrimSpace(childKey))
			if r.mode == BundleModeShare && (!isShareAllowedKey(childKey) ||
				isSecretFieldKey(lowerChild) && lowerChild != "process_start_token") {
				continue
			}
			out[childKey] = r.value(childKey, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = r.value(key, child)
		}
		return out
	case string:
		return r.stringValue(lower, typed)
	case float64:
		if r.mode == BundleModeShare && isIdentifierKey(lower) && r.hash != nil {
			return r.hash(strconv.FormatFloat(typed, 'f', -1, 64))
		}
		return typed
	default:
		return typed
	}
}

func isSecretFieldKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "command" || key == "args" || secretFieldPattern.MatchString(key)
}

func allowedShareKeys(values string) map[string]bool {
	result := make(map[string]bool)
	for _, value := range strings.Fields(values) {
		result[value] = true
	}
	return result
}

func isShareAllowedKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return shareAllowedKeys[key] || strings.HasPrefix(key, "replay.") || strings.HasPrefix(key, "route.")
}

func (r *bundleRedactor) stringValue(key, value string) string {
	value = r.scrub(value)
	if key == "process_start_token" {
		if diagnostics.IsSafeProcessStartTokenEvidence(value) {
			return value
		}
		return "[redacted]"
	}
	if isIdentifierKey(key) && value != "" && r.hash != nil {
		parts := strings.Split(value, ",")
		for i := range parts {
			part := strings.TrimSpace(parts[i])
			if part == "0" {
				parts[i] = part
			} else {
				parts[i] = r.hash(part)
			}
		}
		return strings.Join(parts, ",")
	}
	if strings.Contains(key, "path") || strings.Contains(key, "directory") || strings.Contains(key, "cwd") {
		if r.mode == BundleModeShare {
			return r.sharePathAlias(value)
		}
		return r.aliasPaths(value)
	}
	if r.mode == BundleModeForensic {
		return value
	}
	if strings.HasPrefix(key, "replay.") && safeTokenPattern.MatchString(value) && !containsSecretMarker(value) {
		return value
	}
	if (strings.HasPrefix(key, "route.") || key == "source_kind" || key == "reducer_action" || key == "reducer_reason" ||
		key == "normalization_outcome" || key == "used_fallback_identity") &&
		safeTokenPattern.MatchString(value) && !containsSecretMarker(value) {
		return value
	}
	if isShareEvidenceScalar(key, value) {
		return value
	}
	// Reasons can contain wrapped operating-system or provider errors even
	// though most current producers use stable enums. Hash them in share mode
	// so future free-form reasons cannot become a secret exfiltration path.
	if key == "reason" && value != "" && r.hash != nil {
		return r.hash(value)
	}
	if key == "event" && value != "" {
		if value == protocol.EventHarnessProcessExited {
			return value
		}
		payload := hookevent.HookPayload{}
		if _, known := hookevent.ResolveAction(value, payload); !known && !hookevent.IsIgnoredEvent(value, payload) && r.hash != nil {
			return r.hash(value)
		}
	}
	if key == "action" && value != "" {
		if _, known := hookevent.ResolveAction(value, hookevent.HookPayload{}); !known && r.hash != nil {
			return r.hash(value)
		}
	}
	if key == "harness" && value != "" && harness.NormalizeKnown(value) == "" && r.hash != nil {
		return r.hash(value)
	}
	if isShareStringField(key) && safeTokenPattern.MatchString(value) && !containsSecretMarker(value) {
		return value
	}
	if isTimeField(key) {
		if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return value
		}
	}
	if value == "" {
		return ""
	}
	return "[redacted]"
}

func isShareEvidenceScalar(key, value string) bool {
	switch key {
	case "attempt", "daemon_sequence", "generation", "render_generation", "source_sequence", "process_pid",
		"withdrawn_facts", "cleared_subagents", "process_exits", "last_process_exit_at",
		"attempted_processes", "unknown_processes", "origin_window_id",
		// Runtime reconciliation edges: counts of sessions and of what a
		// pass changed.
		"live_sessions", "attached_sessions", "servers", "ended", "released", "detached", "consecutive_failures":
		_, err := strconv.ParseUint(value, 10, 64)
		return err == nil
	case "duration_ms":
		number, err := strconv.ParseFloat(value, 64)
		return err == nil && number >= 0 && len(value) <= 16
	case "pipe_mode", "before_looping", "after_looping", "synthesized_stop":
		_, err := strconv.ParseBool(value)
		return err == nil
	case "source_generations":
		if value == "" {
			return true
		}
		for _, part := range strings.Split(value, ",") {
			if _, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64); err != nil {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (r *bundleRedactor) sharePathAlias(value string) string {
	for _, alias := range r.aliases {
		if value == alias.alias || strings.HasPrefix(value, alias.alias+string(os.PathSeparator)) || value == alias.path || strings.HasPrefix(value, alias.path+string(os.PathSeparator)) {
			return alias.alias
		}
	}
	if value == "" {
		return ""
	}
	return "[PATH]"
}

func (r *bundleRedactor) scrub(value string) string {
	value = r.aliasPaths(value)
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "[redacted]")
	}
	return value
}

func (r *bundleRedactor) aliasPaths(value string) string {
	for _, alias := range r.aliases {
		value = strings.ReplaceAll(value, alias.path, alias.alias)
	}
	return absolutePathPattern.ReplaceAllString(value, `${1}[PATH]`)
}

func isIdentifierKey(key string) bool {
	if dot := strings.LastIndexByte(key, '.'); dot >= 0 {
		key = key[dot+1:]
	}
	if key == "schema_version" || key == "source_sequence" {
		return false
	}
	return identifierPattern.MatchString(key) || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_ids") ||
		strings.HasSuffix(key, "_source_key") || key == "source_key" || key == "profile" || key == "scope"
}

func isShareStringField(key string) bool {
	switch key {
	case "mode", "status", "stage", "outcome", "reason", "verdict", "coverage", "harness", "event", "action",
		"level", "component", "error_class", "classification", "certainty", "kind", "scope", "check", "source",
		"format", "member", "decision", "availability", "go_version", "version", "revision", "reasons", "journal_snapshot_scope",
		"process_harness", "process_observation", "process_mode", "terminal_hook_evidence", "last_process_exit_reason",
		"last_process_exit_observation", "before_icon", "after_icon", "before_source_kind", "after_source_kind",
		"journal_detail",
		// Backend and runtime health: fixed vocabularies and release numbers.
		"failure_class", "launch_backend", "severity", "min_version", "amx", "go", "tmux", "kitty", "daemon",
		"notifier", "notifier_version", "companion_app", "tmux_source", "tmux_min",
		// Closed enums published by the ASL diagnostics export and by the
		// cross-system correlation derived from it. Each is a fixed vocabulary
		// on both sides of the pairing, and each is still gated on the safe
		// token shape and the secret markers below.
		"code", "reason_code", "agent_mux_event", "asl_status", "final_status", "error_classification",
		"assurance_state", "projection_status", "phase", "state", "product", "queue_command", "field", "section",
		"journal_stages",
		// Content digests. A digest of withheld material is not the material,
		// and keeping it verbatim is what makes the omission ledger and the
		// byte-identical-capture claim checkable from a shared bundle.
		"sha256", "digest", "export_digest":
		return true
	default:
		return false
	}
}

func isTimeField(key string) bool {
	return strings.HasSuffix(key, "_at") || strings.HasSuffix(key, "_time") || key == "timestamp" || key == "time"
}

func containsSecretMarker(value string) bool {
	for _, pattern := range secretPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func (r *bundleRedactor) nativeLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if r.mode == BundleModeForensic {
		var decoded any
		if json.Unmarshal([]byte(line), &decoded) == nil {
			sanitized := r.value("", decoded)
			if data, err := json.Marshal(sanitized); err == nil {
				return string(data)
			}
		}
		return r.scrub(line)
	}
	fields := make(map[string]string)
	var object map[string]any
	if json.Unmarshal([]byte(line), &object) == nil {
		for output, candidates := range map[string][]string{
			"timestamp":   {"timestamp", "time", "ts", "observed_at"},
			"level":       {"level", "severity"},
			"component":   {"component", "target", "logger", "module"},
			"status":      {"status", "state", "outcome"},
			"error_class": {"error_class", "errorClass", "code"},
		} {
			for _, candidate := range candidates {
				if value, ok := object[candidate]; ok {
					fields[output] = fmt.Sprint(value)
					break
				}
			}
		}
	}
	if fields["level"] == "" {
		upper := strings.ToUpper(line)
		for _, level := range []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"} {
			if strings.Contains(upper, level) {
				fields["level"] = strings.ToLower(level)
				break
			}
		}
	}
	if fields["error_class"] == "" {
		fields["error_class"] = classifyNativeError(line)
	}
	for key, value := range fields {
		value = r.scrub(strings.TrimSpace(value))
		if value == "" || !safeTokenPattern.MatchString(value) || containsSecretMarker(value) {
			delete(fields, key)
			continue
		}
		fields[key] = value
	}
	if timestamp := fields["timestamp"]; timestamp != "" {
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
			delete(fields, "timestamp")
		}
	}
	if level := strings.ToLower(fields["level"]); level != "" {
		switch level {
		case "trace", "debug", "info", "warn", "warning", "error", "fatal":
			fields["level"] = level
		default:
			delete(fields, "level")
		}
	}
	if status := strings.ToLower(fields["status"]); status != "" {
		switch status {
		case "ok", "success", "succeeded", "failed", "failure", "error", "warning", "cancelled", "canceled", "timeout", "retrying", "skipped", "started", "finished", "complete", "completed", "degraded", "unavailable":
			fields["status"] = status
		default:
			delete(fields, "status")
		}
	}
	// Never trust a producer-supplied error class. Derive the small controlled
	// taxonomy from the line and discard arbitrary values in that field.
	if class := classifyNativeError(line); class != "" {
		fields["error_class"] = class
	} else {
		delete(fields, "error_class")
	}
	if component := fields["component"]; component != "" && r.hash != nil {
		fields["component"] = r.hash(component)
	}
	if len(fields) == 0 {
		return ""
	}
	data, _ := json.Marshal(fields)
	return string(data)
}

func classifyNativeError(line string) string {
	lower := strings.ToLower(line)
	classes := []struct {
		name  string
		terms []string
	}{
		{"timeout", []string{"timeout", "timed out", "deadline exceeded"}},
		{"permission_denied", []string{"permission denied", "unauthorized", "forbidden"}},
		{"connection_refused", []string{"connection refused", "connection reset", "broken pipe"}},
		{"rate_limited", []string{"rate limit", "too many requests"}},
		{"cancelled", []string{"cancelled", "canceled", "interrupt"}},
		{"invalid_input", []string{"invalid input", "invalid argument", "parse error"}},
	}
	for _, class := range classes {
		for _, term := range class.terms {
			if strings.Contains(lower, term) {
				return class.name
			}
		}
	}
	return ""
}
