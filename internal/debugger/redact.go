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

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

type BundleMode string

const (
	BundleModeShare    BundleMode = "share"
	BundleModeForensic BundleMode = "forensic"
)

var (
	secretFieldPattern  = regexp.MustCompile(`(?i)(authorization|cookie|credential|password|passwd|secret|token|api[_ -]?key|private[_ -]?key|prompt|tool[_ -]?(input|output|content|arguments?)|message|transcript|payload|content)`)
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
		regexp.MustCompile(`(?s)-----BEGIN [^-]+-----.*?-----END [^-]+-----`),
	}
	shareAllowedKeys = allowedShareKeys(`
		action activation after alive asl_run_id attempt availability before binary_hash build build_matches build_time bytes
		cause certainty check check_error classification cli cli_build cli_version code corrupt_lines count coverage daemon
		daemon_sequence decision degraded degraded_reason delivery diagnostics dirty disabled edges enabled ended_at entries error
		event evidence evidence_graph evidence_ids expected_sequences expires_at external files files_read findings finished_at
		first_failed_stage first_missing_stage from generated_at go_version harness harness_counts harness_session_id health healthy
		hook_silence_seconds hook_status hooks id identity issues journal journal_enabled journal_keep_days journal_max_mib kind
		kitty_available last_seen last_seen_age_seconds last_seen_events latency_ms launch_id launch_max_bytes level local_only
		managed max_bytes message missing_steps mode module native native_keep_days native_logging native_max_mib
		native_session_max_mib nodes normalized_source_id normalized_source_sequence observed_at observed_steps outcome outcomes
		override parent_trace_id path payload_hmac payload_size pid producer_source_id producer_source_sequence profile profile_count
		profiles project_count quota_bytes raw_sources ready reason recent record records relation render_generation request_id revision
		route_decision run_id schema_version scope selector session_id sessions silent source source_event_id source_event_ids source_id
		source_key source_kind source_sequence span_id stage stages started_at status storage success suggested_commands summary sync timestamp to tool_use_id
		trace_id transform truncated turn_id unmanaged uptime_seconds value verdict verified_cli_version version warning warnings
		window_id reducer_action reducer_reason normalization_outcome used_fallback_identity
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
	if secretFieldPattern.MatchString(lower) {
		return "[redacted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			if r.mode == BundleModeShare && (!isShareAllowedKey(childKey) || secretFieldPattern.MatchString(childKey)) {
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
	case "attempt", "daemon_sequence", "generation", "render_generation", "source_sequence":
		_, err := strconv.ParseUint(value, 10, 64)
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
		"format", "member", "decision", "availability", "go_version", "version", "revision":
		return true
	default:
		return false
	}
}

func isTimeField(key string) bool {
	return strings.HasSuffix(key, "_at") || key == "timestamp" || key == "time"
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
