package worktree

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	dbutil "github.com/kandev/kandev/internal/db"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
)

func TestSQLiteStoreMutationFenceRejectsCleanupBarrier(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	store.seedSessionWithEnvironment(t, "session-fenced", "task-fenced")
	wt := &Worktree{
		ID: "worktree-fenced", SessionID: "session-fenced", RepositoryID: "repo-fenced",
		Path: "/tmp/original", Branch: "feature/original", Status: StatusActive,
	}
	if err := store.CreateWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO task_resource_cleanup_jobs (
			id, operation_id, task_id, trigger, state, resource_snapshot,
			created_at, updated_at
		) VALUES (
			'cleanup-fenced', 'operation-fenced', 'task-fenced', 'archive', 'prepared', '{}',
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`); err != nil {
		t.Fatal(err)
	}
	update := *wt
	update.Path = "/tmp/changed"
	update.Branch = "feature/changed"
	wantUpdatedAt := update.UpdatedAt
	if err := store.UpdateWorktree(ctx, &update); !errors.Is(err, ErrTaskCleanupInProgress) {
		t.Fatalf("UpdateWorktree error = %v, want ErrTaskCleanupInProgress", err)
	}
	if !update.UpdatedAt.Equal(wantUpdatedAt) {
		t.Fatalf("fenced update changed caller generation: got %v want %v", update.UpdatedAt, wantUpdatedAt)
	}
	if err := store.DeleteWorktree(ctx, wt.ID); !errors.Is(err, ErrTaskCleanupInProgress) {
		t.Fatalf("DeleteWorktree error = %v, want ErrTaskCleanupInProgress", err)
	}
	assertPersistedWorktreeBinding(t, store, wt.ID, "/tmp/original", "feature/original", StatusActive)
}

func TestSQLiteStoreMutationFenceRejectsDurableOrphan(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	store.seedSessionWithEnvironment(t, "session-orphan", "task-orphan")
	wt := &Worktree{
		ID: "worktree-orphan", SessionID: "session-orphan", RepositoryID: "repo-orphan",
		Path: "/tmp/orphan", Branch: "feature/orphan", Status: StatusActive,
	}
	if err := store.CreateWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = 'task-orphan'`); err != nil {
		t.Fatal(err)
	}
	update := *wt
	update.Path = "/tmp/replacement"
	wantUpdatedAt := update.UpdatedAt
	if err := store.UpdateWorktree(ctx, &update); !errors.Is(err, ErrWorktreeReleaseConflict) {
		t.Fatalf("UpdateWorktree error = %v, want ErrWorktreeReleaseConflict", err)
	}
	if !update.UpdatedAt.Equal(wantUpdatedAt) {
		t.Fatalf("orphan fence changed caller generation: got %v want %v", update.UpdatedAt, wantUpdatedAt)
	}
	if err := store.DeleteWorktree(ctx, wt.ID); !errors.Is(err, ErrWorktreeReleaseConflict) {
		t.Fatalf("DeleteWorktree error = %v, want ErrWorktreeReleaseConflict", err)
	}
	assertPersistedWorktreeBinding(t, store, wt.ID, "/tmp/orphan", "feature/orphan", StatusActive)
}

