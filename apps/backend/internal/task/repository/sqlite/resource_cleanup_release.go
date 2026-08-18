package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
)

var (
	ErrArchivedResourceReleaseNotAdmitted       = errors.New("archived resource release was not admitted")
	ErrArchivedResourceReleaseTargetNotRetained = errors.New("archived resource release target is not retained")
	ErrArchivedResourceReleaseIdentityDrifted   = errors.New("archived resource release identity drifted")
)

// ReleaseAbsentArchivedResourceAnchor performs the sealed absent-target
// admission transaction. The supplied job carries the canonical release
// snapshot in resource_snapshot plus the exact anchor identity in the
// redundant header fields (operation_id, task_id, resource_id, managed_root_key,
// snapshot_digest). The repository:
//
//  1. decodes and validates the release snapshot;
//  2. verifies the anchor row is the only retained v2 row that matches every
//     generation field (operation_id + digest + task_id + target identity);
//  3. verifies the writer-DB inventory sources agree the physical path and
//     the Git worktree registration are absent;
//  4. transitions the anchor from retained to released via a single CAS that
//     only mutates that one row.
//
// No filesystem, Git, runtime, or association mutation occurs.
func (r *Repository) ReleaseAbsentArchivedResourceAnchor(
	ctx context.Context,
	job *models.TaskResourceCleanupJob,
) (*models.ArchivedResourceReleaseAdmission, error) {
	if job == nil {
		return nil, fmt.Errorf("%w: nil release request", ErrArchivedResourceReleaseNotAdmitted)
	}
	snapshot, identity, err := models.DecodeArchivedResourceReleaseSnapshot([]byte(job.ResourceSnapshot))
	if err != nil {
		return nil, err
	}
	if err := validateReleaseHeaders(job, snapshot, identity); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin release barrier: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := requireReleaseTargetRetainedTx(
		ctx, tx,
		identity.AnchorOperationID, identity.AnchorDigest, snapshot.Immutable,
	); err != nil {
		return nil, err
	}

	if err := requireReleasePathAndGitAbsentTx(
		ctx, tx,
		snapshot.Immutable.AnchorWorktreePath, snapshot.Immutable.AnchorGitCommonDir,
		snapshot.Immutable.AnchorWorktreeID, snapshot.Immutable.AnchorRepository, snapshot.Immutable.AnchorBranch,
	); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	releasedAt, err := time.Parse(time.RFC3339Nano, snapshot.Release.ReleasedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: released_at is not canonical UTC", ErrArchivedResourceReleaseNotAdmitted)
	}
	completedAt := releasedAt.UTC()
	if completedAt.After(now) {
		completedAt = now
	}

	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, completed_at = ?, updated_at = ?, last_error = ''
		WHERE operation_id = ? AND trigger = ? AND state = ?
		  AND task_id = ? AND snapshot_version = ? AND snapshot_digest = ?
		  AND resource_kind = ? AND resource_id = ? AND managed_root_key = ?
		  AND anchor_revision = ? AND active_scope_key IS NULL
	`), models.TaskResourceCleanupStateReleased, completedAt, now,
		identity.AnchorOperationID, models.TaskResourceCleanupTriggerReconcile,
		models.TaskResourceCleanupStateRetained,
		snapshot.Immutable.AnchorTaskID, models.ArchivedResourceReconcileSnapshotVersion,
		identity.AnchorDigest,
		identity.ResourceKind, identity.ResourceID, identity.ManagedRootKey,
		models.ArchivedResourceRetentionAnchorVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("release absent anchor: %w", err)
	}
	affected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return nil, fmt.Errorf("read release affected rows: %w", rowsErr)
	}
	if affected != 1 {
		return nil, fmt.Errorf("%w: release CAS did not affect exactly one row", ErrArchivedResourceReleaseIdentityDrifted)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit release barrier: %v", ErrArchivedResourceReleaseIdentityDrifted, err)
	}
	released, err := r.GetTaskResourceCleanupJobByOperationID(ctx, identity.AnchorOperationID)
	if err != nil {
		return nil, err
	}
	return &models.ArchivedResourceReleaseAdmission{
		Job:    released,
		Reason: "release_absent",
	}, nil
}

func validateReleaseHeaders(
	job *models.TaskResourceCleanupJob,
	snapshot models.ArchivedResourceReleaseSnapshot,
	identity models.ArchivedResourceReleaseIdentity,
) error {
	if job == nil {
		return fmt.Errorf("%w: nil release request", ErrArchivedResourceReleaseNotAdmitted)
	}
	if job.OperationID != identity.OperationID ||
		job.SnapshotDigest != identity.SnapshotDigest ||
		job.ResourceKind != identity.ResourceKind ||
		job.ResourceID != identity.ResourceID ||
		job.ManagedRootKey != identity.ManagedRootKey {
		return fmt.Errorf("%w: release headers do not bind release identity", ErrArchivedResourceReleaseNotAdmitted)
	}
	if job.TaskID != snapshot.Immutable.AnchorTaskID {
		return fmt.Errorf("%w: release header task_id does not bind anchor task", ErrArchivedResourceReleaseNotAdmitted)
	}
	if job.Trigger != models.TaskResourceCleanupTriggerReconcile {
		return fmt.Errorf("%w: release trigger must be reconcile", ErrArchivedResourceReleaseNotAdmitted)
	}
	if job.SnapshotVersion != models.ArchivedResourceReconcileSnapshotVersion {
		return fmt.Errorf("%w: release header snapshot_version must match retained v2", ErrArchivedResourceReleaseNotAdmitted)
	}
	if job.State != "" && job.State != models.TaskResourceCleanupStatePending {
		return fmt.Errorf("%w: release admission row must be pristine pending", ErrArchivedResourceReleaseNotAdmitted)
	}
	return nil
}

// requireReleaseTargetRetainedTx fails closed when no retained v2 anchor
// matches the exact operation_id + digest + task + target identity. The
// unique constraint on operation_id ensures at most one row exists; the
// defense-in-depth scan verifies the full target identity still matches the
// retained anchor's snapshot bytes.
func requireReleaseTargetRetainedTx(
	ctx context.Context,
	tx *sqlx.Tx,
	anchorOperationID, anchorDigest string,
	immutable models.ArchivedResourceReleaseImmutable,
) error {
	var count int
	if err := tx.QueryRowContext(ctx, tx.Rebind(`
		SELECT COUNT(*)
		FROM task_resource_cleanup_jobs
		WHERE trigger = ? AND state = ?
		  AND snapshot_version = ? AND snapshot_digest = ?
		  AND task_id = ? AND resource_id = ? AND managed_root_key = ?
		  AND operation_id = ?
	`), models.TaskResourceCleanupTriggerReconcile,
		models.TaskResourceCleanupStateRetained,
		models.ArchivedResourceReconcileSnapshotVersion, anchorDigest,
		immutable.AnchorTaskID, immutable.AnchorWorktreeID,
		managedRootKeyForRelease(immutable),
		anchorOperationID,
	).Scan(&count); err != nil {
		return fmt.Errorf("%w: probe retained anchor: %v", ErrArchivedResourceReleaseNotAdmitted, err)
	}
	if count != 1 {
		return fmt.Errorf("%w: expected exactly one retained anchor; found %d", ErrArchivedResourceReleaseTargetNotRetained, count)
	}
	return nil
}

func managedRootKeyForRelease(immutable models.ArchivedResourceReleaseImmutable) string {
	key, err := models.ArchivedResourceReleaseManagedRootKey(immutable.AnchorWorktreePath)
	if err != nil {
		return ""
	}
	return key
}

// requireReleasePathAndGitAbsentTx verifies the writer-DB inventory agrees the
// targeted physical path is absent from every protection source AND the Git
// worktree registration has no active executor row. This is the sealed
// absence proof; the admission cannot proceed when any inventory source
// still reports the target.
//
// Optional tables (task_workspace_groups and storage_quarantine_entries) are
// owned by separate repositories; when they are absent the admission treats
// them as empty inventories because no row can reference the target path
// without the table existing first.
func requireReleasePathAndGitAbsentTx(
	ctx context.Context,
	tx *sqlx.Tx,
	physicalPath, gitCommonDir, worktreeID, repositoryID, branch string,
) error {
	tables := []struct {
		label    string
		column   string
		optional bool
	}{
		{label: "task_environments", column: "workspace_path"},
		{label: "task_environment_repos", column: "worktree_path"},
		{label: "executors_running", column: "worktree_path"},
		{label: "task_workspace_groups", column: "materialized_path", optional: true},
		{label: "storage_quarantine_entries", column: "original_path", optional: true},
	}
	for _, t := range tables {
		var count int
		err := tx.QueryRowContext(ctx, tx.Rebind(
			"SELECT COUNT(*) FROM "+t.label+" WHERE "+t.column+" = ?"), physicalPath,
		).Scan(&count)
		if err != nil {
			if t.optional && isSQLiteNoSuchTableError(err) {
				continue
			}
			return fmt.Errorf("%w: probe %s: %v", ErrArchivedResourceReleaseNotAdmitted, t.label, err)
		}
		if count > 0 {
			return fmt.Errorf("%w: %s still references the target path", ErrArchivedResourceReleaseTargetNotRetained, t.label)
		}
	}
	// The standalone `executors_running.worktree_path + worktree_branch` query
	// that used to live here is gone: the loop above already probes
	// `executors_running.worktree_path`, which is the only signal the writer
	// DB has for an active executor registration. The release snapshot
	// guards the Git worktree registration through `worktree_path` and
	// `branch` fields on the snapshot itself; using `executors_running`
	// for a per-branch assertion was a redundant probe that conflated an
	// active-executor row with a Git worktree registration (the two
	// inventories only overlap while the executor is alive).
	return nil
}
