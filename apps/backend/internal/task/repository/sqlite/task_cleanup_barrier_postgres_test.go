package sqlite

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/kandev/kandev/internal/testutil"
)

func TestPostgresCleanupReservationAndEnvironmentMutationShareTaskRowLock(t *testing.T) {
	dsn := testutil.PostgresDSNFromEnv(t)
	root := testutil.OpenIsolatedPostgres(t, dsn)
	if _, err := NewWithDB(root, root, nil); err != nil {
		t.Fatalf("init postgres schema: %v", err)
	}
	var schema string
	if err := root.Get(&schema, `SELECT current_schema()`); err != nil {
		t.Fatal(err)
	}
	mutationDB := openPostgresSchemaHandle(t, dsn, schema)
	reservationDB := openPostgresSchemaHandle(t, dsn, schema)
	mutationRepo := &Repository{db: mutationDB, ro: mutationDB}
	reservationRepo := &Repository{db: reservationDB, ro: reservationDB}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	seedPostgresBarrierEnvironment(t, root, "first")
	mutationTx, err := mutationDB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mutationTx.Rollback() }()
	if err := mutationRepo.mutableEnvironmentRepoByIDLocked(ctx, mutationTx, "repo-row-first"); err != nil {
		t.Fatal(err)
	}
	if _, err := mutationTx.Exec(`
		UPDATE task_environment_repos
		SET worktree_path = '/tmp/mutation-winner'
		WHERE id = 'repo-row-first'
	`); err != nil {
		t.Fatal(err)
	}
	var reservationPID int
	if err := reservationDB.Get(&reservationPID, `SELECT pg_backend_pid()`); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	reserved := make(chan error, 1)
	go func() {
		close(started)
		reserved <- reservationRepo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
			ID: "job-first", OperationID: "archive:first", TaskID: "task-first",
			Trigger: models.TaskResourceCleanupTriggerArchive,
			State:   models.TaskResourceCleanupStatePrepared,
		})
	}()
	<-started
	waitForPostgresBlock(t, root, reservationPID)
	if err := mutationTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-reserved; err != nil {
		t.Fatalf("reservation after mutation commit: %v", err)
	}
	var path string
	if err := root.Get(&path, `SELECT worktree_path FROM task_environment_repos WHERE id = 'repo-row-first'`); err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/mutation-winner" {
		t.Fatalf("winning mutation path = %q", path)
	}

	seedPostgresBarrierEnvironment(t, root, "reverse")
	reverseStarted := make(chan struct{})
	reverseReserved := make(chan error, 1)
	go func() {
		close(reverseStarted)
		reverseReserved <- reservationRepo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
			ID: "job-reverse", OperationID: "archive:reverse", TaskID: "task-reverse",
			Trigger: models.TaskResourceCleanupTriggerArchive,
			State:   models.TaskResourceCleanupStatePrepared,
		})
	}()
	<-reverseStarted
	if err := <-reverseReserved; err != nil {
		t.Fatalf("reverse reservation: %v", err)
	}
	update := &models.TaskEnvironmentRepo{
		ID: "repo-row-reverse", RepositoryID: "repository-reverse",
		WorktreeID: "worktree-reverse", WorktreePath: "/tmp/late-mutation",
		WorktreeBranch: "feature/late", Status: "active",
	}
	if err := mutationRepo.UpdateTaskEnvironmentRepo(ctx, update); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("mutation after reservation error = %v, want ErrTaskCleanupInProgress", err)
	}
	if err := root.Get(&path, `SELECT worktree_path FROM task_environment_repos WHERE id = 'repo-row-reverse'`); err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/reverse" {
		t.Fatalf("late mutation changed reverse path to %q", path)
	}
}

func openPostgresSchemaHandle(t *testing.T, dsn, schema string) *sqlx.DB {
	t.Helper()
	database, err := sqlx.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	return database
}

func seedPostgresBarrierEnvironment(t *testing.T, database *sqlx.DB, suffix string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := database.Exec(database.Rebind(`
		INSERT INTO tasks (id, title, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`), "task-"+suffix, "Task "+suffix, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(database.Rebind(`
		INSERT INTO task_environments (
			id, task_id, executor_type, status, created_at, updated_at
		) VALUES (?, ?, 'worktree', 'ready', ?, ?)
	`), "environment-"+suffix, "task-"+suffix, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(database.Rebind(`
		INSERT INTO task_environment_repos (
			id, task_environment_id, repository_id, worktree_id,
			worktree_path, worktree_branch, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?)
	`), "repo-row-"+suffix, "environment-"+suffix, "repository-"+suffix,
		"worktree-"+suffix, "/tmp/"+suffix, "feature/"+suffix, now, now); err != nil {
		t.Fatal(err)
	}
}

func waitForPostgresBlock(t *testing.T, observer *sqlx.DB, backendPID int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blockers int
		if err := observer.Get(&blockers, observer.Rebind(`
			SELECT COALESCE(cardinality(pg_blocking_pids(?)), 0)
		`), backendPID); err != nil {
			t.Fatal(err)
		}
		if blockers > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup reservation did not block on the mutation task-row lock")
		}
		runtime.Gosched()
	}
}
