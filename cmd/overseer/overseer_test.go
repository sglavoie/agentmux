package overseer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/spf13/cobra"
)

// policyEnvelope is the shared schema-v1 success envelope with this command's
// payload decoded into it, which is the shape a JSON caller actually parses.
type policyEnvelope struct {
	SchemaVersion int        `json:"schema_version"`
	Data          policyData `json:"data"`
}

// configProfile is the smallest configuration cli.LoadRunnableConfig accepts:
// one profile, and the [harness_emojis] entry validation requires for every
// harness a profile references.
const configProfile = `[harness_emojis]
claude = "🦊"

[[profiles]]
name = "work"
harness = "claude"
home_dir = "~/.claude-work"
color = "blue"
`

// projectFixture is one [[projects]] entry. optIn is written verbatim, so ""
// means the key is omitted entirely — "never asked", which is not consent.
type projectFixture struct {
	title     string
	directory string
	optIn     string
}

func renderConfig(projects ...projectFixture) string {
	var body strings.Builder
	body.WriteString(configProfile)
	for _, project := range projects {
		fmt.Fprintf(&body, "\n[[projects]]\ntitle = %q\ndirectory = %q\nprofile = \"work\"\n", project.title, project.directory)
		if project.optIn != "" {
			fmt.Fprintf(&body, "asl_overseer = %s\n", project.optIn)
		}
	}
	return body.String()
}

// configPath points this test at its own config file and returns the path, so a
// test can rewrite it between invocations.
func configPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("AMX_CONFIG", path)
	return path
}

func writeConfigFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
}

func stageConfig(t *testing.T, projects ...projectFixture) string {
	t.Helper()
	path := configPath(t)
	writeConfigFile(t, path, renderConfig(projects...))
	return path
}

// stageDir creates a directory the identity resolver can canonicalize.
func stageDir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{t.TempDir()}, parts...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	return dir
}

// requireGit skips when Git is unavailable. Authorization resolves every
// repository through a Git probe and refuses when it cannot, so without Git
// these fixtures would assert the fail-closed path while claiming to assert the
// decision.
func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	return git
}

func executePolicy(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := Command()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"policy"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// requireQuietExit asserts the invocation exited nonzero without asking Execute
// to print anything of its own, which is the contract for a command that has
// already written a complete result.
func requireQuietExit(t *testing.T, err error) {
	t.Helper()
	var exit *cli.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("error = %v (%T); want *cli.ExitError", err, err)
	}
	if exit.Code != 1 {
		t.Errorf("exit code = %d, want 1", exit.Code)
	}
	if !exit.Quiet {
		t.Error("exit is not quiet; the structured result would be followed by a redundant stderr line")
	}
}

func decodePolicy(t *testing.T, stdout string) policyEnvelope {
	t.Helper()
	var envelope policyEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode policy envelope: %v\n%s", err, stdout)
	}
	if envelope.SchemaVersion != cli.JSONSchemaVersion {
		t.Errorf("schema_version = %d, want %d", envelope.SchemaVersion, cli.JSONSchemaVersion)
	}
	return envelope
}

func decodeFailure(t *testing.T, stdout string) cli.JSONError {
	t.Helper()
	var envelope cli.JSONErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v\n%s", err, stdout)
	}
	if envelope.SchemaVersion != cli.JSONSchemaVersion {
		t.Errorf("schema_version = %d, want %d", envelope.SchemaVersion, cli.JSONSchemaVersion)
	}
	if strings.Contains(stdout, `"allowed"`) {
		t.Errorf("a failed request emitted a decision:\n%s", stdout)
	}
	return envelope.Error
}

