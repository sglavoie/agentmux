// Package search provides one typed full-text search surface over Agent Mux's
// durable cross-entity search documents.
package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/datastore"
)

// EntityType is a stable search result discriminator.
type EntityType string

const (
	EntityProject      EntityType = "project"
	EntityConversation EntityType = "conversation"
	EntityPlan         EntityType = "plan"
	EntityHandoff      EntityType = "handoff"
	EntityActivity     EntityType = "activity"
)

var entityTypes = []EntityType{
	EntityProject,
	EntityConversation,
	EntityPlan,
	EntityHandoff,
	EntityActivity,
}

// EntityTypes returns the supported types in deterministic display order.
func EntityTypes() []EntityType { return append([]EntityType(nil), entityTypes...) }

// ParseEntityType validates a public type filter.
func ParseEntityType(value string) (EntityType, error) {
	want := EntityType(strings.ToLower(strings.TrimSpace(value)))
	for _, entityType := range entityTypes {
		if want == entityType {
			return want, nil
		}
	}
	return "", fmt.Errorf("search: unknown entity type %q", value)
}

// Query contains user text and optional exact metadata filters. Multiple
// values within a filter are ORed; distinct filter categories are ANDed.
type Query struct {
	Text       string
	Types      []EntityType
	Workspaces []string
	Harnesses  []string
	Profiles   []string
	From       time.Time
	To         time.Time
	Limit      int
}

// Action describes the entity's stable primary navigation/action affordance.
type Action struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Match contains presentation-neutral FTS match metadata. Rank is SQLite
// FTS5's bm25 score: lower values are stronger matches.
type Match struct {
	Rank         float64 `json:"rank"`
	TitleMatched bool    `json:"title_matched"`
	Expression   string  `json:"expression"`
}

// Result is one stable cross-entity search hit.
type Result struct {
	EntityID      string     `json:"entity_id"`
	EntityType    EntityType `json:"entity_type"`
	WorkspaceID   string     `json:"workspace_id,omitempty"`
	Harness       string     `json:"harness,omitempty"`
	Profile       string     `json:"profile,omitempty"`
	Harnesses     []string   `json:"harnesses,omitempty"`
	Profiles      []string   `json:"profiles,omitempty"`
	OccurredAt    time.Time  `json:"occurred_at,omitzero"`
	Title         string     `json:"title"`
	Snippet       string     `json:"snippet"`
	Target        string     `json:"target,omitempty"`
	Match         Match      `json:"match"`
	PrimaryAction Action     `json:"primary_action"`
}

// Service searches an opened Agent Mux datastore.
type Service struct{ store *datastore.Store }

// New constructs a search service.
func New(store *datastore.Store) (*Service, error) {
	if store == nil || store.DB() == nil {
		return nil, errors.New("search: service requires an open datastore")
	}
	return &Service{store: store}, nil
}

const (
	defaultLimit = 50
	maxLimit     = 500
	markStart    = "\x1f"
	markEnd      = "\x1e"
)

// Compile converts normal user text to quoted AND terms. FTS operators in
// user input remain literal tokens rather than becoming executable syntax.
// Control runes are removed before building the expression because SQLite's
// FTS interface treats NUL as a string terminator.
func Compile(text string) (string, error) {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	terms := strings.Fields(text)
	if len(terms) == 0 {
		return "", errors.New("search: query text is required")
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " AND "), nil
}

