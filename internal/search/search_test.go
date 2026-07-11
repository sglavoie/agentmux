package search

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/datastore"
)

func TestCompileQuotesEveryTokenAndDoesNotExposeOperators(t *testing.T) {
	got, err := Compile(`alpha OR beta "quoted"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := `"alpha" AND "OR" AND "beta" AND """quoted"""`
	if got != want {
		t.Fatalf("Compile = %q, want %q", got, want)
	}
	if _, err := Compile(" \t\n "); err == nil {
		t.Fatal("Compile(empty) unexpectedly succeeded")
	}
}

func TestCompileStripsNULAndOtherControlRunes(t *testing.T) {
	got, err := Compile("alpha\x00 beta\x1b gamma\u009b delta")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if want := `"alpha" AND "beta" AND "gamma" AND "delta"`; got != want {
		t.Fatalf("Compile = %q, want %q", got, want)
	}
	if strings.ContainsFunc(got, unicode.IsControl) {
		t.Fatalf("Compile retained control runes: %q", got)
	}
}

func TestSearchReturnsAllEntityTypesWithStableMetadata(t *testing.T) {
	store := datastore.OpenTest(t)
	ctx := context.Background()
	insertSearchFixtures(t, store)
	service, err := New(store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	results, err := service.Search(ctx, Query{Text: "shared needle", Limit: 20})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("results = %d, want 5: %#v", len(results), results)
	}
	seen := make(map[EntityType]Result, len(results))
	for _, result := range results {
		seen[result.EntityType] = result
		if result.EntityID == "" || result.PrimaryAction.ID == "" {
			t.Fatalf("result lacks stable identity/action: %#v", result)
		}
		if result.Match.Expression != `"shared" AND "needle"` {
			t.Fatalf("expression = %q", result.Match.Expression)
		}
		if strings.ContainsAny(result.Snippet, markStart+markEnd) {
			t.Fatalf("snippet leaked internal markers: %q", result.Snippet)
		}
	}
	for _, entityType := range EntityTypes() {
		if _, ok := seen[entityType]; !ok {
			t.Errorf("missing %s result", entityType)
		}
	}
	if seen[EntityPlan].Target != "/plans/one" {
		t.Errorf("plan target = %q", seen[EntityPlan].Target)
	}
	if seen[EntityHandoff].Target != "/handoffs/one.md" {
		t.Errorf("handoff target = %q", seen[EntityHandoff].Target)
	}

	again, err := service.Search(ctx, Query{Text: "shared needle", Limit: 20})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	for i := range results {
		if results[i].EntityType != again[i].EntityType || results[i].EntityID != again[i].EntityID {
			t.Fatalf("order changed at %d: %#v vs %#v", i, results[i], again[i])
		}
	}
}

func TestSearchQuotedANDUnicodeAndFilters(t *testing.T) {
	store := datastore.OpenTest(t)
	ctx := context.Background()
	insertSearchFixtures(t, store)
	service, _ := New(store)

	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO search_documents (
			entity_type, entity_id, workspace_id, harness, profile_name,
			occurred_at_ns, title, normalized_content, updated_at_ns
		) VALUES ('conversation', 'codex:unicode', 'ws-a', 'codex', 'work', ?, 'Café 東京', 'naïve résumé 東京', ?)
	`, time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC).UnixNano(), time.Now().UnixNano()); err != nil {
		t.Fatalf("insert unicode document: %v", err)
	}

	unicodeResults, err := service.Search(ctx, Query{Text: "cafe 東京"})
	if err != nil {
		t.Fatalf("unicode Search: %v", err)
	}
	if len(unicodeResults) != 1 || unicodeResults[0].EntityID != "codex:unicode" || !unicodeResults[0].Match.TitleMatched {
		t.Fatalf("unicode results = %#v", unicodeResults)
	}

	// OR is a quoted literal term, so this does not become an FTS union.
	operatorResults, err := service.Search(ctx, Query{Text: "shared OR needle"})
	if err != nil {
		t.Fatalf("operator Search: %v", err)
	}
	if len(operatorResults) != 0 {
		t.Fatalf("operator input returned %d results, want 0", len(operatorResults))
	}
	for _, punctuation := range []string{`"`, `*`, `-`, `NEAR(shared needle)`} {
		if _, err := service.Search(ctx, Query{Text: punctuation}); err != nil {
			t.Errorf("literal punctuation query %q failed: %v", punctuation, err)
		}
	}

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	filtered, err := service.Search(ctx, Query{
		Text: "shared needle", Types: []EntityType{EntityConversation},
		Workspaces: []string{"ws-a"}, Harnesses: []string{"CODEX"},
		Profiles: []string{"SHARED-PROFILE"}, From: from, To: to,
	})
	if err != nil {
		t.Fatalf("filtered Search: %v", err)
	}
	if len(filtered) != 1 || filtered[0].EntityID != "codex:conv-1" {
		t.Fatalf("filtered results = %#v", filtered)
	}

	plans, err := service.Search(ctx, Query{
		Text: "shared needle", Types: []EntityType{EntityPlan}, Workspaces: []string{"ws-a"},
	})
	if err != nil {
		t.Fatalf("linked plan Search: %v", err)
	}
	if len(plans) != 1 || plans[0].EntityID != "plan-1" {
		t.Fatalf("linked plan results = %#v", plans)
	}
}

