---
id: "03-environment-repo-generation-cas"
title: "Add environment-repository generation CAS"
status: pending
wave: 2
depends_on: ["02-worktree-failure-atomicity"]
plan: "plan.md"
spec: "../../specs/tasks/archived-resource-safety/spec.md"
---

# Task 03: Add environment-repository generation CAS

- **Acceptance:** Release binds all fourteen v0.88 `task_environment_repos` columns: operation-bound identity snapshot `id`, `task_environment_id`, `repository_id`, `branch_slug`, `worktree_id`, `worktree_path`, `worktree_branch`, `created_at`; mutable CAS generation `position`, `error_message`, `status`, `updated_at`, `merged_at`, `deleted_at`. Complete tombstone replay is byte-stable and zero-write.
- **Acceptance:** Row replacement and an independent drift mutation for each of the fourteen columns, partial tombstone, reactivation, and concurrent stale release all fail closed without overwriting a newer generation.
- **Acceptance:** SQLite raw lexical and PostgreSQL typed timestamp behavior have explicit tests; PostgreSQL is `NOT_RUN` when the isolated DSN is absent.
- **Verification:** `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/worktree ./internal/agent/runtime/lifecycle -run 'Release|Cleanup|Environment' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test -race ./internal/worktree -run 'Release|Cleanup|Environment' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go vet ./internal/worktree ./internal/agent/runtime/lifecycle`
- **Files likely touched:** `internal/worktree/errors.go`; `manager.go`; `manager_cleanup.go`; `store.go`; new generation tests; `store_postgres_test.go`; compile-affected lifecycle fakes.
- **Dependencies:** Task 02.
- **Parallelism:** sequential because it extends the cleanup contract from Task 02.
- **Inputs:** archived-resource-safety Data model; exact v0.88 `task_environment_repos` schema.

## Results

Pending.
