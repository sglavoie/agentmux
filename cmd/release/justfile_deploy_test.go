package release

// Deployment-contract guards over the Justfile. `just install` must replace
// the binary atomically (stage beside the destination, then rename) — a
// remove-then-copy sequence leaves a window where amx is absent or partially
// written, and a partially copied binary is exactly the artifact confusion the
// build-identity checks exist to catch. The install-restart-smoke recipe is
// the deployment gate: install, restart, wait for readiness, strict doctor.

import (
	"os"
	"os/exec"
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
	// The restart and its gates live in the private restart-verify recipe,
	// which `update` shares, and in the recipes it runs after the restart, so
	// the smoke is judged through all of them.
	smoke := justfileRecipeClosure(t, justfile, "restart-verify")
	header, _ := justfileRecipeWithHeader(t, justfile, "restart-verify")
	if !strings.Contains(header, `&& (daemon-ready "50") verify-daemon`) {
		t.Errorf("restart-verify must wait for readiness, then verify, after the restart: %s", header)
	}

	for _, want := range []string{
		"daemon restart",
		"ready: true",
		// A required ASL bridge handshakes after readiness; doctor must not
		// judge it before the bounded wait for that handshake.
		"(mode required",
		"doctor --strict",
	} {
		if !strings.Contains(smoke, want) {
			t.Errorf("restart-verify recipe missing %q:\n%s", want, smoke)
		}
	}
	if !strings.Contains(justfile, "install-restart-smoke: install restart-verify\n") {
		t.Error("install-restart-smoke must depend on install, then restart-verify")
	}
}

// TestJustUpdateInstallsEverythingBeforeVerifying keeps `just update` in the
// order that makes its doctor run meaningful: the binary, the hook files it
// writes, and the app are all installed before the daemon restarts onto them
// and strict doctor judges them. It is an operator convenience, so it must stay out of the gates the way
// every mac-* recipe does; TestMacAppRecipesStayOutOfTheGates covers that.
func TestJustUpdateInstallsEverythingBeforeVerifying(t *testing.T) {
	justfile := readJustfile(t)
	justfileRecipe(t, justfile, "update")
	if !strings.Contains(justfile, "\nupdate: install sync-hooks mac-install restart-verify\n") {
		t.Error("update must depend on install, sync-hooks, mac-install, then restart-verify")
	}
	if !strings.Contains(justfileRecipe(t, justfile, "sync-hooks"), "config hooks-sync") {
		t.Error("sync-hooks must run the installed amx's hooks-sync")
	}
}

// TestMacAppRecipesStayOutOfTheGates keeps the Agent Mux.app recipes opt-in.
// They need Xcode and sign with the operator's identity, so a machine that can
// build and release amx must not need either: check, acceptance, and the
// paired release gate never reach a mac-* recipe or the macos/ tree, directly
// or through a recipe they depend on.
func TestMacAppRecipesStayOutOfTheGates(t *testing.T) {
	justfile := readJustfile(t)
	for _, name := range []string{"mac-app", "mac-test", "mac-install", "mac-uninstall", "mac-ghosttykit", "mac-tmux", "mac-release-gate"} {
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
		for _, forbidden := range []string{"mac-", "macos/", "desktop-release-gate", "xcodebuild"} {
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

// justfileRecipeClosure returns the header and body of name and of every
// recipe it depends on, transitively, whether the dependency runs before it
// (`a: b`) or after it (`a: && b`), with or without arguments (`(b "50")`).
// A guard moved into a shared private recipe is still the recipe's guard, so
// the contract tests judge what running the recipe runs.
func justfileRecipeClosure(t *testing.T, justfile, name string) string {
	t.Helper()
	dep := regexp.MustCompile(`\(?([a-z][a-z0-9-]*)`)
	var out strings.Builder
	seen := map[string]bool{}
	queue := []string{name}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		header, body := justfileRecipeWithHeader(t, justfile, next)
		out.WriteString(header + body)
		_, deps, _ := strings.Cut(header, ":")
		for _, m := range dep.FindAllStringSubmatch(deps, -1) {
			queue = append(queue, m[1])
		}
	}
	return out.String()
}

// TestJustMacReleaseGateIsTheDesktopGateAndNothingElse keeps the desktop gate
// a gate of its own: its recipe runs the hidden desktop-release-gate command
// over this checkout, passes the operator's flags through, and reaches none of
// the recipes that install, restart, or release the pair.
func TestJustMacReleaseGateIsTheDesktopGateAndNothingElse(t *testing.T) {
	justfile := readJustfile(t)
	header, body := justfileRecipeWithHeader(t, justfile, "mac-release-gate")
	if !strings.Contains(body, `desktop-release-gate --amx-root "{{ root }}" {{ flags }}`) {
		t.Errorf("mac-release-gate does not run the desktop gate over this checkout with the caller's flags:\n%s", body)
	}
	if _, deps, _ := strings.Cut(header, ":"); strings.TrimSpace(deps) != "" {
		t.Errorf("mac-release-gate depends on %q; the gate runs its own steps so its record lists them", deps)
	}
	for _, forbidden := range []string{"install", "restart", "paired-release-gate", "release-gate --asl-root", "--tag"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("mac-release-gate reaches %q:\n%s", forbidden, body)
		}
	}

	for _, name := range []string{"amx-root", "signing-key", "evidence-dir", "notarized", "rehearsal", "force", "json"} {
		if desktopGateCmd.Flags().Lookup(name) == nil {
			t.Errorf("desktop-release-gate does not declare --%s", name)
		}
	}
	if !desktopGateCmd.Hidden || !desktopGateCmd.SilenceErrors || !desktopGateCmd.SilenceUsage {
		t.Error("desktop-release-gate must be hidden and map its own outcomes to exit codes")
	}
}

// TestNamedArgReadsTheDocumentedForms pins scripts/named-arg, which the
// recipes that take `key=value` arguments share: the prefix is stripped, a
// bare value passes through, and a leading ~ is the home directory, since the
// documented `asl_root=~/scripts` reaches the recipe unexpanded in some
// shells.
func TestNamedArgReadsTheDocumentedForms(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct{ name, value, want string }{
		{"asl_root", "asl_root=/src/scripts", "/src/scripts"},
		{"asl_root", "/src/scripts", "/src/scripts"},
		{"asl_root", "asl_root=~/scripts", home + "/scripts"},
		{"signing_key", "~/.ssh/amx_release", home + "/.ssh/amx_release"},
		{"signing_key", "signing_key=", ""},
		{"tag", "tag=yes", "yes"},
		{"asl_root", "~", home},
		{"asl_root", "~other/scripts", "~other/scripts"},
		{"asl_root", "signing_key=/key", "signing_key=/key"},
	} {
		cmd := exec.Command(filepath.Join("..", "..", "scripts", "named-arg"), tc.name, tc.value)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("named-arg %s %q: %v", tc.name, tc.value, err)
		}
		if got := strings.TrimSuffix(string(out), "\n"); got != tc.want {
			t.Errorf("named-arg %s %q = %q, want %q", tc.name, tc.value, got, tc.want)
		}
	}
}

