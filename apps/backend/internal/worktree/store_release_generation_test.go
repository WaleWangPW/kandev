package worktree

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSQLiteWorktreeReleaseRejectsIndependentDriftInEveryColumn(t *testing.T) {
	type mutation struct {
		name   string
		mutate func(*testing.T, *SQLiteStore, *WorktreeReleaseSnapshot)
	}
	mutations := []mutation{
		{name: "id", mutate: updateReleaseColumn("id", "row-replaced")},
		{name: "task_environment_id", mutate: func(t *testing.T, store *SQLiteStore, _ *WorktreeReleaseSnapshot) {
			store.seedSessionWithEnvironment(t, "release-other-session", "release-other-task")
			updateReleaseColumn("task_environment_id", "env-release-other-session")(t, store, nil)
		}},
		{name: "repository_id", mutate: updateReleaseColumn("repository_id", "repository-replaced")},
		{name: "branch_slug", mutate: updateReleaseColumn("branch_slug", "replacement-slug")},
		{name: "worktree_id", mutate: updateReleaseColumn("worktree_id", "worktree-replaced")},
		{name: "worktree_path", mutate: updateReleaseColumn("worktree_path", "/tmp/replaced")},
		{name: "worktree_branch", mutate: updateReleaseColumn("worktree_branch", "feature/replaced")},
		{name: "position", mutate: updateReleaseColumn("position", 91)},
		{name: "error_message", mutate: updateReleaseColumn("error_message", "new generation")},
		{name: "status", mutate: updateReleaseColumn("status", StatusMerged)},
		{name: "created_at", mutate: func(t *testing.T, store *SQLiteStore, snapshot *WorktreeReleaseSnapshot) {
			updateReleaseColumn("created_at", snapshot.CreatedAt.Add(time.Microsecond))(t, store, snapshot)
		}},
		{name: "updated_at", mutate: func(t *testing.T, store *SQLiteStore, snapshot *WorktreeReleaseSnapshot) {
			updateReleaseColumn("updated_at", snapshot.UpdatedAt.Add(time.Microsecond))(t, store, snapshot)
		}},
		{name: "merged_at", mutate: updateReleaseColumn("merged_at", nil)},
		{name: "deleted_at", mutate: func(t *testing.T, store *SQLiteStore, snapshot *WorktreeReleaseSnapshot) {
			updateReleaseColumn("deleted_at", snapshot.UpdatedAt.Add(time.Second))(t, store, snapshot)
		}},
	}

	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			expected := seedSQLiteReleaseGeneration(t, store, tc.name)
			tc.mutate(t, store, expected)
			before := sqliteReleaseRowFingerprint(t, store)

			if _, err := store.ReleaseWorktreeReferenceCAS(context.Background(), expected); !errors.Is(err, ErrWorktreeReleaseConflict) {
				t.Fatalf("ReleaseWorktreeReferenceCAS error = %v, want generation conflict", err)
			}
			after := sqliteReleaseRowFingerprint(t, store)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("release wrote after %s drift:\nbefore=%q\nafter =%q", tc.name, before, after)
			}
		})
	}
}

