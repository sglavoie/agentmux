package plans

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/workspace"
)

const planSearchEntityType = "plan"

// Repository stores plan metadata, viewer file lists, literal-search content,
// and explicit workspace links in SQLite. It never changes plan directories.
type Repository struct {
	store *datastore.Store
	now   func() time.Time
}

// ImportRecord describes a completed import from the compatibility plan
// index. It is separate from transcript's similarly shaped type so the two
// import contracts can evolve independently.
type ImportRecord struct {
	SourceName  string
	SourcePath  string
	Fingerprint string
	CompletedAt time.Time
	RecordCount int
}

// NewRepository constructs a plan repository over an opened datastore.
func NewRepository(store *datastore.Store) (*Repository, error) {
	if store == nil || store.DB() == nil {
		return nil, errors.New("plans: repository requires an open datastore")
	}
	return &Repository{store: store, now: time.Now}, nil
}

// Upsert replaces a plan's metadata, file list, and searchable content. The
// absolute directory path is its durable identity, matching PlanKey.
func (r *Repository) Upsert(ctx context.Context, meta PlanMeta, searchText string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		return r.upsertTx(ctx, tx, meta, &searchText)
	})
}

// UpsertMetadata replaces metadata and the viewer file list while retaining
// any existing search document. It is used for a refresh cache hit.
func (r *Repository) UpsertMetadata(ctx context.Context, meta PlanMeta) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		return r.upsertTx(ctx, tx, meta, nil)
	})
}

// Get returns a plan by its PlanKey.
func (r *Repository) Get(ctx context.Context, key string) (PlanMeta, bool, error) {
	row := r.store.DB().QueryRowContext(ctx, planSelect+` WHERE id = ?`, key)
	meta, err := r.scanPlan(ctx, row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanMeta{}, false, nil
	}
	if err != nil {
		return PlanMeta{}, false, fmt.Errorf("plans: reading repository entry %q: %w", key, err)
	}
	return meta, true, nil
}

// List returns every indexed plan in modification-time order. A path tie-break
// makes results deterministic when two plans have the same modification time.
func (r *Repository) List(ctx context.Context) ([]PlanMeta, error) {
	rows, err := r.store.DB().QueryContext(ctx, planSelect+` ORDER BY mod_time_ns DESC, path`)
	if err != nil {
		return nil, fmt.Errorf("plans: listing repository entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	metas := make([]PlanMeta, 0)
	for rows.Next() {
		meta, err := r.scanPlan(ctx, rows)
		if err != nil {
			return nil, fmt.Errorf("plans: scanning repository entry: %w", err)
		}
		metas = append(metas, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plans: iterating repository entries: %w", err)
	}
	return metas, nil
}

// Delete removes only indexed plan metadata, files, search content, and
// explicit links. It deliberately never removes a plan directory.
func (r *Repository) Delete(ctx context.Context, key string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?`, planSearchEntityType, key); err != nil {
			return fmt.Errorf("plans: deleting search document: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM plans WHERE id = ?`, key)
		if err != nil {
			return fmt.Errorf("plans: deleting repository entry %q: %w", key, err)
		}
		return requirePlanAffected(result, key)
	})
}

// SearchText returns the indexed literal-search corpus for one plan.
func (r *Repository) SearchText(ctx context.Context, key string) (string, bool, error) {
	var content string
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT normalized_content FROM search_documents
		WHERE entity_type = ? AND entity_id = ?
	`, planSearchEntityType, key).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("plans: reading search document for %q: %w", key, err)
	}
	return content, true, nil
}

// SetSearchText replaces searchable content for an indexed plan without
// touching either the plan directory or the compatibility sidecar.
func (r *Repository) SetSearchText(ctx context.Context, key, searchText string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		var name string
		var modTime int64
		err := tx.QueryRowContext(ctx, `SELECT display_name, mod_time_ns FROM plans WHERE id = ?`, key).Scan(&name, &modTime)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("plans: no repository entry for %q", key)
		}
		if err != nil {
			return fmt.Errorf("plans: reading search-document owner: %w", err)
		}
		return r.upsertSearchDocumentTx(ctx, tx, key, name, modTime, searchText)
	})
}

// DeleteSearchText removes an indexed search document while retaining the
// plan and its explicit workspace links.
func (r *Repository) DeleteSearchText(ctx context.Context, key string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ? AND entity_id = ?`, planSearchEntityType, key)
		if err != nil {
			return fmt.Errorf("plans: deleting search document: %w", err)
		}
		return nil
	})
}

