package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

// ResolveArchivedResourceReconcileUnarchive is the sealed unarchive writer.
// The exact task generation, cancellable legacy cleanup jobs, pristine pending
// v2/v3 operations, retained association generations, and task unarchive CAS
// commit in one transaction. Claimed/running legacy cleanup fails closed.
func (r *Repository) ResolveArchivedResourceReconcileUnarchive(
	ctx context.Context,
	participantTaskID string,
	expectedArchivedAt time.Time,
	expectedCascadeID string,
	reconcileEnabled bool,
) (bool, error) {
	return r.resolveArchivedResourceReconcileUnarchive(
		ctx, participantTaskID, expectedArchivedAt, expectedCascadeID, reconcileEnabled,
	)
}

func (r *Repository) resolveArchivedResourceReconcileUnarchive(
	ctx context.Context,
	participantTaskID string,
	expectedArchivedAt time.Time,
	expectedCascadeID string,
	reconcileEnabled bool,
) (bool, error) {
	if participantTaskID == "" || expectedArchivedAt.IsZero() {
		return false, fmt.Errorf("%w: exact task archive generation is required", ErrArchivedResourceReconcileConflict)
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, fmt.Errorf("begin reconcile unarchive barrier: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	legacyJobs, jobs, historicalJobs, err := loadArchivedResourceUnarchiveJobsTx(
		ctx, tx, r.db.DriverName(), participantTaskID, expectedArchivedAt,
	)
	if err != nil {
		return false, err
	}
	expectedTaskGenerations, err := archivedResourceUnarchiveTaskGenerations(
		jobs, participantTaskID, expectedArchivedAt,
	)
	if err != nil {
		return false, err
	}
	archivedTasks, err := validateArchivedResourceTaskGenerationsTx(
		ctx, tx, r.db.DriverName(), expectedTaskGenerations, participantTaskID, expectedCascadeID,
	)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if err := validateArchivedResourceUnarchiveJobState(job, reconcileEnabled); err != nil {
			return false, err
		}
	}
	for _, job := range historicalJobs {
		if !reconcileEnabled || job.State != models.TaskResourceCleanupStateRetained {
			return false, fmt.Errorf("%w: historical reconcile operation %q is in state %q", ErrArchivedResourceReconcileConflict, job.OperationID, job.State)
		}
	}
	for _, job := range legacyJobs {
		if !archivedTasks[job.TaskID] {
			return false, fmt.Errorf("%w: active group participant %s still has cleanup intent", ErrArchivedResourceReconcileConflict, job.TaskID)
		}
		if job.State == models.TaskResourceCleanupStateRunning {
			return false, fmt.Errorf("%w: legacy cleanup %q is running", ErrArchivedResourceReconcileConflict, job.OperationID)
		}
	}
	now := time.Now().UTC()
	for _, job := range legacyJobs {
		if err := cancelLegacyArchivedResourceCleanupTx(ctx, tx, job, now); err != nil {
			return false, err
		}
	}
	for _, job := range jobs {
		switch job.State {
		case models.TaskResourceCleanupStatePending:
			if err := cancelPristineArchivedResourceReconcileTx(ctx, tx, job, now); err != nil {
				return false, err
			}
		case models.TaskResourceCleanupStateRetained:
			associations, err := archivedResourceJobAssociations(job)
			if err != nil {
				return false, err
			}
			if job.CompletedAt == nil {
				return false, fmt.Errorf("%w: retained reconcile has no completion generation", ErrArchivedResourceReconcileConflict)
			}
			if err := validateOrRestoreArchivedResourceAssociationsTx(ctx, tx, r.db.DriverName(), associations, job.CompletedAt.UTC(), now, true); err != nil {
				return false, err
			}
		}
	}
	for _, job := range historicalJobs {
		associations, err := archivedResourceJobAssociations(job)
		if err != nil {
			return false, err
		}
		// Historical retained anchors remain durable physical protectors, but
		// they cannot restore an older logical generation. They are accepted
		// only when every bound association is already active.
		if err := validateOrRestoreArchivedResourceAssociationsTx(
			ctx, tx, r.db.DriverName(), associations, time.Time{}, now, false,
		); err != nil {
			return false, err
		}
	}
	if err := unarchiveArchivedResourceTaskGenerationTx(
		ctx, tx, participantTaskID, expectedArchivedAt, expectedCascadeID, now,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("%w: commit reconcile unarchive barrier: %v", repoerrors.ErrTransactionOutcomeUnknown, err)
	}
	return true, nil
}

func loadArchivedResourceUnarchiveJobsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	participantTaskID string,
	expectedArchivedAt time.Time,
) ([]*models.TaskResourceCleanupJob, []*models.TaskResourceCleanupJob, []*models.TaskResourceCleanupJob, error) {
	rows, err := tx.QueryxContext(ctx, tx.Rebind(`
		SELECT `+taskResourceCleanupColumns+` FROM task_resource_cleanup_jobs
		WHERE (
			trigger IN (?, ?) AND snapshot_version=0
			AND active_scope_key IS NULL AND state IN (?, ?, ?, ?)
		) OR (
			trigger=? AND snapshot_version IN (?, ?)
			AND state IN (?, ?, ?, ?, ?, ?)
		)
		ORDER BY id ASC`+reconcileForUpdate(driver)),
		models.TaskResourceCleanupTriggerArchive,
		models.TaskResourceCleanupTriggerCascadeArchive,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait,
		models.TaskResourceCleanupTriggerReconcile,
		models.ArchivedResourceReconcileSnapshotVersion,
		models.ArchivedResourceGroupReconcileSnapshotVersion,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait,
		models.TaskResourceCleanupStateRetained,
		models.TaskResourceCleanupStateBlocked)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	allLegacyJobs := make([]*models.TaskResourceCleanupJob, 0)
	jobs := make([]*models.TaskResourceCleanupJob, 0)
	historicalJobs := make([]*models.TaskResourceCleanupJob, 0)
	protectedTaskIDs := map[string]struct{}{participantTaskID: {}}
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, nil, nil, scanErr
		}
		if job.Trigger != models.TaskResourceCleanupTriggerReconcile {
			allLegacyJobs = append(allLegacyJobs, job)
			continue
		}
		switch job.SnapshotVersion {
		case models.ArchivedResourceReconcileSnapshotVersion:
			if job.TaskID != participantTaskID {
				continue
			}
			snapshot, _, err := models.ValidateArchivedResourceReconcileJobHeaders(job)
			if err != nil {
				return nil, nil, nil, err
			}
			archivedAt, _ := time.Parse(time.RFC3339Nano, snapshot.Immutable.ArchivedAt)
			if !archivedAt.UTC().Equal(expectedArchivedAt.UTC()) {
				historicalJobs = append(historicalJobs, job)
				continue
			}
		case models.ArchivedResourceGroupReconcileSnapshotVersion:
			snapshot, _, err := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
			if err != nil {
				return nil, nil, nil, err
			}
			participantArchivedAt, found := archivedResourceGroupTaskGeneration(snapshot.Immutable.Tasks, participantTaskID)
			if !found {
				continue
			}
			if !participantArchivedAt.Equal(expectedArchivedAt.UTC()) {
				historicalJobs = append(historicalJobs, job)
				continue
			}
			for _, participant := range snapshot.Immutable.Tasks {
				protectedTaskIDs[participant.TaskID] = struct{}{}
			}
		default:
			continue
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	legacyJobs := make([]*models.TaskResourceCleanupJob, 0)
	for _, job := range allLegacyJobs {
		if _, protected := protectedTaskIDs[job.TaskID]; !protected {
			continue
		}
		if !isGenericTaskResourceCleanupJob(job) ||
			(job.Trigger != models.TaskResourceCleanupTriggerArchive &&
				job.Trigger != models.TaskResourceCleanupTriggerCascadeArchive) {
			return nil, nil, nil, fmt.Errorf("%w: legacy cleanup job identity drifted", ErrArchivedResourceReconcileConflict)
		}
		legacyJobs = append(legacyJobs, job)
	}
	sortArchivedResourceUnarchiveJobs(jobs)
	sortArchivedResourceUnarchiveJobs(historicalJobs)
	return legacyJobs, jobs, historicalJobs, nil
}

