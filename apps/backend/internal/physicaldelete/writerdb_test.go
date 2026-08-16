package physicaldelete

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLInventorySourceFailsClosedWhenProtectionSourceUnavailable(t *testing.T) {
	db := openInventoryTestDB(t)
	inventory, err := NewSQLInventorySource(db).Load(context.Background())
	if !errors.Is(err, ErrInventoryIncomplete) {
		t.Fatalf("Load error = %v, want ErrInventoryIncomplete", err)
	}
	if inventory.Complete {
		t.Fatal("incomplete read returned Complete=true")
	}
}

func TestSQLInventorySourceLoadsV088ProtectionSources(t *testing.T) {
	db := openInventoryTestDB(t)
	createInventorySchema(t, db)
	paths := inventoryTestPaths(t)
	execInventoryStatements(t, db,
		`INSERT INTO task_environments VALUES ('env-row', 'ready', ?)`, paths[0],
		`INSERT INTO task_environment_repos VALUES ('repo-row', 'active', ?)`, paths[1],
		`INSERT INTO executors_running VALUES ('exec-row', 'running', ?)`, paths[2],
		`INSERT INTO task_workspace_groups VALUES ('group-row', 'cleanup_pending', ?)`, paths[3],
		`INSERT INTO storage_quarantine_entries VALUES ('quarantine-row', 'quarantined', ?, ?)`, paths[4], paths[5],
		`INSERT INTO task_resource_cleanup_jobs VALUES ('cleanup-row', 'operation-test', 'archive', 'pending', '{}', 0, '', '', '', '', 0, NULL)`,
	)

	inventory, err := NewSQLInventorySource(db).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !inventory.Complete || len(inventory.ActiveWorktrees) != 0 ||
		len(inventory.TaskEnvironments) != 1 || len(inventory.EnvironmentRepositories) != 1 ||
		len(inventory.ExecutorWorktrees) != 1 || len(inventory.WorkspaceGroups) != 1 ||
		len(inventory.QuarantineEntries) != 1 || len(inventory.CleanupAnchors) != 0 {
		t.Fatalf("unexpected v0.88 inventory counts: %+v", inventory)
	}
	if _, err := inventory.validate(); err != nil {
		t.Fatalf("inventory.validate: %v", err)
	}
}

func TestSQLInventorySourceAcceptsOnlyKnownGenericCleanupMatrix(t *testing.T) {
	triggers := []string{"archive", "delete", "cascade_archive", "cascade_delete", "workspace_delete", "quick_chat_expire"}
	states := []string{"prepared", "pending", "running", "retry_wait", "succeeded", "failed", "cancelled"}
	for _, trigger := range triggers {
		for _, state := range states {
			t.Run(trigger+"/"+state, func(t *testing.T) {
				db := openInventoryTestDB(t)
				createInventorySchema(t, db)
				_, err := db.Exec(`INSERT INTO task_resource_cleanup_jobs VALUES (?, 'operation-test', ?, ?, '{}', 0, '', '', '', '', 0, NULL)`, "cleanup-row", trigger, state)
				if err != nil {
					t.Fatal(err)
				}
				inventory, err := NewSQLInventorySource(db).Load(context.Background())
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if !inventory.Complete || len(inventory.CleanupAnchors) != 0 {
					t.Fatalf("generic row became an anchor: %+v", inventory)
				}
			})
		}
	}
}

func TestSQLInventorySourceRejectsUnvalidatedReconcileAndFutureCleanup(t *testing.T) {
	tests := []struct {
		name    string
		trigger string
		state   string
		want    error
	}{
		{name: "reconcile", trigger: "archived_resource_reconcile", state: "pending", want: ErrInventoryIncomplete},
		{name: "future trigger", trigger: "future_cleanup", state: "pending", want: ErrUnknownInventory},
		{name: "future state", trigger: "archive", state: "future_state", want: ErrUnknownInventory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryTestDB(t)
			createInventorySchema(t, db)
			_, err := db.Exec(`INSERT INTO task_resource_cleanup_jobs VALUES ('cleanup-row', 'operation-test', ?, ?, '{}', 0, '', '', '', '', 0, NULL)`, tt.trigger, tt.state)
			if err != nil {
				t.Fatal(err)
			}
			inventory, err := NewSQLInventorySource(db).Load(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Load error = %v, want %v", err, tt.want)
			}
			if inventory.Complete {
				t.Fatal("rejected cleanup row returned Complete=true")
			}
		})
	}
}

func TestSQLInventorySourceRejectsUnknownStatePerResourceKind(t *testing.T) {
	tests := []struct {
		name      string
		statement string
	}{
		{name: "environment", statement: `INSERT INTO task_environments VALUES ('row', 'future', ?)`},
		{name: "environment repo", statement: `INSERT INTO task_environment_repos VALUES ('row', 'future', ?)`},
		{name: "executor", statement: `INSERT INTO executors_running VALUES ('row', 'future', ?)`},
		{name: "workspace group", statement: `INSERT INTO task_workspace_groups VALUES ('row', 'future', ?)`},
		{name: "quarantine", statement: `INSERT INTO storage_quarantine_entries VALUES ('row', 'future', ?, ?)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryTestDB(t)
			createInventorySchema(t, db)
			paths := inventoryTestPaths(t)
			args := []any{paths[0]}
			if tt.name == "quarantine" {
				args = []any{paths[0], paths[1]}
			}
			if _, err := db.Exec(tt.statement, args...); err != nil {
				t.Fatal(err)
			}
			inventory, err := NewSQLInventorySource(db).Load(context.Background())
			if !errors.Is(err, ErrUnknownInventory) {
				t.Fatalf("Load error = %v, want ErrUnknownInventory", err)
			}
			if inventory.Complete {
				t.Fatal("unknown resource state returned Complete=true")
			}
		})
	}
}

func openInventoryTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createInventorySchema(t *testing.T, db *sqlx.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE task_environments (id TEXT, status TEXT, workspace_path TEXT)`,
		`CREATE TABLE task_environment_repos (id TEXT, status TEXT, worktree_path TEXT)`,
		`CREATE TABLE executors_running (id TEXT, status TEXT, worktree_path TEXT)`,
		`CREATE TABLE task_workspace_groups (id TEXT, cleanup_status TEXT, materialized_path TEXT)`,
		`CREATE TABLE storage_quarantine_entries (id TEXT, state TEXT, original_path TEXT, quarantine_path TEXT)`,
		`CREATE TABLE task_resource_cleanup_jobs (id TEXT, operation_id TEXT, trigger TEXT, state TEXT, resource_snapshot TEXT, snapshot_version INTEGER, snapshot_digest TEXT, resource_kind TEXT, resource_id TEXT, managed_root_key TEXT, anchor_revision INTEGER, active_scope_key TEXT)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
}

func inventoryTestPaths(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 6)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprintf("path-%d", i))
		if err := os.Mkdir(paths[i], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func execInventoryStatements(t *testing.T, db *sqlx.DB, entries ...any) {
	t.Helper()
	for len(entries) > 0 {
		statement, ok := entries[0].(string)
		if !ok {
			t.Fatal("statement entry is not a string")
		}
		entries = entries[1:]
		argCount := 0
		for _, char := range statement {
			if char == '?' {
				argCount++
			}
		}
		if len(entries) < argCount {
			t.Fatal("statement arguments are incomplete")
		}
		if _, err := db.Exec(statement, entries[:argCount]...); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
		entries = entries[argCount:]
	}
}
