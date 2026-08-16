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

const taskResourceCleanupColumns = `
	id, operation_id, task_id, trigger, state, resource_snapshot, snapshot_version,
	snapshot_digest, resource_kind, resource_id, managed_root_key, anchor_revision,
	active_scope_key, attempts, next_attempt_at, last_error, created_at, updated_at, completed_at`

func (r *Repository) CreateTaskResourceCleanupJob(ctx context.Context, job *models.TaskResourceCleanupJob) error {
	if job == nil {
		return fmt.Errorf("task resource cleanup job is required")
	}
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.State == "" {
		job.State = models.TaskResourceCleanupStatePending
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin task resource cleanup reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if taskCleanupBarrierState(job.State) {
		// Reservation for a live task takes the same task-row lock as every
		// ownership mutation, before inspecting operation replay or active jobs. A
		// mutation that commits first is therefore visible before this INSERT;
		// a reservation that commits first is visible to the mutation's barrier
		// read. A replay after logical task deletion remains a zero-write no-op.
		if err := r.lockTaskCleanupDomainLocked(ctx, tx, job.TaskID); err != nil {
			if !errors.Is(err, ErrTaskNotFound) {
				return err
			}
			replay, replayErr := r.taskResourceCleanupOperationExistsLocked(ctx, tx, job.OperationID)
			if replayErr != nil {
				return replayErr
			}
			if replay {
				return tx.Commit()
			}
			// Durable cleanup jobs intentionally outlive task deletion. Recovery
			// and late workspace-cascade capture may therefore persist a job after
			// the exact logical task row is gone. There is then no live ownership
			// mutation domain to race; preserve that established behavior, but
			// re-read active-job semantics in this same writer transaction before
			// inserting the orphan recovery intent.
			active, activeErr := r.activeTaskResourceCleanupJobLocked(ctx, tx, job.TaskID)
			if activeErr != nil {
				return activeErr
			}
			if active {
				return fmt.Errorf("%w: %s", repoerrors.ErrTaskCleanupInProgress, job.TaskID)
			}
			// Continue to the insert below.
		} else {
			replay, err := r.taskResourceCleanupOperationExistsLocked(ctx, tx, job.OperationID)
			if err != nil {
				return err
			}
			if replay {
				return tx.Commit()
			}
			active, err := r.activeTaskResourceCleanupJobLocked(ctx, tx, job.TaskID)
			if err != nil {
				return err
			}
			if active {
				return fmt.Errorf("%w: %s", repoerrors.ErrTaskCleanupInProgress, job.TaskID)
			}
		}
	}

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
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read task resource cleanup reservation result: %w", err)
	}
	if rows == 0 {
		replay, replayErr := r.taskResourceCleanupOperationExistsLocked(ctx, tx, job.OperationID)
		if replayErr != nil {
			return replayErr
		}
		if !replay {
			return fmt.Errorf("task resource cleanup reservation lost without operation replay")
		}
	} else if rows != 1 {
		return fmt.Errorf("task resource cleanup reservation affected %d rows", rows)
	}
	return tx.Commit()
}

func taskCleanupBarrierState(state models.TaskResourceCleanupState) bool {
	return state == models.TaskResourceCleanupStatePrepared ||
		state == models.TaskResourceCleanupStatePending ||
		state == models.TaskResourceCleanupStateRunning ||
		state == models.TaskResourceCleanupStateRetryWait
}

func (r *Repository) taskResourceCleanupOperationExistsLocked(
	ctx context.Context,
	tx *sqlx.Tx,
	operationID string,
) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT EXISTS (
			SELECT 1 FROM task_resource_cleanup_jobs WHERE operation_id = ?
		)`), operationID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check task resource cleanup operation replay: %w", err)
	}
	return exists, nil
}

func (r *Repository) activeTaskResourceCleanupJobLocked(
	ctx context.Context,
	tx *sqlx.Tx,
	taskID string,
) (bool, error) {
	var active bool
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT EXISTS (
			SELECT 1 FROM task_resource_cleanup_jobs
			WHERE task_id = ? AND state IN (?, ?, ?, ?)
		)`), taskID,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait,
	).Scan(&active); err != nil {
		return false, fmt.Errorf("check active task resource cleanup reservation: %w", err)
	}
	return active, nil
}

// UpdateTaskResourceCleanupSnapshot writes the resource inventory captured
// after the prepared barrier was reserved. The barrier row exists before the
// inventory query so concurrent session/worktree creation is rejected while
// the snapshot is being assembled.
func (r *Repository) UpdateTaskResourceCleanupSnapshot(ctx context.Context, operationID, snapshot string) error {
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET resource_snapshot = ?, updated_at = ?
		WHERE operation_id = ? AND state = ?
	`), snapshot, time.Now().UTC(), operationID, models.TaskResourceCleanupStatePrepared)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("task resource cleanup job %s not found or not prepared", operationID)
	}
	return nil
}

// HasActiveTaskResourceCleanupJob reports whether teardown has been admitted
// for a task. The prepared state is included because the cleanup intent is
// persisted before task deletion and before the worker is allowed to run.
func (r *Repository) HasActiveTaskResourceCleanupJob(ctx context.Context, taskID string) (bool, error) {
	var active bool
	err := r.ro.QueryRowContext(ctx, r.ro.Rebind(`
		SELECT EXISTS (
			SELECT 1 FROM task_resource_cleanup_jobs
			WHERE task_id = ? AND state IN (?, ?, ?, ?)
		)
	`), taskID,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait,
	).Scan(&active)
	return active, err
}

func (r *Repository) GetTaskResourceCleanupJobByOperationID(ctx context.Context, operationID string) (*models.TaskResourceCleanupJob, error) {
	row := r.ro.QueryRowContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs WHERE operation_id = ?
	`), operationID)
	return scanTaskResourceCleanupJob(row)
}