func TestPolicyAllowsOptedInRepository(t *testing.T) {
	requireGit(t)
	app := stageDir(t, "app")
	stageConfig(t, projectFixture{title: "App", directory: app, optIn: "true"})

	stdout, stderr, err := executePolicy(t, "--json", "--access", "context", "--plan", "plan-1", "--repository", "app="+app)
	if err != nil {
		t.Fatalf("policy returned %v\n%s", err, stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	data := decodePolicy(t, stdout).Data

	if data.Contract.Name != contractName || data.Contract.Version != contractVersion {
		t.Errorf("contract = %+v, want %s/%d", data.Contract, contractName, contractVersion)
	}
	if got, want := strings.Join(data.Contract.AccessCategories, ","), "context,mutation,action"; got != want {
		t.Errorf("access_categories = %q, want %q", got, want)
	}
	if !data.Allowed || data.Reason != "allowed" {
		t.Fatalf("decision = allowed:%t reason:%s, want an allowed decision: %s", data.Allowed, data.Reason, data.Summary)
	}
	if data.Plan != "plan-1" || data.Access != "context" {
		t.Errorf("plan/access = %q/%q, want plan-1/context", data.Plan, data.Access)
	}
	if !strings.HasPrefix(data.ConfigFingerprint, "sha256:") || !strings.HasPrefix(data.RequestDigest, "sha256:") {
		t.Errorf("fingerprint %q and digest %q must both be sha256-prefixed", data.ConfigFingerprint, data.RequestDigest)
	}
	if len(data.Repositories) != 1 {
		t.Fatalf("repositories = %d, want 1", len(data.Repositories))
	}
	repository := data.Repositories[0]
	if repository.Alias != "app" || repository.Path != app {
		t.Errorf("repository identity = %q/%q, want app/%s", repository.Alias, repository.Path, app)
	}
	if repository.ResolvedPath == "" {
		t.Error("resolved_path is empty; the canonical identity is part of the answer")
	}
	if len(repository.Declarations) != 1 {
		t.Fatalf("declarations = %+v, want the one applicable project", repository.Declarations)
	}
	declaration := repository.Declarations[0]
	if declaration.Title != "App" || declaration.Profile != "work" || !declaration.Configured || !declaration.OptIn {
		t.Errorf("declaration = %+v, want the opted-in App project under profile work", declaration)
	}
	if declaration.ConfiguredDirectory != app {
		t.Errorf("configured_directory = %q, want %q", declaration.ConfiguredDirectory, app)
	}
}

func TestPolicyRefusalIsADecisionAndExitsNonzero(t *testing.T) {
	requireGit(t)
	app := stageDir(t, "app")
	stageConfig(t, projectFixture{title: "App", directory: app, optIn: "false"})

	stdout, _, err := executePolicy(t, "--json", "--access", "action", "--repository", "app="+app)
	requireQuietExit(t, err)

	// A refusal is a well-formed answer, not a command failure: it travels in
	// the data envelope so the caller gets the reason and the declaration that
	// produced it, rather than an error string.
	data := decodePolicy(t, stdout).Data
	if data.Allowed || data.Reason != "declined" {
		t.Fatalf("decision = allowed:%t reason:%s, want a declined refusal", data.Allowed, data.Reason)
	}
	if len(data.Repositories) != 1 || data.Repositories[0].Allowed {
		t.Fatalf("repositories = %+v, want one refused repository", data.Repositories)
	}
	if !strings.Contains(data.Repositories[0].Detail, app) {
		t.Errorf("detail = %q, want the configured directory %q", data.Repositories[0].Detail, app)
	}
	declaration := data.Repositories[0].Declarations[0]
	if !declaration.Configured || declaration.OptIn {
		t.Errorf("declaration = %+v, want an explicit decline", declaration)
	}
}

func TestPolicyRefusesThePlanWhenAnyRepositoryIsExcluded(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	app := filepath.Join(root, "app")
	api := filepath.Join(root, "api")
	for _, dir := range []string{app, api} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	stageConfig(t,
		projectFixture{title: "App", directory: app, optIn: "true"},
		projectFixture{title: "Api", directory: api, optIn: "false"},
	)

	stdout, _, err := executePolicy(t, "--json", "--access", "mutation", "--repository", "app="+app, "--repository", "api="+api)
	requireQuietExit(t, err)

	data := decodePolicy(t, stdout).Data
	if data.Allowed {
		t.Fatal("one excluded repository did not deny the plan")
	}
	if data.Reason != "declined" {
		t.Errorf("reason = %s, want declined", data.Reason)
	}
	// Every repository is still reported: the operator needs the whole picture,
	// not just the first thing that refused.
	if len(data.Repositories) != 2 {
		t.Fatalf("repositories = %d, want 2", len(data.Repositories))
	}
	if !data.Repositories[0].Allowed || data.Repositories[1].Allowed {
		t.Errorf("per-repository outcomes = %t/%t, want the opted-in one permitted and the declined one refused",
			data.Repositories[0].Allowed, data.Repositories[1].Allowed)
	}
}

func TestPolicyRefusesUndeclaredAndUnresolvableRepositories(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	declared := filepath.Join(root, "app")
	undeclared := filepath.Join(root, "elsewhere")
	for _, dir := range []string{declared, undeclared} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	stageConfig(t, projectFixture{title: "App", directory: declared, optIn: "true"})

	for name, testCase := range map[string]struct {
		path string
		want string
	}{
		"undeclared":      {path: undeclared, want: "undeclared"},
		"path_unresolved": {path: filepath.Join(root, "absent"), want: "path_unresolved"},
	} {
		t.Run(name, func(t *testing.T) {
			stdout, _, err := executePolicy(t, "--json", "--access", "context", "--repository", "repo="+testCase.path)
			requireQuietExit(t, err)
			data := decodePolicy(t, stdout).Data
			if data.Allowed || data.Reason != testCase.want {
				t.Fatalf("decision = allowed:%t reason:%s, want %s", data.Allowed, data.Reason, testCase.want)
			}
		})
	}
}

func TestPolicyRefusesARequestNamingNoRepository(t *testing.T) {
	stageConfig(t)

	stdout, _, err := executePolicy(t, "--json", "--access", "context")
	requireQuietExit(t, err)

	data := decodePolicy(t, stdout).Data
	if data.Allowed || data.Reason != "no_repositories" {
		t.Fatalf("decision = allowed:%t reason:%s, want a no_repositories refusal", data.Allowed, data.Reason)
	}
	// An empty list must marshal as [] rather than null: a caller ranging over
	// the field should find nothing, not fail to find the field.
	if !strings.Contains(stdout, `"repositories":[]`) {
		t.Errorf("repositories did not marshal as an empty array:\n%s", stdout)
	}
}

func TestPolicyMalformedConfigurationFailsClosed(t *testing.T) {
	path := configPath(t)
	// A quoted boolean is rejected by the TOML decoder itself, which fails the
	// whole load — so no project is authorized rather than some being skipped.
	writeConfigFile(t, path, configProfile+"\n[[projects]]\ntitle = \"App\"\ndirectory = \"/srv/app\"\nprofile = \"work\"\nasl_overseer = \"true\"\n")

	stdout, _, err := executePolicy(t, "--json", "--access", "context", "--repository", "app=/srv/app")
	requireQuietExit(t, err)

	failure := decodeFailure(t, stdout)
	if failure.Code != "config_error" {
		t.Fatalf("code = %q, want config_error: %s", failure.Code, failure.Message)
	}
	if !strings.Contains(failure.Message, "asl_overseer") {
		t.Errorf("message = %q, want the offending key named", failure.Message)
	}
}

func TestPolicyUnreadableConfigurationFailsClosed(t *testing.T) {
	// TestMain leaves AMX_CONFIG pointing at a file that does not exist, so this
	// case asserts the absent-configuration path without staging anything.
	stdout, _, err := executePolicy(t, "--json", "--access", "context", "--repository", "app=/srv/app")
	requireQuietExit(t, err)

	if failure := decodeFailure(t, stdout); failure.Code != "config_error" {
		t.Fatalf("code = %q, want config_error: %s", failure.Code, failure.Message)
	}
}

func TestPolicyRejectsUnsupportedAccessCategory(t *testing.T) {
	stageConfig(t)

	stdout, _, err := executePolicy(t, "--json", "--access", "Context", "--repository", "app=/srv/app")
	requireQuietExit(t, err)

	failure := decodeFailure(t, stdout)
	if failure.Code != "unsupported_access" {
		t.Fatalf("code = %q, want unsupported_access: %s", failure.Code, failure.Message)
	}
	// The refusal names what could have been asked, so a JSON caller can correct
	// itself without a second round trip.
	var offered []string
	for _, candidate := range failure.Candidates {
		offered = append(offered, candidate.ID)
		if candidate.DisplayName == "" {
			t.Errorf("candidate %q carries no description", candidate.ID)
		}
	}
	if got, want := strings.Join(offered, ","), "context,mutation,action"; got != want {
		t.Errorf("candidates = %q, want %q", got, want)
	}
}

func TestPolicyRequiresAnAccessCategory(t *testing.T) {
	stageConfig(t)

	stdout, _, err := executePolicy(t, "--json", "--repository", "app=/srv/app")
	requireQuietExit(t, err)

	failure := decodeFailure(t, stdout)
	if failure.Code != "missing_parameter" {
		t.Fatalf("code = %q, want missing_parameter: %s", failure.Code, failure.Message)
	}
	if len(failure.Parameters) != 1 || failure.Parameters[0].Name != "access" || !failure.Parameters[0].Required {
		t.Fatalf("parameters = %+v, want a required access parameter", failure.Parameters)
	}
}

func TestPolicyRejectsUnsupportedContractVersionBeforeReadingConfiguration(t *testing.T) {
	// No configuration is staged: TestMain's absent path would fail the load, so
	// reaching unsupported_contract_version proves the request is checked first
	// and that an unsupported caller gets the same answer either way.
	stdout, _, err := executePolicy(t, "--json", "--access", "context", "--contract-version", "2", "--repository", "app=/srv/app")
	requireQuietExit(t, err)

	failure := decodeFailure(t, stdout)
	if failure.Code != "unsupported_contract_version" {
		t.Fatalf("code = %q, want unsupported_contract_version: %s", failure.Code, failure.Message)
	}
	if len(failure.Candidates) != 1 || failure.Candidates[0].ID != "1" {
		t.Fatalf("candidates = %+v, want the supported contract version", failure.Candidates)
	}
}

func TestPolicyRejectsRepositorySpecsItCannotTrust(t *testing.T) {
	stageConfig(t)

	for name, spec := range map[string]string{
		"relative path": "app=relative/path",
		"bare relative": "relative/path",
		"empty path":    "app=",
		"empty alias":   "=/srv/app",
	} {
		t.Run(name, func(t *testing.T) {
			stdout, _, err := executePolicy(t, "--json", "--access", "context", "--repository", spec)
			requireQuietExit(t, err)
			if failure := decodeFailure(t, stdout); failure.Code != "invalid_repository" {
				t.Fatalf("code = %q, want invalid_repository: %s", failure.Code, failure.Message)
			}
		})
	}
}

func TestPolicyRejectsARepeatedAlias(t *testing.T) {
	stageConfig(t)

	stdout, _, err := executePolicy(t, "--json", "--access", "context", "--repository", "app=/srv/one", "--repository", "app=/srv/two")
	requireQuietExit(t, err)

	failure := decodeFailure(t, stdout)
	if failure.Code != "invalid_repository" {
		t.Fatalf("code = %q, want invalid_repository: %s", failure.Code, failure.Message)
	}
	if !strings.Contains(failure.Message, "/srv/one") || !strings.Contains(failure.Message, "/srv/two") {
		t.Errorf("message = %q, want both conflicting paths named", failure.Message)
	}
}

// The alias separator has to survive a path that contains one. Splitting on the
// first `=` unconditionally would turn /srv/a=b into the alias "/srv/a".
func TestParseRepositoriesSplitsAliasFromPath(t *testing.T) {
	for name, testCase := range map[string]struct {
		spec  string
		alias string
		path  string
	}{
		"alias and path":        {spec: "api=/srv/api", alias: "api", path: "/srv/api"},
		"path with =":           {spec: "/srv/a=b", alias: "", path: "/srv/a=b"},
		"alias keeps = in path": {spec: "api=/srv/a=b", alias: "api", path: "/srv/a=b"},
		"bare absolute path":    {spec: "/srv/api", alias: "", path: "/srv/api"},
		"tilde path":            {spec: "~/projects/api", alias: "", path: "~/projects/api"},
		"tilde path with alias": {spec: "api=~/projects/api", alias: "api", path: "~/projects/api"},
	} {
		t.Run(name, func(t *testing.T) {
			repositories, err := parseRepositories([]string{testCase.spec})
			if err != nil {
				t.Fatalf("parseRepositories(%q): %v", testCase.spec, err)
			}
			if repositories[0].Alias != testCase.alias || repositories[0].Path != testCase.path {
				t.Fatalf("parsed %q as %q/%q, want %q/%q", testCase.spec,
					repositories[0].Alias, repositories[0].Path, testCase.alias, testCase.path)
			}
		})
	}
}

// The whole point of the surface: an answer is not a lease. Withdrawing consent
// between two identical questions changes the second answer, and both the
// configuration fingerprint and the request digest say so.
func TestPolicyReadsConfigurationOnEveryRequest(t *testing.T) {
	requireGit(t)
	app := stageDir(t, "app")
	path := configPath(t)
	writeConfigFile(t, path, renderConfig(projectFixture{title: "App", directory: app, optIn: "true"}))

	args := []string{"--json", "--access", "context", "--plan", "plan-1", "--repository", "app=" + app}
	stdout, _, err := executePolicy(t, args...)
	if err != nil {
		t.Fatalf("first invocation returned %v\n%s", err, stdout)
	}
	granted := decodePolicy(t, stdout).Data
	if !granted.Allowed {
		t.Fatalf("first invocation refused: %s", granted.Summary)
	}

	writeConfigFile(t, path, renderConfig(projectFixture{title: "App", directory: app, optIn: "false"}))

	stdout, _, err = executePolicy(t, args...)
	requireQuietExit(t, err)
	withdrawn := decodePolicy(t, stdout).Data
	if withdrawn.Allowed || withdrawn.Reason != "declined" {
		t.Fatalf("second invocation = allowed:%t reason:%s, want a declined refusal", withdrawn.Allowed, withdrawn.Reason)
	}
	if withdrawn.ConfigFingerprint == granted.ConfigFingerprint {
		t.Error("the configuration fingerprint did not move when the opt-in was withdrawn")
	}
	// The digest covers the fingerprint, so the same question asked against
	// different configuration is a different question.
	if withdrawn.RequestDigest == granted.RequestDigest {
		t.Error("the request digest did not move when configuration changed")
	}
}

// A memoized evaluator would answer the second question with the first
// question's configuration, which is exactly the cached permission this surface
// must not have.
func TestPolicyLoadsConfigurationOncePerInvocation(t *testing.T) {
	requireGit(t)
	app := stageDir(t, "app")
	loads := 0
	previous := overseerLoadConfig
	overseerLoadConfig = func() (*config.Config, error) {
		loads++
		return &config.Config{Projects: []config.Session{{
			Title: "App", Directory: app, Profile: "work", ASLOverseer: &[]bool{true}[0],
		}}}, nil
	}
	t.Cleanup(func() { overseerLoadConfig = previous })

	for range 2 {
		if _, _, err := executePolicy(t, "--json", "--access", "context", "--repository", "app="+app); err != nil {
			t.Fatalf("policy returned %v", err)
		}
	}
	if loads != 2 {
		t.Fatalf("configuration was loaded %d times for 2 invocations, want 2", loads)
	}
}

// The digest is what makes a decision evidence for one question rather than a
// transferable grant.
func TestPolicyRequestDigestBindsTheQuestion(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	app := filepath.Join(root, "app")
	api := filepath.Join(root, "api")
	for _, dir := range []string{app, api} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	stageConfig(t,
		projectFixture{title: "App", directory: app, optIn: "true"},
		projectFixture{title: "Api", directory: api, optIn: "true"},
	)

	digestFor := func(t *testing.T, args ...string) string {
		t.Helper()
		stdout, _, err := executePolicy(t, args...)
		if err != nil {
			t.Fatalf("policy returned %v\n%s", err, stdout)
		}
		return decodePolicy(t, stdout).Data.RequestDigest
	}

	baseline := digestFor(t, "--json", "--access", "context", "--plan", "plan-1", "--repository", "app="+app)
	if repeated := digestFor(t, "--json", "--access", "context", "--plan", "plan-1", "--repository", "app="+app); repeated != baseline {
		t.Fatalf("the same question produced two digests: %q and %q", baseline, repeated)
	}
	for name, args := range map[string][]string{
		"another access category": {"--json", "--access", "action", "--plan", "plan-1", "--repository", "app=" + app},
		"another plan":            {"--json", "--access", "context", "--plan", "plan-2", "--repository", "app=" + app},
		"another alias":           {"--json", "--access", "context", "--plan", "plan-1", "--repository", "primary=" + app},
		"a wider repository set":  {"--json", "--access", "context", "--plan", "plan-1", "--repository", "app=" + app, "--repository", "api=" + api},
	} {
		if got := digestFor(t, args...); got == baseline {
			t.Errorf("%s reused the baseline digest %q", name, baseline)
		}
	}
}

// Nothing in the payload may look like something a caller can hold on to. This
// guards the shape against a well-meaning later addition of a TTL or a token.
func TestPolicyPayloadCarriesNoReusableGrant(t *testing.T) {
	requireGit(t)
	app := stageDir(t, "app")
	stageConfig(t, projectFixture{title: "App", directory: app, optIn: "true"})

	stdout, _, err := executePolicy(t, "--json", "--access", "context", "--repository", "app="+app)
	if err != nil {
		t.Fatalf("policy returned %v\n%s", err, stdout)
	}
	var raw struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("decode payload: %v\n%s", err, stdout)
	}
	for _, forbidden := range []string{"expires_at", "expires", "ttl", "token", "lease", "grant", "valid_until"} {
		if _, ok := raw.Data[forbidden]; ok {
			t.Errorf("payload carries %q; a decision is evidence for one request, not a lease", forbidden)
		}
	}
}

func TestPolicyTextModeCarriesTheDecision(t *testing.T) {
	requireGit(t)
	app := stageDir(t, "app")
	stageConfig(t, projectFixture{title: "App", directory: app, optIn: "false"})

	stdout, _, err := executePolicy(t, "--access", "context", "--plan", "plan-1", "--repository", "app="+app)
	// Text and JSON must agree on the exit status, or a shell caller and a
	// script caller would disagree about whether they were authorized.
	requireQuietExit(t, err)

	for _, want := range []string{
		"contract: overseer_policy/1",
		"plan: plan-1",
		"access: context",
		"decision: refused",
		"reason: declined",
		"config_fingerprint: sha256:",
		"request_digest: sha256:",
		"declaration\tApp\t" + app + "\tdeclined",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("text output is missing %q:\n%s", want, stdout)
		}
	}
}

