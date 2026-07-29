---
id: "25-watcher-polling"
title: "Azure watcher polling"
status: pending
wave: 12
depends_on: ["24-watcher-persistence"]
plan: "plan.md"
spec: "../../specs/azure-devops-integration/spec.md"
---

# Task 25: Azure Watcher Polling

## Acceptance

- One Azure watcher poller schedules due enabled work-item and PR watches,
  executes provider-native WIQL/PR filters, and publishes normalized events only
  for matches not reserved in the current generation.
- Create and Run now perform an immediate bounded check; provider/auth/query
  failures record watch error and publish no task event.
- Closed/terminal provider items and cleanup policy are reconciled without
  deleting unrelated or manually created Kandev tasks.

## Verification

- `go test ./internal/azuredevops -run 'Test(WorkItem|PullRequest)Watch(Check|Initial|Trigger|Poller|Cleanup)'` from `apps/backend`.

## Files Likely Touched

- `apps/backend/internal/azuredevops/service_watches.go`
- `apps/backend/internal/azuredevops/service_watches_test.go`
- `apps/backend/internal/azuredevops/service_watch_events.go`
- `apps/backend/internal/azuredevops/poller_watches.go`
- `apps/backend/internal/azuredevops/poller_watches_test.go`
- `apps/backend/internal/azuredevops/client.go`
- `apps/backend/internal/azuredevops/rest_client.go`
- `apps/backend/internal/azuredevops/mock_client.go`
- `apps/backend/internal/events/types.go`

## Dependencies

Task 24.

## Parallelism

Sequential. Poll scheduling, provider checks, reservations, and cleanup share
watch state.

## Inputs

- Spec: watcher behavior, failure modes, and match scenario.
- GitLab `poller.go`, `service_watches.go`, and `service_issue_watches.go`.

## Risks

- WIQL may return large existing sets. Respect top/batch limits and reservation
  checks before event publication to avoid a task burst after restart.

## Output Contract

Report scheduling and dedup behavior, RED/GREEN commands, files changed, mock
coverage, risks, and update task/plan status.
