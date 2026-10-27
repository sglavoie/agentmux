package config

// Surgical editing of the [defaults.asl] table.
//
// config.toml is an authoritative source artifact, and the TOML library this
// project vendors decodes only. Round-tripping through a marshaller would
// therefore mean re-emitting the whole file from a struct, which discards every
// comment, blank line, key order, and inline annotation the operator wrote —
// including the commented-out alternatives people keep in a config precisely
// because they are about to need them again. A pairing command is not entitled
// to that much collateral damage, so it edits text.
//
// The editor is deliberately narrow. It understands exactly one shape — a
// [defaults.asl] table header with plain `key = value` lines under it — and
// refuses anything it cannot rewrite unambiguously rather than guessing. A
// refusal here costs an operator one manual edit; a wrong guess costs them a
// config that no longer parses, which the daemon reads at startup.

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// ASLPairing is the set of values pairing persists. Empty strings are left
// untouched rather than written as empty, so a caller can update the command
// without asserting anything about the state root.
type ASLPairing struct {
	// Command is the absolute executable path. Pairing persists an absolute
	// path on purpose: a bare name would put the daemon back in the business of
	// searching an environment it did not choose.
	Command string
	// StateRoot is the peer's own state root, recorded so discovery and ASL
	// cannot drift apart silently.
	StateRoot string
	// Required, when set, writes `enabled = true`. Pairing that verified a live
	// bridge declares the integration mandatory, because that is the state
	// whose later breakage `amx doctor --strict` is meant to report.
	Required bool
}

// aslSectionHeader is the only table header this editor writes or recognizes.
const aslSectionHeader = "[defaults.asl]"

// ErrASLTableAmbiguous reports a [defaults.asl] configuration this editor
// declines to rewrite.
type ErrASLTableAmbiguous struct {
	Detail string
	// Remediation is the exact manual edit that makes the file editable.
	Remediation string
}

func (e *ErrASLTableAmbiguous) Error() string {
	return "config.toml: " + e.Detail
}

// ApplyASLPairing returns config.toml contents with the pairing values set.
//
// An existing [defaults.asl] table is edited in place: keys it already has are
// rewritten where they sit, keeping their surrounding comments, and keys it
// lacks are appended to the end of that table rather than to the end of the
// file, so they stay under the header that gives them meaning. A file with no
// such table gains one at the end.
//
// The returned bytes are the whole file. Callers replace it atomically; this
// function does no I/O so it can be tested against exact text.
func ApplyASLPairing(original []byte, pairing ASLPairing) ([]byte, error) {
	values := map[string]string{}
	if trimmed := strings.TrimSpace(pairing.Command); trimmed != "" {
		values["command"] = quoteTOMLString(trimmed)
	}
	if trimmed := strings.TrimSpace(pairing.StateRoot); trimmed != "" {
		values["state_root"] = quoteTOMLString(trimmed)
	}
	if pairing.Required {
		values["enabled"] = "true"
	}
	if len(values) == 0 {
		return append([]byte(nil), original...), nil
	}

	text := string(original)
	// Recorded so the trailing newline the file had (or did not have) survives
	// a rewrite; a spurious one would show up in every future diff.
	endsWithNewline := text == "" || strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}

	if err := rejectDottedASLKeys(lines); err != nil {
		return nil, err
	}

	start, end, found := findASLTable(lines)
	if !found {
		return requireParsable(renderLines(appendASLTable(lines, values), endsWithNewline))
	}

	edited, remaining, err := rewriteASLKeys(lines, start, end, values)
	if err != nil {
		return nil, err
	}
	edited = insertRemainingKeys(edited, start, end, remaining)
	return requireParsable(renderLines(edited, endsWithNewline))
}