// Search runs a deterministic filtered FTS5 query.
func (s *Service) Search(ctx context.Context, query Query) ([]Result, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return nil, errors.New("search: nil service")
	}
	expression, err := Compile(query.Text)
	if err != nil {
		return nil, err
	}
	if err := validateQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	where := []string{"search_documents_fts MATCH ?"}
	args := []any{expression}
	types := query.Types
	if len(types) == 0 {
		types = entityTypes
	}
	appendInFilter(&where, &args, "d.entity_type", entityTypeStrings(types))
	appendWorkspaceFilter(&where, &args, query.Workspaces)
	appendHarnessFilter(&where, &args, query.Harnesses)
	appendProfileFilter(&where, &args, query.Profiles)
	if !query.From.IsZero() {
		where = append(where, "d.occurred_at_ns >= ?")
		args = append(args, query.From.UnixNano())
	}
	if !query.To.IsZero() {
		where = append(where, "d.occurred_at_ns <= ?")
		args = append(args, query.To.UnixNano())
	}
	args = append(args, limit)

	statement := `
		SELECT d.entity_type, d.entity_id, COALESCE(d.workspace_id, ''),
			COALESCE(d.harness, ''), COALESCE(d.profile_name, ''),
			d.occurred_at_ns, d.title,
			highlight(search_documents_fts, 0, ?, ?),
			snippet(search_documents_fts, 1, ?, ?, ' … ', 28),
			bm25(search_documents_fts, 6.0, 1.0),
			CASE d.entity_type
				WHEN 'plan' THEN COALESCE((SELECT p.path FROM plans p WHERE p.id = d.entity_id), d.entity_id)
				WHEN 'handoff' THEN COALESCE((SELECT h.path FROM handoffs h WHERE h.id = d.entity_id), d.entity_id)
				ELSE d.entity_id
			END
		FROM search_documents_fts
		JOIN search_documents d ON d.id = search_documents_fts.rowid
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY bm25(search_documents_fts, 6.0, 1.0),
			d.occurred_at_ns DESC, d.entity_type, d.entity_id
		LIMIT ?`
	queryArgs := append([]any{markStart, markEnd, markStart, markEnd}, args...)
	rows, err := s.store.DB().QueryContext(ctx, statement, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("search: query documents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	results := make([]Result, 0)
	for rows.Next() {
		var result Result
		var occurredAt int64
		var highlightedTitle, rawSnippet string
		if err := rows.Scan(
			&result.EntityType, &result.EntityID, &result.WorkspaceID,
			&result.Harness, &result.Profile, &occurredAt, &result.Title,
			&highlightedTitle, &rawSnippet, &result.Match.Rank, &result.Target,
		); err != nil {
			return nil, fmt.Errorf("search: scan result: %w", err)
		}
		if occurredAt != 0 {
			result.OccurredAt = time.Unix(0, occurredAt)
		}
		result.Match.Expression = expression
		result.Match.TitleMatched = strings.Contains(highlightedTitle, markStart)
		result.Snippet = typeAwareSnippet(result.EntityType, rawSnippet, result.Title)
		result.PrimaryAction = primaryAction(result.EntityType)
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: iterate results: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("search: close result rows: %w", err)
	}
	if err := populateProjectMemberships(ctx, s.store.DB(), results); err != nil {
		return nil, err
	}
	return results, nil
}

func validateQuery(query Query) error {
	if query.Limit < 0 || query.Limit > maxLimit {
		return fmt.Errorf("search: limit must be between 1 and %d", maxLimit)
	}
	if !query.From.IsZero() && !query.To.IsZero() && query.From.After(query.To) {
		return errors.New("search: from date must not be after to date")
	}
	for _, entityType := range query.Types {
		if _, err := ParseEntityType(string(entityType)); err != nil {
			return err
		}
	}
	return nil
}

func appendInFilter(where *[]string, args *[]any, column string, values []string) {
	if len(values) == 0 {
		return
	}
	*where = append(*where, column+" IN ("+placeholders(len(values))+")")
	for _, value := range values {
		*args = append(*args, value)
	}
}

func appendWorkspaceFilter(where *[]string, args *[]any, values []string) {
	values = cleanValues(values)
	if len(values) == 0 {
		return
	}
	ph := placeholders(len(values))
	*where = append(*where, `(d.workspace_id IN (`+ph+`)
		OR (d.entity_type = 'conversation' AND EXISTS (
			SELECT 1 FROM conversations c
			WHERE c.harness || ':' || c.session_id = d.entity_id
				AND c.workspace_id IN (`+ph+`)
		))
		OR (d.entity_type = 'plan' AND EXISTS (
			SELECT 1 FROM plan_workspace_links l
			WHERE l.plan_id = d.entity_id AND l.workspace_id IN (`+ph+`)
		)))`)
	for repeat := 0; repeat < 3; repeat++ {
		for _, value := range values {
			*args = append(*args, value)
		}
	}
}

func appendHarnessFilter(where *[]string, args *[]any, values []string) {
	values = lowerValues(values)
	if len(values) == 0 {
		return
	}
	ph := placeholders(len(values))
	*where = append(*where, `(lower(d.harness) IN (`+ph+`)
		OR (d.entity_type = 'project' AND EXISTS (
			SELECT 1 FROM workspace_harness_memberships whm
			WHERE whm.workspace_id = d.workspace_id
				AND lower(whm.harness) IN (`+ph+`)
		)))`)
	for repeat := 0; repeat < 2; repeat++ {
		for _, value := range values {
			*args = append(*args, value)
		}
	}
}

func appendProfileFilter(where *[]string, args *[]any, values []string) {
	values = lowerValues(values)
	if len(values) == 0 {
		return
	}
	ph := placeholders(len(values))
	*where = append(*where, `(lower(d.profile_name) IN (`+ph+`)
		OR (d.entity_type = 'conversation' AND EXISTS (
			SELECT 1 FROM conversations c
			JOIN profile_memberships pm ON pm.conversation_id = c.id
			WHERE c.harness || ':' || c.session_id = d.entity_id
				AND lower(pm.profile_name) IN (`+ph+`)
		))
		OR (d.entity_type = 'project' AND EXISTS (
			SELECT 1 FROM workspace_profile_memberships wpm
			WHERE wpm.workspace_id = d.workspace_id
				AND lower(wpm.profile_name) IN (`+ph+`)
		)))`)
	for repeat := 0; repeat < 3; repeat++ {
		for _, value := range values {
			*args = append(*args, value)
		}
	}
}

func populateProjectMemberships(ctx context.Context, db *sql.DB, results []Result) error {
	workspaceIDs := make([]string, 0)
	resultIndexes := make(map[string][]int)
	for i := range results {
		if results[i].EntityType != EntityProject || results[i].WorkspaceID == "" {
			continue
		}
		if _, ok := resultIndexes[results[i].WorkspaceID]; !ok {
			workspaceIDs = append(workspaceIDs, results[i].WorkspaceID)
		}
		resultIndexes[results[i].WorkspaceID] = append(resultIndexes[results[i].WorkspaceID], i)
	}
	if len(workspaceIDs) == 0 {
		return nil
	}

	profiles, err := loadWorkspaceMemberships(ctx, db,
		"workspace_profile_memberships", "profile_name", workspaceIDs)
	if err != nil {
		return fmt.Errorf("search: load project profile memberships: %w", err)
	}
	harnesses, err := loadWorkspaceMemberships(ctx, db,
		"workspace_harness_memberships", "harness", workspaceIDs)
	if err != nil {
		return fmt.Errorf("search: load project harness memberships: %w", err)
	}
	for workspaceID, indexes := range resultIndexes {
		for _, index := range indexes {
			results[index].Profiles = append([]string(nil), profiles[workspaceID]...)
			results[index].Harnesses = append([]string(nil), harnesses[workspaceID]...)
		}
	}
	return nil
}

func loadWorkspaceMemberships(ctx context.Context, db *sql.DB, table, valueColumn string, workspaceIDs []string) (map[string][]string, error) {
	statement := `SELECT workspace_id, ` + valueColumn + ` FROM ` + table + `
		WHERE workspace_id IN (` + placeholders(len(workspaceIDs)) + `)
		ORDER BY workspace_id, position, lower(` + valueColumn + `), ` + valueColumn
	args := make([]any, 0, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		args = append(args, workspaceID)
	}
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	memberships := make(map[string][]string, len(workspaceIDs))
	for rows.Next() {
		var workspaceID, value string
		if err := rows.Scan(&workspaceID, &value); err != nil {
			return nil, err
		}
		memberships[workspaceID] = append(memberships[workspaceID], value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return memberships, nil
}

func placeholders(count int) string { return strings.TrimSuffix(strings.Repeat("?,", count), ",") }

func cleanValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func lowerValues(values []string) []string {
	values = cleanValues(values)
	for i := range values {
		values[i] = strings.ToLower(values[i])
	}
	return cleanValues(values)
}

func entityTypeStrings(values []EntityType) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return cleanValues(out)
}

func typeAwareSnippet(entityType EntityType, snippet, title string) string {
	snippet = strings.NewReplacer(markStart, "", markEnd, "").Replace(strings.TrimSpace(snippet))
	if snippet != "" {
		return snippet
	}
	switch entityType {
	case EntityProject:
		return "Project: " + title
	case EntityConversation:
		return "Conversation: " + title
	case EntityPlan:
		return "Plan: " + title
	case EntityHandoff:
		return "Handoff: " + title
	case EntityActivity:
		return "Activity: " + title
	default:
		return title
	}
}

func primaryAction(entityType EntityType) Action {
	switch entityType {
	case EntityProject:
		return Action{ID: "workspace.open", Label: "open workspace"}
	case EntityConversation:
		return Action{ID: "session.continue", Label: "continue conversation"}
	case EntityPlan:
		return Action{ID: "plan.open", Label: "open plan"}
	case EntityHandoff:
		return Action{ID: "handoff.open", Label: "open handoff"}
	case EntityActivity:
		return Action{ID: "activity.open", Label: "open activity"}
	default:
		return Action{ID: "entity.open", Label: "open"}
	}
}
