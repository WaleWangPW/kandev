package physicaldelete

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
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
	ID               string  `db:"id"`
	OperationID      string  `db:"operation_id"`
	Trigger          string  `db:"trigger"`
	State            string  `db:"state"`
	ResourceSnapshot string  `db:"resource_snapshot"`
	SnapshotVersion  int     `db:"snapshot_version"`
	SnapshotDigest   string  `db:"snapshot_digest"`
	ResourceKind     string  `db:"resource_kind"`
	ResourceID       string  `db:"resource_id"`
	ManagedRootKey   string  `db:"managed_root_key"`
	AnchorRevision   int64   `db:"anchor_revision"`
	ActiveScopeKey   *string `db:"active_scope_key"`
}

func selectCleanupAnchors(ctx context.Context, tx *sqlx.Tx) ([]CleanupAnchor, error) {
	rows := make([]sqlCleanupJob, 0)
	if err := tx.SelectContext(ctx, &rows, tx.Rebind(`
		SELECT id, operation_id, trigger, state, resource_snapshot,
			snapshot_version, snapshot_digest, resource_kind, resource_id,
			managed_root_key, anchor_revision, active_scope_key
		FROM task_resource_cleanup_jobs`)); err != nil {
		return nil, fmt.Errorf("%w: load cleanup anchors: %v", ErrInventoryIncomplete, err)
	}
	anchors := make([]CleanupAnchor, 0, len(rows))
	for _, row := range rows {
		anchor, err := decodeCleanupAnchorRow(row)
		if err != nil {
			return nil, err
		}
		if anchor.validated {
			anchors = append(anchors, anchor)
		}
	}
	return anchors, nil
}

// decodeCleanupAnchorRow validates one cleanup row and produces a
// CleanupAnchor. Generic cleanup rows produce an empty (unvalidated) anchor
// so they remain visible to the inventory without becoming release
// candidates. v2/v3 rows are decoded by the canonical Task05 decoders; any
// decode error fails the entire inventory closed.
func decodeCleanupAnchorRow(row sqlCleanupJob) (CleanupAnchor, error) {
	switch row.Trigger {
	case "archive", "delete", "cascade_archive", "cascade_delete", "workspace_delete", "quick_chat_expire":
		if !knownGenericCleanupState(row.State) {
			return CleanupAnchor{}, fmt.Errorf("%w: unknown generic cleanup state %q", ErrUnknownInventory, row.State)
		}
		return CleanupAnchor{
			ID:             row.ID,
			OperationID:    row.OperationID,
			State:          row.State,
			SnapshotDigest: row.SnapshotDigest,
		}, nil
	case "reconcile", "archived_resource_reconcile":
		return decodeRetainedReconcileAnchor(row)
	default:
		return CleanupAnchor{}, fmt.Errorf("%w: unknown cleanup trigger %q", ErrUnknownInventory, row.Trigger)
	}
}