// findASLTable returns the half-open line range of the [defaults.asl] table
// body — the lines after its header, up to the next table header or the end of
// the file.
func findASLTable(lines []string) (start, end int, found bool) {
	for i, line := range lines {
		if tableHeaderName(line) != "defaults.asl" {
			continue
		}
		start = i + 1
		end = len(lines)
		for j := start; j < len(lines); j++ {
			if tableHeaderName(lines[j]) != "" {
				end = j
				break
			}
		}
		return start, end, true
	}
	return 0, 0, false
}

// tableHeaderName returns the dotted name of a table header line, or "" when
// the line is not one. Array-of-tables headers ([[profiles]]) are headers too:
// they end the [defaults.asl] body, so they must be recognized, but their name
// can never equal "defaults.asl" and so never matches above.
func tableHeaderName(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return ""
	}
	trimmed = stripTrailingComment(trimmed)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return ""
	}
	name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(name, "["), "]"))
	if name == "" {
		return ""
	}
	return name
}

// rejectDottedASLKeys refuses a file that configures the table in any form
// other than a [defaults.asl] header: dotted keys under [defaults]
// (`asl.command = "..."`), an inline table there (`asl = { ... }`), or keys at
// the top of the file that reach it (`defaults.asl.command = "..."`,
// `defaults = { ... }`). Each is valid TOML and means the same thing, but
// writing a [defaults.asl] header alongside one would redefine the table and
// make the file fail to parse — which the operator would discover at the next
// daemon start, not here.
func rejectDottedASLKeys(lines []string) error {
	atRoot, inDefaults := true, false
	for _, line := range lines {
		if name := tableHeaderName(line); name != "" {
			atRoot, inDefaults = false, name == "defaults"
			continue
		}
		if !atRoot && !inDefaults {
			continue
		}
		key, _, ok := splitKeyValue(line)
		if !ok {
			continue
		}
		if inDefaults && (key == "asl" || strings.HasPrefix(key, "asl.")) {
			return &ErrASLTableAmbiguous{
				Detail: fmt.Sprintf(
					"[defaults] configures the bridge through the key %q, which cannot be "+
						"updated without redefining the table", key),
				Remediation: "move the asl settings under a [defaults.asl] header as plain key = value lines and re-run",
			}
		}
		if atRoot && (key == "defaults" || key == "defaults.asl" || strings.HasPrefix(key, "defaults.asl.")) {
			return &ErrASLTableAmbiguous{
				Detail: fmt.Sprintf(
					"the top of the file configures the bridge through the key %q, which cannot be "+
						"updated without redefining the table", key),
				Remediation: "move the asl settings under a [defaults.asl] header as plain key = value lines and re-run",
			}
		}
	}
	return nil
}

// requireParsable refuses an edit whose result is not valid TOML. The checks
// above cover the shapes this editor knows it cannot rewrite; this is the
// backstop for any it does not, because the caller replaces config.toml with
// these bytes and the daemon reads them at startup.
func requireParsable(edited []byte) ([]byte, error) {
	var probe map[string]any
	if _, err := toml.Decode(string(edited), &probe); err != nil {
		return nil, &ErrASLTableAmbiguous{
			Detail:      fmt.Sprintf("updating [defaults.asl] would leave a file that does not parse (%v)", err),
			Remediation: "set the asl settings under a single [defaults.asl] header as plain key = value lines and re-run",
		}
	}
	return edited, nil
}

// rewriteASLKeys replaces the values of keys already present in the table and
// returns the ones that still need inserting. A duplicate key is refused rather
// than rewritten twice: TOML forbids it, so the file was already broken, and
// silently picking one occurrence would hide that.
func rewriteASLKeys(lines []string, start, end int, values map[string]string) ([]string, map[string]string, error) {
	edited := append([]string(nil), lines...)
	remaining := make(map[string]string, len(values))
	for key, value := range values {
		remaining[key] = value
	}
	seen := map[string]bool{}
	for i := start; i < end; i++ {
		key, _, ok := splitKeyValue(edited[i])
		if !ok {
			continue
		}
		if seen[key] {
			return nil, nil, &ErrASLTableAmbiguous{
				Detail:      fmt.Sprintf("[defaults.asl] defines %q more than once", key),
				Remediation: fmt.Sprintf("delete the duplicate %s line and re-run", key),
			}
		}
		seen[key] = true
		value, wanted := values[key]
		if !wanted {
			continue
		}
		edited[i] = rewriteValue(edited[i], key, value)
		delete(remaining, key)
	}
	return edited, remaining, nil
}

