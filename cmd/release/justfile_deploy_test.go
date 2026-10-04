package release

// Deployment-contract guards over the Justfile. `just install` must replace
// the binary atomically (stage beside the destination, then rename) — a
// remove-then-copy sequence leaves a window where amx is absent or partially
// written, and a partially copied binary is exactly the artifact confusion the
// build-identity checks exist to catch. The install-restart-smoke recipe is
// the deployment gate: install, restart, wait for readiness, strict doctor.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readJustfile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "Justfile"))
	if err != nil {
		t.Fatalf("read Justfile: %v", err)
	}
	return string(data)
}

func justfileRecipe(t *testing.T, justfile, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `:.*$`)
	loc := re.FindStringIndex(justfile)
	if loc == nil {
		t.Fatalf("Justfile has no %q recipe", name)
	}
	rest := justfile[loc[1]:]
	end := len(rest)
	// A recipe ends at the next non-indented, non-empty line.
	for i, line := range strings.Split(rest, "\n") {
		if i == 0 {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			end = strings.Index(rest, line)
			break
		}
	}
	return rest[:end]
}

func TestJustInstallReplacesBinaryAtomically(t *testing.T) {
	justfile := readJustfile(t)
	install := justfileRecipe(t, justfile, "install")

	if strings.Contains(install, "rm -f") {
		t.Errorf("install recipe removes the binary before replacing it (non-atomic window):\n%s", install)
	}
	if !strings.Contains(install, ".amx.staging") {
		t.Errorf("install recipe does not stage the binary beside the destination:\n%s", install)
	}
	if !strings.Contains(install, `mv -f "{{prefix_bin}}/.amx.staging" "{{prefix_bin}}/amx"`) {
		t.Errorf("install recipe does not rename the staged binary into place:\n%s", install)
	}
	staging := strings.Index(install, ".amx.staging")
	target := strings.Index(install, `mv -f`)
	if staging == -1 || target == -1 || target < staging {
		t.Errorf("install recipe must stage before renaming:\n%s", install)
	}
}

func TestJustInstallRestartSmokeGatesOnReadinessAndStrictDoctor(t *testing.T) {
	justfile := readJustfile(t)
	smoke := justfileRecipe(t, justfile, "install-restart-smoke")

	for _, want := range []string{
		"daemon restart",
		"ready: true",
		"doctor --strict",
	} {
		if !strings.Contains(smoke, want) {
			t.Errorf("install-restart-smoke recipe missing %q:\n%s", want, smoke)
		}
	}
	if !strings.Contains(justfile, "install-restart-smoke: install") {
		t.Error("install-restart-smoke must depend on install")
	}
}

// TestMacAppRecipesStayOutOfTheGates keeps the Agent Mux.app recipes opt-in.
// They need Xcode and sign with the operator's identity, so a machine that can
// build and release amx must not need either: check, acceptance, and the
// paired release gate never reach a mac-* recipe or the macos/ tree, directly
// or through a recipe they depend on.
func TestMacAppRecipesStayOutOfTheGates(t *testing.T) {
	justfile := readJustfile(t)
	for _, name := range []string{"mac-app", "mac-test", "mac-install"} {
		justfileRecipeWithHeader(t, justfile, name)
	}

	seen := map[string]bool{}
	queue := []string{"check", "acceptance", "paired-release-gate"}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		header, body := justfileRecipeWithHeader(t, justfile, name)
		for _, forbidden := range []string{"mac-", "macos/"} {
			if strings.Contains(header+body, forbidden) {
				t.Errorf("recipe %q reaches %q; the macOS app recipes must stay opt-in:\n%s%s", name, forbidden, header, body)
			}
		}
		// Dependencies follow the colon: `check: acceptance lint`.
		_, deps, _ := strings.Cut(header, ":")
		for _, dep := range strings.Fields(deps) {
			if regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(dep) {
				queue = append(queue, dep)
			}
		}
	}
}

// justfileRecipeWithHeader is justfileRecipe for recipes that take
// parameters: it returns the header line and the indented body.
func justfileRecipeWithHeader(t *testing.T, justfile, name string) (string, string) {
	t.Helper()
	// Parameters may carry defaults (`configuration="Debug"`), but a `:=`
	// variable assignment is not a recipe.
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `(\s[^\n]*)?:([^=\n][^\n]*)?$`)
	loc := re.FindStringIndex(justfile)
	if loc == nil {
		t.Fatalf("Justfile has no %q recipe", name)
	}
	header := justfile[loc[0]:loc[1]]
	rest := justfile[loc[1]:]
	end := len(rest)
	for i, line := range strings.Split(rest, "\n") {
		if i == 0 {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			end = strings.Index(rest, line)
			break
		}
	}
	return header, rest[:end]
}
