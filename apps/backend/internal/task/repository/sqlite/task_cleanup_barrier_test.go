package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	dbutil "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/kandev/kandev/internal/testutil"
)

func newBarrierTestRepo(t *testing.T) *Repository {
	t.Helper()
	dbConn, err := dbutil.OpenSQLite(filepath.Join(t.TempDir(), "barrier.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlxDB := sqlx.NewDb(dbConn, "sqlite3")
	t.Cleanup(func() { _ = sqlxDB.Close() })
	repo, err := NewWithDB(sqlxDB, sqlxDB, nil)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	return repo
}

func seedBarrierTask(t *testing.T, repo *Repository, taskID string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := repo.db.Exec(repo.db.Rebind(`
		INSERT INTO tasks (id, workspace_id, title, created_at, updated_at)
		VALUES (?, 'ws-1', 'barrier task', ?, ?)`), taskID, now, now); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func reserveBarrier(t *testing.T, repo *Repository, taskID, operationID string) {
	t.Helper()
	ctx := context.Background()
	job := &models.TaskResourceCleanupJob{
		OperationID: operationID, TaskID: taskID,
		Trigger: models.TaskResourceCleanupTriggerArchive,
		State:   models.TaskResourceCleanupStatePrepared,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("reserve barrier: %v", err)
	}
}

func TestTaskCleanupBarrier_RejectsSessionCreation(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-session")
	reserveBarrier(t, repo, "task-barrier-session", "op-session")

	err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-barrier", TaskID: "task-barrier-session",
		State: models.TaskSessionStateCreated,
	})
	if !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("CreateTaskSession error = %v, want ErrTaskCleanupInProgress", err)
	}
}

func TestTaskCleanupBarrier_RejectsEnvironmentCreation(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-env")
	reserveBarrier(t, repo, "task-barrier-env", "op-env")

	err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-barrier", TaskID: "task-barrier-env", ExecutorType: "worktree",
		WorkspacePath: "/tmp", Status: models.TaskEnvironmentStatusReady,
	})
	if !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("CreateTaskEnvironment error = %v, want ErrTaskCleanupInProgress", err)
	}
}

func TestTaskCleanupBarrier_RejectsEnvRepoCreation(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-repo")
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-barrier-repo", TaskID: "task-barrier-repo", ExecutorType: "worktree",
		WorkspacePath: "/tmp", Status: models.TaskEnvironmentStatusReady,
	}); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	reserveBarrier(t, repo, "task-barrier-repo", "op-repo")

	err := repo.CreateTaskEnvironmentRepo(ctx, &models.TaskEnvironmentRepo{
		TaskEnvironmentID: "env-barrier-repo", RepositoryID: "repo-1",
		WorktreeID: "wt-1", WorktreePath: "/tmp/wt-1",
	})
	if !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("CreateTaskEnvironmentRepo error = %v, want ErrTaskCleanupInProgress", err)
	}
}

func TestTaskCleanupBarrier_AllowsCreationWithoutBarrier(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-open")

	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-open", TaskID: "task-barrier-open", State: models.TaskSessionStateCreated,
	}); err != nil {
		t.Fatalf("CreateTaskSession without barrier: %v", err)
	}
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-open", TaskID: "task-barrier-open", ExecutorType: "worktree",
		WorkspacePath: "/tmp", Status: models.TaskEnvironmentStatusReady,
	}); err != nil {
		t.Fatalf("CreateTaskEnvironment without barrier: %v", err)
	}
	if err := repo.CreateTaskEnvironmentRepo(ctx, &models.TaskEnvironmentRepo{
		TaskEnvironmentID: "env-open", RepositoryID: "repo-1",
		WorktreeID: "wt-1", WorktreePath: "/tmp/wt-1",
	}); err != nil {
		t.Fatalf("CreateTaskEnvironmentRepo without barrier: %v", err)
	}
}

