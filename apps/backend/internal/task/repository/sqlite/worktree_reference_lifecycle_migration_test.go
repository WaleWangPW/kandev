package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
)

func TestTaskDeletePreservesEnvironmentRepoReleaseGeneration(t *testing.T) {
	repo := newRepoForEntityTests(t)
	ctx := context.Background()
	seedWorkspace(t, repo, "ws-release-authority")
	if err := repo.CreateWorkflow(ctx, &models.Workflow{
		ID: "wf-release-authority", WorkspaceID: "ws-release-authority", Name: "workflow",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTask(ctx, &models.Task{
		ID: "task-release-authority", WorkspaceID: "ws-release-authority",
		WorkflowID: "wf-release-authority", Title: "task",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRepository(ctx, &models.Repository{
		ID: "repo-release-authority", WorkspaceID: "ws-release-authority", Name: "repo",
	}); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	updatedAt := createdAt.Add(time.Second)
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-release-authority", TaskID: "task-release-authority",
		ExecutorType: "worktree", WorkspacePath: "/tmp/release-authority-root",
		Status:    models.TaskEnvironmentStatusReady,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
		Repos: []*models.TaskEnvironmentRepo{{
			ID: "env-repo-release-authority", RepositoryID: "repo-release-authority",
			BranchSlug: "release", WorktreeID: "worktree-release-authority",
			WorktreePath: "/tmp/release-authority", WorktreeBranch: "feature/release-authority",
			Position: 4, ErrorMessage: "preserved", Status: "active",
			CreatedAt: createdAt, UpdatedAt: updatedAt,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	type generation struct {
		ID, EnvironmentID, RepositoryID, BranchSlug string
		WorktreeID, WorktreePath, WorktreeBranch    string
		Position                                    int
		ErrorMessage, Status                        string
		CreatedAt, UpdatedAt                        time.Time
		MergedAt, DeletedAt                         *time.Time
	}
	load := func() generation {
		t.Helper()
		var got generation
		if err := repo.db.QueryRowxContext(ctx, `
			SELECT id, task_environment_id, repository_id, branch_slug,
				worktree_id, worktree_path, worktree_branch,
				position, error_message, status,
				created_at, updated_at, merged_at, deleted_at
			FROM task_environment_repos WHERE id = ?
		`, "env-repo-release-authority").Scan(
			&got.ID, &got.EnvironmentID, &got.RepositoryID, &got.BranchSlug,
			&got.WorktreeID, &got.WorktreePath, &got.WorktreeBranch,
			&got.Position, &got.ErrorMessage, &got.Status,
			&got.CreatedAt, &got.UpdatedAt, &got.MergedAt, &got.DeletedAt,
		); err != nil {
			t.Fatalf("load authoritative row: %v", err)
		}
		return got
	}
	want := load()
	if err := repo.DeleteTask(ctx, "task-release-authority"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repo.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM task_environments WHERE id = ?`, "env-release-authority"); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("task environment count = %d, want cascade delete", count)
	}
	got := load()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("authoritative generation drifted:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestDetachTaskEnvironmentRepoLifecycleMigratesExistingSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lifecycle-attached.db")
	dbConn, err := db.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := sqlx.NewDb(dbConn, "sqlite3")
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		`CREATE TABLE task_environments (id TEXT PRIMARY KEY)`,
		`CREATE TABLE task_environment_repos (
			id TEXT PRIMARY KEY, task_environment_id TEXT NOT NULL,
			repository_id TEXT NOT NULL, branch_slug TEXT NOT NULL DEFAULT '',
			worktree_id TEXT DEFAULT '', worktree_path TEXT DEFAULT '',
			worktree_branch TEXT DEFAULT '', position INTEGER DEFAULT 0,
			error_message TEXT DEFAULT '', status TEXT NOT NULL DEFAULT 'active',
			created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
			merged_at TIMESTAMP, deleted_at TIMESTAMP,
			FOREIGN KEY (task_environment_id) REFERENCES task_environments(id) ON DELETE CASCADE,
			UNIQUE(task_environment_id, repository_id, branch_slug))`,
		`INSERT INTO task_environments VALUES ('env-legacy')`,
		`INSERT INTO task_environment_repos VALUES (
			'row-legacy', 'env-legacy', 'repo-legacy', '', 'wt-legacy', '/tmp/legacy',
			'feature/legacy', 3, 'warning', 'active',
			'2026-08-15T01:02:03Z', '2026-08-15T01:02:04Z', NULL, NULL)`,
	} {
		if _, err := sqlDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	repo := &Repository{db: sqlDB, ro: sqlDB}
	want := loadSQLiteLifecycleGeneration(t, sqlDB, "row-legacy")
	if err := repo.detachTaskEnvironmentRepoLifecycle(); err != nil {
		t.Fatal(err)
	}
	if err := repo.detachTaskEnvironmentRepoLifecycle(); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	attached, err := repo.sqliteTaskEnvironmentRepoLifecycleAttached()
	if err != nil {
		t.Fatal(err)
	}
	if attached {
		t.Fatal("environment lifecycle foreign key remains attached")
	}
	if got := loadSQLiteLifecycleGeneration(t, sqlDB, "row-legacy"); !reflect.DeepEqual(got, want) {
		t.Fatalf("existing generation changed during detach:\n got: %+v\nwant: %+v", got, want)
	}
	if _, err := sqlDB.Exec(`DELETE FROM task_environments WHERE id = 'env-legacy'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM task_environment_repos WHERE id = 'row-legacy'`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migrated authority row count = %d, want 1", count)
	}
}

type sqliteLifecycleGeneration struct {
	ID, EnvironmentID, RepositoryID, BranchSlug string
	WorktreeID, WorktreePath, WorktreeBranch    string
	Position                                    int
	ErrorMessage, Status                        string
	CreatedAt, UpdatedAt                        string
	MergedAt, DeletedAt                         sql.NullString
}

func loadSQLiteLifecycleGeneration(t *testing.T, database *sqlx.DB, id string) sqliteLifecycleGeneration {
	t.Helper()
	var got sqliteLifecycleGeneration
	if err := database.QueryRow(`
		SELECT id, task_environment_id, repository_id, branch_slug,
			worktree_id, worktree_path, worktree_branch,
			position, error_message, status,
			CAST(created_at AS TEXT), CAST(updated_at AS TEXT),
			CAST(merged_at AS TEXT), CAST(deleted_at AS TEXT)
		FROM task_environment_repos WHERE id = ?
	`, id).Scan(
		&got.ID, &got.EnvironmentID, &got.RepositoryID, &got.BranchSlug,
		&got.WorktreeID, &got.WorktreePath, &got.WorktreeBranch,
		&got.Position, &got.ErrorMessage, &got.Status,
		&got.CreatedAt, &got.UpdatedAt, &got.MergedAt, &got.DeletedAt,
	); err != nil {
		t.Fatalf("load SQLite lifecycle generation %s: %v", id, err)
	}
	return got
}

func TestDetachTaskEnvironmentRepoLifecycleSQLiteFailureRollsBack(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lifecycle-rollback.db")
	dbConn, err := db.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := sqlx.NewDb(dbConn, "sqlite3")
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		`CREATE TABLE task_environments (id TEXT PRIMARY KEY)`,
		`CREATE TABLE task_environment_repos (
			id TEXT PRIMARY KEY, task_environment_id TEXT NOT NULL,
			repository_id TEXT NOT NULL, branch_slug TEXT NOT NULL DEFAULT '',
			worktree_id TEXT DEFAULT '', worktree_path TEXT DEFAULT '',
			worktree_branch TEXT DEFAULT '', position INTEGER DEFAULT 0,
			error_message TEXT DEFAULT '', status TEXT NOT NULL DEFAULT 'active',
			created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
			merged_at TIMESTAMP, deleted_at TIMESTAMP,
			FOREIGN KEY (task_environment_id) REFERENCES task_environments(id) ON DELETE CASCADE,
			UNIQUE(task_environment_id, repository_id, branch_slug))`,
		`INSERT INTO task_environments VALUES ('env-rollback')`,
		`INSERT INTO task_environment_repos (
			id, task_environment_id, repository_id, created_at, updated_at
		) VALUES ('row-rollback', 'env-rollback', 'repo-rollback', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		// Force the first migration statement to fail inside the transaction.
		`CREATE TABLE task_environment_repos_detached (sentinel TEXT)`,
	} {
		if _, err := sqlDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	repo := &Repository{db: sqlDB, ro: sqlDB}
	if err := repo.detachTaskEnvironmentRepoLifecycle(); err == nil {
		t.Fatal("migration failure returned nil")
	}
	attached, err := repo.sqliteTaskEnvironmentRepoLifecycleAttached()
	if err != nil {
		t.Fatal(err)
	}
	if !attached {
		t.Fatal("failed migration detached the original lifecycle key")
	}
	var count int
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM task_environment_repos WHERE id = 'row-rollback'`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("original row count after rollback = %d, want 1", count)
	}
}