func TestPolicyTextModeExitsZeroWhenAllowed(t *testing.T) {
	requireGit(t)
	app := stageDir(t, "app")
	stageConfig(t, projectFixture{title: "App", directory: app, optIn: "true"})

	stdout, _, err := executePolicy(t, "--access", "context", "--repository", "app="+app)
	if err != nil {
		t.Fatalf("policy returned %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "decision: allowed") {
		t.Errorf("text output does not report the decision:\n%s", stdout)
	}
}

// A real checkout, resolved through the real Git probe rather than a staged
// directory, so the wiring between the command and the identity resolver is
// exercised end to end at least once.
func TestPolicyAuthorizesARealCheckout(t *testing.T) {
	git := requireGit(t)
	repo := stageDir(t, "checkout")
	runGit(t, git, repo, "init")
	nested := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", nested, err)
	}
	stageConfig(t, projectFixture{title: "Checkout", directory: repo, optIn: "true"})

	stdout, _, err := executePolicy(t, "--json", "--access", "mutation", "--repository", "repo="+nested)
	if err != nil {
		t.Fatalf("policy returned %v\n%s", err, stdout)
	}
	data := decodePolicy(t, stdout).Data
	if !data.Allowed {
		t.Fatalf("a directory inside an opted-in checkout was refused: %s", data.Summary)
	}
	if len(data.Repositories) != 1 || data.Repositories[0].ResolvedPath == "" {
		t.Fatalf("repositories = %+v, want one resolved repository", data.Repositories)
	}
}

