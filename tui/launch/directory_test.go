package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDirectory(t *testing.T) {
	cases := []struct{ name, configured, typed, want string }{
		{"empty clears", "/src/api", "  ", ""},
		{"configured clears", "/src/api", "/src/api/", ""},
		{"absolute stands", "/src/api", "/src/other", "/src/other"},
		{"tilde stands", "/src/api", "~/work/", "~/work"},
		{"relative starts at the project", "~/src/api", "services/auth", "~/src/api/services/auth"},
		{"parent of the project", "/src/api", "../web", "/src/web"},
		{"dot is the project", "/src/api", ".", ""},
	}
	for _, tc := range cases {
		if got := ResolveDirectory(tc.configured, tc.typed); got != tc.want {
			t.Errorf("%s: ResolveDirectory(%q, %q) = %q, want %q", tc.name, tc.configured, tc.typed, got, tc.want)
		}
	}
}

func TestCompleteDirectory(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"services/auth", "services/audit", "scripts", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "setup.sh"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, typed, want string }{
		{"several share a prefix", "s", "s"},
		{"one match gains a slash", "sc", "scripts/"},
		{"files are not offered", "se", "services/"},
		{"below a relative parent", "services/a", "services/au"},
		{"hidden needs its dot", "", "s"},
		{"hidden with its dot", ".", ".git/"},
		{"absolute parent", root + "/scr", root + "/scripts/"},
		{"nothing matches", "zz", "zz"},
	}
	for _, tc := range cases {
		if got := CompleteDirectory(root, tc.typed); got != tc.want {
			t.Errorf("%s: CompleteDirectory(%q) = %q, want %q", tc.name, tc.typed, got, tc.want)
		}
	}
}
