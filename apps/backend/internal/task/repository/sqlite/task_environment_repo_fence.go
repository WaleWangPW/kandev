package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

func (r *Repository) mutableEnvironmentRepoByIDLocked(
	ctx context.Context,
	tx *sqlx.Tx,
	repoID string,
) error {
	return r.mutableEnvironmentRepoTaskLocked(ctx, tx, `ter.id = ?`, repoID)
}

func (r *Repository) mutableEnvironmentReposByEnvironmentLocked(
	ctx context.Context,
	tx *sqlx.Tx,
	environmentID string,
) error {
	var taskID string
	query := `
		SELECT task.id
		FROM task_environments environment
		INNER JOIN tasks task ON task.id = environment.task_id
		WHERE environment.id = ?`
	if err := tx.QueryRowContext(ctx, r.db.Rebind(query), environmentID).Scan(&taskID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("environment-repository mutation fenced: logical parent is absent")
		}
		return fmt.Errorf("resolve environment-repository mutation owner: %w", err)
	}
	return r.taskCleanupBarrierLocked(ctx, tx, taskID)
}

func (r *Repository) mutableEnvironmentReposBySessionLocked(
	ctx context.Context,
	tx *sqlx.Tx,
	sessionID string,
) error {
	return r.mutableEnvironmentRepoTaskLocked(ctx, tx, `EXISTS (
		SELECT 1 FROM task_sessions session
		WHERE session.id = ? AND session.task_environment_id = ter.task_environment_id
	)`, sessionID)
}

func (r *Repository) mutableEnvironmentRepoTaskLocked(
	ctx context.Context,
	tx *sqlx.Tx,
	predicate string,
	argument string,
) error {
	var taskID string
	query := `
		SELECT task.id
		FROM task_environment_repos ter
		INNER JOIN task_environments environment ON environment.id = ter.task_environment_id
		INNER JOIN tasks task ON task.id = environment.task_id
		WHERE ` + predicate + `
		LIMIT 1`
	if err := tx.QueryRowContext(ctx, r.db.Rebind(query), argument).Scan(&taskID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("environment-repository mutation fenced: logical parent is absent")
		}
		return fmt.Errorf("resolve environment-repository mutation owner: %w", err)
	}
	return r.taskCleanupBarrierLocked(ctx, tx, taskID)
}
