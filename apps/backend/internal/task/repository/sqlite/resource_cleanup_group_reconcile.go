package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

// AdmitArchivedResourceGroupReconcile persists one complete v3 worktree group.
func (r *Repository) AdmitArchivedResourceGroupReconcile(
	ctx context.Context,
	job *models.TaskResourceCleanupJob,
) (*models.ArchivedResourceReconcileAdmission, error) {
	snapshot, _, err := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
	if err != nil {
		return nil, err
	}
	if !isPristineArchivedResourceJob(job) {
		return nil, fmt.Errorf("%w: new group job is not pristine", ErrArchivedResourceReconcileConflict)
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin group reconcile admission: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if replay, replayErr := findArchivedResourceGroupReplay(ctx, tx, r.db.DriverName(), job); replayErr != nil || replay != nil {
		if replayErr != nil {
			return nil, replayErr
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit group reconcile replay: %w", err)
		}
		return &models.ArchivedResourceReconcileAdmission{Job: replay, Created: false}, nil
	}
	if err := validateArchivedResourceGroupGenerationAndRuntimeTx(ctx, tx, r.db.DriverName(), snapshot); err != nil {
		return nil, err
	}
	associations, err := loadArchivedResourceAssociationsTx(ctx, tx, r.db.DriverName(), snapshot.Immutable.Target.WorktreeID, true)
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
	job.CreatedAt, job.UpdatedAt = now, now
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
		return nil, fmt.Errorf("insert group reconcile anchor: %w", err)
	}
	count, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return nil, fmt.Errorf("read admitted group anchor affected rows: %w", rowsErr)
	}
	if count != 1 {
		return nil, fmt.Errorf("%w: group operation or scope exists", ErrArchivedResourceReconcileConflict)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit group reconcile admission: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
	}
	return &models.ArchivedResourceReconcileAdmission{Job: job, Created: true}, nil
}

func (r *Repository) CompleteArchivedResourceGroupReconcileRetention(
	ctx context.Context,
	id string,
	attempt int,
) (*models.ArchivedResourceReconcileCompletion, error) {
	completion, err := r.completeArchivedResourceGroupReconcileRetentionTx(ctx, id, attempt)
	if err == nil || errors.Is(err, repoerrors.ErrTransactionOutcomeUnknown) ||
		!errors.Is(err, ErrArchivedResourceReconcileConflict) {
		return completion, err
	}
	return nil, r.blockDeterministicArchivedResourceCompletion(ctx, id, attempt, err)
}

