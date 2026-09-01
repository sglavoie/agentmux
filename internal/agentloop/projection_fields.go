package agentloop

// Per-field readers for a projection block. Each one enforces the schema-1
// shape of a single member and reports a typed ProjectionError, so the decoder
// in projection_decode.go stays a list of fields rather than a list of checks.

import (
	"encoding/json"
	"strings"
	"time"
)

func isJSONNull(raw json.RawMessage) bool {
	return string(bytesTrimSpace(raw)) == "null"
}

func bytesTrimSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

func projectionString(members map[string]json.RawMessage, key string, limit int) (string, error) {
	raw, ok := members[key]
	if !ok {
		return "", projectionErrorf(ReasonInvalidProjection, "%s is missing", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", projectionErrorf(ReasonInvalidProjection, "%s is not a string", key)
	}
	if value == "" || strings.TrimSpace(value) != value {
		return "", projectionErrorf(ReasonInvalidProjection, "%s is empty or not trimmed", key)
	}
	if len(value) > limit {
		return "", projectionErrorf(ReasonInvalidProjection, "%s exceeds %d characters", key, limit)
	}
	return value, nil
}

func projectionNullableString(members map[string]json.RawMessage, key string, limit int) (string, error) {
	if isJSONNull(members[key]) {
		return "", nil
	}
	return projectionString(members, key, limit)
}

func projectionAbsPath(members map[string]json.RawMessage, key string, nullable bool) (string, error) {
	value, err := func() (string, error) {
		if nullable {
			return projectionNullableString(members, key, maxPathChars)
		}
		return projectionString(members, key, maxPathChars)
	}()
	if err != nil || value == "" {
		return "", err
	}
	if !strings.HasPrefix(value, "/") {
		return "", projectionErrorf(ReasonInvalidProjection, "%s is not an absolute path", key)
	}
	return value, nil
}

func projectionInt(members map[string]json.RawMessage, key string, minimum, maximum int) (int, error) {
	raw, ok := members[key]
	if !ok {
		return 0, projectionErrorf(ReasonInvalidProjection, "%s is missing", key)
	}
	var value json.Number
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, projectionErrorf(ReasonInvalidProjection, "%s is not a number", key)
	}
	parsed, err := value.Int64()
	if err != nil {
		return 0, projectionErrorf(ReasonInvalidProjection, "%s is not an integer", key)
	}
	if parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, projectionErrorf(ReasonInvalidProjection, "%s is outside [%d,%d]", key, minimum, maximum)
	}
	return int(parsed), nil
}

func projectionNullableInt(members map[string]json.RawMessage, key string, minimum, maximum int) (int, error) {
	if isJSONNull(members[key]) {
		return -1, nil
	}
	return projectionInt(members, key, minimum, maximum)
}

func projectionUint(members map[string]json.RawMessage, key string, minimum int) (uint64, error) {
	value, err := projectionInt(members, key, minimum, maxIntValue)
	if err != nil {
		return 0, err
	}
	return uint64(value), nil
}

func projectionPhase(members map[string]json.RawMessage) (string, error) {
	phase, err := projectionString(members, "phase", 32)
	if err != nil {
		return "", err
	}
	if _, ok := projectionPhases[phase]; !ok {
		return "", projectionErrorf(ReasonInvalidProjection, "phase %q is not in schema %d", phase, ProjectionSchema)
	}
	return phase, nil
}

func projectionTimestamp(members map[string]json.RawMessage, key string, nullable bool) (string, time.Time, error) {
	raw, err := func() (string, error) {
		if nullable {
			return projectionNullableString(members, key, maxTimestampChars)
		}
		return projectionString(members, key, maxTimestampChars)
	}()
	if err != nil || raw == "" {
		return "", time.Time{}, err
	}
	parsed, parseErr := time.Parse(time.RFC3339Nano, raw)
	if parseErr != nil {
		return "", time.Time{}, projectionErrorf(ReasonInvalidProjection, "%s is not an RFC 3339 timestamp", key)
	}
	return raw, parsed, nil
}

// projectionProcess decodes the nested process block with the same exact-member
// discipline as the projection itself.
func projectionProcess(members map[string]json.RawMessage) (*ProcessRef, error) {
	raw := members["process"]
	if isJSONNull(raw) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, projectionErrorf(ReasonInvalidProjection, "process is not an object")
	}
	if len(fields) != 2 {
		return nil, projectionErrorf(ReasonInvalidProjection, "process must carry exactly pid and start_token")
	}
	pid, err := projectionInt(fields, "pid", 1, maxProjectionPID)
	if err != nil {
		return nil, err
	}
	token, err := projectionString(fields, "start_token", maxStartTokenChars)
	if err != nil {
		return nil, err
	}
	return &ProcessRef{PID: pid, StartToken: token}, nil
}
