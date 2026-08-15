package sqlite

import (
	"fmt"
	"strings"

	"github.com/kandev/kandev/internal/db/dialect"
)

// detachTaskEnvironmentRepoLifecycle makes task_environment_repos a durable
// physical-reference authority. Its environment ID remains an immutable
// identity field, but deleting the logical parent must not erase the exact
// generation before physical cleanup can bind and tombstone it.
func (r *Repository) detachTaskEnvironmentRepoLifecycle() error {
	if dialect.IsPostgres(r.db.DriverName()) {
		return r.detachTaskEnvironmentRepoLifecyclePostgres()
	}
	return r.detachTaskEnvironmentRepoLifecycleSQLite()
}

func (r *Repository) detachTaskEnvironmentRepoLifecyclePostgres() error {
	rows, err := r.db.Query(`
		SELECT con.conname, src.attname, target.relname, dst.attname
		FROM pg_constraint con
		JOIN pg_class source ON source.oid = con.conrelid
		JOIN pg_namespace nsp ON nsp.oid = source.relnamespace
		JOIN pg_class target ON target.oid = con.confrelid
		JOIN LATERAL unnest(con.conkey) WITH ORDINALITY source_key(attnum, ord) ON true
		JOIN LATERAL unnest(con.confkey) WITH ORDINALITY target_key(attnum, ord)
			ON target_key.ord = source_key.ord
		JOIN pg_attribute src ON src.attrelid = source.oid AND src.attnum = source_key.attnum
		JOIN pg_attribute dst ON dst.attrelid = target.oid AND dst.attnum = target_key.attnum
		WHERE source.relname = 'task_environment_repos'
			AND nsp.nspname = current_schema()
			AND con.contype = 'f'
		ORDER BY con.conname, source_key.ord`)
	if err != nil {
		return fmt.Errorf("inspect task_environment_repos lifecycle foreign key: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var targetNames []string
	seen := make(map[string]int)
	for rows.Next() {
		var name, sourceColumn, targetTable, targetColumn string
		if err := rows.Scan(&name, &sourceColumn, &targetTable, &targetColumn); err != nil {
			return fmt.Errorf("scan task_environment_repos lifecycle foreign key: %w", err)
		}
		seen[name]++
		if sourceColumn == "task_environment_id" && targetTable == "task_environments" && targetColumn == "id" {
			targetNames = append(targetNames, name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect task_environment_repos lifecycle foreign key: %w", err)
	}
	for name, columns := range seen {
		if columns != 1 || !containsString(targetNames, name) {
			return fmt.Errorf("detach task_environment_repos lifecycle: unknown foreign key %q", name)
		}
	}
	targetNames = uniqueStrings(targetNames)
	if len(targetNames) > 1 {
		return fmt.Errorf("detach task_environment_repos lifecycle: multiple lifecycle foreign keys")
	}
	if len(targetNames) == 0 {
		return nil
	}
	name := strings.ReplaceAll(targetNames[0], `"`, `""`)
	if _, err := r.db.Exec(`ALTER TABLE task_environment_repos DROP CONSTRAINT "` + name + `"`); err != nil {
		return fmt.Errorf("detach task_environment_repos lifecycle foreign key: %w", err)
	}
	return nil
}

func (r *Repository) detachTaskEnvironmentRepoLifecycleSQLite() error {
	attached, err := r.sqliteTaskEnvironmentRepoLifecycleAttached()
	if err != nil || !attached {
		return err
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return fmt.Errorf("detach task_environment_repos lifecycle: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	statements := []string{
		`CREATE TABLE task_environment_repos_detached (
			id TEXT PRIMARY KEY,
			task_environment_id TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			branch_slug TEXT NOT NULL DEFAULT '',
			worktree_id TEXT DEFAULT '',
			worktree_path TEXT DEFAULT '',
			worktree_branch TEXT DEFAULT '',
			position INTEGER DEFAULT 0,
			error_message TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			merged_at TIMESTAMP,
			deleted_at TIMESTAMP,
			UNIQUE(task_environment_id, repository_id, branch_slug)
		)`,
		`INSERT INTO task_environment_repos_detached (
			id, task_environment_id, repository_id, branch_slug,
			worktree_id, worktree_path, worktree_branch,
			position, error_message, status,
			created_at, updated_at, merged_at, deleted_at
		) SELECT
			id, task_environment_id, repository_id, branch_slug,
			worktree_id, worktree_path, worktree_branch,
			position, error_message, status,
			created_at, updated_at, merged_at, deleted_at
		FROM task_environment_repos`,
		`DROP TABLE task_environment_repos`,
		`ALTER TABLE task_environment_repos_detached RENAME TO task_environment_repos`,
		`CREATE INDEX IF NOT EXISTS idx_task_environment_repos_env_id
			ON task_environment_repos(task_environment_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_environment_repos_repository_id
			ON task_environment_repos(repository_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("detach task_environment_repos lifecycle: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("detach task_environment_repos lifecycle: commit: %w", err)
	}
	return nil
}

func (r *Repository) sqliteTaskEnvironmentRepoLifecycleAttached() (bool, error) {
	rows, err := r.db.Query(`PRAGMA foreign_key_list(task_environment_repos)`)
	if err != nil {
		return false, fmt.Errorf("inspect task_environment_repos foreign keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	exact := 0
	for rows.Next() {
		var id, sequence int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return false, fmt.Errorf("scan task_environment_repos foreign key: %w", err)
		}
		if table == "task_environments" && from == "task_environment_id" && to == "id" {
			exact++
			continue
		}
		return false, fmt.Errorf("inspect task_environment_repos foreign keys: unknown %s.%s", table, from)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("inspect task_environment_repos foreign keys: %w", err)
	}
	if exact > 1 {
		return false, fmt.Errorf("inspect task_environment_repos foreign keys: multiple lifecycle keys")
	}
	return exact == 1, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