func sortArchivedResourceUnarchiveJobs(jobs []*models.TaskResourceCleanupJob) {
	sort.SliceStable(jobs, func(i, j int) bool {
		left, right := jobs[i], jobs[j]
		if left.CompletedAt != nil || right.CompletedAt != nil {
			if left.CompletedAt == nil {
				return false
			}
			if right.CompletedAt == nil {
				return true
			}
			if !left.CompletedAt.Equal(*right.CompletedAt) {
				return left.CompletedAt.After(*right.CompletedAt)
			}
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID > right.ID
	})
}

func isGenericTaskResourceCleanupJob(job *models.TaskResourceCleanupJob) bool {
	return job != nil && job.SnapshotVersion == 0 && job.SnapshotDigest == "" &&
		job.ResourceKind == "" && job.ResourceID == "" && job.ManagedRootKey == "" &&
		job.AnchorRevision == 0 && job.ActiveScopeKey == nil
}

func archivedResourceGroupTaskGeneration(
	tasks []models.ArchivedResourceGroupReconcileTask,
	taskID string,
) (time.Time, bool) {
	for _, task := range tasks {
		if task.TaskID != taskID {
			continue
		}
		archivedAt, err := time.Parse(time.RFC3339Nano, task.ArchivedAt)
		return archivedAt.UTC(), err == nil
	}
	return time.Time{}, false
}

func archivedResourceUnarchiveTaskGenerations(
	jobs []*models.TaskResourceCleanupJob,
	participantTaskID string,
	expectedArchivedAt time.Time,
) (map[string]time.Time, error) {
	expected := map[string]time.Time{participantTaskID: expectedArchivedAt.UTC()}
	add := func(taskID, rawArchivedAt string) error {
		archivedAt, err := time.Parse(time.RFC3339Nano, rawArchivedAt)
		if err != nil {
			return fmt.Errorf("%w: invalid task archive generation", ErrArchivedResourceReconcileConflict)
		}
		if current, ok := expected[taskID]; ok && !current.Equal(archivedAt.UTC()) {
			return fmt.Errorf("%w: task %s is bound to multiple archive generations", ErrArchivedResourceReconcileConflict, taskID)
		}
		expected[taskID] = archivedAt.UTC()
		return nil
	}
	for _, job := range jobs {
		switch job.SnapshotVersion {
		case models.ArchivedResourceReconcileSnapshotVersion:
			snapshot, _, err := models.ValidateArchivedResourceReconcileJobHeaders(job)
			if err != nil {
				return nil, err
			}
			if err := add(snapshot.Immutable.OriginTaskID, snapshot.Immutable.ArchivedAt); err != nil {
				return nil, err
			}
		case models.ArchivedResourceGroupReconcileSnapshotVersion:
			snapshot, _, err := models.ValidateArchivedResourceGroupReconcileJobHeaders(job)
			if err != nil {
				return nil, err
			}
			for _, task := range snapshot.Immutable.Tasks {
				if err := add(task.TaskID, task.ArchivedAt); err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("%w: unsupported reconcile snapshot", ErrArchivedResourceReconcileConflict)
		}
	}
	return expected, nil
}

func validateArchivedResourceTaskGenerationsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	expected map[string]time.Time,
	participantTaskID string,
	expectedCascadeID string,
) (map[string]bool, error) {
	taskIDs := make([]string, 0, len(expected))
	for taskID := range expected {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	archivedTasks := make(map[string]bool, len(taskIDs))
	for _, taskID := range taskIDs {
		var archivedAt sql.NullTime
		var cascadeID string
		if err := tx.QueryRowxContext(ctx, tx.Rebind(`
			SELECT archived_at, COALESCE(archived_by_cascade_id,'')
			FROM tasks WHERE id=?`+reconcileForUpdate(driver)), taskID).Scan(&archivedAt, &cascadeID); err != nil {
			return nil, fmt.Errorf("%w: read exact task archive generation: %v", ErrArchivedResourceReconcileConflict, err)
		}
		if !archivedAt.Valid {
			if taskID == participantTaskID {
				return nil, fmt.Errorf("%w: task %s is already active", ErrArchivedResourceReconcileConflict, taskID)
			}
			archivedTasks[taskID] = false
			continue
		}
		if !archivedAt.Time.UTC().Equal(expected[taskID].UTC()) {
			return nil, fmt.Errorf("%w: task %s archive generation drifted", ErrArchivedResourceReconcileConflict, taskID)
		}
		if taskID == participantTaskID && cascadeID != expectedCascadeID {
			return nil, fmt.Errorf("%w: task %s cascade generation drifted", ErrArchivedResourceReconcileConflict, taskID)
		}
		archivedTasks[taskID] = true
	}
	return archivedTasks, nil
}

func cancelLegacyArchivedResourceCleanupTx(
	ctx context.Context,
	tx *sqlx.Tx,
	job *models.TaskResourceCleanupJob,
	now time.Time,
) error {
	if job == nil || job.State == models.TaskResourceCleanupStateRunning {
		return fmt.Errorf("%w: legacy cleanup is not transactionally cancellable", ErrArchivedResourceReconcileConflict)
	}
	result, err := tx.ExecContext(ctx, tx.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state=?, completed_at=?, updated_at=?
		WHERE id=? AND operation_id=? AND task_id=? AND trigger=? AND state=? AND attempts=?
		  AND snapshot_version=0 AND snapshot_digest='' AND resource_kind=''
		  AND resource_id='' AND managed_root_key='' AND anchor_revision=0
		  AND active_scope_key IS NULL AND resource_snapshot=?
	`), models.TaskResourceCleanupStateCancelled, now, now,
		job.ID, job.OperationID, job.TaskID, job.Trigger, job.State, job.Attempts, job.ResourceSnapshot)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("%w: legacy cleanup generation drifted", ErrArchivedResourceReconcileConflict)
	}
	return nil
}

func unarchiveArchivedResourceTaskGenerationTx(
	ctx context.Context,
	tx *sqlx.Tx,
	participantTaskID string,
	expectedArchivedAt time.Time,
	expectedCascadeID string,
	now time.Time,
) error {
	result, err := tx.ExecContext(ctx, tx.Rebind(`
		UPDATE tasks
		SET archived_at=NULL, archived_by_cascade_id='', updated_at=?
		WHERE id=? AND archived_at=? AND COALESCE(archived_by_cascade_id,'')=?
	`), now, participantTaskID, expectedArchivedAt.UTC(), expectedCascadeID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("%w: exact task unarchive generation drifted", ErrArchivedResourceReconcileConflict)
	}
	return nil
}

func validateArchivedResourceUnarchiveJobState(job *models.TaskResourceCleanupJob, reconcileEnabled bool) error {
	if job.State == models.TaskResourceCleanupStatePending && job.Attempts == 0 && job.AnchorRevision == 0 &&
		job.NextAttemptAt == nil && job.CompletedAt == nil && job.LastError == "" {
		return nil
	}
	if reconcileEnabled && job.State == models.TaskResourceCleanupStateRetained {
		return nil
	}
	return fmt.Errorf("%w: reconcile operation %q is in state %q", ErrArchivedResourceReconcileConflict, job.OperationID, job.State)
}

func archivedResourceJobAssociations(job *models.TaskResourceCleanupJob) ([]models.ArchivedResourceReconcileAssociation, error) {
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

func cancelPristineArchivedResourceReconcileTx(
	ctx context.Context,
	tx *sqlx.Tx,
	job *models.TaskResourceCleanupJob,
	now time.Time,
) error {
	result, err := tx.ExecContext(ctx, tx.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state=?, active_scope_key=NULL, completed_at=?, updated_at=?
		WHERE id=? AND operation_id=? AND trigger=? AND state=? AND task_id=?
		  AND attempts=0 AND next_attempt_at IS NULL AND completed_at IS NULL AND last_error=''
		  AND snapshot_version=? AND snapshot_digest=? AND resource_kind=?
		  AND resource_id=? AND managed_root_key=? AND anchor_revision=0
		  AND active_scope_key=? AND resource_snapshot=?
	`), models.TaskResourceCleanupStateCancelled, now, now,
		job.ID, job.OperationID, models.TaskResourceCleanupTriggerReconcile,
		models.TaskResourceCleanupStatePending, job.TaskID,
		job.SnapshotVersion, job.SnapshotDigest, job.ResourceKind,
		job.ResourceID, job.ManagedRootKey, job.ActiveScopeKey, job.ResourceSnapshot)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("%w: pristine reconcile changed before cancellation", ErrArchivedResourceReconcileConflict)
	}
	return nil
}

func validateOrRestoreArchivedResourceAssociationsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	driver string,
	associations []models.ArchivedResourceReconcileAssociation,
	expectedTombstone time.Time,
	now time.Time,
	mutate bool,
) error {
	for _, association := range associations {
		var (
			sessionID, ownerTaskID, worktreeID, repositoryID string
			branchSlug, worktreePath, worktreeBranch, status string
			createdAt, updatedAt                             time.Time
			deletedAt                                        sql.NullTime
		)
		err := tx.QueryRowxContext(ctx, tx.Rebind(`
			SELECT `+archivedResourceSessionIDExpr+`, te.task_id, ter.worktree_id, ter.repository_id,
			       COALESCE(ter.branch_slug,''), ter.worktree_path, ter.worktree_branch,
			       ter.status, ter.created_at, ter.updated_at, ter.deleted_at
			FROM task_environment_repos ter
			JOIN task_environments te ON te.id=ter.task_environment_id
			WHERE ter.id=?`+reconcileForUpdate(driver)), association.AssociationID).Scan(
			&sessionID, &ownerTaskID, &worktreeID, &repositoryID,
			&branchSlug, &worktreePath, &worktreeBranch, &status,
			&createdAt, &updatedAt, &deletedAt)
		if err != nil {
			return fmt.Errorf("%w: association %s is unavailable", ErrArchivedResourceReconcileConflict, association.AssociationID)
		}
		expectedCreatedAt, _ := time.Parse(time.RFC3339Nano, association.CreatedAt)
		if sessionID != association.SessionID || ownerTaskID != association.TaskID ||
			worktreeID != association.WorktreeID || repositoryID != association.RepositoryID ||
			branchSlug != association.BranchSlug || worktreePath != association.WorktreePath ||
			worktreeBranch != association.WorktreeBranch || !createdAt.UTC().Equal(expectedCreatedAt) {
			return fmt.Errorf("%w: association %s identity drifted", ErrArchivedResourceReconcileConflict, association.AssociationID)
		}
		if status == "active" && !deletedAt.Valid {
			continue
		}
		if status != "deleted" || !deletedAt.Valid ||
			!updatedAt.UTC().Equal(expectedTombstone) || !deletedAt.Time.UTC().Equal(expectedTombstone) {
			return fmt.Errorf("%w: association %s tombstone generation drifted", ErrArchivedResourceReconcileConflict, association.AssociationID)
		}
		if !mutate {
			continue
		}
		result, err := tx.ExecContext(ctx, tx.Rebind(`
			UPDATE task_environment_repos
			SET status='active', deleted_at=NULL, updated_at=?
			WHERE id=? AND status='deleted' AND updated_at=? AND deleted_at=?
		`), now, association.AssociationID, expectedTombstone, expectedTombstone)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return fmt.Errorf("%w: association %s changed during restoration", ErrArchivedResourceReconcileConflict, association.AssociationID)
		}
	}
	return nil
}