func TestTaskCleanupBarrierRejectsEnvironmentRepoMutationAndBranchUpdate(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-mutation")
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-barrier-mutation", TaskID: "task-barrier-mutation",
		ExecutorType: "worktree", WorkspacePath: "/tmp", Status: models.TaskEnvironmentStatusReady,
		Repos: []*models.TaskEnvironmentRepo{{
			ID: "repo-row-barrier-mutation", RepositoryID: "repository",
			WorktreeID: "worktree", WorktreePath: "/tmp/worktree",
			WorktreeBranch: "feature/original", Status: "active",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-barrier-mutation", TaskID: "task-barrier-mutation",
		TaskEnvironmentID: "env-barrier-mutation", State: models.TaskSessionStateCreated,
	}); err != nil {
		t.Fatal(err)
	}
	reserveBarrier(t, repo, "task-barrier-mutation", "op-mutation")

	envRepo := &models.TaskEnvironmentRepo{
		ID: "repo-row-barrier-mutation", BranchSlug: "changed",
		WorktreeID: "worktree", WorktreePath: "/tmp/changed",
		WorktreeBranch: "feature/changed", Status: "active",
	}
	wantUpdatedAt := envRepo.UpdatedAt
	for name, mutate := range map[string]func() error{
		"update": func() error { return repo.UpdateTaskEnvironmentRepo(ctx, envRepo) },
		"delete": func() error { return repo.DeleteTaskEnvironmentRepo(ctx, envRepo.ID) },
		"delete by environment": func() error {
			return repo.DeleteTaskEnvironmentReposByEnv(ctx, "env-barrier-mutation")
		},
		"branch": func() error {
			return repo.UpdateTaskSessionWorktreeBranch(ctx, "session-barrier-mutation", "feature/changed")
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := mutate(); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
				t.Fatalf("mutation error = %v, want ErrTaskCleanupInProgress", err)
			}
		})
	}
	if !envRepo.UpdatedAt.Equal(wantUpdatedAt) {
		t.Fatalf("fenced update changed caller generation: got %v want %v", envRepo.UpdatedAt, wantUpdatedAt)
	}
	var path, branch, status string
	if err := repo.db.QueryRow(`
		SELECT worktree_path, worktree_branch, status
		FROM task_environment_repos WHERE id = 'repo-row-barrier-mutation'
	`).Scan(&path, &branch, &status); err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/worktree" || branch != "feature/original" || status != "active" {
		t.Fatalf("barrier mutation changed row: path=%q branch=%q status=%q", path, branch, status)
	}
}

func TestEnvironmentRepoOrphanRejectsMutationAndEnvironmentIDReuse(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-orphan-owner")
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-durable-orphan", TaskID: "task-orphan-owner",
		ExecutorType: "worktree", WorkspacePath: "/tmp", Status: models.TaskEnvironmentStatusReady,
		Repos: []*models.TaskEnvironmentRepo{{
			ID: "repo-row-durable-orphan", RepositoryID: "repository",
			WorktreeID: "worktree", WorktreePath: "/tmp/worktree",
			WorktreeBranch: "feature/original", Status: "active",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteTask(ctx, "task-orphan-owner"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTaskEnvironmentRepo(ctx, &models.TaskEnvironmentRepo{
		ID: "repo-row-durable-orphan", WorktreeID: "worktree", WorktreePath: "/tmp/changed",
		Status: "active",
	}); err == nil {
		t.Fatal("orphan update succeeded")
	}
	if err := repo.DeleteTaskEnvironmentRepo(ctx, "repo-row-durable-orphan"); err == nil {
		t.Fatal("orphan delete succeeded")
	}
	seedBarrierTask(t, repo, "task-reuse-attempt")
	err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-durable-orphan", TaskID: "task-reuse-attempt",
		ExecutorType: "worktree", WorkspacePath: "/tmp/reused", Status: models.TaskEnvironmentStatusReady,
	})
	if err == nil {
		t.Fatal("environment ID with durable tombstone was reused")
	}
}

func TestSQLiteCleanupReservationAndEnvironmentMutationShareWriterOrder(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-sqlite-linearization")
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-sqlite-linearization", TaskID: "task-sqlite-linearization",
		ExecutorType: "worktree", WorkspacePath: "/tmp", Status: models.TaskEnvironmentStatusReady,
		Repos: []*models.TaskEnvironmentRepo{{
			ID: "repo-row-sqlite-linearization", RepositoryID: "repository",
			WorktreeID: "worktree", WorktreePath: "/tmp/original",
			WorktreeBranch: "feature/original", Status: "active",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	mutationTx, err := repo.db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mutationTx.Rollback() }()
	if err := repo.mutableEnvironmentRepoByIDLocked(ctx, mutationTx, "repo-row-sqlite-linearization"); err != nil {
		t.Fatal(err)
	}
	if _, err := mutationTx.Exec(`
		UPDATE task_environment_repos
		SET worktree_path = '/tmp/mutation-winner'
		WHERE id = 'repo-row-sqlite-linearization'
	`); err != nil {
		t.Fatal(err)
	}

	waitsBefore := repo.db.Stats().WaitCount
	started := make(chan struct{})
	reserved := make(chan error, 1)
	go func() {
		close(started)
		reserved <- repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
			ID: "job-sqlite-linearization", OperationID: "archive:sqlite-linearization",
			TaskID: "task-sqlite-linearization", Trigger: models.TaskResourceCleanupTriggerArchive,
			State: models.TaskResourceCleanupStatePrepared,
		})
	}()
	<-started
	waitForSQLiteWriterWait(t, repo.db, waitsBefore)
	if err := mutationTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-reserved; err != nil {
		t.Fatalf("reservation after mutation commit: %v", err)
	}

	update := &models.TaskEnvironmentRepo{
		ID: "repo-row-sqlite-linearization", RepositoryID: "repository",
		WorktreeID: "worktree", WorktreePath: "/tmp/late-mutation",
		WorktreeBranch: "feature/late", Status: "active",
	}
	if err := repo.UpdateTaskEnvironmentRepo(ctx, update); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("mutation after reservation error = %v, want ErrTaskCleanupInProgress", err)
	}
	var path string
	if err := repo.db.Get(&path, `
		SELECT worktree_path FROM task_environment_repos
		WHERE id = 'repo-row-sqlite-linearization'
	`); err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/mutation-winner" {
		t.Fatalf("late mutation changed path to %q", path)
	}
}