func TestSearchNULAndControlRunesAreLiteralSafe(t *testing.T) {
	store := datastore.OpenTest(t)
	ctx := context.Background()
	insertSearchFixtures(t, store)
	service, err := New(store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	results, err := service.Search(ctx, Query{Text: "shared\x00 needle\x1b"})
	if err != nil {
		t.Fatalf("Search with NUL/control query: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("results = %d, want 5: %#v", len(results), results)
	}
	if got, want := results[0].Match.Expression, `"shared" AND "needle"`; got != want {
		t.Fatalf("expression = %q, want %q", got, want)
	}
}

func insertSearchFixtures(t *testing.T, store *datastore.Store) {
	t.Helper()
	ctx := context.Background()
	when := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC).UnixNano()
	now := time.Now().UnixNano()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO workspaces (id, canonical_path, display_name, created_at_ns, updated_at_ns) VALUES ('ws-a', '/repo/a', 'Alpha', ?, ?)`, []any{now, now}},
		{`INSERT INTO conversations (harness, session_id, workspace_id, display_profile, source_path, last_at_ns, created_at_ns, updated_at_ns) VALUES ('codex', 'conv-1', 'ws-a', 'work', '/transcripts/one.jsonl', ?, ?, ?)`, []any{when, now, now}},
		{`INSERT INTO profile_memberships (conversation_id, profile_name, position, created_at_ns) SELECT id, 'shared-profile', 0, ? FROM conversations WHERE session_id = 'conv-1'`, []any{now}},
		{`INSERT INTO plans (id, path, display_name, mod_time_ns, created_at_ns, updated_at_ns) VALUES ('plan-1', '/plans/one', 'Plan One', ?, ?, ?)`, []any{when, now, now}},
		{`INSERT INTO plan_workspace_links (plan_id, workspace_id, created_at_ns) VALUES ('plan-1', 'ws-a', ?)`, []any{now}},
		{`INSERT INTO handoffs (id, workspace_id, path, content_hash, status, created_at_ns, updated_at_ns) VALUES ('handoff-1', 'ws-a', '/handoffs/one.md', 'hash', 'ready', ?, ?)`, []any{now, now}},
		{`INSERT INTO activity_items (id, workspace_id, kind, summary, occurred_at_ns, created_at_ns) VALUES (1, 'ws-a', 'test', 'Shared needle activity', ?, ?)`, []any{when, now}},
	}
	for _, statement := range statements {
		if _, err := store.DB().ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("fixture statement: %v", err)
		}
	}
	documents := []struct {
		entityType, entityID, workspaceID, harness, profile, title, content string
	}{
		{"project", "ws-a", "ws-a", "codex", "work", "Shared Needle Project", "shared needle project content"},
		{"conversation", "codex:conv-1", "", "codex", "work", "Conversation", "shared needle conversation content"},
		{"plan", "plan-1", "", "", "", "Plan One", "shared needle plan content"},
		{"handoff", "handoff-1", "ws-a", "", "", "Handoff One", "shared needle handoff content"},
		{"activity", "1", "ws-a", "", "", "Activity One", "shared needle activity content"},
	}
	for _, document := range documents {
		if _, err := store.DB().ExecContext(ctx, `
			INSERT INTO search_documents (
				entity_type, entity_id, workspace_id, harness, profile_name,
				occurred_at_ns, title, normalized_content, updated_at_ns
			) VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)
		`, document.entityType, document.entityID, document.workspaceID, document.harness, document.profile,
			when, document.title, document.content, now); err != nil {
			t.Fatalf("insert %s document: %v", document.entityType, err)
		}
	}
}
