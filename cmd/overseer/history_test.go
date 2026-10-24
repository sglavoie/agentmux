package overseer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/overseerpolicy"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// historyEnvelope is the shared schema-v1 success envelope with the history
// payload decoded into it.
type historyEnvelope struct {
	SchemaVersion int         `json:"schema_version"`
	Data          historyData `json:"data"`
}

// historySecret appears only inside message bodies in these fixtures. Finding it
// in an answer that should not carry it means content crossed the boundary.
const historySecret = "SECRET-TRANSCRIPT-TEXT"

// renderHistoryConfig writes a configuration whose one profile reads its
// transcripts from a staged home, so a test poses the corpus the command walks.
func renderHistoryConfig(home string, projects ...projectFixture) string {
	var body strings.Builder
	body.WriteString("[harness_emojis]\nclaude = \"🦊\"\n\n[[profiles]]\nname = \"work\"\nharness = \"claude\"\nhome_dir = ")
	fmt.Fprintf(&body, "%q\ncolor = \"blue\"\n", home)
	for _, project := range projects {
		fmt.Fprintf(&body, "\n[[projects]]\ntitle = %q\ndirectory = %q\nprofile = \"work\"\n", project.title, project.directory)
		if project.optIn != "" {
			fmt.Fprintf(&body, "asl_overseer = %s\n", project.optIn)
		}
	}
	return body.String()
}

func executeHistory(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := Command()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"history"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func decodeHistory(t *testing.T, stdout string) historyEnvelope {
	t.Helper()
	var envelope historyEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode history envelope: %v\n%s", err, stdout)
	}
	if envelope.SchemaVersion != cli.JSONSchemaVersion {
		t.Errorf("schema_version = %d, want %d", envelope.SchemaVersion, cli.JSONSchemaVersion)
	}
	return envelope
}

