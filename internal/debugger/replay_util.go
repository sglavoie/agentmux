package debugger

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func rawInt(raw json.RawMessage) int {
	value, _ := strconv.Atoi(rawString(raw))
	return value
}

func parseBool(values ...string) bool {
	for _, value := range values {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return false
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseUint64(value string) uint64 {
	parsed, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseUint64List(value string) []uint64 {
	result := make([]uint64, 0)
	for _, part := range strings.Split(value, ",") {
		if parsed := parseUint64(part); parsed > 0 {
			result = append(result, parsed)
		}
	}
	return result
}

func hasEvidenceKeys(evidence map[string]string, keys ...string) bool {
	for _, key := range keys {
		if _, ok := evidence[key]; !ok {
			return false
		}
	}
	return true
}

func firstNonEmptyReplay(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringInOrEmpty(values []string, candidate string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func addCSVSet(set map[string]struct{}, values string) {
	for _, value := range strings.Split(values, ",") {
		addSet(set, strings.TrimSpace(value))
	}
}

func addSet(set map[string]struct{}, value string) {
	if value != "" {
		set[value] = struct{}{}
	}
}

func sortedStringSet(set map[string]struct{}) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func sortedUint64Set(set map[uint64]struct{}) []uint64 {
	values := make([]uint64, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func equalNonEmptySets(recorded, recomputed map[string]struct{}) bool {
	if len(recorded) == 0 {
		return true
	}
	if len(recorded) != len(recomputed) {
		return false
	}
	for value := range recorded {
		if _, ok := recomputed[value]; !ok {
			return false
		}
	}
	return true
}

func laterRenderStage(left, right string) string {
	rank := map[string]int{"queued": 1, "attempted": 2, "persisted": 3, "rendered": 4, "failed": 5}
	if rank[right] >= rank[left] {
		return right
	}
	return left
}
