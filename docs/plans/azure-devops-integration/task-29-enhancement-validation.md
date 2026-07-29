---
id: "29-enhancement-validation"
title: "Azure enhancement validation"
status: pending
wave: 14
depends_on:
  [
    "18-immediate-activation",
    "27-work-item-detail-ui",
    "28-automation-settings",
  ]
plan: "plan.md"
spec: "../../specs/azure-devops-integration/spec.md"
---

# Task 29: Azure Enhancement Validation

## Acceptance

- Desktop Playwright proves immediate post-save availability, restored
  preferences, detail/discussion, assignment, column move, quick-action task
  creation/linking, preset customization/reset, and watcher create/run/reset.
- Mobile Playwright proves direct card-to-detail navigation, full-height
  containment/internal scrolling, 44px actions, assignment/move/task launch,
  mobile watcher management, safe-area clearance, and no document horizontal
  overflow.
- Public docs describe remembered filters, read-only detail fields, allowed
  assignment/status changes, PAT identity semantics, quick actions/default
  queries, watchers, and immediate activation.

## Verification

- `pnpm e2e:run --host tests/integrations/azure-devops.spec.ts -- --project=chromium` from `apps/web`.
- `pnpm e2e:run --host tests/integrations/mobile-azure-devops.spec.ts -- --project=mobile-chrome` from `apps/web`.
- `node --test scripts/validate-public-docs.test.mjs` from the repository root.
- `node scripts/validate-public-docs.mjs` from the repository root.

## Files Likely Touched

- `apps/backend/internal/azuredevops/mock_client.go`
- `apps/backend/internal/azuredevops/mock_controller.go`
- `apps/backend/internal/azuredevops/mock_client_test.go`
- `apps/web/e2e/helpers/api-client.ts`
- `apps/web/e2e/tests/integrations/azure-devops.spec.ts`
- `apps/web/e2e/tests/integrations/mobile-azure-devops.spec.ts`
- `docs/public/integrations.md`

## Dependencies

Tasks 18, 27, and 28, transitively all enhancement tasks.

## Parallelism

Sequential. E2E fixtures and documentation assert the final integrated contract.

## Inputs

- All new scenarios in the Azure DevOps spec.
- Required skills during implementation: `/e2e`, `/mobile-parity`,
  `/docs-maintainer`.
- Existing Azure mock controller and desktop/mobile integration specs.

## Risks

- Run through the managed E2E command so both Go and production Vite artifacts
  are rebuilt; `--no-build` can silently validate stale code.

## Output Contract

Report desktop/mobile outcomes, geometry assertions, screenshots/visual
inspection, docs validation, exact commands/results, files changed, residual
risks, and update task/plan status.
