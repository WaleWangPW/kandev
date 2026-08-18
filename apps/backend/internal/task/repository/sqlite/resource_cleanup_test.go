package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

func TestTaskResourceCleanupReservationReplayAndActiveSemantics(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	seedBarrierTask(t, repo, "task-reservation-semantics")
	first := &models.TaskResourceCleanupJob{
		ID: "job-reservation-first", OperationID: "delete:reservation-semantics",
		TaskID: "task-reservation-semantics", Trigger: models.TaskResourceCleanupTriggerDelete,
		State: models.TaskResourceCleanupStatePrepared, ResourceSnapshot: `{"generation":"first"}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &models.TaskResourceCleanupJob{
		ID: "job-reservation-second", OperationID: "delete:reservation-other",
		TaskID: first.TaskID, Trigger: models.TaskResourceCleanupTriggerDelete,
		State: models.TaskResourceCleanupStatePending,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, second); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("second active reservation error = %v, want ErrTaskCleanupInProgress", err)
	}

	if err := repo.DeleteTask(ctx, first.TaskID); err != nil {
		t.Fatal(err)
	}
	replay := &models.TaskResourceCleanupJob{
		ID: "job-replay-different", OperationID: first.OperationID,
		TaskID: first.TaskID, Trigger: models.TaskResourceCleanupTriggerArchive,
		State: models.TaskResourceCleanupStatePending, ResourceSnapshot: `{"generation":"different"}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, replay); err != nil {
		t.Fatalf("operation replay after task deletion: %v", err)
	}
	stored, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, first.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != first.ID || stored.TaskID != first.TaskID || stored.Trigger != first.Trigger ||
		stored.ResourceSnapshot != first.ResourceSnapshot {
		t.Fatalf("operation replay changed winner: %+v", stored)
	}
	missing := &models.TaskResourceCleanupJob{
		ID: "job-missing-task", OperationID: "delete:missing-task-new-operation",
		TaskID: first.TaskID, Trigger: models.TaskResourceCleanupTriggerDelete,
		State: models.TaskResourceCleanupStatePending,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, missing); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("new reservation beside active orphan error = %v, want ErrTaskCleanupInProgress", err)
	}
}