func decodeRetainedReconcileAnchor(row sqlCleanupJob) (CleanupAnchor, error) {
	if row.SnapshotVersion != 2 && row.SnapshotVersion != 3 {
		return CleanupAnchor{}, fmt.Errorf("%w: reconcile snapshot version %d is not supported", ErrInventoryIncomplete, row.SnapshotVersion)
	}
	if row.ResourceSnapshot == "" {
		return CleanupAnchor{}, fmt.Errorf("%w: reconcile row %q has empty snapshot", ErrInventoryIncomplete, row.OperationID)
	}
	switch row.SnapshotVersion {
	case 2:
		snapshot, identity, err := models.DecodeArchivedResourceReconcileSnapshot([]byte(row.ResourceSnapshot))
		if err != nil {
			return CleanupAnchor{}, fmt.Errorf("%w: decode v2 reconcile %q: %v", ErrInventoryIncomplete, row.OperationID, err)
		}
		if identity.OperationID != row.OperationID {
			return CleanupAnchor{}, fmt.Errorf("%w: v2 reconcile %q operation_id drift", ErrInventoryIncomplete, row.OperationID)
		}
		if identity.SnapshotDigest != row.SnapshotDigest {
			return CleanupAnchor{}, fmt.Errorf("%w: v2 reconcile %q digest drift", ErrInventoryIncomplete, row.OperationID)
		}
		if identity.ResourceKind != row.ResourceKind ||
			identity.ResourceID != row.ResourceID ||
			identity.ManagedRootKey != row.ManagedRootKey {
			return CleanupAnchor{}, fmt.Errorf("%w: v2 reconcile %q identity drift", ErrInventoryIncomplete, row.OperationID)
		}
		return CleanupAnchor{
			ID:              row.ID,
			OperationID:     row.OperationID,
			State:           row.State,
			Path:            snapshot.Immutable.Target.WorktreePath,
			RepositoryID:    snapshot.Immutable.Target.RepositoryID,
			Branch:          snapshot.Immutable.Target.Branch,
			HeadOID:         snapshot.Immutable.Target.HeadOID,
			SnapshotDigest:  row.SnapshotDigest,
			SnapshotVersion: row.SnapshotVersion,
			ResourceKind:    row.ResourceKind,
			ResourceID:      row.ResourceID,
			TaskID:          snapshot.Immutable.OriginTaskID,
			ManagedRootKey:  row.ManagedRootKey,
			AnchorRevision:  int(row.AnchorRevision),
			validated:       true,
		}, nil
	case 3:
		snapshot, identity, err := models.DecodeArchivedResourceGroupReconcileSnapshot([]byte(row.ResourceSnapshot))
		if err != nil {
			return CleanupAnchor{}, fmt.Errorf("%w: decode v3 reconcile %q: %v", ErrInventoryIncomplete, row.OperationID, err)
		}
		if identity.OperationID != row.OperationID {
			return CleanupAnchor{}, fmt.Errorf("%w: v3 reconcile %q operation_id drift", ErrInventoryIncomplete, row.OperationID)
		}
		if identity.SnapshotDigest != row.SnapshotDigest {
			return CleanupAnchor{}, fmt.Errorf("%w: v3 reconcile %q digest drift", ErrInventoryIncomplete, row.OperationID)
		}
		if identity.ResourceKind != row.ResourceKind ||
			identity.ResourceID != row.ResourceID ||
			identity.ManagedRootKey != row.ManagedRootKey {
			return CleanupAnchor{}, fmt.Errorf("%w: v3 reconcile %q identity drift", ErrInventoryIncomplete, row.OperationID)
		}
		// v3 groups carry one or more branches; the inventory uses the first
		// branch's identity as the canonical anchor identity so the admission
		// request can target the worktree by branch + head_oid.
		var branch, headOID string
		if len(snapshot.Immutable.Branches) > 0 {
			branch = snapshot.Immutable.Branches[0].Branch
			headOID = snapshot.Immutable.Branches[0].HeadOID
		}
		return CleanupAnchor{
			ID:              row.ID,
			OperationID:     row.OperationID,
			State:           row.State,
			Path:            snapshot.Immutable.Target.WorktreePath,
			RepositoryID:    snapshot.Immutable.Target.RepositoryID,
			Branch:          branch,
			HeadOID:         headOID,
			SnapshotDigest:  row.SnapshotDigest,
			SnapshotVersion: row.SnapshotVersion,
			ResourceKind:    row.ResourceKind,
			ResourceID:      row.ResourceID,
			TaskID:          snapshot.Immutable.CoordinatorTaskID,
			ManagedRootKey:  row.ManagedRootKey,
			AnchorRevision:  int(row.AnchorRevision),
			validated:       true,
		}, nil
	default:
		return CleanupAnchor{}, fmt.Errorf("%w: reconcile snapshot version %d is not supported", ErrInventoryIncomplete, row.SnapshotVersion)
	}
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
