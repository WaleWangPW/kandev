package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

var (
	ErrArchivedResourceReconcileRequiresExactAPI = errors.New("archived resource reconcile requires exact repository API")
	ErrArchivedResourceAnchorImmutable           = errors.New("archived resource retention anchor is immutable")
	ErrArchivedResourceReconcileConflict         = errors.New("archived resource reconcile snapshot conflicts with durable state")
	ErrArchivedResourceReconcileNotFound         = errors.New("archived resource reconcile job not found")
)

const archivedResourceCompletionBlockReason = "reconcile completion failed with verified zero durable effects"

// archivedResourceSessionIDExpr derives the deterministic session identity of
// one task_environment_repos row. v0.88 removed task_session_worktrees;
// environment-repository rows are the sole physical-worktree authority, so the
// owning environment id is the stable association participant identity bound
// into every canonical snapshot (docs/specs/tasks/archived-resource-safety).
const archivedResourceSessionIDExpr = `ter.task_environment_id`

// AdmitArchivedResourceReconcile atomically binds a strict v2 snapshot to the
// current task archive generation and complete task-owned association set.
func (r *Repository) AdmitArchivedResourceReconcile(
	ctx context.Context,
	job *models.TaskResourceCleanupJob,
) (*models.ArchivedResourceReconcileAdmission, error) {
	snapshot, _, err := models.ValidateArchivedResourceReconcileJobHeaders(job)
	if err != nil {
		return nil, err
	}
	if !isPristineArchivedResourceJob(job) {
		return nil, fmt.Errorf("%w: new job is not an unclaimed pending anchor", ErrArchivedResourceReconcileConflict)
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin archived reconcile admission: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := loadArchivedResourceJobByOperationTx(ctx, tx, r.db.DriverName(), job.OperationID)
	if err == nil {
		if _, _, validateErr := models.ValidateArchivedResourceReconcileJobHeaders(existing); validateErr != nil ||
			existing.ResourceSnapshot != job.ResourceSnapshot || existing.SnapshotDigest != job.SnapshotDigest {
			return nil, fmt.Errorf("%w: operation id is bound to different or invalid state", ErrArchivedResourceReconcileConflict)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit archived reconcile replay: %w", err)
		}
		return &models.ArchivedResourceReconcileAdmission{Job: existing, Created: false}, nil
	}
	if !errors.Is(err, ErrArchivedResourceReconcileNotFound) {
		return nil, err
	}
	existingScope, err := loadArchivedResourceJobByScopeTx(ctx, tx, r.db.DriverName(), *job.ActiveScopeKey)
	if err == nil {
		if _, _, validateErr := models.ValidateArchivedResourceReconcileJobHeaders(existingScope); validateErr == nil &&
			existingScope.OperationID == job.OperationID && existingScope.ResourceSnapshot == job.ResourceSnapshot {
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("commit archived reconcile scope replay: %w", err)
			}
			return &models.ArchivedResourceReconcileAdmission{Job: existingScope, Created: false}, nil
		}
		return nil, fmt.Errorf("%w: active scope is bound to another operation", ErrArchivedResourceReconcileConflict)
	}
	if !errors.Is(err, ErrArchivedResourceReconcileNotFound) {
		return nil, err
	}

	if err := validateArchivedTaskGenerationAndRuntimeTx(ctx, tx, r.db.DriverName(), snapshot); err != nil {
		return nil, err
	}
	associations, err := loadArchivedResourceAssociationsTx(
		ctx, tx, r.db.DriverName(), snapshot.Immutable.Target.WorktreeID, true,
	)
	if err != nil {
		return nil, err
	}
	if err := compareArchivedResourceAssociations(snapshot.Immutable.Associations, associations); err != nil {
		return nil, err
	}

	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		INSERT INTO task_resource_cleanup_jobs (`+taskResourceCleanupColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(operation_id) DO NOTHING
	`), job.ID, job.OperationID, job.TaskID, job.Trigger, job.State,
		job.ResourceSnapshot, job.SnapshotVersion, job.SnapshotDigest,
		job.ResourceKind, job.ResourceID, job.ManagedRootKey, job.AnchorRevision,
		job.ActiveScopeKey, job.Attempts, job.NextAttemptAt, job.LastError,
		job.CreatedAt, job.UpdatedAt, job.CompletedAt)
	if err != nil {
		return nil, fmt.Errorf("insert archived reconcile anchor: %w", err)
	}
	count, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return nil, fmt.Errorf("read admitted anchor affected rows: %w", rowsErr)
	}
	if count != 1 {
		return nil, fmt.Errorf("%w: operation or active scope already exists", ErrArchivedResourceReconcileConflict)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit archived reconcile admission: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
	}
	return &models.ArchivedResourceReconcileAdmission{Job: job, Created: true}, nil
}

