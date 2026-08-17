package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	dbutil "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
)

const legacyTaskResourceCleanupJobsDDL = `
	CREATE TABLE task_resource_cleanup_jobs (
		id TEXT PRIMARY KEY,
		operation_id TEXT NOT NULL UNIQUE,
		task_id TEXT NOT NULL,
		trigger TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'pending',
		resource_snapshot TEXT NOT NULL DEFAULT '{}',
		attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt_at TIMESTAMP,
		last_error TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL,
		completed_at TIMESTAMP
	)`

func openArchivedResourceCleanupMigrationDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dbConn, err := dbutil.OpenSQLite(filepath.Join(t.TempDir(), "archived-resource-cleanup-migration.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database := sqlx.NewDb(dbConn, "sqlite3")
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func createLegacyArchivedResourceCleanupTable(t *testing.T, database *sqlx.DB) {
	t.Helper()
	if _, err := database.Exec(legacyTaskResourceCleanupJobsDDL); err != nil {
		t.Fatalf("create legacy cleanup table: %v", err)
	}
}

func TestArchivedResourceCleanupMigratesLegacyAndHalfMigratedSQLite(t *testing.T) {
	for _, tc := range []struct {
		name       string
		preMigrate func(*testing.T, *sqlx.DB)
	}{
		{name: "legacy"},
		{
			name: "half_migrated",
			preMigrate: func(t *testing.T, database *sqlx.DB) {
				t.Helper()
				for _, statement := range []string{
					`ALTER TABLE task_resource_cleanup_jobs ADD COLUMN snapshot_version INTEGER NOT NULL DEFAULT 0`,
					`ALTER TABLE task_resource_cleanup_jobs ADD COLUMN snapshot_digest TEXT NOT NULL DEFAULT ''`,
				} {
					if _, err := database.Exec(statement); err != nil {
						t.Fatalf("prepare half-migrated table: %v", err)
					}
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := openArchivedResourceCleanupMigrationDB(t)
			createLegacyArchivedResourceCleanupTable(t, database)
			if _, err := database.Exec(`
				INSERT INTO task_resource_cleanup_jobs (
					id, operation_id, task_id, trigger, state, resource_snapshot,
					attempts, next_attempt_at, last_error, created_at, updated_at, completed_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, "legacy-job", "legacy-operation", "legacy-task", "archive", "retry_wait", `{"legacy":true}`,
				3, "2026-08-17T01:02:03.123456+08:00", "preserved", "2026-08-17T01:00:00+08:00",
				"2026-08-17T01:01:00+08:00", nil); err != nil {
				t.Fatalf("seed legacy row: %v", err)
			}
			if tc.preMigrate != nil {
				tc.preMigrate(t, database)
			}

			repo, err := NewWithDB(database, database, nil)
			if err != nil {
				t.Fatalf("migrate cleanup schema: %v", err)
			}
			verifyArchivedResourceCleanupColumns(t, repo)
			if _, err := NewWithDB(database, database, nil); err != nil {
				t.Fatalf("replay cleanup migration: %v", err)
			}

			var id, operationID, taskID, trigger, state, snapshot, lastError string
			var attempts int
			var nextAttempt, createdAt, updatedAt, completedAt sql.NullString
			var snapshotVersion, anchorRevision int
			var snapshotDigest, resourceKind, resourceID, managedRootKey string
			var activeScope sql.NullString
			if err := database.QueryRow(`
				SELECT id, operation_id, task_id, trigger, state, resource_snapshot,
					attempts, CAST(next_attempt_at AS TEXT), last_error,
					CAST(created_at AS TEXT), CAST(updated_at AS TEXT), CAST(completed_at AS TEXT),
					snapshot_version, snapshot_digest, resource_kind, resource_id,
					managed_root_key, anchor_revision, active_scope_key
				FROM task_resource_cleanup_jobs WHERE id = 'legacy-job'
			`).Scan(
				&id, &operationID, &taskID, &trigger, &state, &snapshot,
				&attempts, &nextAttempt, &lastError, &createdAt, &updatedAt, &completedAt,
				&snapshotVersion, &snapshotDigest, &resourceKind, &resourceID,
				&managedRootKey, &anchorRevision, &activeScope,
			); err != nil {
				t.Fatalf("read migrated row: %v", err)
			}
			if id != "legacy-job" || operationID != "legacy-operation" || taskID != "legacy-task" ||
				trigger != "archive" || state != "retry_wait" || snapshot != `{"legacy":true}` ||
				attempts != 3 || !nextAttempt.Valid || nextAttempt.String != "2026-08-17T01:02:03.123456+08:00" ||
				lastError != "preserved" || !createdAt.Valid || createdAt.String != "2026-08-17T01:00:00+08:00" ||
				!updatedAt.Valid || updatedAt.String != "2026-08-17T01:01:00+08:00" || completedAt.Valid ||
				snapshotVersion != 0 || snapshotDigest != "" || resourceKind != "" || resourceID != "" ||
				managedRootKey != "" || anchorRevision != 0 || activeScope.Valid {
				t.Fatalf("legacy cleanup row changed during migration: id=%q operation=%q task=%q trigger=%q state=%q snapshot=%q attempts=%d next=%+v lastError=%q created=%+v updated=%+v completed=%+v headers=%d/%q/%q/%q/%q/%d/%+v",
					id, operationID, taskID, trigger, state, snapshot, attempts, nextAttempt, lastError,
					createdAt, updatedAt, completedAt, snapshotVersion, snapshotDigest, resourceKind,
					resourceID, managedRootKey, anchorRevision, activeScope)
			}
		})
	}
}

func TestArchivedResourceCleanupSchemaReplayPreservesRetainedAnchor(t *testing.T) {
	database := openArchivedResourceCleanupMigrationDB(t)
	if _, err := database.Exec(taskResourceCleanupSchemaDDL); err != nil {
		t.Fatalf("create current cleanup table: %v", err)
	}
	snapshot := `{"schema_version":2,"retention_anchor":{"state":"retained"}}`
	if _, err := database.Exec(`
		INSERT INTO task_resource_cleanup_jobs (
			id, operation_id, task_id, trigger, state, resource_snapshot,
			snapshot_version, snapshot_digest, resource_kind, resource_id,
			managed_root_key, anchor_revision, active_scope_key,
			attempts, next_attempt_at, last_error, created_at, updated_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "retained-job", "retained-operation", "retained-task", "reconcile", "retained", snapshot,
		2, "snapshot-digest", "git_worktree", "retained-worktree", "root-digest", 1, nil,
		1, nil, "", "2026-08-17T01:00:00.123456+08:00", "2026-08-17T01:01:00.123456+08:00",
		"2026-08-17T01:01:00.123456+08:00"); err != nil {
		t.Fatalf("seed retained anchor: %v", err)
	}
	if _, err := NewWithDB(database, database, nil); err != nil {
		t.Fatalf("first schema replay: %v", err)
	}
	if _, err := NewWithDB(database, database, nil); err != nil {
		t.Fatalf("second schema replay: %v", err)
	}
	var gotSnapshot, digest, kind, resourceID, rootKey string
	var version, revision, attempts int
	var createdAt, updatedAt, completedAt string
	if err := database.QueryRow(`
		SELECT resource_snapshot, snapshot_digest, resource_kind, resource_id, managed_root_key,
			snapshot_version, anchor_revision, attempts,
			CAST(created_at AS TEXT), CAST(updated_at AS TEXT), CAST(completed_at AS TEXT)
		FROM task_resource_cleanup_jobs WHERE id = 'retained-job'
	`).Scan(&gotSnapshot, &digest, &kind, &resourceID, &rootKey, &version, &revision, &attempts,
		&createdAt, &updatedAt, &completedAt); err != nil {
		t.Fatalf("read retained anchor after replay: %v", err)
	}
	if gotSnapshot != snapshot || digest != "snapshot-digest" || kind != "git_worktree" ||
		resourceID != "retained-worktree" || rootKey != "root-digest" || version != 2 ||
		revision != 1 || attempts != 1 || createdAt != "2026-08-17T01:00:00.123456+08:00" ||
		updatedAt != "2026-08-17T01:01:00.123456+08:00" || completedAt != "2026-08-17T01:01:00.123456+08:00" {
		t.Fatalf("retained anchor changed during schema replay: snapshot=%q digest=%q kind=%q resource=%q root=%q version=%d revision=%d attempts=%d created=%q updated=%q completed=%q",
			gotSnapshot, digest, kind, resourceID, rootKey, version, revision, attempts, createdAt, updatedAt, completedAt)
	}
}

func TestArchivedResourceCleanupSchemaRejectsMalformedActiveScopeIndex(t *testing.T) {
	database := openArchivedResourceCleanupMigrationDB(t)
	if _, err := database.Exec(taskResourceCleanupSchemaDDL); err != nil {
		t.Fatalf("create current cleanup table: %v", err)
	}
	if _, err := database.Exec(`CREATE INDEX uniq_task_resource_cleanup_jobs_active_scope ON task_resource_cleanup_jobs(active_scope_key)`); err != nil {
		t.Fatalf("seed malformed cleanup index: %v", err)
	}
	if _, err := NewWithDB(database, database, nil); err == nil || !strings.Contains(err.Error(), "required partial unique constraint") {
		t.Fatalf("malformed active-scope index accepted: %v", err)
	}
}

func TestArchivedResourceCleanupSchemaColumnsPresent(t *testing.T) {
	repo := newRepoForHealTests(t)
	verifyArchivedResourceCleanupColumns(t, repo)
}

func verifyArchivedResourceCleanupColumns(t *testing.T, repo *Repository) {
	t.Helper()
	driver := repo.db.DriverName()
	if dialect.IsPostgres(driver) {
		rows, err := repo.db.Query(`
			SELECT column_name, data_type, is_nullable, column_default
			FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'task_resource_cleanup_jobs'`)
		if err != nil {
			t.Fatalf("read postgres columns: %v", err)
		}
		defer func() { _ = rows.Close() }()
		actual := map[string]string{}
		for rows.Next() {
			var name, dataType, nullable string
			var defaultValue sql.NullString
			if err := rows.Scan(&name, &dataType, &nullable, &defaultValue); err != nil {
				t.Fatalf("scan column: %v", err)
			}
			actual[name] = dataType + "|" + nullable + "|" + defaultValue.String
		}
		for _, want := range []string{
			"snapshot_version|integer|NO|0",
			"snapshot_digest|text|NO|''",
			"resource_kind|text|NO|''",
			"resource_id|text|NO|''",
			"managed_root_key|text|NO|''",
			"anchor_revision|bigint|NO|0",
		} {
			if _, ok := actual[want[:strings.Index(want, "|")]]; !ok {
				t.Fatalf("missing column: %s", want)
			}
		}
		return
	}
	rows, err := repo.db.Query(`PRAGMA table_info('task_resource_cleanup_jobs')`)
	if err != nil {
		t.Fatalf("read sqlite columns: %v", err)
	}
	defer func() { _ = rows.Close() }()
	actual := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typeName string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		actual[name] = true
	}
	for _, want := range []string{
		"snapshot_version", "snapshot_digest", "resource_kind", "resource_id",
		"managed_root_key", "anchor_revision", "active_scope_key",
	} {
		if !actual[want] {
			t.Fatalf("missing sqlite column: %s", want)
		}
	}
}

func TestArchivedResourceCleanupPartialUniqueIndexPresent(t *testing.T) {
	repo := newRepoForHealTests(t)
	driver := repo.db.DriverName()
	if dialect.IsPostgres(driver) {
		var unique, valid bool
		var predicate string
		if err := repo.db.QueryRow(archivedResourceCleanupPostgresIndexVerificationSQL).Scan(
			&unique, &valid, &predicate,
		); err != nil {
			t.Fatalf("verify postgres index: %v", err)
		}
		if !unique || !valid {
			t.Fatalf("postgres index not enforced as partial unique: unique=%v valid=%v", unique, valid)
		}
		if normalizeArchivedResourceIndexPredicate(predicate) != "active_scope_key is not null" {
			t.Fatalf("postgres index predicate = %q", predicate)
		}
		return
	}
	var unique, partial int
	if err := repo.db.QueryRow(`
		SELECT "unique", partial FROM pragma_index_list('task_resource_cleanup_jobs')
		WHERE name = 'uniq_task_resource_cleanup_jobs_active_scope'`).Scan(&unique, &partial); err != nil {
		t.Fatalf("verify sqlite index: %v", err)
	}
	if unique != 1 || partial != 1 {
		t.Fatalf("sqlite index not partial unique: unique=%d partial=%d", unique, partial)
	}
	var indexedColumn string
	if err := repo.db.QueryRow(`
		SELECT name FROM pragma_index_info('uniq_task_resource_cleanup_jobs_active_scope') WHERE seqno = 0`).Scan(&indexedColumn); err != nil {
		t.Fatalf("read sqlite index column: %v", err)
	}
	if indexedColumn != "active_scope_key" {
		t.Fatalf("indexed column = %q, want active_scope_key", indexedColumn)
	}
}

func TestArchivedResourceReconcileRejectsInvalidState(t *testing.T) {
	repo := newRepoForHealTests(t)
	if _, _, err := repo.ClaimArchivedResourceReconcileJob(t.Context(), "missing"); err == nil {
		t.Fatal("claim of missing job accepted")
	}
	if _, err := repo.CancelNeverClaimedArchivedResourceReconcile(t.Context(), &models.TaskResourceCleanupJob{}); err == nil {
		t.Fatal("cancel of non-pristine job accepted")
	}
	if _, err := repo.GetRunningArchivedResourceReconcileJob(t.Context(), "missing"); err == nil {
		t.Fatal("running lookup of missing job accepted")
	}
}

func TestArchivedResourceReconcileCompletionNotFoundOnUnclaimed(t *testing.T) {
	repo := newRepoForHealTests(t)
	_, err := repo.CompleteArchivedResourceReconcileRetention(t.Context(), "missing", 1)
	if err == nil {
		t.Fatal("complete on missing job accepted")
	}
}