func TestSQLiteWorktreeReleaseRejectsRowReplacement(t *testing.T) {
	store := newTestStore(t)
	expected := seedSQLiteReleaseGeneration(t, store, "replacement")
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM task_environment_repos WHERE id = ?`, expected.ID); err != nil {
		t.Fatalf("delete expected generation: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO task_environment_repos (
			id, task_environment_id, repository_id, branch_slug,
			worktree_id, worktree_path, worktree_branch,
			position, error_message, status,
			created_at, updated_at, merged_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"replacement-row-id",
		expected.TaskEnvironmentID, expected.RepositoryID, expected.BranchSlug,
		expected.WorktreeID, expected.WorktreePath, expected.WorktreeBranch,
		expected.Position, expected.ErrorMessage, expected.Status,
		expected.CreatedAt, expected.UpdatedAt, expected.MergedAt, expected.DeletedAt,
	); err != nil {
		t.Fatalf("insert replacement generation: %v", err)
	}
	before := sqliteReleaseRowFingerprint(t, store)
	if _, err := store.ReleaseWorktreeReferenceCAS(ctx, expected); !errors.Is(err, ErrWorktreeReleaseConflict) {
		t.Fatalf("release replacement error = %v, want generation conflict", err)
	}
	if after := sqliteReleaseRowFingerprint(t, store); !reflect.DeepEqual(after, before) {
		t.Fatalf("release changed replacement row: before=%q after=%q", before, after)
	}
}

func TestSQLiteWorktreeReleaseCanonicalOffsetAndTombstoneReplay(t *testing.T) {
	store := newTestStore(t)
	expected := seedSQLiteReleaseGeneration(t, store, "offset")
	ctx := context.Background()

	const utcToken = "2026-08-15T04:00:00Z"
	const offsetToken = "2026-08-15T12:00:00+08:00"
	if _, err := store.db.ExecContext(ctx, `UPDATE task_environment_repos SET created_at = ?`, utcToken); err != nil {
		t.Fatalf("set UTC token: %v", err)
	}
	expected, err := store.PrepareWorktreeRelease(
		ctx, expected.WorktreeID, expected.TaskEnvironmentID, expected.RepositoryID, expected.BranchSlug,
	)
	if err != nil {
		t.Fatalf("prepare UTC generation: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE task_environment_repos SET created_at = ?`, offsetToken); err != nil {
		t.Fatalf("replace with equal offset token: %v", err)
	}

	released, err := store.ReleaseWorktreeReferenceCAS(ctx, expected)
	if err != nil {
		t.Fatalf("release equal offset instant: %v", err)
	}
	if !completeWorktreeTombstone(released) {
		t.Fatalf("released row = %+v, want complete tombstone", released)
	}
	var createdRaw string
	if err := store.db.QueryRowContext(ctx, `SELECT CAST(created_at AS TEXT) FROM task_environment_repos`).Scan(&createdRaw); err != nil {
		t.Fatalf("read created_at token: %v", err)
	}
	if createdRaw != offsetToken {
		t.Fatalf("created_at token = %q, want preserved %q", createdRaw, offsetToken)
	}

	beforeReplay := sqliteReleaseRowFingerprint(t, store)
	var changesBefore int64
	if err := store.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changesBefore); err != nil {
		t.Fatalf("read changes before replay: %v", err)
	}
	replayed, err := store.ReleaseWorktreeReferenceCAS(ctx, expected)
	if err != nil {
		t.Fatalf("replay complete tombstone: %v", err)
	}
	if !equalWorktreeReleaseSnapshot(released, replayed) {
		t.Fatalf("replayed row changed: released=%+v replayed=%+v", released, replayed)
	}
	afterReplay := sqliteReleaseRowFingerprint(t, store)
	if !reflect.DeepEqual(afterReplay, beforeReplay) {
		t.Fatalf("complete tombstone replay was not byte-stable:\nbefore=%q\nafter =%q", beforeReplay, afterReplay)
	}
	var changesAfter int64
	if err := store.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changesAfter); err != nil {
		t.Fatalf("read changes after replay: %v", err)
	}
	if changesAfter != changesBefore {
		t.Fatalf("complete tombstone replay executed a write: total_changes %d -> %d", changesBefore, changesAfter)
	}
}

func TestSQLiteWorktreeReleaseReplayRejectsTerminalTimestampDrift(t *testing.T) {
	for _, column := range []string{"updated_at", "deleted_at"} {
		t.Run(column, func(t *testing.T) {
			store := newTestStore(t)
			expected := seedSQLiteReleaseGeneration(t, store, "terminal-drift-"+column)
			released, err := store.ReleaseWorktreeReferenceCAS(context.Background(), expected)
			if err != nil {
				t.Fatalf("initial release: %v", err)
			}
			updateReleaseColumn(column, released.ReleaseAt.Add(time.Microsecond))(t, store, released)
			before := sqliteReleaseRowFingerprint(t, store)
			if _, err := store.ReleaseWorktreeReferenceCAS(context.Background(), expected); !errors.Is(err, ErrWorktreeReleaseConflict) {
				t.Fatalf("replay after %s drift error = %v, want generation conflict", column, err)
			}
			if after := sqliteReleaseRowFingerprint(t, store); !reflect.DeepEqual(after, before) {
				t.Fatalf("replay overwrote %s drift: before=%q after=%q", column, before, after)
			}
		})
	}
}

