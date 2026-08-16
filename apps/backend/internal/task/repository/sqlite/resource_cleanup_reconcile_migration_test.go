package sqlite

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
)

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
