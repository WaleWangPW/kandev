package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
)

var (
	ErrArchivedResourceEnvironmentRetirementNotAdmitted   = errors.New("archived resource environment retirement was not admitted")
	ErrArchivedResourceEnvironmentRetirementParticipant   = errors.New("archived resource environment is a participant in an active cleanup")
	ErrArchivedResourceEnvironmentRetirementUnknownRow    = errors.New("archived resource environment retirement target row is unknown")
	ErrArchivedResourceEnvironmentRetirementIdentityDrift = errors.New("archived resource environment retirement identity drifted")
)

// KnownWorkspaceGroupRetirementStates are the four workspace-group inventory
// states accepted by the retirement admission (docs/specs/tasks/archived-
// resource-safety/spec.md). Any other value, including NULL or empty, causes
// fail-closed zero mutation.
var KnownWorkspaceGroupRetirementStates = []string{"active", "cleanup_pending", "cleaned", "cleanup_failed"}

// RetireStaleArchivedResourceEnvironmentReference performs the exact-set
// environment retirement transaction. The supplied job carries the canonical
// retirement snapshot in resource_snapshot plus the redundant headers. The
// repository:
//
//  1. decodes and validates the retirement snapshot;
//  2. confirms every workspace-group inventory state is one of the four
//     known states (unknown / malformed rows cause fail-closed zero mutation);
//  3. confirms no active v2 or v3 reconcile anchor treats the environment as a
//     participant, including non-coordinator v3 task participants;
//  4. deletes only the exact task_environment_repos rows captured by admission
//     inside a single serializable transaction.
//
// No filesystem, Git, runtime, or retained-anchor mutation occurs.
func (r *Repository) RetireStaleArchivedResourceEnvironmentReference(
	ctx context.Context,
	job *models.TaskResourceCleanupJob,
) (*models.ArchivedResourceEnvironmentRetirementIdentity, error) {
	if job == nil {
		return nil, fmt.Errorf("%w: nil retirement request", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
	}
	snapshot, identity, err := models.DecodeArchivedResourceEnvironmentRetirementSnapshot([]byte(job.ResourceSnapshot))
	if err != nil {
		return nil, err
	}
	if err := validateRetirementHeaders(job, snapshot, identity); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin retirement barrier: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := requireWorkspaceGroupInventoryKnownTx(ctx, tx); err != nil {
		return nil, err
	}

	if err := requireRetirementEnvironmentStoppedOrFailedTx(ctx, tx, snapshot); err != nil {
		return nil, err
	}

	if err := requireRetirementEnvironmentNotActiveParticipantTx(
		ctx, tx, snapshot.Immutable.EnvironmentID, snapshot.Immutable.TaskID,
	); err != nil {
		return nil, err
	}

	if err := deleteRetirementEnvironmentReposTx(ctx, tx, snapshot); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit retirement barrier: %v", ErrArchivedResourceEnvironmentRetirementIdentityDrift, err)
	}
	return &identity, nil
}

