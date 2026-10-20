// Package textutil holds small text helpers shared by packages that must not
// depend on each other, such as the transcript and plan indexes.
package textutil

// Snippet collapses newlines, carriage returns, and tabs to spaces, folds
// runs of spaces into one, and keeps at most max runes, appending "…" when it
// cut anything. The transcript and plan indexes store their snippets through
// it, so Past Sessions and Plans rows have one look.
func Snippet(s string, max int) string {
	out := make([]rune, 0, max)
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if r == ' ' && prevSpace {
			continue
		}
		if len(out) >= max {
			return string(out) + "…"
		}
		prevSpace = r == ' '
		out = append(out, r)
	}
	return string(out)
}