// ClearSearchDocuments drops only plan search documents. It leaves metadata,
// links, and plan directories untouched.
func (r *Repository) ClearSearchDocuments(ctx context.Context) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ?`, planSearchEntityType)
		if err != nil {
			return fmt.Errorf("plans: clearing search documents: %w", err)
		}
		return nil
	})
}

// HasDurableReferences reports whether deleting a derived plan row would also
// erase an explicit workspace link or detach durable handoff metadata.
func (r *Repository) HasDurableReferences(ctx context.Context, key string) (bool, error) {
	var referenced int
	err := r.store.DB().QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM plan_workspace_links WHERE plan_id = ?)
		    OR EXISTS(SELECT 1 FROM handoffs WHERE plan_id = ?)
	`, key, key).Scan(&referenced)
	if err != nil {
		return false, fmt.Errorf("plans: checking durable references for %q: %w", key, err)
	}
	return referenced != 0, nil
}

// LinkWorkspace creates an explicit durable plan-to-workspace association.
// It is idempotent and never infers or creates any other association.
func (r *Repository) LinkWorkspace(ctx context.Context, planKey, workspaceID string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		if err := requirePlanTx(ctx, tx, planKey); err != nil {
			return err
		}
		if err := requireWorkspaceTx(ctx, tx, workspaceID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO plan_workspace_links (plan_id, workspace_id, created_at_ns)
			VALUES (?, ?, ?)
		`, planKey, workspaceID, r.now().UnixNano())
		if err != nil {
			return fmt.Errorf("plans: linking %q to workspace %q: %w", planKey, workspaceID, err)
		}
		return nil
	})
}

// UnlinkWorkspace removes one explicit plan-to-workspace association. It does
// not delete the plan, workspace, or any other explicit association.
func (r *Repository) UnlinkWorkspace(ctx context.Context, planKey, workspaceID string) error {
	return r.store.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM plan_workspace_links WHERE plan_id = ? AND workspace_id = ?
		`, planKey, workspaceID)
		if err != nil {
			return fmt.Errorf("plans: unlinking %q from workspace %q: %w", planKey, workspaceID, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("plans: checking unlink for %q: %w", planKey, err)
		}
		if count == 0 {
			return fmt.Errorf("plans: no explicit link from %q to workspace %q", planKey, workspaceID)
		}
		return nil
	})
}