func TestSQLiteWorktreeReleaseRejectsPartialTombstones(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		deletedAt interface{}
	}{
		{name: "deleted_without_timestamp", status: StatusDeleted, deletedAt: nil},
		{name: "active_with_timestamp", status: StatusActive, deletedAt: "2026-08-15T04:00:00Z"},
		{name: "merged_with_timestamp", status: StatusMerged, deletedAt: "2026-08-15T04:00:00Z"},
		{name: "unknown_status", status: "future", deletedAt: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			expected := seedSQLiteReleaseGeneration(t, store, "partial-"+tc.name)
			if _, err := store.db.ExecContext(context.Background(), `
				UPDATE task_environment_repos SET status = ?, deleted_at = ?
			`, tc.status, tc.deletedAt); err != nil {
				t.Fatalf("seed partial tombstone: %v", err)
			}
			partial, err := store.PrepareWorktreeRelease(
				context.Background(), expected.WorktreeID, expected.TaskEnvironmentID,
				expected.RepositoryID, expected.BranchSlug,
			)
			if err != nil {
				t.Fatalf("prepare partial tombstone: %v", err)
			}
			before := sqliteReleaseRowFingerprint(t, store)
			if _, err := store.ReleaseWorktreeReferenceCAS(context.Background(), partial); !errors.Is(err, ErrWorktreeReleaseConflict) {
				t.Fatalf("partial release error = %v, want generation conflict", err)
			}
			if after := sqliteReleaseRowFingerprint(t, store); !reflect.DeepEqual(after, before) {
				t.Fatalf("partial tombstone was mutated: before=%q after=%q", before, after)
			}
		})
	}
}

func TestSQLiteWorktreeReleaseRejectsReactivation(t *testing.T) {
	store := newTestStore(t)
	active := seedSQLiteReleaseGeneration(t, store, "reactivation")
	terminal, err := store.ReleaseWorktreeReferenceCAS(context.Background(), active)
	if err != nil {
		t.Fatalf("initial release: %v", err)
	}
	if _, err := store.db.ExecContext(context.Background(), `
		UPDATE task_environment_repos
		SET status = ?, deleted_at = NULL, updated_at = ?
	`, StatusActive, terminal.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatalf("reactivate row: %v", err)
	}
	before := sqliteReleaseRowFingerprint(t, store)
	if _, err := store.ReleaseWorktreeReferenceCAS(context.Background(), terminal); !errors.Is(err, ErrWorktreeReleaseConflict) {
		t.Fatalf("release reactivated row error = %v, want generation conflict", err)
	}
	if after := sqliteReleaseRowFingerprint(t, store); !reflect.DeepEqual(after, before) {
		t.Fatalf("stale terminal release overwrote reactivation: before=%q after=%q", before, after)
	}
}

func TestSQLiteWorktreeReleaseConcurrentStaleGenerationFailsClosed(t *testing.T) {
	store := newTestStore(t)
	stale := seedSQLiteReleaseGeneration(t, store, "concurrent")
	if _, err := store.db.ExecContext(context.Background(), `UPDATE task_environment_repos SET position = ?`, stale.Position+1); err != nil {
		t.Fatalf("advance generation: %v", err)
	}
	winner, err := store.PrepareWorktreeRelease(
		context.Background(), stale.WorktreeID, stale.TaskEnvironmentID, stale.RepositoryID, stale.BranchSlug,
	)
	if err != nil {
		t.Fatalf("prepare winner: %v", err)
	}

	type result struct {
		name string
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for name, snapshot := range map[string]*WorktreeReleaseSnapshot{"stale": stale, "winner": winner} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, releaseErr := store.ReleaseWorktreeReferenceCAS(context.Background(), snapshot)
			results <- result{name: name, err: releaseErr}
		}()
	}
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
		t.Fatalf("current generation release: %v", got["winner"])
	}
	current, err := store.PrepareWorktreeRelease(
		context.Background(), winner.WorktreeID, winner.TaskEnvironmentID, winner.RepositoryID, winner.BranchSlug,
	)
	if err != nil {
		t.Fatalf("read final generation: %v", err)
	}
	if current.Position != winner.Position || !completeWorktreeTombstone(current) {
		t.Fatalf("final generation = %+v, want winner position %d tombstone", current, winner.Position)
	}
}