func (r *Repository) GetTaskResourceCleanupJob(ctx context.Context, id string) (*models.TaskResourceCleanupJob, error) {
	row := r.ro.QueryRowContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs WHERE id = ?
	`), id)
	return scanTaskResourceCleanupJob(row)
}

func (r *Repository) ListPreparedTaskResourceCleanupJobs(ctx context.Context) ([]*models.TaskResourceCleanupJob, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE state = ? ORDER BY created_at ASC
	`), models.TaskResourceCleanupStatePrepared)
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

func scanTaskResourceCleanupJob(row interface{ Scan(...any) error }) (*models.TaskResourceCleanupJob, error) {
	job := &models.TaskResourceCleanupJob{}
	err := row.Scan(&job.ID, &job.OperationID, &job.TaskID, &job.Trigger, &job.State,
		&job.ResourceSnapshot, &job.SnapshotVersion, &job.SnapshotDigest,
		&job.ResourceKind, &job.ResourceID, &job.ManagedRootKey, &job.AnchorRevision,
		&job.ActiveScopeKey, &job.Attempts, &job.NextAttemptAt, &job.LastError,
		&job.CreatedAt, &job.UpdatedAt, &job.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task resource cleanup job not found")
	}
	return job, err
}

func (r *Repository) ListDueTaskResourceCleanupJobs(ctx context.Context, now time.Time, limit int) ([]*models.TaskResourceCleanupJob, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE state = ? OR (state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
		ORDER BY created_at ASC LIMIT ?
	`), models.TaskResourceCleanupStatePending, models.TaskResourceCleanupStateRetryWait, now.UTC(), limit)
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

func (r *Repository) MarkTaskResourceCleanupJobRunning(ctx context.Context, id string) (bool, error) {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, attempts = attempts + 1, next_attempt_at = NULL, updated_at = ?
		WHERE id = ? AND state IN (?, ?)
	`), models.TaskResourceCleanupStateRunning, now, id,
		models.TaskResourceCleanupStatePending, models.TaskResourceCleanupStateRetryWait)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

func (r *Repository) StartPreparedTaskResourceCleanupJob(ctx context.Context, id string) (bool, error) {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, next_attempt_at = NULL, updated_at = ?
		WHERE id = ? AND state = ?
	`), models.TaskResourceCleanupStatePending, now, id, models.TaskResourceCleanupStatePrepared)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

func (r *Repository) CompleteTaskResourceCleanupJob(ctx context.Context, id string, state models.TaskResourceCleanupState, lastError string, nextAttemptAt *time.Time) error {
	now := time.Now().UTC()
	var completedAt *time.Time
	if state == models.TaskResourceCleanupStateSucceeded ||
		state == models.TaskResourceCleanupStateFailed ||
		state == models.TaskResourceCleanupStateCancelled {
		completedAt = &now
	}
	_, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, last_error = ?, next_attempt_at = ?, completed_at = ?, updated_at = ?
		WHERE id = ?
	`), state, lastError, nextAttemptAt, completedAt, now, id)
	return err
}

// CompleteClaimedTaskResourceCleanupJob applies a worker result only to the
// exact running claim that produced it. A concurrent cancellation or a newer
// retry generation wins and keeps its state and historical metadata.
func (r *Repository) CompleteClaimedTaskResourceCleanupJob(
	ctx context.Context,
	id string,
	attempt int,
	state models.TaskResourceCleanupState,
	lastError string,
	nextAttemptAt *time.Time,
) (bool, error) {
	now := time.Now().UTC()
	var completedAt *time.Time
	if state == models.TaskResourceCleanupStateSucceeded ||
		state == models.TaskResourceCleanupStateFailed ||
		state == models.TaskResourceCleanupStateCancelled {
		completedAt = &now
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, last_error = ?, next_attempt_at = ?, completed_at = ?, updated_at = ?
		WHERE id = ? AND state = ? AND attempts = ?
	`), state, lastError, nextAttemptAt, completedAt, now, id,
		models.TaskResourceCleanupStateRunning, attempt)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

func (r *Repository) CancelArchiveTaskResourceCleanupJobs(ctx context.Context, taskID string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, completed_at = ?, updated_at = ?
		WHERE task_id = ? AND trigger IN (?, ?) AND state IN (?, ?, ?, ?)
	`), models.TaskResourceCleanupStateCancelled, now, now, taskID,
		models.TaskResourceCleanupTriggerArchive, models.TaskResourceCleanupTriggerCascadeArchive,
		models.TaskResourceCleanupStatePrepared, models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait)
	return err
}

func (r *Repository) ResetRunningTaskResourceCleanupJobs(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, next_attempt_at = ?, updated_at = ? WHERE state = ?
	`), models.TaskResourceCleanupStateRetryWait, now, now, models.TaskResourceCleanupStateRunning)
	return err
}
