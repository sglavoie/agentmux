package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	searchsvc "github.com/sglavoie/agentmux/internal/search"
)

func resetSearchCommandTestState(t *testing.T) {
	t.Helper()
	oldLoadConfig := searchCommandLoadConfig
	oldOpenStore := searchCommandOpenStore
	oldSync := searchCommandSync
	t.Cleanup(func() {
		searchCommandLoadConfig = oldLoadConfig
		searchCommandOpenStore = oldOpenStore
		searchCommandSync = oldSync
	})
}

func TestSearchCommandJSONUsesSchemaV1AndFilters(t *testing.T) {
	resetSearchCommandTestState(t)
	store := datastore.OpenTest(t)
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC).UnixNano()
	if _, err := store.DB().ExecContext(context.Background(), `
		INSERT INTO search_documents (
			entity_type, entity_id, harness, profile_name, occurred_at_ns,
			title, normalized_content, updated_at_ns
		) VALUES
			('conversation', 'codex:one', 'codex', 'work', ?, 'One', 'unified search needle', ?),
			('plan', 'plan-two', NULL, NULL, ?, 'Two', 'unified search needle', ?)
	`, now, now, now, now); err != nil {
		t.Fatalf("insert documents: %v", err)
	}
	searchCommandLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	searchCommandOpenStore = func(context.Context) (*datastore.Store, error) { return store, nil }
	searchCommandSync = func(context.Context, *datastore.Store, *config.Config) error { return nil }
	command := newSearchCommand()
	for name, value := range map[string]string{
		"json": "true", "type": "conversation", "harness": "codex", "profile": "work",
		"from": "2026-07-10", "to": "2026-07-10", "limit": "10",
	} {
		if err := command.Flags().Set(name, value); err != nil {
			t.Fatalf("set %s: %v", name, err)
		}
	}
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.RunE(command, []string{"unified", "search"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			Query   string             `json:"query"`
			Results []searchsvc.Result `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != JSONSchemaVersion || payload.Data.Query != "unified search" {
		t.Fatalf("payload = %+v", payload)
	}
	if len(payload.Data.Results) != 1 || payload.Data.Results[0].EntityID != "codex:one" || payload.Data.Results[0].PrimaryAction.ID != "session.continue" {
		t.Fatalf("results = %#v", payload.Data.Results)
	}
}

func TestSearchCommandHumanOutputAndStructuredMissingQuery(t *testing.T) {
	resetSearchCommandTestState(t)
	store := datastore.OpenTest(t)
	now := time.Now().UnixNano()
	if _, err := store.DB().Exec(`
		INSERT INTO search_documents (entity_type, entity_id, occurred_at_ns, title, normalized_content, updated_at_ns)
		VALUES ('activity', '42', ?, 'Build finished', 'release needle', ?)
	`, now, now); err != nil {
		t.Fatal(err)
	}
	searchCommandLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	searchCommandOpenStore = func(context.Context) (*datastore.Store, error) { return store, nil }
	searchCommandSync = func(context.Context, *datastore.Store, *config.Config) error { return nil }
	command := newSearchCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.RunE(command, []string{"release", "needle"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("activity\t42")) || !bytes.Contains(out.Bytes(), []byte("activity.open")) {
		t.Fatalf("human output = %q", out.String())
	}

	if err := command.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := command.RunE(command, nil)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet {
		t.Fatalf("error = %v, want quiet ExitError", err)
	}
	var failure JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &failure); err != nil {
		t.Fatalf("failure JSON: %v", err)
	}
	if failure.Error.Code != "missing_parameter" || failure.Error.Parameters[0].Name != "query" {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestSearchCommandRejectsInvalidFilters(t *testing.T) {
	resetSearchCommandTestState(t)
	command := newSearchCommand()
	if err := command.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("type", "unknown"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	command.SetOut(&out)
	err := command.RunE(command, []string{"needle"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %v", err)
	}
	var failure JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &failure); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if failure.Error.Code != "invalid_search" {
		t.Fatalf("failure = %+v", failure)
	}
}