// writeClaudeTranscript stages one Claude transcript working in cwd, carrying a
// user and an assistant message that both contain historySecret.
func writeClaudeTranscript(t *testing.T, home, encodedDir, sessionID, cwd string) string {
	t.Helper()
	path := filepath.Join(home, "projects", encodedDir, sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := strings.Join([]string{
		`{"type":"user","timestamp":"2026-08-01T10:00:00.000Z","sessionId":"` + sessionID + `","cwd":"` + cwd +
			`","message":{"role":"user","content":[{"type":"text","text":"` + historySecret + ` from ` + sessionID + `"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-01T10:00:05.000Z","sessionId":"` + sessionID + `","cwd":"` + cwd +
			`","message":{"role":"assistant","content":[{"type":"text","text":"` + historySecret + ` reply in ` + sessionID + `"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// historyFixture stages two sibling project directories, one transcript in each,
// and a configuration that opts both in. Which of them a plan may see is then a
// question of the repository set the caller names.
type historyFixture struct {
	configPath  string
	home        string
	inPlan      string
	outOfPlan   string
	inPlanPath  string
	outOfPlanPa string
}

func stageHistoryFixture(t *testing.T) historyFixture {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	inPlan := filepath.Join(root, "app")
	outOfPlan := filepath.Join(root, "other")
	for _, dir := range []string{inPlan, outOfPlan} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	path := configPath(t)
	writeConfigFile(t, path, renderHistoryConfig(home,
		projectFixture{title: "App", directory: inPlan, optIn: "true"},
		projectFixture{title: "Other", directory: outOfPlan, optIn: "true"},
	))

	return historyFixture{
		configPath:  path,
		home:        home,
		inPlan:      inPlan,
		outOfPlan:   outOfPlan,
		inPlanPath:  writeClaudeTranscript(t, home, "-app", "app-session", inPlan),
		outOfPlanPa: writeClaudeTranscript(t, home, "-other", "other-session", outOfPlan),
	}
}

func findHistoryConversation(data historyData, id string) (conversationView, bool) {
	for _, conversation := range data.Conversations {
		if conversation.ID == id {
			return conversation, true
		}
	}
	return conversationView{}, false
}

func findHistoryWithheld(data historyData, id string) (withheldView, bool) {
	for _, entry := range data.Withheld {
		if entry.ID == id {
			return entry, true
		}
	}
	return withheldView{}, false
}

func TestHistoryReturnsBoundedEvidenceForAuthorizedConversations(t *testing.T) {
	fixture := stageHistoryFixture(t)

	stdout, stderr, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.inPlan)
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	data := decodeHistory(t, stdout).Data

	if data.Contract.Name != historyContractName || data.Contract.Version != historyContractVersion {
		t.Errorf("contract = %s/%d, want %s/%d", data.Contract.Name, data.Contract.Version, historyContractName, historyContractVersion)
	}
	if data.Access != historyAccess {
		t.Errorf("access = %q, want %q", data.Access, historyAccess)
	}
	if !data.Allowed {
		t.Fatalf("plan was refused: %s", data.Summary)
	}
	if data.ConfigFingerprint == "" || !strings.HasPrefix(data.RequestDigest, "sha256:") {
		t.Errorf("answer is missing its evidence fields: fingerprint=%q digest=%q", data.ConfigFingerprint, data.RequestDigest)
	}

	conversation, ok := findHistoryConversation(data, "app-session")
	if !ok {
		t.Fatalf("authorized conversation missing: %+v", data.Conversations)
	}
	if conversation.Directory != fixture.inPlan {
		t.Errorf("directory = %q, want %q", conversation.Directory, fixture.inPlan)
	}
	if len(conversation.Projects) != 1 || conversation.Projects[0] != "App" {
		t.Errorf("projects = %v, want [App]", conversation.Projects)
	}
	if conversation.SessionClass == "" {
		t.Error("session_class is empty; the answer must classify the session")
	}
	if len(conversation.Excerpts) != 2 {
		t.Fatalf("excerpts = %d, want 2", len(conversation.Excerpts))
	}
	if conversation.Excerpts[0].Index != 0 || conversation.Excerpts[0].Role != "user" {
		t.Errorf("first excerpt = index %d role %q, want 0/user", conversation.Excerpts[0].Index, conversation.Excerpts[0].Role)
	}
	if conversation.Excerpts[1].Role != "assistant" || conversation.Excerpts[1].Timestamp == "" {
		t.Errorf("second excerpt = role %q timestamp %q, want assistant with a timestamp",
			conversation.Excerpts[1].Role, conversation.Excerpts[1].Timestamp)
	}
	if !strings.Contains(conversation.Excerpts[0].Text, "app-session") {
		t.Errorf("excerpt text = %q, want the authorized conversation's message", conversation.Excerpts[0].Text)
	}

	// The other project is enrolled, but this plan does not participate in it.
	// It is reported as discovered-and-not-read, and its content is absent.
	withheld, ok := findHistoryWithheld(data, "other-session")
	if !ok {
		t.Fatalf("out-of-plan conversation missing from withheld: %+v", data.Withheld)
	}
	if withheld.Reason != reasonOutOfScope {
		t.Errorf("withheld reason = %q, want %q", withheld.Reason, reasonOutOfScope)
	}
	if strings.Contains(stdout, "other-session's") || strings.Contains(stdout, fixture.outOfPlan) {
		t.Errorf("answer names the out-of-plan project: %s", stdout)
	}
	if strings.Count(stdout, historySecret) != 2 {
		t.Errorf("expected exactly the two authorized excerpts to carry message text: %s", stdout)
	}
	if data.Examined != 2 {
		t.Errorf("examined = %d, want 2", data.Examined)
	}
}

// writeClassifiedClaudeTranscript writes one transcript whose user record
// carries the marker that decides its class, so a payload assertion can name the
// class it expects rather than reading whatever the default fixture happens to
// produce. The markers are the ones observeClaudeRecord acts on.
func writeClassifiedClaudeTranscript(t *testing.T, home, encodedDir, sessionID, cwd, markers string) string {
	t.Helper()
	path := filepath.Join(home, "projects", encodedDir, sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := `{"type":"user","timestamp":"2026-08-01T10:00:00.000Z","sessionId":"` + sessionID + `","cwd":"` + cwd +
		`",` + markers + `"message":{"role":"user","content":[{"type":"text","text":"` + historySecret + ` from ` + sessionID + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// session_class is the field ASL's overseer reads to decide whether a user turn
// can carry a human authorization, and it reads it fail-closed: only "main"
// yields an instruction, "automated" yields a generated prompt, and anything
// else — including a spelling a future amx might introduce — authorizes nothing.
// That makes the closed vocabulary a cross-repository contract rather than a
// display detail, so the answer is asserted to stay inside it, and to still
// distinguish the automated conversation from the human one it is the whole
// point of the field to separate.
func TestHistoryClassifiesEveryConversationWithinThePublishedVocabulary(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	app := filepath.Join(root, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeClassifiedClaudeTranscript(t, home, "-app", "typed-session", app, `"entrypoint":"cli",`)
	writeClassifiedClaudeTranscript(t, home, "-app", "sidechain-session", app, `"entrypoint":"cli","isSidechain":true,`)
	writeClaudeTranscript(t, home, "-app", "unmarked-session", app)
	path := configPath(t)
	writeConfigFile(t, path, renderHistoryConfig(home, projectFixture{title: "App", directory: app, optIn: "true"}))

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+app)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	data := decodeHistory(t, stdout).Data

	published := map[string]bool{
		string(transcript.SessionClassMain):      true,
		string(transcript.SessionClassAutomated): true,
		string(transcript.SessionClassUnknown):   true,
	}
	if len(data.Conversations) != 3 {
		t.Fatalf("conversations = %d, want 3: %+v", len(data.Conversations), data.Conversations)
	}
	for _, conversation := range data.Conversations {
		if !published[conversation.SessionClass] {
			t.Errorf("session_class = %q for %s, which is outside the published vocabulary a caller can act on",
				conversation.SessionClass, conversation.ID)
		}
	}

	want := map[string]transcript.SessionClass{
		"typed-session":     transcript.SessionClassMain,
		"sidechain-session": transcript.SessionClassAutomated,
		"unmarked-session":  transcript.SessionClassUnknown,
	}
	for id, class := range want {
		conversation, ok := findHistoryConversation(data, id)
		if !ok {
			t.Fatalf("conversation %s missing from the answer", id)
		}
		if conversation.SessionClass != string(class) {
			t.Errorf("session_class for %s = %q, want %q", id, conversation.SessionClass, class)
		}
	}
}

// A plan-level refusal ends the command before the corpus is walked, even for
// identity. Nothing about the conversations is discovered, because the plan that
// would have justified looking at them was not authorized.
func TestHistoryPlanRefusalPerformsNoDiscovery(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	app := filepath.Join(root, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeClaudeTranscript(t, home, "-app", "app-session", app)
	path := configPath(t)
	writeConfigFile(t, path, renderHistoryConfig(home, projectFixture{title: "App", directory: app, optIn: "false"}))

	original := historyRun
	t.Cleanup(func() { historyRun = original })
	historyRun = func(*config.Config, map[string]transcript.Reader, transcript.Authorizer, transcript.HistoryRequest) (transcript.HistoryResult, error) {
		t.Error("the corpus was walked after a plan-level refusal")
		return transcript.HistoryResult{}, nil
	}

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+app)
	requireQuietExit(t, err)

	data := decodeHistory(t, stdout).Data
	if data.Allowed || data.Reason != "declined" {
		t.Fatalf("decision = allowed:%t reason:%s, want a declined refusal", data.Allowed, data.Reason)
	}
	if len(data.Conversations) != 0 || data.Examined != 0 {
		t.Fatalf("refused plan returned %d conversations and examined %d", len(data.Conversations), data.Examined)
	}
	if strings.Contains(stdout, historySecret) {
		t.Fatalf("refused plan returned message content: %s", stdout)
	}
}

// The transcript metadata cache is populated by a refresh that reads every
// message body and writes the text into a search sidecar. A history request must
// not be able to serve an excluded conversation from it, so the excluded text
// stays absent from the answer even when the cache is holding it.
func TestHistoryDoesNotServeConversationsCachedByAnIndexRefresh(t *testing.T) {
	fixture := stageHistoryFixture(t)

	cfg, err := cli.LoadRunnableConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	index, err := transcript.LoadIndexAt(filepath.Join(t.TempDir(), "transcripts.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	if err := index.Refresh(cfg, cli.TranscriptReaders()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	cached := false
	for _, meta := range index.List(transcript.ListFilter{}) {
		if meta.Path == fixture.outOfPlanPa && strings.Contains(meta.SearchText+meta.Snippet, historySecret) {
			cached = true
		}
	}
	if !cached {
		t.Fatal("the refresh did not cache the out-of-plan conversation's text; the premise of this test is gone")
	}

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.inPlan)
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	data := decodeHistory(t, stdout).Data
	if _, ok := findHistoryConversation(data, "other-session"); ok {
		t.Fatal("history returned the out-of-plan conversation the cache was holding")
	}
	if strings.Contains(stdout, "reply in other-session") || strings.Contains(stdout, "from other-session") {
		t.Fatalf("history answer carries cached out-of-plan message text: %s", stdout)
	}
}

// Configuration is read on every invocation, so withdrawing an opt-in stops the
// next request rather than the next process.
func TestHistoryRevocationTakesEffectOnTheNextRequest(t *testing.T) {
	fixture := stageHistoryFixture(t)

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.inPlan)
	if err != nil {
		t.Fatalf("first history returned %v\n%s", err, stdout)
	}
	if _, ok := findHistoryConversation(decodeHistory(t, stdout).Data, "app-session"); !ok {
		t.Fatalf("first request did not return the authorized conversation:\n%s", stdout)
	}

	writeConfigFile(t, fixture.configPath, renderHistoryConfig(fixture.home,
		projectFixture{title: "App", directory: fixture.inPlan, optIn: "false"},
		projectFixture{title: "Other", directory: fixture.outOfPlan, optIn: "true"},
	))

	stdout, _, err = executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.inPlan)
	requireQuietExit(t, err)
	data := decodeHistory(t, stdout).Data
	if data.Allowed {
		t.Fatal("the revoked plan was still authorized in the same process")
	}
	if len(data.Conversations) != 0 || strings.Contains(stdout, historySecret) {
		t.Fatalf("the revoked request still returned conversation content: %s", stdout)
	}
}

// A conversation whose identity names two working directories is unavailable
// even when both are inside the authorized repository, because which of its
// messages belong to which project is a question only its content could answer.
func TestHistoryWithholdsMixedProjectConversations(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	app := filepath.Join(root, "app")
	nested := filepath.Join(app, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(home, "projects", "-mixed", "mixed-session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := strings.Join([]string{
		`{"type":"user","timestamp":"2026-08-01T10:00:00.000Z","sessionId":"mixed-session","cwd":"` + app +
			`","message":{"role":"user","content":[{"type":"text","text":"` + historySecret + ` one"}]}}`,
		`{"type":"user","timestamp":"2026-08-01T11:00:00.000Z","sessionId":"mixed-session","cwd":"` + nested +
			`","message":{"role":"user","content":[{"type":"text","text":"` + historySecret + ` two"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	configFile := configPath(t)
	writeConfigFile(t, configFile, renderHistoryConfig(home, projectFixture{title: "App", directory: app, optIn: "true"}))

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+app)
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	data := decodeHistory(t, stdout).Data
	withheld, ok := findHistoryWithheld(data, "mixed-session")
	if !ok {
		t.Fatalf("mixed-project conversation missing from withheld: %+v", data.Withheld)
	}
	if withheld.Reason != transcript.AdmissionAmbiguous {
		t.Errorf("withheld reason = %q, want %q", withheld.Reason, transcript.AdmissionAmbiguous)
	}
	if len(data.Conversations) != 0 || strings.Contains(stdout, historySecret) {
		t.Fatalf("a mixed-project conversation was returned: %s", stdout)
	}
}

// A withheld entry names the conversation and the reason and nothing that
// locates it.
func TestHistoryWithheldEntriesRevealNoLocation(t *testing.T) {
	fixture := stageHistoryFixture(t)

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.inPlan)
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	encoded, err := json.Marshal(decodeHistory(t, stdout).Data.Withheld)
	if err != nil {
		t.Fatalf("marshal withheld: %v", err)
	}
	for _, leak := range []string{fixture.outOfPlan, fixture.outOfPlanPa, "Other", historySecret} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("withheld payload leaks %q: %s", leak, encoded)
		}
	}
}

func TestHistoryReportsAppliedBoundsAndBindsThemIntoTheDigest(t *testing.T) {
	fixture := stageHistoryFixture(t)

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.inPlan,
		"--limit", "1", "--messages", "1", "--max-chars", "8")
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	bounded := decodeHistory(t, stdout).Data
	if bounded.Limits != (limitsView{Conversations: 1, Messages: 1, ExcerptChars: 8}) {
		t.Fatalf("limits = %+v, want the requested bounds", bounded.Limits)
	}
	conversation, ok := findHistoryConversation(bounded, "app-session")
	if !ok {
		t.Fatalf("authorized conversation missing: %+v", bounded.Conversations)
	}
	if !conversation.Truncated || len(conversation.Excerpts) != 1 {
		t.Fatalf("conversation = truncated:%t excerpts:%d, want a truncated single excerpt",
			conversation.Truncated, len(conversation.Excerpts))
	}
	// The tail is what an incident is about, and the excerpt still says where it
	// came from.
	if conversation.Excerpts[0].Index != 1 || conversation.Excerpts[0].Role != "assistant" {
		t.Errorf("excerpt = index %d role %q, want the last message", conversation.Excerpts[0].Index, conversation.Excerpts[0].Role)
	}
	if len([]rune(conversation.Excerpts[0].Text)) != 8 || !conversation.Excerpts[0].Truncated {
		t.Errorf("excerpt text = %q (truncated=%v), want 8 characters and truncated",
			conversation.Excerpts[0].Text, conversation.Excerpts[0].Truncated)
	}

	stdout, _, err = executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.inPlan)
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	unbounded := decodeHistory(t, stdout).Data
	if unbounded.Limits.Conversations != transcript.DefaultHistoryConversations ||
		unbounded.Limits.Messages != transcript.DefaultHistoryMessages ||
		unbounded.Limits.ExcerptChars != transcript.DefaultHistoryExcerptChars {
		t.Fatalf("default limits = %+v, want the package defaults", unbounded.Limits)
	}
	// An answer taken under one bound is not evidence for a request that asked
	// for another, and the digest is what makes that checkable.
	if bounded.RequestDigest == unbounded.RequestDigest {
		t.Fatal("the request digest does not change with the bounds it was taken under")
	}
}

func TestHistoryClampsOversizedBounds(t *testing.T) {
	fixture := stageHistoryFixture(t)

	stdout, _, err := executeHistory(t, "--json", "--repository", "app="+fixture.inPlan,
		"--limit", "100000", "--messages", "100000", "--max-chars", "100000")
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	limits := decodeHistory(t, stdout).Data.Limits
	if limits != (limitsView{
		Conversations: transcript.MaxHistoryConversations,
		Messages:      transcript.MaxHistoryMessages,
		ExcerptChars:  transcript.MaxHistoryExcerptChars,
	}) {
		t.Fatalf("limits = %+v, want the package ceilings", limits)
	}
}

func TestHistoryNarrowsByHarnessAndSince(t *testing.T) {
	fixture := stageHistoryFixture(t)

	stdout, _, err := executeHistory(t, "--json", "--repository", "app="+fixture.inPlan, "--harness", "codex")
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	data := decodeHistory(t, stdout).Data
	if len(data.Conversations) != 0 || data.Examined != 0 {
		t.Fatalf("harness narrowing returned %d conversations", len(data.Conversations))
	}
	if len(data.Harnesses) != 1 || data.Harnesses[0] != "codex" {
		t.Errorf("harnesses = %v, want [codex]", data.Harnesses)
	}

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	stdout, _, err = executeHistory(t, "--json", "--repository", "app="+fixture.inPlan, "--since", future)
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	data = decodeHistory(t, stdout).Data
	if data.Examined != 0 {
		t.Fatalf("since narrowing examined %d conversations", data.Examined)
	}
	if data.Since != future {
		t.Errorf("since = %q, want %q", data.Since, future)
	}
	if strings.Contains(stdout, historySecret) {
		t.Fatalf("a narrowed request returned message content: %s", stdout)
	}
}

// A request this contract version cannot answer is a structured error rather
// than a decision, because there is no decision to report.
func TestHistoryRejectsUnanswerableRequests(t *testing.T) {
	fixture := stageHistoryFixture(t)

	cases := []struct {
		name string
		args []string
		code string
	}{
		{"contract version", []string{"--contract-version", "99"}, "unsupported_contract_version"},
		{"relative repository", []string{"--repository", "app=relative/path"}, "invalid_repository"},
		{"malformed since", []string{"--repository", "app=" + fixture.inPlan, "--since", "yesterday"}, "invalid_since"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, _, err := executeHistory(t, append([]string{"--json"}, tc.args...)...)
			if err == nil {
				t.Fatalf("history accepted an unanswerable request:\n%s", stdout)
			}
			if failure := decodeFailure(t, stdout); failure.Code != tc.code {
				t.Fatalf("error code = %q, want %q", failure.Code, tc.code)
			}
		})
	}
}

// Text mode carries the same answer a script would get, so an operator
// diagnosing a refusal is not told less.
func TestHistoryTextModeCarriesTheWholeAnswer(t *testing.T) {
	fixture := stageHistoryFixture(t)

	stdout, _, err := executeHistory(t, "--plan", "plan-1", "--repository", "app="+fixture.inPlan)
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	for _, want := range []string{
		"contract: " + historyContractName,
		"access: " + historyAccess,
		"decision: allowed",
		"config_fingerprint: ",
		"request_digest: sha256:",
		"limits: conversations=",
		"conversation\tclaude\tapp-session",
		"withheld\tclaude\tother-session\t" + reasonOutOfScope,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("text output is missing %q:\n%s", want, stdout)
		}
	}
}

// controlledReader is the corpus seam the mid-request revocation tests share.
//
// It enumerates a fixed candidate list without opening anything, and it records
// every path whose messages were read, in order. That is what lets those tests
// assert on a call that did not happen rather than on content that did not
// appear: a conversation absent from the answer proves only that it was not
// returned, while a conversation absent from this list was never opened.
type controlledReader struct {
	candidates []transcript.Candidate
	// onRead runs after a read has been served. It is where a test moves the
	// operator's configuration, so the change lands between one conversation and
	// the next inside a single request.
	onRead func()
	reads  []string
}

func (r *controlledReader) Harness() string { return "claude" }

// Discover is the content-reading path this boundary exists to precede. History
// must never reach it.
func (r *controlledReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	return nil, errors.New("history reached the content-reading discovery path")
}

func (r *controlledReader) DiscoverMetadata(string, transcript.MetadataOptions) ([]transcript.Candidate, error) {
	return append([]transcript.Candidate(nil), r.candidates...), nil
}

func (r *controlledReader) ReadMessages(path string) ([]transcript.Message, error) {
	r.reads = append(r.reads, path)
	if r.onRead != nil {
		r.onRead()
	}
	return []transcript.Message{{
		Role:      "user",
		Text:      historySecret + " from " + filepath.Base(path),
		Timestamp: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
	}}, nil
}

// controlledHistory is two conversations in one enrolled repository, behind a
// corpus seam, with the operator's real configuration file staged so a test can
// rewrite it while the request is in flight.
type controlledHistory struct {
	reader     *controlledReader
	configPath string
	home       string
	app        string
	firstPath  string
	secondPath string
}

func stageControlledHistory(t *testing.T) *controlledHistory {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	app := filepath.Join(root, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", app, err)
	}

	path := configPath(t)
	writeConfigFile(t, path, renderHistoryConfig(home, projectFixture{title: "App", directory: app, optIn: "true"}))

	fixture := &controlledHistory{
		configPath: path,
		home:       home,
		app:        app,
		firstPath:  filepath.Join(home, "first-session.jsonl"),
		secondPath: filepath.Join(home, "second-session.jsonl"),
	}
	// The modification times order the walk, newest first, so which conversation
	// is opened before the configuration moves is fixed rather than incidental.
	fixture.reader = &controlledReader{candidates: []transcript.Candidate{
		{ID: "first-session", Path: fixture.firstPath, Directories: []string{app}, FileModTime: time.Unix(2_000, 0)},
		{ID: "second-session", Path: fixture.secondPath, Directories: []string{app}, FileModTime: time.Unix(1_000, 0)},
	}}

	original := historyReaders
	t.Cleanup(func() { historyReaders = original })
	historyReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{"claude": fixture.reader}
	}
	return fixture
}

// Enrollment is rechecked before every conversation is opened, against the
// configuration that exists at that moment. Withdrawing a participating
// project's opt-in after one conversation has been read therefore stops the
// next one inside the same request, rather than only stopping the next request.
func TestHistoryRechecksEnrollmentBetweenConversations(t *testing.T) {
	fixture := stageControlledHistory(t)
	fixture.reader.onRead = func() {
		// The operator withdraws the opt-in between the first conversation and
		// the second, through the real configuration file the command reads.
		writeConfigFile(t, fixture.configPath, renderHistoryConfig(fixture.home,
			projectFixture{title: "App", directory: fixture.app, optIn: "false"}))
	}

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.app)
	requireQuietExit(t, err)

	if want := []string{fixture.firstPath}; !slices.Equal(fixture.reader.reads, want) {
		t.Fatalf("conversations opened = %v, want %v; the withdrawal did not stop the request", fixture.reader.reads, want)
	}

	data := decodeHistory(t, stdout).Data
	if data.Allowed {
		t.Fatal("the request stayed authorized after the opt-in was withdrawn")
	}
	if data.Reason != string(overseerpolicy.ReasonDeclined) {
		t.Errorf("reason = %q, want %q; the refusal was not decided against the configuration that exists now",
			data.Reason, overseerpolicy.ReasonDeclined)
	}
	if len(data.Conversations) != 0 {
		t.Errorf("a refused request returned %d conversations", len(data.Conversations))
	}
	if strings.Contains(stdout, historySecret) {
		t.Fatalf("evidence read before the withdrawal was returned beside the refusal: %s", stdout)
	}
	if data.Examined != 2 {
		t.Errorf("examined = %d, want 2; a stopped request must not read as an empty corpus", data.Examined)
	}
	if data.ConfigFingerprint == "" || !strings.HasPrefix(data.RequestDigest, "sha256:") {
		t.Errorf("refusal is missing its evidence fields: fingerprint=%q digest=%q", data.ConfigFingerprint, data.RequestDigest)
	}
}

// An answer that cannot be obtained is not an answer of yes. Configuration that
// becomes unreadable partway through a request stops the remaining conversations
// and fails the request, rather than letting the evaluator the invocation opened
// with stand in for the answer nobody can currently give.
func TestHistoryConfigFailureBetweenConversationsFailsClosed(t *testing.T) {
	fixture := stageControlledHistory(t)
	fixture.reader.onRead = func() {
		writeConfigFile(t, fixture.configPath, "this is not = = valid toml\n")
	}

	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+fixture.app)
	requireQuietExit(t, err)

	if want := []string{fixture.firstPath}; !slices.Equal(fixture.reader.reads, want) {
		t.Fatalf("conversations opened = %v, want %v; an unreadable configuration did not stop the request",
			fixture.reader.reads, want)
	}
	if strings.Contains(stdout, historySecret) {
		t.Fatalf("evidence read before the failure was returned: %s", stdout)
	}
	failure := decodeFailure(t, stdout)
	if failure.Code != "config_invalid" {
		t.Errorf("error code = %q, want config_invalid", failure.Code)
	}
}

func TestHistoryScopeContainmentRespectsPathBoundaries(t *testing.T) {
	if withinScope("/srv/api-internal", []string{"/srv/api"}) {
		t.Error("/srv/api-internal was treated as inside /srv/api")
	}
	if !withinScope("/srv/api", []string{"/srv/api"}) {
		t.Error("a repository root is not inside itself")
	}
	if !withinScope(filepath.Join("/srv", "api", "cmd"), []string{"/srv/api"}) {
		t.Error("a subdirectory was treated as outside its repository")
	}
	if withinScope("/srv/api", nil) {
		t.Error("an empty scope admitted a directory")
	}
}