// ClaimArchivedResourceReconcileJob is intentionally separate from the generic
// cleanup worker claim so a disabled feature cannot accidentally execute it.
func (r *Repository) ClaimArchivedResourceReconcileJob(
	ctx context.Context,
	id string,
) (*models.TaskResourceCleanupJob, bool, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := loadArchivedResourceJobByIDTx(ctx, tx, r.db.DriverName(), id)
	if err != nil {
		return nil, false, err
	}
	if _, err := models.ValidateArchivedResourceAnyReconcileJobHeaders(job); err != nil {
		return nil, false, err
	}
	if job.State != models.TaskResourceCleanupStatePending && job.State != models.TaskResourceCleanupStateRetryWait {
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return job, false, nil
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, attempts = attempts + 1, next_attempt_at = NULL, updated_at = ?
		WHERE id = ? AND trigger = ? AND state = ? AND attempts = ?
		  AND snapshot_version = ? AND snapshot_digest = ?
		  AND active_scope_key = ?
	`), models.TaskResourceCleanupStateRunning, now,
		job.ID, models.TaskResourceCleanupTriggerReconcile, job.State, job.Attempts,
		job.SnapshotVersion, job.SnapshotDigest, job.ActiveScopeKey)
	if err != nil {
		return nil, false, err
	}
	count, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return nil, false, fmt.Errorf("read claimed anchor affected rows: %w", rowsErr)
	}
	if count != 1 {
		return nil, false, fmt.Errorf("%w: claim generation drifted", ErrArchivedResourceReconcileConflict)
	}
	job.State = models.TaskResourceCleanupStateRunning
	job.Attempts++
	job.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("%w: commit archived reconcile claim: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
	}
	return job, true, nil
}

// CompleteArchivedResourceReconcileRetention is the sole transition that can
// tombstone associations. Association changes and the retained anchor commit
// in one writer transaction.
func (r *Repository) CompleteArchivedResourceReconcileRetention(
	ctx context.Context,
	id string,
	attempt int,
) (*models.ArchivedResourceReconcileCompletion, error) {
	completion, err := r.completeArchivedResourceReconcileRetentionTx(ctx, id, attempt)
	if err == nil || errors.Is(err, repoerrors.ErrTransactionOutcomeUnknown) ||
		!errors.Is(err, ErrArchivedResourceReconcileConflict) {
		return completion, err
	}
	return nil, r.blockDeterministicArchivedResourceCompletion(ctx, id, attempt, err)
}

func (r *Repository) completeArchivedResourceReconcileRetentionTx(
	ctx context.Context,
	id string,
	attempt int,
) (*models.ArchivedResourceReconcileCompletion, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin archived reconcile completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	job, err := loadArchivedResourceJobByIDTx(ctx, tx, r.db.DriverName(), id)
	if err != nil {
		return nil, err
	}
	snapshot, _, err := models.ValidateArchivedResourceReconcileJobHeaders(job)
	if err != nil {
		return nil, err
	}
	if job.State == models.TaskResourceCleanupStateRetained {
		if job.Attempts != attempt || attempt <= 0 {
			return nil, fmt.Errorf("%w: retained replay claim does not match", ErrArchivedResourceReconcileConflict)
		}
		remaining, loadErr := loadArchivedResourceAssociationsTx(
			ctx, tx, r.db.DriverName(), snapshot.Immutable.Target.WorktreeID, true,
		)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, association := range remaining {
			for _, historical := range snapshot.Immutable.Associations {
				if association.AssociationID == historical.AssociationID && association.UpdatedAt == historical.UpdatedAt {
					return nil, fmt.Errorf("%w: historical association generation is unexpectedly active", ErrArchivedResourceReconcileConflict)
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.ArchivedResourceReconcileCompletion{
			Job: job, AssociationsUnbound: len(snapshot.Immutable.Associations), Replayed: true,
		}, nil
	}
	if job.State != models.TaskResourceCleanupStateRunning || job.Attempts != attempt || attempt <= 0 {
		return nil, fmt.Errorf("%w: running claim does not match", ErrArchivedResourceReconcileConflict)
	}
	if err := validateArchivedTaskGenerationAndRuntimeTx(ctx, tx, r.db.DriverName(), snapshot); err != nil {
		return nil, err
	}
	loaded, err := loadArchivedResourceAssociationsWithCASTokensTx(
		ctx, tx, r.db.DriverName(), snapshot.Immutable.Target.WorktreeID, true,
	)
	if err != nil {
		return nil, err
	}
	if err := compareArchivedResourceAssociations(snapshot.Immutable.Associations, loaded.associations); err != nil {
		return nil, err
	}

	now := completionTimestamp(snapshot.Immutable.Associations, time.Now().UTC())
	if err := tombstoneArchivedResourceAssociationsTx(
		ctx, tx, r.db.DriverName(), snapshot.Immutable.Associations, loaded.casTokens, now,
	); err != nil {
		return nil, err
	}

	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, anchor_revision = anchor_revision + 1,
			last_error = '', next_attempt_at = NULL, completed_at = ?, updated_at = ?
		WHERE id = ? AND trigger = ? AND state = ? AND attempts = ?
		  AND snapshot_version = ? AND snapshot_digest = ?
		  AND resource_kind = ? AND resource_id = ? AND managed_root_key = ?
		  AND anchor_revision = ? AND active_scope_key = ?
		  AND resource_snapshot = ?
	`), models.TaskResourceCleanupStateRetained, now, now,
		job.ID, models.TaskResourceCleanupTriggerReconcile,
		models.TaskResourceCleanupStateRunning, attempt,
		job.SnapshotVersion, job.SnapshotDigest, job.ResourceKind, job.ResourceID,
		job.ManagedRootKey, job.AnchorRevision, job.ActiveScopeKey, job.ResourceSnapshot)
	if err != nil {
		return nil, fmt.Errorf("retain archived reconcile anchor: %w", err)
	}
	count, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return nil, fmt.Errorf("read retained anchor affected rows: %w", rowsErr)
	}
	if count != 1 {
		return nil, fmt.Errorf("%w: cleanup job generation drifted", ErrArchivedResourceReconcileConflict)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit archived reconcile retention: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
	}
	job.State = models.TaskResourceCleanupStateRetained
	job.AnchorRevision++
	job.CompletedAt = &now
	job.UpdatedAt = now
	return &models.ArchivedResourceReconcileCompletion{
		Job: job, AssociationsUnbound: len(snapshot.Immutable.Associations), Replayed: false,
	}, nil
}