func waitForSQLiteWriterWait(t *testing.T, database *sqlx.DB, before int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for database.Stats().WaitCount <= before {
		if time.Now().After(deadline) {
			t.Fatal("cleanup reservation did not wait for the SQLite writer transaction")
		}
		runtime.Gosched()
	}
}

// TestTaskCleanupBarrier_CommittedCreationIsIncludedInInventory proves the
// inverse race ordering: a session/worktree that commits before the barrier
// is reserved remains visible to the later inventory.
func TestTaskCleanupBarrier_CommittedCreationIsIncludedInInventory(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-order")

	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-order", TaskID: "task-barrier-order", State: models.TaskSessionStateCreated,
	}); err != nil {
		t.Fatalf("CreateTaskSession: %v", err)
	}
	reserveBarrier(t, repo, "task-barrier-order", "op-order")

	sessions, err := repo.ListTaskSessions(ctx, "task-barrier-order")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "session-order" {
		t.Fatalf("inventory = %+v, want the committed session", sessions)
	}
}

// TestTaskCleanupBarrier_ReleasedBarrierAllowsCreation proves a completed or
// cancelled barrier no longer blocks session creation.
func TestTaskCleanupBarrier_ReleasedBarrierAllowsCreation(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-released")
	reserveBarrier(t, repo, "task-barrier-released", "op-released")
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "op-released")
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if err := repo.CompleteTaskResourceCleanupJob(ctx, job.ID, models.TaskResourceCleanupStateCancelled, "", nil); err != nil {
		t.Fatalf("cancel barrier: %v", err)
	}

	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-released", TaskID: "task-barrier-released", State: models.TaskSessionStateCreated,
	}); err != nil {
		t.Fatalf("CreateTaskSession after barrier release: %v", err)
	}
}

func TestTaskCleanupBarrierPostgres_RejectsSessionCreation(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	if err != nil {
		t.Fatalf("init postgres: %v", err)
	}
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-pg")
	reserveBarrier(t, repo, "task-barrier-pg", "op-pg")

	err = repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-pg", TaskID: "task-barrier-pg", State: models.TaskSessionStateCreated,
	})
	if !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("postgres CreateTaskSession error = %v, want ErrTaskCleanupInProgress", err)
	}
}

func TestTaskCleanupBarrierPostgres_AllowsCreationWithoutBarrier(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	if err != nil {
		t.Fatalf("init postgres: %v", err)
	}
	ctx := context.Background()
	seedBarrierTask(t, repo, "task-barrier-pg-open")

	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-pg-open", TaskID: "task-barrier-pg-open", State: models.TaskSessionStateCreated,
	}); err != nil {
		t.Fatalf("postgres CreateTaskSession without barrier: %v", err)
	}
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-pg-open", TaskID: "task-barrier-pg-open", ExecutorType: "worktree",
		WorkspacePath: "/tmp", Status: models.TaskEnvironmentStatusReady,
	}); err != nil {
		t.Fatalf("postgres CreateTaskEnvironment without barrier: %v", err)
	}
}
