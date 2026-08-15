package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

// taskCleanupBarrierLocked serializes session/worktree creation against task
// lifecycle cleanup (ADR-2026-08-08). PostgreSQL takes a row lock on the
// owning task so a concurrent barrier reservation either commits before the
// creation and is observed, or blocks until the creation commits; SQLite's
// single-writer transaction is the serialization. Returns
// repoerrors.ErrTaskCleanupInProgress when a prepared/pending/running cleanup
// barrier exists for the task.
func (r *Repository) taskCleanupBarrierLocked(ctx context.Context, tx *sqlx.Tx, taskID string) error {
	if err := r.lockTaskCleanupDomainLocked(ctx, tx, taskID); err != nil {
		return err
	}

	var active bool
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT EXISTS (
			SELECT 1 FROM task_resource_cleanup_jobs
			WHERE task_id = ? AND state IN (?, ?, ?, ?)
		)
	`), taskID,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait,
	).Scan(&active); err != nil {
		return fmt.Errorf("check task cleanup barrier: %w", err)
	}
	if active {
		return fmt.Errorf("%w: %s", repoerrors.ErrTaskCleanupInProgress, taskID)
	}
	return nil
}

// lockTaskCleanupDomainLocked is the shared linearization point for both
// ownership mutations and cleanup-barrier reservation. PostgreSQL takes the
// exact task row FOR UPDATE; SQLite executes inside the process-wide single
// writer transaction, so the same query validates the task identity while the
// writer connection remains exclusively held.
func (r *Repository) lockTaskCleanupDomainLocked(ctx context.Context, tx *sqlx.Tx, taskID string) error {
	query := `SELECT id FROM tasks WHERE id = ?`
	if dialect.IsPostgres(r.db.DriverName()) {
		query += ` FOR UPDATE`
	}
	var lockedTaskID string
	if err := tx.QueryRowContext(ctx, r.db.Rebind(query), taskID).Scan(&lockedTaskID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		}
		return fmt.Errorf("lock task cleanup domain: %w", err)
	}
	if lockedTaskID != taskID {
		return fmt.Errorf("lock task cleanup domain: identity drift")
	}
	return nil
}