func seedSQLiteReleaseGeneration(t *testing.T, store *SQLiteStore, suffix string) *WorktreeReleaseSnapshot {
	t.Helper()
	ctx := context.Background()
	sessionID := "release-session-" + suffix
	taskID := "release-task-" + suffix
	store.seedSessionWithEnvironment(t, sessionID, taskID)
	createdAt := time.Date(2026, 8, 15, 4, 0, 0, 123_456_000, time.UTC)
	updatedAt := createdAt.Add(time.Second)
	mergedAt := createdAt.Add(500 * time.Millisecond)
	wt := &Worktree{
		ID:           "release-worktree-" + suffix,
		SessionID:    sessionID,
		RepositoryID: "release-repository-" + suffix,
		BranchSlug:   "release-slug-" + suffix,
		Path:         "/tmp/release-" + suffix,
		Branch:       "feature/release-" + suffix,
		Status:       StatusActive,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		MergedAt:     &mergedAt,
	}
	if err := store.CreateWorktree(ctx, wt); err != nil {
		t.Fatalf("create release worktree: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE task_environment_repos
		SET id = ?, position = ?, error_message = ?, created_at = ?, updated_at = ?, merged_at = ?
		WHERE worktree_id = ?
	`, "release-row-"+suffix, 7, "release warning", createdAt, updatedAt, mergedAt, wt.ID); err != nil {
		t.Fatalf("complete release generation: %v", err)
	}
	snapshot, err := store.PrepareWorktreeRelease(
		ctx, wt.ID, wt.TaskEnvironmentID, wt.RepositoryID, wt.BranchSlug,
	)
	if err != nil {
		t.Fatalf("prepare release generation: %v", err)
	}
	return snapshot
}

func updateReleaseColumn(column string, value interface{}) func(*testing.T, *SQLiteStore, *WorktreeReleaseSnapshot) {
	return func(t *testing.T, store *SQLiteStore, _ *WorktreeReleaseSnapshot) {
		t.Helper()
		query := fmt.Sprintf("UPDATE task_environment_repos SET %s = ?", column)
		if _, err := store.db.ExecContext(context.Background(), query, value); err != nil {
			t.Fatalf("mutate %s: %v", column, err)
		}
	}
}

func sqliteReleaseRowFingerprint(t *testing.T, store *SQLiteStore) []string {
	t.Helper()
	var mergedAt, deletedAt sql.NullString
	values := make([]string, 12)
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT id, task_environment_id, repository_id, branch_slug,
		       worktree_id, worktree_path, worktree_branch,
		       CAST(position AS TEXT), error_message, status,
		       CAST(created_at AS TEXT), CAST(updated_at AS TEXT),
		       CAST(merged_at AS TEXT), CAST(deleted_at AS TEXT)
		FROM task_environment_repos LIMIT 1
	`).Scan(
		&values[0], &values[1], &values[2], &values[3],
		&values[4], &values[5], &values[6], &values[7],
		&values[8], &values[9], &values[10], &values[11],
		&mergedAt, &deletedAt,
	); err != nil {
		t.Fatalf("fingerprint release row: %v", err)
	}
	values = append(values,
		fmt.Sprintf("%t:%s", mergedAt.Valid, mergedAt.String),
		fmt.Sprintf("%t:%s", deletedAt.Valid, deletedAt.String),
	)
	return values
}
