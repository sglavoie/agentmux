package transcript

import (
	"os"
	"strings"
)

// ShortSummary is Summary for a one-line row: the same text with each long
// absolute path cut to its last element (see ShortenPaths). Rows that list
// sessions use it; JSON, piped output, and session show keep Summary whole.
func (m SessionMeta) ShortSummary() string {
	home, _ := os.UserHomeDir()
	return ShortenPaths(m.Summary(), home)
}

// ShortenPaths shortens the absolute paths in a one-line summary. A session
// opened by a command such as `/asl run /Users/me/.local/share/agent-plans/
// 4f4e36c66116-93448e2a42c6/ --harness claude` is summarized by its prompt,
// and in a row cut to the terminal's width the path's home directory and
// parent folders crowded out its last element, the one part that tells two
// such sessions apart. So home is written as "~", and a path of more than two
// elements is cut to "…/" and its last one.
//
// A path is a space-separated word, or the value of a NAME=value word, that
// starts with "/" or "~/" (a URL's "//" does not count). A slash command such
// as "/asl" is a path of one element and is left as it is.
func ShortenPaths(text, home string) string {
	home = strings.TrimSuffix(home, "/")
	words := strings.Split(text, " ")
	for i, word := range words {
		start := 0
		if name, value, ok := strings.Cut(word, "="); ok && name != "" && !strings.Contains(name, "/") {
			start = len(word) - len(value)
		}
		words[i] = word[:start] + shortenPath(word[start:], home)
	}
	return strings.Join(words, " ")
}

func shortenPath(path, home string) string {
	if strings.HasPrefix(path, "//") {
		return path
	}
	if home != "" {
		if path == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(path, home+"/"); ok {
			path = "~/" + rest
		}
	}
	var rest string
	switch {
	case strings.HasPrefix(path, "~/"):
		rest = path[2:]
	case strings.HasPrefix(path, "/"):
		rest = path[1:]
	default:
		return path
	}
	trailing := strings.HasSuffix(rest, "/")
	elements := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' })
	if len(elements) <= 2 {
		return path
	}
	short := "…/" + elements[len(elements)-1]
	if trailing {
		short += "/"
	}
	return short
}