func (r *Repository) blockDeterministicArchivedResourceCompletion(
	ctx context.Context,
	id string,
	attempt int,
	completionErr error,
) error {
	current, err := r.GetRunningArchivedResourceReconcileJob(ctx, id)
	if err != nil || current.Attempts != attempt || attempt <= 0 {
		if err != nil {
			return fmt.Errorf("%w: rebind failed completion: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
		}
		return fmt.Errorf("%w: failed completion attempt drifted", repoerrors.ErrTransactionOutcomeUnknown)
	}
	blocked, err := r.blockRunningArchivedResourceReconcileJob(
		ctx, current, archivedResourceCompletionBlockReason,
	)
	if err != nil {
		return fmt.Errorf("%w: block failed completion: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
	}
	if !blocked {
		return fmt.Errorf("%w: failed completion has mixed or drifted durable state", repoerrors.ErrTransactionOutcomeUnknown)
	}
	return completionErr
}

// CancelNeverClaimedArchivedResourceReconcile performs the sole disabled-mode
// lifecycle mutation: an exact DB-only CAS of a pristine pending operation.
func (r *Repository) CancelNeverClaimedArchivedResourceReconcile(
	ctx context.Context,
	expected *models.TaskResourceCleanupJob,
) (bool, error) {
	if !isPristineArchivedResourceJob(expected) {
		return false, fmt.Errorf("%w: cancellation target is not pristine pending", ErrArchivedResourceReconcileConflict)
	}
	if _, err := models.ValidateArchivedResourceAnyReconcileJobHeaders(expected); err != nil {
		return false, err
	}
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, active_scope_key = NULL, completed_at = ?, updated_at = ?
		WHERE id = ? AND operation_id = ? AND trigger = ? AND state = ?
		  AND task_id = ? AND attempts = 0 AND next_attempt_at IS NULL
		  AND completed_at IS NULL AND last_error = ''
		  AND snapshot_version = ? AND snapshot_digest = ?
		  AND resource_kind = ? AND resource_id = ? AND managed_root_key = ?
		  AND anchor_revision = 0 AND active_scope_key = ?
		  AND resource_snapshot = ?
	`), models.TaskResourceCleanupStateCancelled, now, now,
		expected.ID, expected.OperationID, models.TaskResourceCleanupTriggerReconcile,
		models.TaskResourceCleanupStatePending, expected.TaskID,
		expected.SnapshotVersion, expected.SnapshotDigest,
		expected.ResourceKind, expected.ResourceID, expected.ManagedRootKey,
		expected.ActiveScopeKey, expected.ResourceSnapshot)
	if err != nil {
		return false, err
	}
	count, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return false, fmt.Errorf("read cancelled reconcile anchor affected rows: %w", rowsErr)
	}
	return count == 1, nil
}

// ListArchivedResourceReconcileJobsByTaskID returns only the durable reconcile
// anchors for one task. It intentionally excludes generic cleanup rows so the
// disabled unarchive exception never needs to inspect an unrelated snapshot.
func (r *Repository) ListArchivedResourceReconcileJobsByTaskID(
	ctx context.Context,
	taskID string,
) ([]*models.TaskResourceCleanupJob, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE task_id = ? AND trigger = ? AND snapshot_version IN (?, ?)
		ORDER BY created_at ASC, id ASC
	`), taskID, models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceReconcileSnapshotVersion,
		models.ArchivedResourceGroupReconcileSnapshotVersion)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*models.TaskResourceCleanupJob, 0)
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ListDueArchivedResourceReconcileJobs is the enabled-only selector used by
// the reconcile worker. It is separate from the generic selector so a disabled
// worker cannot accidentally enumerate or claim an anchor.
func (r *Repository) ListDueArchivedResourceReconcileJobs(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]*models.TaskResourceCleanupJob, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE trigger = ? AND snapshot_version IN (?, ?)
		  AND (state = ? OR (state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)))
		ORDER BY created_at ASC, id ASC LIMIT ?
	`), models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceReconcileSnapshotVersion,
		models.ArchivedResourceGroupReconcileSnapshotVersion,
		models.TaskResourceCleanupStatePending, models.TaskResourceCleanupStateRetryWait,
		now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*models.TaskResourceCleanupJob, 0)
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ListRunningArchivedResourceReconcileJobs returns interrupted exact claims for
// enabled startup recovery. Disabled service paths never call this selector.
func (r *Repository) ListRunningArchivedResourceReconcileJobs(
	ctx context.Context,
) ([]*models.TaskResourceCleanupJob, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE trigger = ? AND snapshot_version IN (?, ?) AND state = ?
		ORDER BY created_at ASC, id ASC
	`), models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceReconcileSnapshotVersion,
		models.ArchivedResourceGroupReconcileSnapshotVersion,
		models.TaskResourceCleanupStateRunning)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*models.TaskResourceCleanupJob, 0)
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if _, validateErr := models.ValidateArchivedResourceAnyReconcileJobHeaders(job); validateErr != nil {
			return nil, validateErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// GetRunningArchivedResourceReconcileJob rebinds one startup candidate after
// process-local participant locks have been acquired.
func (r *Repository) GetRunningArchivedResourceReconcileJob(
	ctx context.Context,
	id string,
) (*models.TaskResourceCleanupJob, error) {
	row := r.db.QueryRowxContext(ctx, r.db.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE id = ? AND trigger = ? AND snapshot_version IN (?, ?) AND state = ?
	`), id, models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceReconcileSnapshotVersion,
		models.ArchivedResourceGroupReconcileSnapshotVersion,
		models.TaskResourceCleanupStateRunning)
	job, err := scanTaskResourceCleanupJob(row)
	if err != nil {
		if isCleanupJobNotFoundError(err) {
			return nil, ErrArchivedResourceReconcileNotFound
		}
		return nil, err
	}
	if _, err := models.ValidateArchivedResourceAnyReconcileJobHeaders(job); err != nil {
		return nil, err
	}
	return job, nil
}

// blockRunningArchivedResourceReconcileJob terminalizes only an exact running
// claim whose complete expected association set is still active. That readback
// proves the failed completion had zero logical effect; any missing/tombstoned
// row is treated as an unknown or mixed outcome and leaves the job untouched.
func (r *Repository) blockRunningArchivedResourceReconcileJob(
	ctx context.Context,
	expected *models.TaskResourceCleanupJob,
	lastError string,
) (bool, error) {
	if expected == nil || expected.State != models.TaskResourceCleanupStateRunning ||
		expected.Attempts <= 0 || lastError == "" {
		return false, fmt.Errorf("%w: exact running claim is required", ErrArchivedResourceReconcileConflict)
	}
	if _, err := models.ValidateArchivedResourceAnyReconcileJobHeaders(expected); err != nil {
		return false, err
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadArchivedResourceJobByIDTx(ctx, tx, r.db.DriverName(), expected.ID)
	if err != nil {
		return false, err
	}
	if !sameRunningArchivedResourceJobGeneration(expected, current) {
		return false, nil
	}
	associations, err := archivedResourceJobSnapshotAssociations(current)
	if err != nil {
		return false, err
	}
	zeroEffect, err := allArchivedResourceAssociationsActiveTx(
		ctx, tx, r.db.DriverName(), associations,
	)
	if err != nil || !zeroEffect {
		return false, err
	}
	jobToken, err := archivedResourceJobCASTokensTx(
		ctx, tx, r.db.DriverName(), current.ID,
	)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, tx.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, last_error = ?, next_attempt_at = NULL, updated_at = ?
		WHERE id = ? AND operation_id = ? AND task_id = ? AND trigger = ?
		  AND state = ? AND attempts = ? AND snapshot_version = ?
		  AND snapshot_digest = ? AND resource_kind = ? AND resource_id = ?
		  AND managed_root_key = ? AND anchor_revision = ? AND active_scope_key = ?
		  AND resource_snapshot = ? AND completed_at IS NULL
		  AND created_at = ? AND updated_at = ?
	`), models.TaskResourceCleanupStateBlocked, lastError, now,
		current.ID, current.OperationID, current.TaskID, current.Trigger,
		models.TaskResourceCleanupStateRunning, current.Attempts,
		current.SnapshotVersion, current.SnapshotDigest, current.ResourceKind,
		current.ResourceID, current.ManagedRootKey, current.AnchorRevision,
		current.ActiveScopeKey, current.ResourceSnapshot,
		jobToken.createdAt, jobToken.updatedAt)
	if err != nil {
		return false, err
	}
	count, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return false, fmt.Errorf("read blocked anchor affected rows: %w", rowsErr)
	}
	if count != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("%w: commit blocked reconcile anchor: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
	}
	return true, nil
}