// WorkspaceIDs returns explicit link targets for a plan in stable order.
func (r *Repository) WorkspaceIDs(ctx context.Context, planKey string) ([]string, error) {
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT workspace_id FROM plan_workspace_links WHERE plan_id = ? ORDER BY workspace_id
	`, planKey)
	if err != nil {
		return nil, fmt.Errorf("plans: listing workspace links for %q: %w", planKey, err)
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("plans: scanning workspace link for %q: %w", planKey, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plans: iterating workspace links for %q: %w", planKey, err)
	}
	return ids, nil
}

// ListByWorkspace returns plans explicitly linked to workspaceID. Global
// callers use List so unlinked plans remain visible in global search.
func (r *Repository) ListByWorkspace(ctx context.Context, workspaceID string) ([]PlanMeta, error) {
	rows, err := r.store.DB().QueryContext(ctx, planSelect+`
		JOIN plan_workspace_links l ON l.plan_id = plans.id
		WHERE l.workspace_id = ?
		ORDER BY plans.mod_time_ns DESC, plans.path
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("plans: listing workspace plans for %q: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()
	plans := make([]PlanMeta, 0)
	for rows.Next() {
		meta, err := r.scanPlan(ctx, rows)
		if err != nil {
			return nil, fmt.Errorf("plans: scanning workspace plan: %w", err)
		}
		plans = append(plans, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plans: iterating workspace plans: %w", err)
	}
	return plans, nil
}

// SuggestionReason identifies why a possible workspace link was surfaced.
// Neither reason has authority to create an explicit link.
type SuggestionReason string

const (
	SuggestionCanonicalPath SuggestionReason = "canonical_path"
	SuggestionNameMatch     SuggestionReason = "name_match"
)

// SuggestionConfidence makes the deliberately weaker title/name heuristic
// visible to callers. Suggestions are always non-authoritative.
type SuggestionConfidence string

const (
	SuggestionHighConfidence SuggestionConfidence = "high"
	SuggestionLowConfidence  SuggestionConfidence = "low"
)

// LinkSuggestion is a possible, never-applied plan-to-workspace association.
// A plan can have multiple suggestions when a path or title is ambiguous.
type LinkSuggestion struct {
	PlanID      string
	PlanName    string
	WorkspaceID string
	Reason      SuggestionReason
	Confidence  SuggestionConfidence
	MatchedText string
}

// SuggestWorkspaceLinks produces non-authoritative hints for the supplied
// workspace projection. Canonical-path occurrences in Markdown are strong
// hints; display/title matches are intentionally low confidence. Existing
// explicit links are omitted, and this method never writes links.
func (r *Repository) SuggestWorkspaceLinks(ctx context.Context, workspaces []workspace.Workspace) ([]LinkSuggestion, error) {
	plans, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	suggestions := make([]LinkSuggestion, 0)
	for _, plan := range plans {
		linked, err := r.WorkspaceIDs(ctx, PlanKey(plan.DirPath))
		if err != nil {
			return nil, err
		}
		linkedSet := make(map[string]struct{}, len(linked))
		for _, id := range linked {
			linkedSet[id] = struct{}{}
		}
		content, _, err := r.SearchText(ctx, PlanKey(plan.DirPath))
		if err != nil {
			return nil, err
		}
		content = strings.ToLower(content)
		for _, ws := range workspaces {
			if ws.ID == "" {
				continue
			}
			if _, linked := linkedSet[ws.ID]; linked {
				continue
			}
			if canonical := strings.TrimSpace(ws.CanonicalPath); canonical != "" && strings.Contains(content, strings.ToLower(canonical)) {
				suggestions = append(suggestions, LinkSuggestion{
					PlanID: PlanKey(plan.DirPath), PlanName: plan.Name, WorkspaceID: ws.ID,
					Reason: SuggestionCanonicalPath, Confidence: SuggestionHighConfidence, MatchedText: canonical,
				})
				continue
			}
			if title, ok := matchingWorkspaceTitle(plan.Name, ws); ok {
				suggestions = append(suggestions, LinkSuggestion{
					PlanID: PlanKey(plan.DirPath), PlanName: plan.Name, WorkspaceID: ws.ID,
					Reason: SuggestionNameMatch, Confidence: SuggestionLowConfidence, MatchedText: title,
				})
			}
		}
	}
	sort.Slice(suggestions, func(a, b int) bool {
		if suggestions[a].PlanID != suggestions[b].PlanID {
			return suggestions[a].PlanID < suggestions[b].PlanID
		}
		if suggestions[a].Reason != suggestions[b].Reason {
			return suggestions[a].Reason < suggestions[b].Reason
		}
		return suggestions[a].WorkspaceID < suggestions[b].WorkspaceID
	})
	return suggestions, nil
}

func (r *Repository) upsertTx(ctx context.Context, tx *sql.Tx, meta PlanMeta, searchText *string) error {
	key := PlanKey(meta.DirPath)
	if key == "" {
		return errors.New("plans: repository entry requires a directory path")
	}
	if meta.Name == "" {
		meta.Name = DisplayName(filepath.Base(meta.DirPath))
	}
	nowNS := r.now().UnixNano()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO plans (id, path, display_name, snippet, mod_time_ns, created_at_ns, updated_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			path = excluded.path,
			display_name = excluded.display_name,
			snippet = excluded.snippet,
			mod_time_ns = excluded.mod_time_ns,
			updated_at_ns = excluded.updated_at_ns
	`, key, meta.DirPath, meta.Name, meta.Snippet, planTimeToNS(meta.ModTime), nowNS, nowNS)
	if err != nil {
		return fmt.Errorf("plans: upserting repository entry %q: %w", key, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plan_files WHERE plan_id = ?`, key); err != nil {
		return fmt.Errorf("plans: replacing file list for %q: %w", key, err)
	}
	for position, relativePath := range meta.Files {
		// Legacy metadata stores only aggregate size. Assign it to the first
		// row so an import round trip keeps the exact total. A source refresh
		// replaces this metadata without touching any plan file.
		fileSize := int64(0)
		if position == 0 {
			fileSize = meta.Size
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO plan_files (plan_id, relative_path, path, file_size, mod_time_ns)
			VALUES (?, ?, ?, ?, ?)
		`, key, relativePath, filepath.Join(meta.DirPath, relativePath), fileSize, planTimeToNS(meta.ModTime))
		if err != nil {
			return fmt.Errorf("plans: storing file %q for %q: %w", relativePath, key, err)
		}
	}
	if searchText == nil {
		return nil
	}
	return r.upsertSearchDocumentTx(ctx, tx, key, meta.Name, planTimeToNS(meta.ModTime), *searchText)
}

func (r *Repository) upsertSearchDocumentTx(ctx context.Context, tx *sql.Tx, key, name string, modTimeNS int64, searchText string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_documents (entity_type, entity_id, occurred_at_ns, title, normalized_content, updated_at_ns)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET
			occurred_at_ns = excluded.occurred_at_ns,
			title = excluded.title,
			normalized_content = excluded.normalized_content,
			updated_at_ns = excluded.updated_at_ns
	`, planSearchEntityType, key, modTimeNS, name, searchText, r.now().UnixNano())
	if err != nil {
		return fmt.Errorf("plans: upserting search document for %q: %w", key, err)
	}
	return nil
}

const planSelect = `SELECT id, path, display_name, snippet, mod_time_ns FROM plans`

type planRowScanner interface{ Scan(dest ...any) error }

func (r *Repository) scanPlan(ctx context.Context, scanner planRowScanner) (PlanMeta, error) {
	var key string
	var meta PlanMeta
	var modTimeNS int64
	if err := scanner.Scan(&key, &meta.DirPath, &meta.Name, &meta.Snippet, &modTimeNS); err != nil {
		return PlanMeta{}, err
	}
	meta.ModTime = planTimeFromNS(modTimeNS)
	rows, err := r.store.DB().QueryContext(ctx, `
		SELECT relative_path, file_size FROM plan_files WHERE plan_id = ? ORDER BY relative_path
	`, key)
	if err != nil {
		return PlanMeta{}, fmt.Errorf("plans: reading file list for %q: %w", key, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var relativePath string
		var size int64
		if err := rows.Scan(&relativePath, &size); err != nil {
			return PlanMeta{}, fmt.Errorf("plans: scanning file list for %q: %w", key, err)
		}
		meta.Files = append(meta.Files, relativePath)
		meta.Size += size
	}
	if err := rows.Err(); err != nil {
		return PlanMeta{}, fmt.Errorf("plans: iterating file list for %q: %w", key, err)
	}
	return meta, nil
}

func requirePlanTx(ctx context.Context, tx *sql.Tx, key string) error {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM plans WHERE id = ?`, key).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("plans: no repository entry for %q", key)
	}
	if err != nil {
		return fmt.Errorf("plans: checking repository entry %q: %w", key, err)
	}
	return nil
}

func requireWorkspaceTx(ctx context.Context, tx *sql.Tx, id string) error {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM workspaces WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("plans: workspace %q does not exist", id)
	}
	if err != nil {
		return fmt.Errorf("plans: checking workspace %q: %w", id, err)
	}
	return nil
}

func requirePlanAffected(result sql.Result, key string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("plans: checking mutation for %q: %w", key, err)
	}
	if count == 0 {
		return fmt.Errorf("plans: no repository entry for %q", key)
	}
	return nil
}

func planTimeToNS(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}

func planTimeFromNS(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

func matchingWorkspaceTitle(planName string, ws workspace.Workspace) (string, bool) {
	planName = normalizedHintText(planName)
	if len(planName) < 3 {
		return "", false
	}
	titles := append([]string{ws.DisplayName}, ws.TitleAliases...)
	for _, title := range titles {
		normalized := normalizedHintText(title)
		if len(normalized) >= 3 && (strings.Contains(planName, normalized) || strings.Contains(normalized, planName)) {
			return title, true
		}
	}
	return "", false
}

func normalizedHintText(value string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		}
	}
	return out.String()
}