func assertPersistedWorktreeBinding(t *testing.T, store *SQLiteStore, worktreeID, path, branch, status string) {
	t.Helper()
	var gotPath, gotBranch, gotStatus string
	if err := store.db.QueryRow(`
		SELECT worktree_path, worktree_branch, status
		FROM task_environment_repos WHERE worktree_id = ?
	`, worktreeID).Scan(&gotPath, &gotBranch, &gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotPath != path || gotBranch != branch || gotStatus != status {
		t.Fatalf("persisted binding = (%q, %q, %q), want (%q, %q, %q)",
			gotPath, gotBranch, gotStatus, path, branch, status)
	}
}

// newTestStore opens a SQLite DB with the task repository schema (which owns
// the task_environment_repos tables the store reads and writes) and
// constructs a *SQLiteStore on it.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbConn, err := dbutil.OpenSQLite(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db := sqlx.NewDb(dbConn, "sqlite3")
	t.Cleanup(func() { _ = db.Close() })

	if _, err := tasksqlite.NewWithDB(db, db, nil); err != nil {
		t.Fatalf("init task schema: %v", err)
	}
	store, err := NewSQLiteStore(db, db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

// seedSessionWithEnvironment creates a task, an environment, and a session
// linked to that environment so worktree records can be persisted.
func (s *SQLiteStore) seedSessionWithEnvironment(t *testing.T, sessionID, taskID string) {
	t.Helper()
	ctx := context.Background()
	envID := "env-" + sessionID
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (id, workspace_id, title, created_at, updated_at)
		VALUES (?, 'workspace', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, taskID, taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO task_environments (id, task_id, executor_type, status, workspace_path, created_at, updated_at)
		VALUES (?, ?, 'worktree', 'ready', '/tmp/' || ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, envID, taskID, taskID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO task_sessions (id, task_id, state, task_environment_id, started_at, updated_at)
		VALUES (?, ?, 'COMPLETED', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, sessionID, taskID, envID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func TestSQLiteStore_ReinitializesSchema(t *testing.T) {
	dbConn, err := dbutil.OpenSQLite(filepath.Join(t.TempDir(), "worktree-replay.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db := sqlx.NewDb(dbConn, "sqlite3")
	t.Cleanup(func() { _ = db.Close() })

	if _, err := tasksqlite.NewWithDB(db, db, nil); err != nil {
		t.Fatalf("first task schema init: %v", err)
	}
	if _, err := NewSQLiteStore(db, db); err != nil {
		t.Fatalf("first worktree schema init: %v", err)
	}
	if _, err := tasksqlite.NewWithDB(db, db, nil); err != nil {
		t.Fatalf("second task schema init: %v", err)
	}
	if _, err := NewSQLiteStore(db, db); err != nil {
		t.Fatalf("second worktree schema init: %v", err)
	}
}

func TestSQLiteStore_ListActiveWorktreePaths(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()

	mustCreate := func(sessionID, taskID string, wt *Worktree) {
		t.Helper()
		store.seedSessionWithEnvironment(t, sessionID, taskID)
		if err := store.CreateWorktree(ctx, wt); err != nil {
			t.Fatalf("create %s: %v", wt.ID, err)
		}
	}

	// Active worktree with a real path — should appear.
	mustCreate("sess-1", "task-1", &Worktree{
		ID:           "wt-active-1",
		SessionID:    "sess-1",
		RepositoryID: "repo-1",
		Path:         "/tmp/kandev/tasks/task1/repoA",
		Branch:       "feature/task1",
		Status:       StatusActive,
		CreatedAt:    now, UpdatedAt: now,
	})

	// Active worktree with empty path — must be filtered out (the GC
	// can't act on an empty path anyway, and the SQL guard prevents
	// accidental wildcard matches).
	mustCreate("sess-2", "task-2", &Worktree{
		ID:           "wt-active-empty",
		SessionID:    "sess-2",
		RepositoryID: "repo-2",
		Path:         "",
		Branch:       "feature/task2",
		Status:       StatusActive,
		CreatedAt:    now, UpdatedAt: now,
	})

	// "Deleted" status worktree — must be filtered out.
	deletedAt := now
	mustCreate("sess-3", "task-3", &Worktree{
		ID:           "wt-deleted",
		SessionID:    "sess-3",
		RepositoryID: "repo-3",
		Path:         "/tmp/kandev/tasks/task3/repoA",
		Branch:       "feature/task3",
		Status:       "deleted",
		CreatedAt:    now, UpdatedAt: now,
		DeletedAt: &deletedAt,
	})

	// Second active worktree to confirm ordering doesn't matter.
	mustCreate("sess-4", "task-4", &Worktree{
		ID:           "wt-active-2",
		SessionID:    "sess-4",
		RepositoryID: "repo-4",
		Path:         "/tmp/kandev/tasks/task4/repoB",
		Branch:       "feature/task4",
		Status:       StatusActive,
		CreatedAt:    now, UpdatedAt: now,
	})

	got, err := store.ListActiveWorktreePaths(ctx)
	if err != nil {
		t.Fatalf("list paths: %v", err)
	}
	sort.Strings(got)

	want := []string{
		"/tmp/kandev/tasks/task1/repoA",
		"/tmp/kandev/tasks/task4/repoB",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
