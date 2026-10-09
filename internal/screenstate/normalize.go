package screenstate

import (
	"hash/fnv"
	"strings"
	"unicode"
)

// NormalizeLines splits a captured pane into lines, right-trims each one, and
// drops trailing blank lines. It is the canonical input to Fingerprint, and the
// only reason it exists separately is that its two rules are observations about
// real captures rather than matters of taste.
//
// The right-trim uses unicode.IsSpace, NOT a " \t" cut set: Claude 2.1.x emits
// U+00A0 after its ❯ composer glyph (see claudeComposerLineRE in the daemon)
// and Go's regexp \s is ASCII-only, so a narrower rule would leave a
// non-breaking space behind. It is required at all because three captures of
// the SAME idle Claude pane taken 5s apart were byte-identical except for a
// trailing space after ❯ that appeared and disappeared. Untrimmed, an idle
// pane's fingerprint flickers, and a stabilization gate keyed on it would
// suppress every stop rather than only the false ones.
//
// Trailing blank lines are dropped because Kitty's get-text pads its output to
// the pane's row count, so a resize or a one-row scroll changes only that
// padding. Leading whitespace and interior blank lines are structural — they
// carry the composer's frame and the gaps between transcript blocks — and are
// preserved exactly.
//
// Nothing else is normalized. Masking digits, in particular, would also mask
// the elapsed timer that makes a working screen unstable, which is the single
// property a fingerprint gate depends on.
func NormalizeLines(screen string) []string {
	lines := strings.Split(screen, "\n")
	for i, line := range lines {
		// A \r\n capture leaves the carriage return at the end of the line;
		// unicode.IsSpace trims it along with any padding.
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Fingerprint is FNV-1a/64 over the normalized lines, remapped away from zero
// so it never collides with the daemon's "check disabled" sentinel
// (AuthoritativeIdle.Generation documents 0 as disabling the generation check).
//
// FNV is the right weight here. This is a same-process, consecutive-tick
// EQUALITY check with no adversary and no persistence: collision resistance
// beyond "two different screens rarely collide" buys nothing, and a collision
// costs at most one stabilization credit that the harness classifier must still
// independently agree with. Two screens that differ only in trailing padding
// are meant to collide — that is what NormalizeLines is for.
func Fingerprint(screen string) int64 {
	digest := fnv.New64a()
	for _, line := range NormalizeLines(screen) {
		// The separator is written after every line so ["ab", ""] and ["a", "b"]
		// cannot hash alike.
		_, _ = digest.Write([]byte(line))
		_, _ = digest.Write([]byte{'\n'})
	}
	sum := int64(digest.Sum64())
	if sum == 0 {
		return 1
	}
	return sum
}

// StripANSI removes terminal escape sequences from a capture, leaving the text
// a plain capture of the same screen would hold. Adapters match text only, so
// a capture taken with styling (`tmux capture-pane -e`, `kitten @ get-text
// --ansi`) must pass through here before it is classified or fingerprinted;
// a plain capture is returned unchanged.
//
// It drops CSI sequences (SGR colour, cursor movement), OSC strings (titles,
// hyperlinks) up to BEL or ST, the other string controls (DCS, SOS, PM, APC),
// and two- and three-byte escapes such as charset selection. An unterminated
// sequence at the end of the capture is dropped rather than leaked as text.
func StripANSI(screen string) string {
	if !strings.ContainsRune(screen, '\x1b') {
		return screen
	}
	var out strings.Builder
	out.Grow(len(screen))
	for i := 0; i < len(screen); {
		if screen[i] != '\x1b' {
			out.WriteByte(screen[i])
			i++
			continue
		}
		i++
		if i >= len(screen) {
			break
		}
		switch c := screen[i]; {
		case c == '[':
			// Parameter and intermediate bytes, then one final byte.
			i++
			for i < len(screen) && (screen[i] < 0x40 || screen[i] > 0x7e) {
				i++
			}
			i++
		case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
			i++
			for i < len(screen) {
				if screen[i] == '\a' {
					i++
					break
				}
				if screen[i] == '\x1b' && i+1 < len(screen) && screen[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		case c >= 0x20 && c <= 0x2f:
			// Intermediate bytes (charset selection and the like), then a final.
			for i < len(screen) && screen[i] >= 0x20 && screen[i] <= 0x2f {
				i++
			}
			i++
		default:
			i++
		}
	}
	return out.String()
}