// TestGatesBuildTheScenarioHarness keeps the gates on the build that has the
// TUI scenario harness. The harness and every test that drives the dashboard
// through it build only with the tuitest tag, and an untagged `go test`,
// `go vet`, or golangci-lint run passes without them, so a recipe that loses
// the tag stays green while it stops checking every committed scenario. The
// release build is the other half: `build` (and so `install`) must not carry
// the tag, or the harness ships again.
func TestGatesBuildTheScenarioHarness(t *testing.T) {
	justfile := readJustfile(t)
	constraint, err := os.ReadFile(filepath.Join("..", "harness_tuitest.go"))
	if err != nil {
		t.Fatalf("read the harness registration: %v", err)
	}
	tag, _, _ := strings.Cut(strings.TrimPrefix(string(constraint), "//go:build "), "\n")
	if !strings.Contains(justfile, "\nharness_tags := \""+tag+"\"\n") {
		t.Fatalf("Justfile's harness_tags does not name the %q tag cmd/harness_tuitest.go builds under", tag)
	}

	for _, tc := range []struct{ recipe, want string }{
		{"test", "go test -tags {{ harness_tags }} ./...\n"},
		{"test", "go test ./cmd\n"},
		{"vet", "go vet ./...\n"},
		{"vet", "go vet -tags {{ harness_tags }} ./...\n"},
		{"race-core", "go test -race -tags {{ harness_tags }} "},
		{"tui-scenarios", "go run -tags {{ harness_tags }} . tui-test run docs/tui-testing/scenarios/*.json"},
		{"lint", "golangci-lint run --allow-parallel-runners\n"},
		{"lint", "golangci-lint run --allow-parallel-runners --build-tags {{ harness_tags }}\n"},
	} {
		if body := justfileRecipe(t, justfile, tc.recipe); !strings.Contains(body, tc.want) {
			t.Errorf("recipe %q does not run %q:\n%s", tc.recipe, strings.TrimSpace(tc.want), body)
		}
	}
	if body := justfileRecipe(t, justfile, "build"); strings.Contains(body, "-tags") {
		t.Errorf("build must produce the release binary, without the harness tag:\n%s", body)
	}

	check := justfileRecipeClosure(t, justfile, "check")
	for _, recipe := range []string{"test", "vet", "race-core", "lint"} {
		header, _ := justfileRecipeWithHeader(t, justfile, recipe)
		if !strings.Contains(check, header) {
			t.Errorf("check does not reach %q, so the tagged build goes unchecked there", recipe)
		}
	}
}