func TestTaskResourceCleanupJobSurvivesTaskDeletion(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	seedExecutorRunningCleanupTask(t, repo, "task-cleanup")

	job := &models.TaskResourceCleanupJob{
		ID: "job-1", OperationID: "delete:task-cleanup", TaskID: "task-cleanup",
		Trigger:          models.TaskResourceCleanupTriggerDelete,
		State:            models.TaskResourceCleanupStatePending,
		ResourceSnapshot: `{"workspace_path":"/tmp/task-cleanup"}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("persist cleanup intent before task deletion: %v", err)
	}
	if err := repo.DeleteTask(ctx, "task-cleanup"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	var taskID, snapshot string
	if err := repo.ro.QueryRowContext(ctx, `
		SELECT task_id, resource_snapshot
		FROM task_resource_cleanup_jobs
		WHERE operation_id = 'delete:task-cleanup'
	`).Scan(&taskID, &snapshot); err != nil {
		t.Fatalf("load cleanup job after task deletion: %v", err)
	}
	if taskID != "task-cleanup" || snapshot != `{"workspace_path":"/tmp/task-cleanup"}` {
		t.Fatalf("cleanup snapshot changed after task cascade: task_id=%q snapshot=%q", taskID, snapshot)
	}
}

func TestTaskResourceCleanupJobClaimAndRetry(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	seedBarrierTask(t, repo, "task-retry")
	job := &models.TaskResourceCleanupJob{
		ID: "job-retry", OperationID: "delete:retry", TaskID: "task-retry",
		Trigger: models.TaskResourceCleanupTriggerDelete,
		State:   models.TaskResourceCleanupStatePending, ResourceSnapshot: `{}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("CreateTaskResourceCleanupJob: %v", err)
	}
	claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v; want true", claimed, err)
	}
	claimed, err = repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID)
	if err != nil || claimed {
		t.Fatalf("second claim = %v, %v; want false", claimed, err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	if err := repo.CompleteTaskResourceCleanupJob(ctx, job.ID, models.TaskResourceCleanupStateRetryWait, "retry", &past); err != nil {
		t.Fatalf("mark retry: %v", err)
	}
	due, err := repo.ListDueTaskResourceCleanupJobs(ctx, time.Now().UTC(), 10)
	if err != nil || len(due) != 1 || due[0].ID != job.ID {
		t.Fatalf("due jobs = %#v, %v; want job-retry", due, err)
	}
}

func TestListPreparedTaskResourceCleanupJobsExcludesRunnableStates(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	for _, job := range []*models.TaskResourceCleanupJob{
		{
			ID: "job-prepared", OperationID: "delete:prepared", TaskID: "task-prepared",
			Trigger: models.TaskResourceCleanupTriggerDelete, State: models.TaskResourceCleanupStatePrepared,
		},
		{
			ID: "job-pending", OperationID: "delete:pending", TaskID: "task-pending",
			Trigger: models.TaskResourceCleanupTriggerDelete, State: models.TaskResourceCleanupStatePending,
		},
	} {
		seedBarrierTask(t, repo, job.TaskID)
		if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
			t.Fatalf("CreateTaskResourceCleanupJob(%s): %v", job.ID, err)
		}
	}

	jobs, err := repo.ListPreparedTaskResourceCleanupJobs(ctx)
	if err != nil {
		t.Fatalf("ListPreparedTaskResourceCleanupJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-prepared" {
		t.Fatalf("prepared jobs = %#v, want only job-prepared", jobs)
	}
}

func TestHasActiveTaskResourceCleanupJobTracksAdmissionStates(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	states := []struct {
		state  models.TaskResourceCleanupState
		active bool
	}{
		{models.TaskResourceCleanupStatePrepared, true},
		{models.TaskResourceCleanupStatePending, true},
		{models.TaskResourceCleanupStateRunning, true},
		{models.TaskResourceCleanupStateRetryWait, true},
		{models.TaskResourceCleanupStateSucceeded, false},
		{models.TaskResourceCleanupStateFailed, false},
		{models.TaskResourceCleanupStateCancelled, false},
	}
	for _, tt := range states {
		t.Run(string(tt.state), func(t *testing.T) {
			taskID := "task-" + string(tt.state)
			seedBarrierTask(t, repo, taskID)
			job := &models.TaskResourceCleanupJob{
				ID: "job-" + string(tt.state), OperationID: "delete:" + taskID, TaskID: taskID,
				Trigger: models.TaskResourceCleanupTriggerDelete, State: tt.state, ResourceSnapshot: `{}`,
			}
			if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
				t.Fatalf("CreateTaskResourceCleanupJob: %v", err)
			}
			active, err := repo.HasActiveTaskResourceCleanupJob(ctx, taskID)
			if err != nil {
				t.Fatalf("HasActiveTaskResourceCleanupJob: %v", err)
			}
			if active != tt.active {
				t.Fatalf("active = %v, want %v", active, tt.active)
			}
		})
	}
}

func TestTaskResourceCleanupJobStaleClaimCannotOverwriteCancellation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	seedBarrierTask(t, repo, "task-cancel-race")
	job := &models.TaskResourceCleanupJob{
		ID: "job-cancel-race", OperationID: "archive:cancel-race", TaskID: "task-cancel-race",
		Trigger: models.TaskResourceCleanupTriggerArchive,
		State:   models.TaskResourceCleanupStatePending, ResourceSnapshot: `{"worktree":"preserve-me"}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("CreateTaskResourceCleanupJob: %v", err)
	}
	claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID)
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v; want true", claimed, err)
	}
	claimedJob, err := repo.GetTaskResourceCleanupJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetTaskResourceCleanupJob after claim: %v", err)
	}
	if err := repo.CancelArchiveTaskResourceCleanupJobs(ctx, job.TaskID); err != nil {
		t.Fatalf("CancelArchiveTaskResourceCleanupJobs: %v", err)
	}
	updated, err := repo.CompleteClaimedTaskResourceCleanupJob(
		ctx, job.ID, claimedJob.Attempts, models.TaskResourceCleanupStateSucceeded, "", nil,
	)
	if err != nil {
		t.Fatalf("CompleteClaimedTaskResourceCleanupJob: %v", err)
	}
	if updated {
		t.Fatal("stale claimed completion overwrote cancellation")
	}
	got, err := repo.GetTaskResourceCleanupJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetTaskResourceCleanupJob: %v", err)
	}
	if got.State != models.TaskResourceCleanupStateCancelled || got.Attempts != claimedJob.Attempts {
		t.Fatalf("job = state %q attempts %d, want cancelled generation %d", got.State, got.Attempts, claimedJob.Attempts)
	}
	if got.ResourceSnapshot != job.ResourceSnapshot || got.CompletedAt == nil {
		t.Fatalf("cancelled job lost historical metadata: %#v", got)
	}
}

func TestTaskResourceCleanupFailedJobIsTerminalAndNotDue(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	seedBarrierTask(t, repo, "task-failed")
	job := &models.TaskResourceCleanupJob{
		ID: "job-failed", OperationID: "delete:failed", TaskID: "task-failed",
		Trigger: models.TaskResourceCleanupTriggerDelete,
		State:   models.TaskResourceCleanupStatePending, ResourceSnapshot: `{}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("CreateTaskResourceCleanupJob: %v", err)
	}
	claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID)
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v; want true", claimed, err)
	}
	claimedJob, err := repo.GetTaskResourceCleanupJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetTaskResourceCleanupJob: %v", err)
	}
	if _, err := repo.CompleteClaimedTaskResourceCleanupJob(
		ctx, job.ID, claimedJob.Attempts, models.TaskResourceCleanupState("failed"), "permanent failure", nil,
	); err != nil {
		t.Fatalf("complete failed cleanup: %v", err)
	}
	got, err := repo.GetTaskResourceCleanupJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("reload failed cleanup: %v", err)
	}
	if got.CompletedAt == nil {
		t.Fatal("failed cleanup has no completion timestamp")
	}
	due, err := repo.ListDueTaskResourceCleanupJobs(ctx, time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("ListDueTaskResourceCleanupJobs: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("failed cleanup is due: %#v", due)
	}
}

