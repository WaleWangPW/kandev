---
id: "26-watcher-dispatch"
title: "Azure watcher dispatch"
status: pending
wave: 12
depends_on: ["25-watcher-polling"]
plan: "plan.md"
spec: "../../specs/azure-devops-integration/spec.md"
---

# Task 26: Azure Watcher Dispatch

## Acceptance

- Provider-specific work-item and PR watcher sources build Kandev task requests
  with Azure URL/identity metadata, repository/base branch, workflow context,
  selected profiles, and interpolated prompt.
- Shared dispatch reserves before creation, enforces `max_inflight_tasks`,
  attaches task ID generation-safely, releases retryable failures, and
  self-disables watches whose referenced dependencies were deleted.
- Backend startup registers the sources, event handlers, poller lifecycle, task
  dependency checks, and watcher metadata keys used by open-task counting.

## Verification

- `go test ./internal/orchestrator -run 'TestAzureDevOps(WorkItem|PullRequest)WatcherSource|TestAzureDevOpsWatcherDispatch'` from `apps/backend`.
- `go test ./internal/backendapp -run 'Test.*AzureDevOps.*Watch'` from `apps/backend`.

## Files Likely Touched

- `apps/backend/internal/orchestrator/source_azuredevops.go`
- `apps/backend/internal/orchestrator/source_azuredevops_test.go`
- `apps/backend/internal/orchestrator/event_handlers_azuredevops.go`
- `apps/backend/internal/orchestrator/event_handlers_azuredevops_test.go`
- `apps/backend/internal/orchestrator/watcher_dispatch_wiring.go`
- `apps/backend/internal/backendapp/services.go`
- `apps/backend/internal/backendapp/helpers.go`
- `apps/backend/internal/task/repository/count_open_watcher_tasks_test.go`
- `apps/backend/internal/events/types.go`

## Dependencies

Task 25.

## Parallelism

Sequential. Registration and shared dispatch wiring are package-wide seams.

## Inputs

- Spec: watcher task-creation, dedup, self-heal, and reset scenarios.
- `source_gitlab.go` for separate review/issue sources.
- Shared `watcher_dispatch.go` and throttle contract.

## Risks

- The metadata key written by each source must exactly match
  `WatchMetadataKey`; otherwise the configured in-flight cap silently does
  nothing.

## Output Contract

Report dispatch lifecycle, metadata keys, RED/GREEN commands, files changed,
self-heal evidence, risks, and update task/plan status.