func (r *Repository) completeArchivedResourceGroupReconcileRetentionTx(
	ctx context.Context,
	id string,
	attempt int,
) (*models.ArchivedResourceReconcileCompletion, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin group reconcile completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	job, err := loadArchivedResourceJobByIDTx(ctx, tx, r.db.DriverName(), id)
	if err != nil {
		return nil, err
	}
	snapshot, _, err := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
	if err != nil {
		return nil, err
	}
	if job.State == models.TaskResourceCleanupStateRetained {
		if job.Attempts != attempt || attempt <= 0 {
			return nil, fmt.Errorf("%w: group replay claim drifted", ErrArchivedResourceReconcileConflict)
		}
		if err := rejectActiveHistoricalAssociationsTx(ctx, tx, r.db.DriverName(), snapshot.Immutable.Target.WorktreeID, snapshot.Immutable.Associations); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.ArchivedResourceReconcileCompletion{Job: job, AssociationsUnbound: len(snapshot.Immutable.Associations), Replayed: true}, nil
	}
	if job.State != models.TaskResourceCleanupStateRunning || job.Attempts != attempt || attempt <= 0 {
		return nil, fmt.Errorf("%w: running group claim drifted", ErrArchivedResourceReconcileConflict)
	}
	if err := validateArchivedResourceGroupGenerationAndRuntimeTx(ctx, tx, r.db.DriverName(), snapshot); err != nil {
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
	if err := retainArchivedResourceJobTx(ctx, tx, r.db.DriverName(), job, attempt, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit group reconcile retention: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
	}
	job.State = models.TaskResourceCleanupStateRetained
	job.AnchorRevision++
	job.CompletedAt, job.UpdatedAt = &now, now
	return &models.ArchivedResourceReconcileCompletion{Job: job, AssociationsUnbound: len(snapshot.Immutable.Associations)}, nil
}

// RestoreArchivedResourceGroupReconcileRetention restores every association in
// each retained group containing participantTaskID. It never restores a subset.
func (r *Repository) RestoreArchivedResourceGroupReconcileRetention(
	ctx context.Context,
	participantTaskID string,
) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, fmt.Errorf("begin group reconcile restoration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	jobs, err := loadRetainedArchivedResourceGroupJobsTx(ctx, tx, r.db.DriverName())
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	restored := false
	for _, job := range jobs {
		snapshot, _, validateErr := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
		if validateErr != nil {
			return false, validateErr
		}
		if !groupContainsTask(snapshot.Immutable.Tasks, participantTaskID) {
			continue
		}
		if job.CompletedAt == nil {
			return false, fmt.Errorf("%w: retained group has no completion generation", ErrArchivedResourceReconcileConflict)
		}
		if err := validateOrRestoreArchivedResourceAssociationsTx(ctx, tx, r.db.DriverName(), snapshot.Immutable.Associations, job.CompletedAt.UTC(), now, true); err != nil {
			return false, err
		}
		restored = true
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit group reconcile restoration: %w", err)
	}
	return restored, nil
}

// ListArchivedResourceGroupReconcileJobsByParticipant returns every active v3
// anchor whose immutable task inventory contains participantTaskID. Callers use
// the complete snapshots to acquire one stable process-local lock set before
// unarchive or worker transitions. Malformed anchors fail the whole inventory.
func (r *Repository) ListArchivedResourceGroupReconcileJobsByParticipant(
	ctx context.Context,
	participantTaskID string,
) ([]*models.TaskResourceCleanupJob, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+` FROM task_resource_cleanup_jobs
		WHERE trigger=? AND snapshot_version=?
		  AND state IN (?, ?, ?, ?, ?, ?)
		ORDER BY created_at ASC, id ASC
	`), models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceGroupReconcileSnapshotVersion,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait,
		models.TaskResourceCleanupStateRetained,
		models.TaskResourceCleanupStateBlocked)
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
		snapshot, _, validateErr := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
		if validateErr != nil {
			return nil, validateErr
		}
		if groupContainsTask(snapshot.Immutable.Tasks, participantTaskID) {
			jobs = append(jobs, job)
		}
	}
	return jobs, rows.Err()
}

func loadRetainedArchivedResourceGroupJobsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
) ([]*models.TaskResourceCleanupJob, error) {
	rows, err := tx.QueryxContext(ctx, tx.Rebind(`
		SELECT `+taskResourceCleanupColumns+` FROM task_resource_cleanup_jobs
		WHERE trigger=? AND snapshot_version=? AND state=?
		ORDER BY created_at DESC, id DESC`+reconcileForUpdate(driver)),
		models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceGroupReconcileSnapshotVersion,
		models.TaskResourceCleanupStateRetained)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []*models.TaskResourceCleanupJob
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func groupContainsTask(tasks []models.ArchivedResourceGroupReconcileTask, taskID string) bool {
	for _, task := range tasks {
		if task.TaskID == taskID {
			return true
		}
	}
	return false
}

func findArchivedResourceGroupReplay(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	job *models.TaskResourceCleanupJob,
) (*models.TaskResourceCleanupJob, error) {
	existing, err := loadArchivedResourceJobByOperationTx(ctx, tx, driver, job.OperationID)
	if err == nil {
		if _, _, validateErr := models.ValidateArchivedResourceGroupReconcileJobHeaders(existing); validateErr != nil ||
			existing.ResourceSnapshot != job.ResourceSnapshot || existing.SnapshotDigest != job.SnapshotDigest {
			return nil, fmt.Errorf("%w: group operation is bound to different state", ErrArchivedResourceReconcileConflict)
		}
		return existing, nil
	}
	if !errors.Is(err, ErrArchivedResourceReconcileNotFound) {
		return nil, err
	}
	existing, err = loadArchivedResourceJobByScopeTx(ctx, tx, driver, *job.ActiveScopeKey)
	if err == nil {
		if _, _, validateErr := models.ValidateArchivedResourceGroupReconcileJobHeaders(existing); validateErr == nil &&
			existing.OperationID == job.OperationID && existing.ResourceSnapshot == job.ResourceSnapshot {
			return existing, nil
		}
		return nil, fmt.Errorf("%w: group active scope is bound to another operation", ErrArchivedResourceReconcileConflict)
	}
	if !errors.Is(err, ErrArchivedResourceReconcileNotFound) {
		return nil, err
	}
	return nil, nil
}

func validateArchivedResourceGroupGenerationAndRuntimeTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	snapshot models.ArchivedResourceGroupReconcileSnapshot,
) error {
	for _, expected := range snapshot.Immutable.Tasks {
		var archivedAt sql.NullTime
		err := tx.QueryRowxContext(ctx, tx.Rebind(`SELECT archived_at FROM tasks WHERE id = ?`+reconcileForUpdate(driver)), expected.TaskID).Scan(&archivedAt)
		if errors.Is(err, sql.ErrNoRows) || !archivedAt.Valid {
			return fmt.Errorf("%w: group task %s is absent or active", ErrArchivedResourceReconcileConflict, expected.TaskID)
		}
		if err != nil {
			return fmt.Errorf("read group task generation: %w", err)
		}
		expectedTime, _ := time.Parse(time.RFC3339Nano, expected.ArchivedAt)
		if !archivedAt.Time.UTC().Equal(expectedTime) {
			return fmt.Errorf("%w: group task %s archive generation drifted", ErrArchivedResourceReconcileConflict, expected.TaskID)
		}
		if err := validateArchivedTaskLivenessTx(ctx, tx, expected.TaskID); err != nil {
			return err
		}
	}
	return nil
}

func rejectActiveHistoricalAssociationsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	worktreeID string,
	expected []models.ArchivedResourceReconcileAssociation,
) error {
	actual, err := loadArchivedResourceAssociationsTx(ctx, tx, driver, worktreeID, true)
	if err != nil {
		return err
	}
	for _, row := range actual {
		for _, historical := range expected {
			if row.AssociationID == historical.AssociationID && row.UpdatedAt == historical.UpdatedAt {
				return fmt.Errorf("%w: historical group association is active", ErrArchivedResourceReconcileConflict)
			}
		}
	}
	return nil
}

func retainArchivedResourceJobTx(
	ctx context.Context,
	tx *sqlx.Tx,
	_ string,
	job *models.TaskResourceCleanupJob,
	attempt int,
	now time.Time,
) error {
	result, err := tx.ExecContext(ctx, tx.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state=?, anchor_revision=anchor_revision+1, last_error='', next_attempt_at=NULL, completed_at=?, updated_at=?
		WHERE id=? AND trigger=? AND state=? AND attempts=? AND snapshot_version=? AND snapshot_digest=?
		  AND resource_kind=? AND resource_id=? AND managed_root_key=? AND anchor_revision=?
		  AND active_scope_key=? AND resource_snapshot=?
	`), models.TaskResourceCleanupStateRetained, now, now, job.ID,
		models.TaskResourceCleanupTriggerReconcile, models.TaskResourceCleanupStateRunning,
		attempt, job.SnapshotVersion, job.SnapshotDigest, job.ResourceKind, job.ResourceID,
		job.ManagedRootKey, job.AnchorRevision, job.ActiveScopeKey, job.ResourceSnapshot)
	if err != nil {
		return fmt.Errorf("retain group reconcile anchor: %w", err)
	}
	count, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("read group anchor affected rows: %w", rowsErr)
	}
	if count != 1 {
		return fmt.Errorf("%w: group cleanup job generation drifted", ErrArchivedResourceReconcileConflict)
	}
	return nil
}