// RestoreArchivedResourceReconcileRetention restores the exact tombstoned
// association rows belonging to retained anchors. The anchor itself remains
// immutable and continues protecting the historical managed root.
func (r *Repository) RestoreArchivedResourceReconcileRetention(
	ctx context.Context,
	taskID string,
) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, fmt.Errorf("begin archived reconcile restoration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryxContext(ctx, tx.Rebind(`
		SELECT id FROM task_resource_cleanup_jobs
		WHERE task_id = ? AND trigger = ? AND snapshot_version = ? AND state = ?
		ORDER BY created_at ASC, id ASC`), taskID,
		models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceReconcileSnapshotVersion,
		models.TaskResourceCleanupStateRetained)
	if err != nil {
		return false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return false, scanErr
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	_ = rows.Close()
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	now := time.Now().UTC()
	for _, id := range ids {
		job, loadErr := loadArchivedResourceJobByIDTx(ctx, tx, r.db.DriverName(), id)
		if loadErr != nil {
			return false, loadErr
		}
		snapshot, _, validateErr := models.ValidateArchivedResourceReconcileJobHeaders(job)
		if validateErr != nil {
			return false, validateErr
		}
		if job.TaskID != taskID {
			return false, fmt.Errorf("%w: retained anchor task mismatch", ErrArchivedResourceReconcileConflict)
		}
		for _, association := range snapshot.Immutable.Associations {
			createdAt, _ := time.Parse(time.RFC3339Nano, association.CreatedAt)
			result, updateErr := tx.ExecContext(ctx, tx.Rebind(`
				UPDATE task_environment_repos AS ter
				SET status = 'active', deleted_at = NULL, updated_at = ?
				WHERE ter.id = ? AND ter.task_environment_id = ? AND ter.worktree_id = ?
				  AND ter.repository_id = ? AND COALESCE(ter.branch_slug, '') = ?
				  AND ter.worktree_path = ? AND ter.worktree_branch = ?
				  AND ter.status = 'deleted' AND ter.deleted_at IS NOT NULL
				  AND ter.created_at = ?
				  AND EXISTS (
					SELECT 1 FROM task_environments te
					WHERE te.id = ter.task_environment_id AND te.task_id = ?
				  )
			`), now, association.AssociationID, association.SessionID,
				association.WorktreeID, association.RepositoryID, association.BranchSlug,
				association.WorktreePath, association.WorktreeBranch, createdAt,
				association.TaskID)
			if updateErr != nil {
				return false, fmt.Errorf("restore association %s: %w", association.AssociationID, updateErr)
			}
			count, rowsErr := result.RowsAffected()
			if rowsErr != nil {
				return false, fmt.Errorf("read restored association %s affected rows: %w", association.AssociationID, rowsErr)
			}
			if count != 1 {
				return false, fmt.Errorf("%w: association %s cannot be restored", ErrArchivedResourceReconcileConflict, association.AssociationID)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit archived reconcile restoration: %w", err)
	}
	return true, nil
}

func isPristineArchivedResourceJob(job *models.TaskResourceCleanupJob) bool {
	return job != nil && job.State == models.TaskResourceCleanupStatePending &&
		job.Attempts == 0 && job.AnchorRevision == 0 &&
		job.NextAttemptAt == nil && job.CompletedAt == nil && job.LastError == ""
}

func loadArchivedResourceJobByOperationTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	operationID string,
) (*models.TaskResourceCleanupJob, error) {
	row := tx.QueryRowxContext(ctx, tx.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs WHERE operation_id = ?`+reconcileForUpdate(driver)), operationID)
	job, err := scanTaskResourceCleanupJob(row)
	if err != nil {
		if isCleanupJobNotFoundError(err) {
			return nil, ErrArchivedResourceReconcileNotFound
		}
		return nil, err
	}
	return job, nil
}

func loadArchivedResourceJobByScopeTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	activeScopeKey string,
) (*models.TaskResourceCleanupJob, error) {
	row := tx.QueryRowxContext(ctx, tx.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs WHERE active_scope_key = ?`+reconcileForUpdate(driver)), activeScopeKey)
	job, err := scanTaskResourceCleanupJob(row)
	if err != nil {
		if isCleanupJobNotFoundError(err) {
			return nil, ErrArchivedResourceReconcileNotFound
		}
		return nil, err
	}
	return job, nil
}

func loadArchivedResourceJobByIDTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	id string,
) (*models.TaskResourceCleanupJob, error) {
	row := tx.QueryRowxContext(ctx, tx.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs WHERE id = ?`+reconcileForUpdate(driver)), id)
	job, err := scanTaskResourceCleanupJob(row)
	if err != nil {
		if isCleanupJobNotFoundError(err) {
			return nil, ErrArchivedResourceReconcileNotFound
		}
		return nil, err
	}
	return job, nil
}

func isCleanupJobNotFoundError(err error) bool {
	return err != nil && err.Error() == "task resource cleanup job not found"
}

func sameRunningArchivedResourceJobGeneration(
	expected *models.TaskResourceCleanupJob,
	current *models.TaskResourceCleanupJob,
) bool {
	if expected == nil || current == nil ||
		expected.State != models.TaskResourceCleanupStateRunning ||
		current.State != models.TaskResourceCleanupStateRunning {
		return false
	}
	return expected.ID == current.ID && expected.OperationID == current.OperationID &&
		expected.TaskID == current.TaskID && expected.Trigger == current.Trigger &&
		expected.ResourceSnapshot == current.ResourceSnapshot &&
		expected.SnapshotVersion == current.SnapshotVersion &&
		expected.SnapshotDigest == current.SnapshotDigest &&
		expected.ResourceKind == current.ResourceKind && expected.ResourceID == current.ResourceID &&
		expected.ManagedRootKey == current.ManagedRootKey &&
		expected.AnchorRevision == current.AnchorRevision &&
		expected.Attempts == current.Attempts && expected.LastError == current.LastError &&
		timePointersEqual(expected.NextAttemptAt, current.NextAttemptAt) &&
		timePointersEqual(expected.CompletedAt, current.CompletedAt) &&
		stringPointersEqual(expected.ActiveScopeKey, current.ActiveScopeKey) &&
		expected.CreatedAt.Equal(current.CreatedAt) && expected.UpdatedAt.Equal(current.UpdatedAt)
}

func timePointersEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func stringPointersEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func archivedResourceJobSnapshotAssociations(
	job *models.TaskResourceCleanupJob,
) ([]models.ArchivedResourceReconcileAssociation, error) {
	switch job.SnapshotVersion {
	case models.ArchivedResourceReconcileSnapshotVersion:
		snapshot, _, err := models.ValidateArchivedResourceReconcileJobHeaders(job)
		if err != nil {
			return nil, err
		}
		return snapshot.Immutable.Associations, nil
	case models.ArchivedResourceGroupReconcileSnapshotVersion:
		snapshot, _, err := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
		if err != nil {
			return nil, err
		}
		return snapshot.Immutable.Associations, nil
	default:
		return nil, fmt.Errorf("%w: unsupported reconcile snapshot", ErrArchivedResourceReconcileConflict)
	}
}

func allArchivedResourceAssociationsActiveTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	associations []models.ArchivedResourceReconcileAssociation,
) (bool, error) {
	for _, association := range associations {
		var status string
		var deletedAt sql.NullTime
		err := tx.QueryRowxContext(ctx, tx.Rebind(`
			SELECT status, deleted_at FROM task_environment_repos
			WHERE id = ?`+reconcileForUpdate(driver)), association.AssociationID).Scan(&status, &deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if status != "active" || deletedAt.Valid {
			return false, nil
		}
	}
	return true, nil
}

func archivedResourceJobCASTokensTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	id string,
) (archivedResourceAssociationCASToken, error) {
	expressions := "created_at, updated_at"
	if !dialect.IsPostgres(driver) {
		expressions = "CAST(created_at AS TEXT), CAST(updated_at AS TEXT)"
	}
	var token archivedResourceAssociationCASToken
	if err := tx.QueryRowxContext(ctx, tx.Rebind(`
		SELECT `+expressions+` FROM task_resource_cleanup_jobs
		WHERE id = ?`+reconcileForUpdate(driver)), id).Scan(
		&token.createdAt, &token.updatedAt,
	); err != nil {
		return archivedResourceAssociationCASToken{}, err
	}
	if token.createdAt == nil || token.updatedAt == nil {
		return archivedResourceAssociationCASToken{}, fmt.Errorf(
			"%w: cleanup job has no exact CAS token", ErrArchivedResourceReconcileConflict,
		)
	}
	return token, nil
}

func validateArchivedTaskGenerationAndRuntimeTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	snapshot models.ArchivedResourceReconcileSnapshot,
) error {
	var archivedAt sql.NullTime
	err := tx.QueryRowxContext(ctx, tx.Rebind(`
		SELECT archived_at FROM tasks WHERE id = ?`+reconcileForUpdate(driver)),
		snapshot.Immutable.OriginTaskID,
	).Scan(&archivedAt)
	if errors.Is(err, sql.ErrNoRows) || !archivedAt.Valid {
		return fmt.Errorf("%w: task is absent or not archived", ErrArchivedResourceReconcileConflict)
	}
	if err != nil {
		return fmt.Errorf("read archived task generation: %w", err)
	}
	expectedArchivedAt, _ := time.Parse(time.RFC3339Nano, snapshot.Immutable.ArchivedAt)
	if !archivedAt.Time.UTC().Equal(expectedArchivedAt) {
		return fmt.Errorf("%w: task archive generation drifted", ErrArchivedResourceReconcileConflict)
	}
	if err := validateArchivedTaskLivenessTx(ctx, tx, snapshot.Immutable.OriginTaskID); err != nil {
		return err
	}
	return nil
}

func validateArchivedTaskLivenessTx(ctx context.Context, tx *sqlx.Tx, taskID string) error {
	var nonterminal int
	if err := tx.QueryRowxContext(ctx, tx.Rebind(`
		SELECT COUNT(*) FROM task_sessions WHERE task_id = ? AND state NOT IN (?, ?, ?)`),
		taskID,
		models.TaskSessionStateCompleted, models.TaskSessionStateFailed, models.TaskSessionStateCancelled,
	).Scan(&nonterminal); err != nil {
		return fmt.Errorf("read task session liveness: %w", err)
	}
	if nonterminal != 0 {
		return fmt.Errorf("%w: task %s has nonterminal sessions", ErrArchivedResourceReconcileConflict, taskID)
	}
	var executors int
	if err := tx.QueryRowxContext(ctx, tx.Rebind(`
		SELECT COUNT(*) FROM executors_running WHERE task_id = ?`), taskID,
	).Scan(&executors); err != nil {
		return fmt.Errorf("read task executor liveness: %w", err)
	}
	if executors != 0 {
		return fmt.Errorf("%w: task %s has executor rows", ErrArchivedResourceReconcileConflict, taskID)
	}
	return nil
}

