package transcript

import (
	"os"
	"strings"
)

// ShortSummary is Summary for a one-line row: the same text with each long
// absolute path cut to its last element (see ShortenSummary). Rows that list
// sessions use it; JSON, piped output, and session show keep Summary whole.
func (m SessionMeta) ShortSummary() string {
	home, _ := os.UserHomeDir()
	return ShortenSummary(m.Summary(), home)
}

// ShortenSummary is a one-line summary as a row shows it: a command's leading
// environment assignments set aside (see elideCommandEnv), then each long
// absolute path cut to its last element (see ShortenPaths).
func ShortenSummary(text, home string) string {
	return ShortenPaths(elideCommandEnv(text), home)
}

// elideCommandEnv writes the environment assignments a summary's command
// leads with as a single "…". A session opened by `/asl
// CLAUDE_CONFIG_DIR=~/.claude-personal run <plan>` is summarized by that
// prompt, and in a row the assignment alone filled the room the plan needed.
// Only a run of NAME=value words (NAME in upper case, as environment variables
// are written) at the start of the text, or right after a leading "/command"
// or Codex "$skill", is elided; an assignment later in a sentence is part of
// what was said and stays.
func elideCommandEnv(text string) string {
	words := strings.Split(text, " ")
	start := 0
	if len(words) > 0 && len(words[0]) > 1 && (words[0][0] == '/' || words[0][0] == '$') && !strings.Contains(words[0][1:], "/") {
		start = 1
	}
	end := start
	for end < len(words) && isEnvAssignment(words[end]) {
		end++
	}
	if end == start {
		return text
	}
	return strings.Join(append(append(words[:start:start], "…"), words[end:]...), " ")
}

// isEnvAssignment reports whether word is NAME=value with an upper-case
// environment variable NAME.
func isEnvAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, r := range name {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
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