func validateRetirementHeaders(
	job *models.TaskResourceCleanupJob,
	snapshot models.ArchivedResourceEnvironmentRetirementSnapshot,
	identity models.ArchivedResourceEnvironmentRetirementIdentity,
) error {
	if job == nil {
		return fmt.Errorf("%w: nil retirement request", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
	}
	if job.Trigger != models.TaskResourceCleanupTriggerReconcile {
		return fmt.Errorf("%w: retirement trigger must be reconcile", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
	}
	if job.SnapshotVersion != models.ArchivedResourceEnvironmentRetirementSnapshotVersion {
		return fmt.Errorf("%w: retirement snapshot_version must match", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
	}
	if job.OperationID != identity.OperationID ||
		job.SnapshotDigest != identity.SnapshotDigest ||
		job.ResourceKind != identity.ResourceKind ||
		job.ResourceID != identity.ResourceID ||
		job.ManagedRootKey != identity.ManagedRootKey {
		return fmt.Errorf("%w: retirement headers do not bind retirement identity", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
	}
	if job.TaskID != snapshot.Immutable.TaskID {
		return fmt.Errorf("%w: retirement header task_id does not bind retirement task", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
	}
	return nil
}

// requireWorkspaceGroupInventoryKnownTx fails closed when any
// task_workspace_groups row has a state outside the four accepted values.
// The table is owned by the office repository; when it is absent (e.g. in
// tests that exercise only the task layer) the absence is treated as an
// empty inventory rather than a malformed state, because no row can carry
// an unknown state without the table existing first.
func requireWorkspaceGroupInventoryKnownTx(ctx context.Context, tx *sqlx.Tx) error {
	rows, err := tx.QueryxContext(ctx, tx.Rebind(`
		SELECT DISTINCT cleanup_status FROM task_workspace_groups WHERE cleanup_status <> ''
	`))
	if err != nil {
		if isSQLiteNoSuchTableError(err) {
			return nil
		}
		return fmt.Errorf("%w: load workspace group inventory: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, err)
	}
	defer func() { _ = rows.Close() }()
	known := make(map[string]struct{}, len(KnownWorkspaceGroupRetirementStates))
	for _, state := range KnownWorkspaceGroupRetirementStates {
		known[state] = struct{}{}
	}
	seen := make(map[string]struct{})
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			return fmt.Errorf("%w: scan workspace group state: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, err)
		}
		seen[state] = struct{}{}
		if _, ok := known[state]; !ok {
			return fmt.Errorf("%w: unknown workspace group state %q", ErrArchivedResourceEnvironmentRetirementNotAdmitted, state)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: scan workspace group states: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, err)
	}
	return nil
}

// isSQLiteNoSuchTableError matches the SQLite "no such table" error text. The
// driver-specific error type is not exported, so a substring check is the
// only portable signal across the v0.85 lineage.
func isSQLiteNoSuchTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist")
}

// requireRetirementEnvironmentStoppedOrFailedTx fails closed when the
// environment row is missing, was mutated away from the admission status, or
// carries an active session reference.
func requireRetirementEnvironmentStoppedOrFailedTx(
	ctx context.Context,
	tx *sqlx.Tx,
	snapshot models.ArchivedResourceEnvironmentRetirementSnapshot,
) error {
	var status string
	var hasActiveSession int
	err := tx.QueryRowContext(ctx, tx.Rebind(`
		SELECT te.status,
			(SELECT COUNT(*) FROM task_sessions ts WHERE ts.task_environment_id = te.id AND ts.state IN ('running', 'starting', 'waiting_for_input'))
		FROM task_environments te
		WHERE te.id = ?
	`), snapshot.Immutable.EnvironmentID).Scan(&status, &hasActiveSession)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: environment %q no longer exists", ErrArchivedResourceEnvironmentRetirementUnknownRow, snapshot.Immutable.EnvironmentID)
		}
		return fmt.Errorf("%w: probe environment row: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, err)
	}
	if status != snapshot.Immutable.EnvironmentStatus {
		return fmt.Errorf("%w: environment status drifted from %q to %q", ErrArchivedResourceEnvironmentRetirementIdentityDrift, snapshot.Immutable.EnvironmentStatus, status)
	}
	if status != "stopped" && status != "failed" {
		return fmt.Errorf("%w: environment status %q is not stoppable", ErrArchivedResourceEnvironmentRetirementNotAdmitted, status)
	}
	if hasActiveSession > 0 {
		return fmt.Errorf("%w: environment has an active session", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
	}
	return nil
}

// requireRetirementEnvironmentNotActiveParticipantTx scans every active v2/v3
// anchor and fails closed when any treats the environment as a participant —
// including non-coordinator v3 task participants. The check is a defense in
// depth over the JSON immutable arrays; the strict decoder already rejects
// the snapshot when a participant row drifts.
func requireRetirementEnvironmentNotActiveParticipantTx(
	ctx context.Context,
	tx *sqlx.Tx,
	environmentID, taskID string,
) error {
	rows, err := tx.QueryxContext(ctx, tx.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE trigger = ? AND snapshot_version IN (?, ?) AND state IN (?, ?, ?, ?, ?)
	`), models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceReconcileSnapshotVersion,
		models.ArchivedResourceGroupReconcileSnapshotVersion,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait,
		models.TaskResourceCleanupStateRetained,
	)
	if err != nil {
		return fmt.Errorf("%w: load active reconcile anchors: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return fmt.Errorf("%w: scan active anchor: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, scanErr)
		}
		if job.TaskID == taskID {
			// A v2 anchor bound to this task is a participant by definition;
			// v3 anchors are participant-aware via the tasks array below.
			return fmt.Errorf("%w: active v2 anchor %q targets task %q", ErrArchivedResourceEnvironmentRetirementParticipant, job.OperationID, taskID)
		}
		switch job.SnapshotVersion {
		case models.ArchivedResourceReconcileSnapshotVersion:
			snapshot, _, decodeErr := models.DecodeArchivedResourceReconcileSnapshot([]byte(job.ResourceSnapshot))
			if decodeErr != nil {
				return fmt.Errorf("%w: decode v2 anchor: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, decodeErr)
			}
			for _, association := range snapshot.Immutable.Associations {
				if association.TaskID == taskID {
					return fmt.Errorf("%w: active v2 anchor %q carries task participant %q", ErrArchivedResourceEnvironmentRetirementParticipant, job.OperationID, taskID)
				}
			}
		case models.ArchivedResourceGroupReconcileSnapshotVersion:
			snapshot, _, decodeErr := models.DecodeArchivedResourceGroupReconcileSnapshot([]byte(job.ResourceSnapshot))
			if decodeErr != nil {
				return fmt.Errorf("%w: decode v3 anchor: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, decodeErr)
			}
			for _, participant := range snapshot.Immutable.Tasks {
				if participant.TaskID == taskID {
					return fmt.Errorf("%w: active v3 anchor %q carries non-coordinator participant %q", ErrArchivedResourceEnvironmentRetirementParticipant, job.OperationID, taskID)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: scan active anchors: %v", ErrArchivedResourceEnvironmentRetirementNotAdmitted, err)
	}
	return nil
}

// deleteRetirementEnvironmentReposTx deletes only the exact repository rows
// captured by admission. The CAS predicate binds the fourteen persisted
// columns per row so a single drift fails the whole retirement.
func deleteRetirementEnvironmentReposTx(
	ctx context.Context,
	tx *sqlx.Tx,
	snapshot models.ArchivedResourceEnvironmentRetirementSnapshot,
) error {
	for _, repo := range snapshot.Immutable.Repositories {
		var mergedAt interface{}
		if repo.MergedAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, repo.MergedAt)
			if err != nil {
				return fmt.Errorf("%w: repository merged_at is not canonical UTC", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
			}
			mergedAt = parsed.UTC()
		} else {
			mergedAt = nil
		}
		var deletedAt interface{}
		if repo.DeletedAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, repo.DeletedAt)
			if err != nil {
				return fmt.Errorf("%w: repository deleted_at is not canonical UTC", ErrArchivedResourceEnvironmentRetirementNotAdmitted)
			}
			deletedAt = parsed.UTC()
		} else {
			deletedAt = nil
		}
		result, err := tx.ExecContext(ctx, tx.Rebind(`
			DELETE FROM task_environment_repos
			WHERE id = ? AND task_environment_id = ? AND repository_id = ?
			  AND branch_slug = ? AND worktree_id = ? AND worktree_path = ?
			  AND worktree_branch = ? AND position = ? AND status = ?
			  AND created_at = ? AND updated_at = ?
			  AND (merged_at IS NULL AND ? IS NULL OR merged_at = ?)
			  AND (deleted_at IS NULL AND ? IS NULL OR deleted_at = ?)
		`), repo.ID, snapshot.Immutable.EnvironmentID, repo.RepositoryID,
			repo.BranchSlug, repo.WorktreeID, repo.WorktreePath,
			repo.WorktreeBranch, repo.Position, repo.Status,
			canonicalTimestamp(repo.CreatedAt), canonicalTimestamp(repo.UpdatedAt),
			mergedAt, mergedAt, deletedAt, deletedAt,
		)
		if err != nil {
			return fmt.Errorf("%w: delete environment repo %q: %v", ErrArchivedResourceEnvironmentRetirementIdentityDrift, repo.ID, err)
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("%w: read retirement delete affected rows: %v", ErrArchivedResourceEnvironmentRetirementIdentityDrift, rowsErr)
		}
		if affected != 1 {
			return fmt.Errorf("%w: expected exactly one deletion; found %d for repository row %q", ErrArchivedResourceEnvironmentRetirementIdentityDrift, affected, repo.ID)
		}
	}
	return nil
}

func canonicalTimestamp(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