type archivedResourceAssociationCASToken struct {
	createdAt any
	updatedAt any
}

type archivedResourceAssociationRows struct {
	associations []models.ArchivedResourceReconcileAssociation
	casTokens    map[string]archivedResourceAssociationCASToken
}

func loadArchivedResourceAssociationsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	worktreeID string,
	lock bool,
) ([]models.ArchivedResourceReconcileAssociation, error) {
	loaded, err := loadArchivedResourceAssociationsWithCASTokensTx(ctx, tx, driver, worktreeID, lock)
	if err != nil {
		return nil, err
	}
	return loaded.associations, nil
}

func loadArchivedResourceAssociationsWithCASTokensTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	worktreeID string,
	lock bool,
) (*archivedResourceAssociationRows, error) {
	casColumns := archivedResourceAssociationCASColumns(driver)
	query := `
		SELECT ter.id, te.task_id, ` + archivedResourceSessionIDExpr + `,
			ter.worktree_id,
			ter.repository_id, COALESCE(ter.branch_slug, ''),
			ter.worktree_path, ter.worktree_branch, ter.status,
			ter.created_at, ter.updated_at, ` + casColumns + `
		FROM task_environment_repos ter
		LEFT JOIN task_environments te ON te.id = ter.task_environment_id
		WHERE ter.worktree_id = ?
		  AND ter.status = 'active' AND ter.deleted_at IS NULL
		ORDER BY ter.id ASC, ter.updated_at ASC`
	if lock {
		query += reconcileForUpdateOf(driver, "ter")
	}
	rows, err := tx.QueryxContext(ctx, tx.Rebind(query), worktreeID)
	if err != nil {
		return nil, fmt.Errorf("read archived resource associations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	loaded := &archivedResourceAssociationRows{
		associations: make([]models.ArchivedResourceReconcileAssociation, 0),
		casTokens:    make(map[string]archivedResourceAssociationCASToken),
	}
	for rows.Next() {
		var association models.ArchivedResourceReconcileAssociation
		var taskID sql.NullString
		var createdAt, updatedAt time.Time
		var createdAtToken, updatedAtToken any
		if err := rows.Scan(
			&association.AssociationID, &taskID, &association.SessionID,
			&association.WorktreeID, &association.RepositoryID, &association.BranchSlug,
			&association.WorktreePath, &association.WorktreeBranch, &association.Status,
			&createdAt, &updatedAt, &createdAtToken, &updatedAtToken,
		); err != nil {
			return nil, err
		}
		if !taskID.Valid || taskID.String == "" {
			return nil, fmt.Errorf("%w: association %s has unknown task ownership", ErrArchivedResourceReconcileConflict, association.AssociationID)
		}
		association.TaskID = taskID.String
		association.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		association.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
		loaded.associations = append(loaded.associations, association)
		loaded.casTokens[association.AssociationID] = archivedResourceAssociationCASToken{
			createdAt: createdAtToken,
			updatedAt: updatedAtToken,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return loaded, nil
}

func archivedResourceAssociationCASColumns(driver string) string {
	if dialect.IsPostgres(driver) {
		return "ter.created_at, ter.updated_at"
	}
	return "CAST(ter.created_at AS TEXT), CAST(ter.updated_at AS TEXT)"
}

func compareArchivedResourceAssociations(
	expected []models.ArchivedResourceReconcileAssociation,
	actual []models.ArchivedResourceReconcileAssociation,
) error {
	expected = append([]models.ArchivedResourceReconcileAssociation(nil), expected...)
	actual = append([]models.ArchivedResourceReconcileAssociation(nil), actual...)
	sort.Slice(expected, func(i, j int) bool { return associationIdentityLess(expected[i], expected[j]) })
	sort.Slice(actual, func(i, j int) bool { return associationIdentityLess(actual[i], actual[j]) })
	if len(expected) != len(actual) {
		return fmt.Errorf("%w: association set size got %d want %d", ErrArchivedResourceReconcileConflict, len(actual), len(expected))
	}
	for i := range expected {
		if !sameArchivedResourceAssociationGeneration(expected[i], actual[i]) {
			return fmt.Errorf("%w: association generation %q drifted", ErrArchivedResourceReconcileConflict, expected[i].AssociationID)
		}
	}
	return nil
}

func sameArchivedResourceAssociationGeneration(
	expected models.ArchivedResourceReconcileAssociation,
	actual models.ArchivedResourceReconcileAssociation,
) bool {
	expectedCreated, createdErr := time.Parse(time.RFC3339Nano, expected.CreatedAt)
	actualCreated, actualCreatedErr := time.Parse(time.RFC3339Nano, actual.CreatedAt)
	expectedUpdated, updatedErr := time.Parse(time.RFC3339Nano, expected.UpdatedAt)
	actualUpdated, actualUpdatedErr := time.Parse(time.RFC3339Nano, actual.UpdatedAt)
	if createdErr != nil || actualCreatedErr != nil || updatedErr != nil || actualUpdatedErr != nil {
		return false
	}
	expected.CreatedAt, actual.CreatedAt = "", ""
	expected.UpdatedAt, actual.UpdatedAt = "", ""
	return expected == actual && expectedCreated.Equal(actualCreated) && expectedUpdated.Equal(actualUpdated)
}

func associationIdentityLess(
	left models.ArchivedResourceReconcileAssociation,
	right models.ArchivedResourceReconcileAssociation,
) bool {
	if left.AssociationID != right.AssociationID {
		return left.AssociationID < right.AssociationID
	}
	if left.SessionID != right.SessionID {
		return left.SessionID < right.SessionID
	}
	return left.UpdatedAt < right.UpdatedAt
}

func completionTimestamp(
	associations []models.ArchivedResourceReconcileAssociation,
	now time.Time,
) time.Time {
	result := now.UTC()
	for _, association := range associations {
		updatedAt, err := time.Parse(time.RFC3339Nano, association.UpdatedAt)
		if err == nil && !result.After(updatedAt) {
			result = updatedAt.Add(time.Microsecond)
		}
	}
	return result
}

func reconcileForUpdate(driver string) string {
	if dialect.IsPostgres(driver) {
		return " FOR UPDATE"
	}
	return ""
}

func reconcileForUpdateOf(driver, alias string) string {
	if dialect.IsPostgres(driver) {
		return " FOR UPDATE OF " + alias
	}
	return ""
}

// tombstoneArchivedResourceAssociationsTx writes the deletion generation only
// when every exact persisted CAS token still matches the snapshot the
// reconciliation admission captured. Any drift returns a fail-closed error and
// leaves the durable state untouched.
func tombstoneArchivedResourceAssociationsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	associations []models.ArchivedResourceReconcileAssociation,
	casTokens map[string]archivedResourceAssociationCASToken,
	now time.Time,
) error {
	for _, association := range associations {
		token, ok := casTokens[association.AssociationID]
		if !ok {
			return fmt.Errorf("%w: association %s has no captured CAS token", ErrArchivedResourceReconcileConflict, association.AssociationID)
		}
		createdAt, updatedAt, createdToken, updatedToken, err := archivedResourceRowTokens(driver, association.CreatedAt, association.UpdatedAt, token)
		if err != nil {
			return err
		}
		query := buildArchivedResourceTombstoneSQL(driver)
		result, err := tx.ExecContext(ctx, tx.Rebind(query),
			now, now,
			association.AssociationID,
			association.SessionID,
			association.WorktreeID,
			association.RepositoryID, association.BranchSlug,
			association.WorktreePath, association.WorktreeBranch,
			createdAt, updatedAt, createdToken, updatedToken,
		)
		if err != nil {
			return fmt.Errorf("tombstone association %s: %w", association.AssociationID, err)
		}
		count, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("read tombstoned association %s affected rows: %w", association.AssociationID, rowsErr)
		}
		if count != 1 {
			return fmt.Errorf("%w: association %s CAS drifted", ErrArchivedResourceReconcileConflict, association.AssociationID)
		}
	}
	return nil
}

func buildArchivedResourceTombstoneSQL(driver string) string {
	if dialect.IsPostgres(driver) {
		return `
			UPDATE task_environment_repos
			SET status = 'deleted', deleted_at = $1, updated_at = $2
			WHERE id = $3 AND task_environment_id = $4 AND worktree_id = $5
			  AND repository_id = $6 AND COALESCE(branch_slug, '') = $7
			  AND worktree_path = $8 AND worktree_branch = $9
			  AND status = 'active' AND deleted_at IS NULL
			  AND created_at = $10 AND updated_at = $11`
	}
	return `
		UPDATE task_environment_repos
		SET status = 'deleted', deleted_at = ?, updated_at = ?
		WHERE id = ? AND task_environment_id = ? AND worktree_id = ?
		  AND repository_id = ? AND COALESCE(branch_slug, '') = ?
		  AND worktree_path = ? AND worktree_branch = ?
		  AND status = 'active' AND deleted_at IS NULL
		  AND created_at = ? AND updated_at = ?`
}

func archivedResourceRowTokens(
	driver string,
	createdAtValue string,
	updatedAtValue string,
	token archivedResourceAssociationCASToken,
) (time.Time, time.Time, any, any, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return time.Time{}, time.Time{}, nil, nil, fmt.Errorf("%w: parse association created_at: %v", ErrArchivedResourceReconcileConflict, err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtValue)
	if err != nil {
		return time.Time{}, time.Time{}, nil, nil, fmt.Errorf("%w: parse association updated_at: %v", ErrArchivedResourceReconcileConflict, err)
	}
	if dialect.IsPostgres(driver) {
		return createdAt, updatedAt, nil, nil, nil
	}
	return time.Time{}, time.Time{}, strings.TrimSpace(asString(token.createdAt)), strings.TrimSpace(asString(token.updatedAt)), nil
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return fmt.Sprintf("%v", value)
}