// The family is the authorization surface and nothing else. A leaf that
// appeared here without being named in this list would be a new way to reach
// project or conversation content past the two that were designed for it.
func TestOverseerFamilyRegistersOnlyTheAuthorizationLeaves(t *testing.T) {
	var names []string
	for _, command := range Command().Commands() {
		names = append(names, command.Name())
	}
	if got := strings.Join(names, ","); got != "history,policy" {
		t.Fatalf("overseer leaves = %q, want history,policy", got)
	}
}

// Command builds a fresh tree per call precisely so flag values cannot leak
// from one construction to the next; a package-level singleton would let one
// caller's --json survive into another's invocation.
func TestCommandReturnsAnIndependentTree(t *testing.T) {
	first, second := Command(), Command()
	if first == second {
		t.Fatal("Command returned the same tree twice; flag state would be shared")
	}
	if err := policyLeaf(t, first).Flags().Set("json", "true"); err != nil {
		t.Fatalf("set --json: %v", err)
	}
	if value, _ := policyLeaf(t, second).Flags().GetBool("json"); value {
		t.Fatal("--json leaked from one tree into another")
	}
}

func policyLeaf(t *testing.T, tree *cobra.Command) *cobra.Command {
	t.Helper()
	for _, command := range tree.Commands() {
		if command.Name() == "policy" {
			return command
		}
	}
	t.Fatal("policy leaf is not registered")
	return nil
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	command := exec.Command(git, append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Agent Mux",
		"GIT_AUTHOR_EMAIL=agentmux@example.invalid",
		"GIT_COMMITTER_NAME=Agent Mux",
		"GIT_COMMITTER_EMAIL=agentmux@example.invalid",
	)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
