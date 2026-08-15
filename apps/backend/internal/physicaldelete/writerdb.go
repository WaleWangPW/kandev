package physicaldelete

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// SQLInventorySource reads every protection source from one serializable
// writer transaction. A missing table or unreadable row makes the complete
// inventory unavailable and therefore cannot grant deletion authority.
type SQLInventorySource struct{ writer *sqlx.DB }

func NewSQLInventorySource(writer *sqlx.DB) *SQLInventorySource {
	return &SQLInventorySource{writer: writer}
}

func (s *SQLInventorySource) Load(ctx context.Context) (Inventory, error) {
	if s == nil || s.writer == nil {
		return Inventory{}, ErrInventoryIncomplete
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := s.writer.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Inventory{}, fmt.Errorf("%w: begin inventory transaction: %v", ErrInventoryIncomplete, err)
	}
	defer func() { _ = tx.Rollback() }()

	inventory := Inventory{}
	if inventory.TaskEnvironments, err = selectResources(ctx, tx, `
		SELECT id, status AS state, workspace_path AS path, '' AS root_path, '' AS common_dir
		FROM task_environments
		WHERE workspace_path <> ''`, ResourceKindTaskEnvironment); err != nil {
		return Inventory{}, err
	}
	// v0.88 task_environment_repos is the sole physical worktree authority.
	// task_session_worktrees no longer exists and must never be resurrected.
	if inventory.EnvironmentRepositories, err = selectResources(ctx, tx, `
		SELECT id, status AS state, worktree_path AS path, '' AS root_path, '' AS common_dir
		FROM task_environment_repos
		WHERE worktree_path <> ''`, ResourceKindEnvironmentRepo); err != nil {
		return Inventory{}, err
	}
	if inventory.ExecutorWorktrees, err = selectResources(ctx, tx, `
		SELECT id, status AS state, worktree_path AS path, '' AS root_path, '' AS common_dir
		FROM executors_running
		WHERE worktree_path <> ''`, ResourceKindExecutorWorktree); err != nil {
		return Inventory{}, err
	}
	if inventory.WorkspaceGroups, err = selectResources(ctx, tx, `
		SELECT id, cleanup_status AS state, materialized_path AS path, '' AS root_path, '' AS common_dir
		FROM task_workspace_groups
		WHERE materialized_path <> ''`, ResourceKindWorkspaceGroup); err != nil {
		return Inventory{}, err
	}
	if inventory.QuarantineEntries, err = selectResources(ctx, tx, `
		SELECT id, state, original_path AS path, quarantine_path AS root_path, '' AS common_dir
		FROM storage_quarantine_entries`, ResourceKindQuarantineEntry); err != nil {
		return Inventory{}, err
	}
	if inventory.CleanupAnchors, err = selectCleanupAnchors(ctx, tx); err != nil {
		return Inventory{}, err
	}
	inventory.Complete = true
	return inventory, nil
}

type sqlProtectedResource struct {
	ID        string `db:"id"`
	State     string `db:"state"`
	Path      string `db:"path"`
	RootPath  string `db:"root_path"`
	CommonDir string `db:"common_dir"`
}

func selectResources(ctx context.Context, tx *sqlx.Tx, query string, kind ResourceKind) ([]ProtectedResource, error) {
	rows := make([]sqlProtectedResource, 0)
	if err := tx.SelectContext(ctx, &rows, tx.Rebind(query)); err != nil {
		return nil, fmt.Errorf("%w: load %s inventory: %v", ErrInventoryIncomplete, kind, err)
	}
	resources := make([]ProtectedResource, 0, len(rows))
	for _, row := range rows {
		path := row.Path
		if path == "" {
			path = row.RootPath
			row.RootPath = ""
		}
		resource := ProtectedResource{
			ID: row.ID, Kind: kind, State: row.State, Path: path,
			RootPath: row.RootPath, CommonDir: row.CommonDir,
		}
		if err := validateProtectedResource(resource); err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

type sqlCleanupJob struct {
	ID               string `db:"id"`
	Trigger          string `db:"trigger"`
	State            string `db:"state"`
	ResourceSnapshot string `db:"resource_snapshot"`
}

func selectCleanupAnchors(ctx context.Context, tx *sqlx.Tx) ([]CleanupAnchor, error) {
	rows := make([]sqlCleanupJob, 0)
	if err := tx.SelectContext(ctx, &rows, tx.Rebind(`
		SELECT id, trigger, state, resource_snapshot
		FROM task_resource_cleanup_jobs`)); err != nil {
		return nil, fmt.Errorf("%w: load cleanup anchors: %v", ErrInventoryIncomplete, err)
	}
	for _, row := range rows {
		if err := validateCleanupRow(row); err != nil {
			return nil, err
		}
	}
	// Task05 owns the v2/v3 schema and canonical anchor decoder. Generic cleanup
	// rows do not become anchors by inference.
	return []CleanupAnchor{}, nil
}

func validateCleanupRow(row sqlCleanupJob) error {
	if row.ID == "" {
		return fmt.Errorf("%w: cleanup row has empty id", ErrInventoryIncomplete)
	}
	if row.Trigger == "archived_resource_reconcile" {
		return fmt.Errorf("%w: reconcile row requires Task05 canonical validation", ErrInventoryIncomplete)
	}
	if !knownGenericCleanupTrigger(row.Trigger) {
		return fmt.Errorf("%w: unknown cleanup trigger %q", ErrUnknownInventory, row.Trigger)
	}
	if !knownGenericCleanupState(row.State) {
		return fmt.Errorf("%w: unknown cleanup state %q", ErrUnknownInventory, row.State)
	}
	return nil
}

func knownGenericCleanupTrigger(trigger string) bool {
	switch trigger {
	case "archive", "delete", "cascade_archive", "cascade_delete", "workspace_delete", "quick_chat_expire":
		return true
	default:
		return false
	}
}

func knownGenericCleanupState(state string) bool {
	switch state {
	case "prepared", "pending", "running", "retry_wait", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

var _ InventorySource = (*SQLInventorySource)(nil)
