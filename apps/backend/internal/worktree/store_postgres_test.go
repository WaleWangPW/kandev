package worktree

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/testutil"
)

func TestSQLiteStore_ReinitializesSchemaOnPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))

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

func TestPostgresWorktreeReleaseUsesTypedTimestampsAndZeroWriteReplay(t *testing.T) {
	store, expected, _ := newPostgresWorktreeReleaseStore(t, "typed")
	offset := time.FixedZone("UTC+8", 8*60*60)
	typedEquivalent := *expected
	typedEquivalent.CreatedAt = expected.CreatedAt.In(offset)
	typedEquivalent.UpdatedAt = expected.UpdatedAt.In(offset)
	if expected.MergedAt != nil {
		mergedAt := expected.MergedAt.In(offset)
		typedEquivalent.MergedAt = &mergedAt
	}

	released, err := store.ReleaseWorktreeReferenceCAS(context.Background(), &typedEquivalent)
	if err != nil {
		t.Fatalf("release typed-equivalent timestamps: %v", err)
	}
	if !completeWorktreeTombstone(released) {
		t.Fatalf("released row = %+v, want complete tombstone", released)
	}
	var beforeXID string
	if err := store.db.QueryRow(`SELECT xmin::text FROM task_environment_repos WHERE id = $1`, expected.ID).Scan(&beforeXID); err != nil {
		t.Fatalf("read tombstone xmin: %v", err)
	}
	if _, err := store.ReleaseWorktreeReferenceCAS(context.Background(), expected); err != nil {
		t.Fatalf("replay complete tombstone: %v", err)
	}
	var afterXID string
	if err := store.db.QueryRow(`SELECT xmin::text FROM task_environment_repos WHERE id = $1`, expected.ID).Scan(&afterXID); err != nil {
		t.Fatalf("read replay xmin: %v", err)
	}
	if afterXID != beforeXID {
		t.Fatalf("complete tombstone replay wrote a new tuple: xmin %s -> %s", beforeXID, afterXID)
	}
}

func TestPostgresWorktreeReleaseConcurrentStaleGenerationFailsClosed(t *testing.T) {
	store, stale, dsn := newPostgresWorktreeReleaseStore(t, "concurrent")
	if _, err := store.db.ExecContext(context.Background(), store.db.Rebind(`
		UPDATE task_environment_repos SET position = ? WHERE id = ?
	`), stale.Position+1, stale.ID); err != nil {
		t.Fatalf("advance generation: %v", err)
	}
	winner, err := store.PrepareWorktreeRelease(
		context.Background(), stale.WorktreeID, stale.TaskEnvironmentID, stale.RepositoryID, stale.BranchSlug,
	)
	if err != nil {
		t.Fatalf("prepare winner generation: %v", err)
	}

	var schema string
	if err := store.db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read isolated schema: %v", err)
	}
	peerDB, err := sqlx.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open peer postgres connection: %v", err)
	}
	peerDB.SetMaxOpenConns(1)
	peerDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = peerDB.Close() })
	if _, err := peerDB.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatalf("bind peer schema: %v", err)
	}
	peer, err := NewSQLiteStore(peerDB, peerDB)
	if err != nil {
		t.Fatalf("create peer worktree store: %v", err)
	}

	type result struct {
		name string
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	launch := func(name string, currentStore *SQLiteStore, snapshot *WorktreeReleaseSnapshot) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, releaseErr := currentStore.ReleaseWorktreeReferenceCAS(context.Background(), snapshot)
			results <- result{name: name, err: releaseErr}
		}()
	}
	launch("stale", store, stale)
	launch("winner", peer, winner)
	close(start)
	wg.Wait()
	close(results)

	got := make(map[string]error, 2)
	for result := range results {
		got[result.name] = result.err
	}
	if !errors.Is(got["stale"], ErrWorktreeReleaseConflict) {
		t.Fatalf("stale release error = %v, want generation conflict", got["stale"])
	}
	if got["winner"] != nil {
		t.Fatalf("winner release: %v", got["winner"])
	}
}

func TestPostgresWorktreeReleaseRejectsTypedTimestampDrift(t *testing.T) {
	store, expected, _ := newPostgresWorktreeReleaseStore(t, "drift")
	if _, err := store.db.ExecContext(context.Background(), store.db.Rebind(`
		UPDATE task_environment_repos SET updated_at = ? WHERE id = ?
	`), expected.UpdatedAt.Add(time.Microsecond), expected.ID); err != nil {
		t.Fatalf("mutate typed timestamp: %v", err)
	}
	if _, err := store.ReleaseWorktreeReferenceCAS(context.Background(), expected); !errors.Is(err, ErrWorktreeReleaseConflict) {
		t.Fatalf("release timestamp drift error = %v, want generation conflict", err)
	}
}

func newPostgresWorktreeReleaseStore(
	t *testing.T,
	suffix string,
) (*SQLiteStore, *WorktreeReleaseSnapshot, string) {
	t.Helper()
	dsn := testutil.PostgresDSNFromEnv(t)
	db := testutil.OpenIsolatedPostgres(t, dsn)
	if _, err := tasksqlite.NewWithDB(db, db, nil); err != nil {
		t.Fatalf("initialize task schema: %v", err)
	}
	store, err := NewSQLiteStore(db, db)
	if err != nil {
		t.Fatalf("create postgres worktree store: %v", err)
	}
	ctx := context.Background()
	taskID := "pg-release-task-" + suffix
	envID := "pg-release-env-" + suffix
	sessionID := "pg-release-session-" + suffix
	rowID := "pg-release-row-" + suffix
	worktreeID := "pg-release-worktree-" + suffix
	createdAt := time.Date(2026, 8, 15, 4, 0, 0, 123_456_000, time.UTC)
	updatedAt := createdAt.Add(time.Second)
	mergedAt := createdAt.Add(500 * time.Millisecond)
	statements := []struct {
		query string
		args  []interface{}
	}{
		{`INSERT INTO tasks (id, workspace_id, title, created_at, updated_at) VALUES (?, 'workspace', ?, ?, ?)`, []interface{}{taskID, taskID, createdAt, updatedAt}},
		{`INSERT INTO task_environments (id, task_id, executor_type, status, workspace_path, created_at, updated_at) VALUES (?, ?, 'worktree', 'ready', '/tmp/release', ?, ?)`, []interface{}{envID, taskID, createdAt, updatedAt}},
		{`INSERT INTO task_sessions (id, task_id, state, task_environment_id, started_at, updated_at) VALUES (?, ?, 'COMPLETED', ?, ?, ?)`, []interface{}{sessionID, taskID, envID, createdAt, updatedAt}},
		{`INSERT INTO task_environment_repos (id, task_environment_id, repository_id, branch_slug, worktree_id, worktree_path, worktree_branch, position, error_message, status, created_at, updated_at, merged_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, []interface{}{rowID, envID, "pg-release-repository-" + suffix, "pg-release-slug-" + suffix, worktreeID, "/tmp/pg-release-" + suffix, "feature/pg-release-" + suffix, 7, "release warning", StatusActive, createdAt, updatedAt, mergedAt}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, db.Rebind(statement.query), statement.args...); err != nil {
			t.Fatalf("seed postgres release generation: %v", err)
		}
	}
	expected, err := store.PrepareWorktreeRelease(
		ctx, worktreeID, envID, "pg-release-repository-"+suffix, "pg-release-slug-"+suffix,
	)
	if err != nil {
		t.Fatalf("prepare postgres release generation: %v", err)
	}
	return store, expected, dsn
}