// rewriteValue rebuilds one assignment, preserving the line's leading
// indentation and any trailing comment. The comment survives because it is
// usually the explanation of why the key is set at all, and pairing changing
// the value does not make that explanation wrong.
func rewriteValue(line, key, value string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	rebuilt := indent + key + " = " + value
	if comment := trailingComment(line); comment != "" {
		rebuilt += "  " + comment
	}
	return rebuilt
}

// insertRemainingKeys adds keys the table did not have, at the end of its body
// but before any trailing blank lines, so the new keys sit with the others
// rather than after the gap that separates this table from the next.
func insertRemainingKeys(lines []string, start, end int, remaining map[string]string) []string {
	if len(remaining) == 0 {
		return lines
	}
	insertAt := end
	for insertAt > start && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	additions := make([]string, 0, len(remaining))
	for _, key := range aslKeyOrder {
		if value, ok := remaining[key]; ok {
			additions = append(additions, key+" = "+value)
		}
	}
	out := make([]string, 0, len(lines)+len(additions))
	out = append(out, lines[:insertAt]...)
	out = append(out, additions...)
	out = append(out, lines[insertAt:]...)
	return out
}

// aslKeyOrder fixes the order new keys are written in, so two machines paired
// the same way produce byte-identical tables and a config under version control
// does not churn.
var aslKeyOrder = []string{"enabled", "command", "state_root"}

// appendASLTable adds a complete [defaults.asl] table at the end of the file.
func appendASLTable(lines []string, values map[string]string) []string {
	out := append([]string(nil), lines...)
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) > 0 {
		out = append(out, "")
	}
	out = append(out, "# Written by `amx config asl-sync`.", aslSectionHeader)
	for _, key := range aslKeyOrder {
		if value, ok := values[key]; ok {
			out = append(out, key+" = "+value)
		}
	}
	return out
}

func renderLines(lines []string, endsWithNewline bool) []byte {
	joined := strings.Join(lines, "\n")
	if endsWithNewline && joined != "" {
		joined += "\n"
	}
	return []byte(joined)
}

// splitKeyValue recognizes a bare `key = value` assignment. Quoted keys are not
// recognized on purpose: `"command" = "asl"` is legal TOML and means the same
// key, but this editor only ever writes the bare form, and treating the two as
// interchangeable would let it append a second definition of a key that is
// already there.
func splitKeyValue(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	idx := strings.IndexByte(trimmed, '=')
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimmed[:idx])
	if key == "" || strings.ContainsAny(key, "\"'[") {
		return "", "", false
	}
	return key, strings.TrimSpace(trimmed[idx+1:]), true
}

// trailingComment returns the `# ...` suffix of a line, or "". It scans with
// quote awareness so a `#` inside a path value is not mistaken for one.
func trailingComment(line string) string {
	if idx := commentIndex(line); idx >= 0 {
		return strings.TrimSpace(line[idx:])
	}
	return ""
}

func stripTrailingComment(line string) string {
	if idx := commentIndex(line); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return strings.TrimSpace(line)
}

// commentIndex returns the offset of the comment marker outside any string, or
// -1. Only basic and literal strings can appear in the values this editor
// touches, and neither may contain a raw newline, so a single-line scan is
// sufficient.
func commentIndex(line string) int {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == '#':
			return i
		}
	}
	return -1
}

// quoteTOMLString renders a basic string. Paths are the only values pairing
// writes, and a path may legitimately contain a quote, a backslash, or — on a
// filesystem that allows it — a control character, so all three are escaped
// rather than assumed away.
func quoteTOMLString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
