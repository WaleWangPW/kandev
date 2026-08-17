package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
)

func TestPostgresCancelRetryableArchiveCleanupLocksAndPreservesArchive(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	if err != nil {
		t.Fatalf("init postgres schema: %v", err)
	}
	ctx := context.Background()
	seedPostgresTask(t, repo, "cancel-archive-pg")
	if archived, err := repo.ArchiveTaskIfActive(ctx, "cancel-archive-pg", "cascade-pg"); err != nil || !archived {
		t.Fatalf("archive = %v, %v", archived, err)
	}
	task, err := repo.GetTask(ctx, "cancel-archive-pg")
	if err != nil || task.ArchivedAt == nil {
		t.Fatalf("load archived task: %#v, %v", task, err)
	}
	job := &models.TaskResourceCleanupJob{
		ID: "cleanup-pg", OperationID: "cascade_archive:cancel-archive-pg",
		TaskID: task.ID, Trigger: models.TaskResourceCleanupTriggerCascadeArchive,
		State: models.TaskResourceCleanupStateRetryWait, ResourceSnapshot: `{}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	cancelled, err := repo.CancelRetryableArchiveTaskResourceCleanupJobs(ctx, []models.ArchiveTaskCleanupCancellationExpectation{{
		TaskID: task.ID, ArchivedAt: *task.ArchivedAt, CascadeID: task.ArchivedByCascadeID,
	}})
	if err != nil || cancelled != 1 {
		t.Fatalf("cancel = %d, %v", cancelled, err)
	}
	stored, err := repo.GetTaskResourceCleanupJob(ctx, job.ID)
	if err != nil || stored.State != models.TaskResourceCleanupStateCancelled || stored.CompletedAt == nil {
		t.Fatalf("stored job = %#v, %v", stored, err)
	}
	current, err := repo.GetTask(ctx, task.ID)
	if err != nil || current.ArchivedAt == nil || !current.ArchivedAt.Equal(*task.ArchivedAt) || current.ArchivedByCascadeID != task.ArchivedByCascadeID {
		t.Fatalf("archive generation changed: %#v, %v", current, err)
	}
}
