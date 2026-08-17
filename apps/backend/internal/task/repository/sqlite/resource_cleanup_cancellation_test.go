package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

func TestCancelRetryableArchiveCleanupBindsArchiveGeneration(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	expected := seedRetryableArchivedCleanup(t, repo, "cancel-exact", "cascade-exact")

	cancelled, err := repo.CancelRetryableArchiveTaskResourceCleanupJobs(ctx, []models.ArchiveTaskCleanupCancellationExpectation{expected})
	if err != nil || cancelled != 1 {
		t.Fatalf("cancel = %d, %v; want 1, nil", cancelled, err)
	}
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, expected.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != models.TaskResourceCleanupStateCancelled || job.CompletedAt == nil {
		t.Fatalf("job after cancel = %#v", job)
	}
	task, err := repo.GetTask(ctx, expected.TaskID)
	if err != nil || task.ArchivedAt == nil || !task.ArchivedAt.Equal(expected.ArchivedAt) || task.ArchivedByCascadeID != expected.CascadeID {
		t.Fatalf("archive generation changed: %#v, %v", task, err)
	}
}

func TestCancelRetryableArchiveCleanupLeavesHistoricalOperationUntouched(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	expected := seedRetryableArchivedCleanup(t, repo, "cancel-history", "cascade-current")
	historicalOperationID := "cascade_archive:cascade-historical:cancel-history"
	now := time.Now().UTC()
	// Simulate a legacy retained retry row from an older archive generation.
	// The normal reservation gate prevents this shape today, but cancellation
	// must still not broaden its exact operation binding when it encounters one.
	if _, err := repo.db.ExecContext(ctx, repo.db.Rebind(`
		INSERT INTO task_resource_cleanup_jobs
		(id, operation_id, task_id, trigger, state, resource_snapshot, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		"cleanup-history", historicalOperationID, expected.TaskID,
		models.TaskResourceCleanupTriggerCascadeArchive,
		models.TaskResourceCleanupStateRetryWait, `{}`, now, now,
	); err != nil {
		t.Fatal(err)
	}
	if cancelled, err := repo.CancelRetryableArchiveTaskResourceCleanupJobs(ctx, []models.ArchiveTaskCleanupCancellationExpectation{expected}); err != nil || cancelled != 1 {
		t.Fatalf("cancel = %d, %v; want 1, nil", cancelled, err)
	}
	current, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, expected.OperationID)
	if err != nil || current.State != models.TaskResourceCleanupStateCancelled {
		t.Fatalf("current operation = %#v, %v", current, err)
	}
	untouched, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, historicalOperationID)
	if err != nil || untouched.State != models.TaskResourceCleanupStateRetryWait || untouched.CompletedAt != nil {
		t.Fatalf("historical operation mutated = %#v, %v", untouched, err)
	}
}

func TestCancelRetryableArchiveCleanupRejectsRunningWithoutPartialMutation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	first := seedRetryableArchivedCleanup(t, repo, "cancel-first", "cascade-pair")
	second := seedRetryableArchivedCleanup(t, repo, "cancel-running", "cascade-pair")
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, second.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}

	_, err = repo.CancelRetryableArchiveTaskResourceCleanupJobs(ctx, []models.ArchiveTaskCleanupCancellationExpectation{first, second})
	if !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("cancel error = %v, want ErrTaskCleanupInProgress", err)
	}
	firstJob, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, first.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if firstJob.State != models.TaskResourceCleanupStateRetryWait || firstJob.CompletedAt != nil {
		t.Fatalf("first job was partially cancelled: %#v", firstJob)
	}
}

func TestCancelRetryableArchiveCleanupRejectsArchiveGenerationDrift(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	expected := seedRetryableArchivedCleanup(t, repo, "cancel-drift", "cascade-drift")
	expected.CascadeID = "other-cascade"
	if _, err := repo.CancelRetryableArchiveTaskResourceCleanupJobs(ctx, []models.ArchiveTaskCleanupCancellationExpectation{expected}); err == nil {
		t.Fatal("cancellation accepted a cascade generation drift")
	}
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, expected.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != models.TaskResourceCleanupStateRetryWait {
		t.Fatalf("generation drift mutated job: %#v", job)
	}
}

func seedRetryableArchivedCleanup(t *testing.T, repo *Repository, taskID, cascadeID string) models.ArchiveTaskCleanupCancellationExpectation {
	t.Helper()
	ctx := context.Background()
	seedBarrierTask(t, repo, taskID)
	archived, err := repo.ArchiveTaskIfActive(ctx, taskID, cascadeID)
	if err != nil || !archived {
		t.Fatalf("archive %s = %v, %v", taskID, archived, err)
	}
	task, err := repo.GetTask(ctx, taskID)
	if err != nil || task.ArchivedAt == nil {
		t.Fatalf("load archived task %s: %#v, %v", taskID, task, err)
	}
	operationID := "cascade_archive:" + cascadeID + ":" + taskID
	job := &models.TaskResourceCleanupJob{
		ID: "cleanup-" + taskID, OperationID: operationID,
		TaskID: taskID, Trigger: models.TaskResourceCleanupTriggerCascadeArchive,
		State: models.TaskResourceCleanupStateRetryWait, ResourceSnapshot: `{}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("create cleanup %s: %v", taskID, err)
	}
	return models.ArchiveTaskCleanupCancellationExpectation{
		TaskID: taskID, ArchivedAt: *task.ArchivedAt, CascadeID: cascadeID, OperationID: operationID,
	}
}
