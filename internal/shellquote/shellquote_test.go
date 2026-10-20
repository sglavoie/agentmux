package shellquote

import (
	"os/exec"
	"testing"
)

func TestQuote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "''"},
		{"plain", "plain"},
		{"/Users/me/.local/bin/amx", "/Users/me/.local/bin/amx"},
		{"--flag=value", "--flag=value"},
		{"user@host:path,a%b+c", "user@host:path,a%b+c"},
		{"_-./", "_-./"},
		{"~", "'~'"},
		{"~/bin/amx", "'~/bin/amx'"},
		{"a~b", "'a~b'"},
		{"with space", "'with space'"},
		{"tab\there", "'tab\there'"},
		{"line\nbreak", "'line\nbreak'"},
		{"it's", `'it'\''s'`},
		{"'", `''\'''`},
		{`double "quotes"`, `'double "quotes"'`},
		{`back\slash`, `'back\slash'`},
		{"$HOME", "'$HOME'"},
		{"`cmd`", "'`cmd`'"},
		{"a;b", "'a;b'"},
		{"a|b&c", "'a|b&c'"},
		{"glob*?[x]", "'glob*?[x]'"},
		{"{a,b}", "'{a,b}'"},
		{"!bang", "'!bang'"},
		{"#comment", "'#comment'"},
		{"caret^", "'caret^'"},
		{"(sub)<in>", "'(sub)<in>'"},
		{"/Users/sébastien/amx", "/Users/sébastien/amx"},
		{"日本語", "日本語"},
		{"٣", "٣"},
		{"em—dash", "'em—dash'"},
		{"emoji🙂", "'emoji🙂'"},
		{"cr\r", "'cr\r'"},
	} {
		if got := Quote(tc.in); got != tc.want {
			t.Errorf("Quote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJoin(t *testing.T) {
	if got := Join(nil); got != "" {
		t.Errorf("Join(nil) = %q", got)
	}
	if got, want := Join([]string{"amx", "", "two words", "it's"}), `amx '' 'two words' 'it'\''s'`; got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}
}

// What a real POSIX shell reads back is the point; the table above only pins
// the spelling. Every word must come back byte-identical, as one argument.
func TestAShellReadsEveryWordBackUnchanged(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	words := []string{
		"", "plain", "~", "~/x", "a b", "tab\tx", "nl\nx", "it's", "''", `"`, `\`, `\'`,
		"$HOME", "${HOME}", "`id`", "$(id)", "a;b", "a|b", "a&b", "*", "?", "[a]", "{a,b}",
		"!x", "#x", "^x", "<x>", "(x)", "sébastien", "日本語", "🙂", "-n", "--", "=x", "%x",
	}
	for _, word := range words {
		// printf receives exactly one argument, or the "[%s]" count shows it.
		out, err := exec.Command(sh, "-c", "printf '[%s]' "+Quote(word)).Output()
		if err != nil {
			t.Fatalf("sh for %q: %v", word, err)
		}
		if got, want := string(out), "["+word+"]"; got != want {
			t.Errorf("sh read Quote(%q) = %q back as %q", word, Quote(word), got)
		}
	}
	out, err := exec.Command(sh, "-c", "printf '[%s]' "+Join(words)).Output()
	if err != nil {
		t.Fatalf("sh for Join: %v", err)
	}
	want := ""
	for _, word := range words {
		want += "[" + word + "]"
	}
	if string(out) != want {
		t.Errorf("sh read Join back as %q, want %q", out, want)
	}
}
