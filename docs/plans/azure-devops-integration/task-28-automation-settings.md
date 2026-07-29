---
id: "28-automation-settings"
title: "Azure automation settings"
status: pending
wave: 13
depends_on:
  [
    "23-provider-presets",
    "24-watcher-persistence",
    "25-watcher-polling",
    "26-watcher-dispatch",
  ]
plan: "plan.md"
spec: "../../specs/azure-devops-integration/spec.md"
---

# Task 28: Azure Automation Settings

## Acceptance

- Azure settings exposes self-explanatory Default queries and Quick actions
  editors with save coordination, per-family validation, and reset-to-current-
  defaults behavior.
- Responsive Work-item watches and Pull-request watches support create/edit,
  enable draft, Run now, reset preview/reset, delete, error display, workflow/
  step/repository/branch/profile selection, cleanup, interval, and in-flight cap.
- Desktop tables and mobile cards expose the same actions; phone create/edit
  uses a full-height internally scrolling dialog/drawer with safe-area clearance
  and no hover-only control.

## Verification

- `pnpm --filter @kandev/web test -- --run components/azure-devops/azure-devops-presets-settings.test.tsx components/azure-devops/azure-devops-watch-settings.test.tsx components/azure-devops/azure-devops-watch-form.test.ts` from `apps`.
- `pnpm run typecheck` from `apps/web`.
- `pnpm --filter @kandev/web lint` from `apps`.

## Files Likely Touched

- `apps/web/lib/types/azure-devops.ts`
- `apps/web/lib/api/domains/azure-devops-api.ts`
- `apps/web/lib/state/slices/azure-devops/types.ts`
- `apps/web/lib/state/slices/azure-devops/azure-devops-slice.ts`
- `apps/web/hooks/domains/azure-devops/use-azure-devops-provider-presets.ts`
- `apps/web/hooks/domains/azure-devops/use-azure-devops-watches.ts`
- `apps/web/components/azure-devops/azure-devops-settings.tsx`
- `apps/web/components/azure-devops/azure-devops-presets-settings.tsx`
- `apps/web/components/azure-devops/azure-devops-watch-settings.tsx`
- `apps/web/components/azure-devops/azure-devops-watch-dialog.tsx`
- `apps/web/components/azure-devops/azure-devops-watch-table.tsx`

## Dependencies

Tasks 23-26.

## Parallelism

Sequential. Settings state, shared save coordinator, and Azure store slice are
common to all controls.

## Inputs

- Spec: preset and watcher settings contracts/scenarios.
- GitHub default queries and action presets sections.
- GitLab watch settings/form/table as the closest responsive two-kind watcher
  composition.
- Shared `WatcherSettingsCard`, `useWatcherEnabledDrafts`, and reset dialog.
- Required skills during implementation: `/tdd`, `/mobile-parity`, `/e2e`.

## Mobile Design Contract

- Desktop outcome: configure presets and manage both watcher kinds in place.
- Phone entry point: the same settings sections render stacked watcher cards;
  visible Add Watch buttons open the corresponding full-height editor.
- The editor header/actions remain fixed, one body owns vertical scrolling,
  picker/menu rows are at least 44px, and bottom actions clear safe area.
- Domain hooks and form normalization are shared; only table-versus-card and
  dialog-versus-drawer presentation differ.

## Risks

- Register every editable section with the settings save coordinator; do not
  add competing page-local save controls for query/action presets.

## Output Contract

Report settings behavior, responsive composition, RED/GREEN commands, rendered
mobile inspection, files changed, risks, and update task/plan status.
