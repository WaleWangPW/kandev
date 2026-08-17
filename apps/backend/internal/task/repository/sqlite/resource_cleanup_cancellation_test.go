package sqlite

import (
	"context"
	"errors"
	"testing"

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
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "cascade_archive:cancel-exact")
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

func TestCancelRetryableArchiveCleanupRejectsRunningWithoutPartialMutation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	first := seedRetryableArchivedCleanup(t, repo, "cancel-first", "cascade-pair")
	second := seedRetryableArchivedCleanup(t, repo, "cancel-running", "cascade-pair")
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "cascade_archive:cancel-running")
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
	firstJob, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "cascade_archive:cancel-first")
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
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "cascade_archive:cancel-drift")
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
	job := &models.TaskResourceCleanupJob{
		ID: "cleanup-" + taskID, OperationID: "cascade_archive:" + taskID,
		TaskID: taskID, Trigger: models.TaskResourceCleanupTriggerCascadeArchive,
		State: models.TaskResourceCleanupStateRetryWait, ResourceSnapshot: `{}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("create cleanup %s: %v", taskID, err)
	}
	return models.ArchiveTaskCleanupCancellationExpectation{
		TaskID: taskID, ArchivedAt: *task.ArchivedAt, CascadeID: cascadeID,
	}
}