func TestTaskResourceCleanupFailedUnclaimedCompletionHasTimestamp(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	seedBarrierTask(t, repo, "task-failed-unclaimed")
	job := &models.TaskResourceCleanupJob{
		ID: "job-failed-unclaimed", OperationID: "delete:failed-unclaimed", TaskID: "task-failed-unclaimed",
		Trigger: models.TaskResourceCleanupTriggerDelete,
		State:   models.TaskResourceCleanupStatePending, ResourceSnapshot: `{}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("CreateTaskResourceCleanupJob: %v", err)
	}
	if err := repo.CompleteTaskResourceCleanupJob(
		ctx, job.ID, models.TaskResourceCleanupStateFailed, "permanent failure", nil,
	); err != nil {
		t.Fatalf("complete unclaimed failed cleanup: %v", err)
	}
	got, err := repo.GetTaskResourceCleanupJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("reload failed cleanup: %v", err)
	}
	if got.CompletedAt == nil {
		t.Fatal("unclaimed failed cleanup has no completion timestamp")
	}
}

func TestPreparedCleanupSnapshotStartAndRunningReset(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	seedBarrierTask(t, repo, "task-prepared-lifecycle")
	job := &models.TaskResourceCleanupJob{
		ID: "job-prepared-lifecycle", OperationID: "delete:prepared-lifecycle", TaskID: "task-prepared-lifecycle",
		Trigger: models.TaskResourceCleanupTriggerDelete, State: models.TaskResourceCleanupStatePrepared,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTaskResourceCleanupSnapshot(ctx, job.OperationID, `{"worktrees":["one"]}`); err != nil {
		t.Fatalf("UpdateTaskResourceCleanupSnapshot: %v", err)
	}
	if err := repo.UpdateTaskResourceCleanupSnapshot(ctx, "missing", `{}`); err == nil {
		t.Fatal("missing prepared snapshot update returned nil")
	}
	started, err := repo.StartPreparedTaskResourceCleanupJob(ctx, job.ID)
	if err != nil || !started {
		t.Fatalf("StartPreparedTaskResourceCleanupJob = %v, %v", started, err)
	}
	started, err = repo.StartPreparedTaskResourceCleanupJob(ctx, job.ID)
	if err != nil || started {
		t.Fatalf("second StartPreparedTaskResourceCleanupJob = %v, %v", started, err)
	}
	claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID)
	if err != nil || !claimed {
		t.Fatalf("MarkTaskResourceCleanupJobRunning = %v, %v", claimed, err)
	}
	if err := repo.ResetRunningTaskResourceCleanupJobs(ctx); err != nil {
		t.Fatalf("ResetRunningTaskResourceCleanupJobs: %v", err)
	}
	got, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, job.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != models.TaskResourceCleanupStateRetryWait || got.NextAttemptAt == nil || got.ResourceSnapshot != `{"worktrees":["one"]}` {
		t.Fatalf("reset job = %+v", got)
	}
	if _, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "missing"); err == nil {
		t.Fatal("missing operation lookup returned nil")
	}
}

// TestResetRunningReconcileJobsStaysRunning pins the generic cleanup
// startup reset against the v2/v3 reconcile path. Both
// ReconcileArchivedResource and ReconcileArchivedResourceGroup admit
// the job and finish synchronously, so a row that is still RUNNING
// after a process is killed must keep the typed-sentinel for the
// operator to replay — the generic archive/delete worker does not
// understand snapshot_version in (2, 3) and would re-run the row
// against the wrong cleanup path.
func TestResetRunningReconcileJobsStaysRunning(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	seedBarrierTask(t, repo, "task-reconcile-v2")
	seedBarrierTask(t, repo, "task-reconcile-v3")
	seedBarrierTask(t, repo, "task-archive-normal")

	insert := func(id, taskID, trigger string, snapshotVersion int) {
		state := models.TaskResourceCleanupStatePending
		job := &models.TaskResourceCleanupJob{
			ID:               id,
			OperationID:      "reconcile:" + id,
			TaskID:           taskID,
			Trigger:          models.TaskResourceCleanupTrigger(trigger),
			State:            state,
			ResourceSnapshot: `{"schema_version":1}`,
			SnapshotVersion:  snapshotVersion,
		}
		if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		if _, err := repo.MarkTaskResourceCleanupJobRunning(ctx, id); err != nil {
			t.Fatalf("mark %s running: %v", id, err)
		}
	}
	insert("reconcile-v2", "task-reconcile-v2", "reconcile", 2)
	insert("reconcile-v3", "task-reconcile-v3", "reconcile", 3)
	insert("archive-normal", "task-archive-normal", "archive", 1)

	if err := repo.ResetRunningTaskResourceCleanupJobs(ctx); err != nil {
		t.Fatalf("ResetRunningTaskResourceCleanupJobs: %v", err)
	}

	checkState := func(id string, want models.TaskResourceCleanupState) {
		got, err := repo.GetTaskResourceCleanupJob(ctx, id)
		if err != nil {
			t.Fatalf("reload %s: %v", id, err)
		}
		if got.State != want {
			t.Fatalf("%s state = %q, want %q", id, got.State, want)
		}
	}
	checkState("reconcile-v2", models.TaskResourceCleanupStateRunning)
	checkState("reconcile-v3", models.TaskResourceCleanupStateRunning)
	checkState("archive-normal", models.TaskResourceCleanupStateRetryWait)
}
