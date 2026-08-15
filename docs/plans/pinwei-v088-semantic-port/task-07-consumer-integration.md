---
id: "07-consumer-integration"
title: "Route managed-root consumers through central admission"
status: pending
wave: 6
depends_on: ["06-terminal-db-only-actions"]
plan: "plan.md"
spec: "../../specs/tasks/archived-resource-safety/spec.md"
---

# Task 07: Route managed-root consumers through central admission

- **Acceptance:** Worktree, task/session/workspace, Office GC, storage/quarantine, agentctl, database reset, recreate, and fallback deletion paths all deny before mutation when admission/executor is unavailable.
- **Acceptance:** Normal user workspace-file edit/delete APIs remain functional and explicitly outside managed-root cleanup.
- **Acceptance:** No alternate WebSocket, MCP, Office, or generic cleanup route can mutate versioned anchors or perform physical actions.
- **Verification:** `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./internal/worktree ./internal/task/... ./internal/office/... ./internal/system/storage/... ./internal/agentctl/server/process ./internal/backendapp -run 'Admission|Cleanup|Delete|Recreate|ArchivedResource' -count=1 && env -u KANDEV_TEST_POSTGRES_DSN go test -race ./internal/worktree ./internal/task/service ./internal/system/storage -run 'Admission|Cleanup|ArchivedResource' -count=1`
- **Files likely touched:** managed-root consumer files discovered by the central inventory; their focused tests; no unrelated file-edit handlers.
- **Dependencies:** Task 06.
- **Parallelism:** sequential because this is the global mutation-entry closure.
- **Inputs:** archived-resource-safety What/Out of scope; ADR 0009 and storage maintenance spec.

## Results

Pending.
