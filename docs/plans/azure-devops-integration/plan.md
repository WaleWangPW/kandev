---
spec: docs/specs/azure-devops-integration/spec.md
created: 2026-07-17
updated: 2026-07-29
status: building
---

# Azure DevOps Integration Plan

## Scope

Implement the Azure DevOps Services integration defined in
[`../../specs/azure-devops-integration/spec.md`](../../specs/azure-devops-integration/spec.md):
workspace-scoped PAT configuration, direct REST work-item and pull-request
reads, persistent task PR links, responsive settings/browse surfaces, immediate
integration availability updates, provider-neutral remote repository selection,
server-side authenticated Azure clones, an Azure Boards view, remembered browse
preferences, rich work-item detail, provider-specific task actions, and
work-item/PR watchers. No Azure runtime path may require `gh` or `az`.

## Architecture

- Add an independent `internal/azuredevops` package. Do not add Azure methods to
  `github.Client` or translate Azure records into GitHub API structs.
- Reuse Jira's workspace-scoped config/secret/health patterns and GitLab's
  source-host REST/task-review patterns.
- Persist provider-native Azure identifiers. Normalize only the summary fields
  required by shared task UI.
- Persist canonical credential-free remote URLs for provider repositories and
  normalize new `remote_url` task inputs alongside the legacy `github_url`
  compatibility field.
- Resolve Azure clone credentials by workspace inside the backend clone path.
  Never expose the PAT to task metadata, an agent environment, a persisted URL,
  or command output.
- Use Azure DevOps REST API 7.1, an injected HTTP client for deterministic
  tests, bounded response bodies, context-aware requests, and typed API errors.
- Discover board context through project teams and team backlog levels, then
  combine the selected backlog's work-item references with its board column
  metadata. Hydrate work items through the existing bounded 200-item batches.
- Keep provider writes behind a fixed server-side field allowlist. Resolve the
  selected board's column/done reference names on the backend and use an Azure
  JSON Patch `/rev` test before assignment or board-position operations. Resolve
  Assign to me from the stored PAT identity; never accept an arbitrary identity
  supplied by the browser.
- Store Azure browse preferences in backend-owned portable user settings, keyed
  by workspace. Do not introduce localStorage/sessionStorage fallback reads or
  dual writes.
- Fetch work-item detail and discussion on demand. Normalize a small allowlist
  of planning fields, sanitize provider HTML, and page comments with Azure's
  opaque continuation token.
- Reuse GitHub's action/default-query preset shapes and GitLab's generation-safe
  issue/review watcher lifecycle without translating Azure records into either
  provider's models.
- Probe saved credentials immediately and return the resulting health in the
  config mutation response. Keep the 90-second health poll as recovery.
- Register the service as non-fatal during backend boot and expose mock routes
  only under `KANDEV_MOCK_AZURE_DEVOPS=true`.

## Backend Touch Points

- New package: `apps/backend/internal/azuredevops/`.
- Service wiring: `apps/backend/internal/backendapp/services.go`,
  `helpers.go`, and `main.go` where pollers are started.
- Repository provider parsing/discovery where provider enums are currently
  restricted to GitHub/GitLab.
- Runtime defaults: `profiles.yaml` for the E2E mock selector only.
- Workspace cleanup and task/repository validation through existing service
  interfaces rather than integration-specific SQL outside the new package.
- User settings models/DTO/store patches for
  `azure_devops_browse_preferences`.
- Work-item detail/comments/current-identity REST methods, normalized planning
  fields, and constrained assignment/column patch generation.
- Persistent task/work-item links, workspace query/action preset overrides, and
  Azure watcher/reservation stores.
- Azure watcher polling plus provider events, orchestrator watcher sources,
  self-heal, in-flight throttling, reset, and cleanup wiring.

## Frontend Touch Points

- Typed API and types under `apps/web/lib/api/domains/azure-devops-api.ts` and
  `apps/web/lib/types/azure-devops.ts`.
- Domain hooks under `apps/web/hooks/domains/azure-devops/`.
- Settings route and integration menu entry.
- `/azure-devops` browse page with a compact work-item/PR segmented view,
  desktop filter rail, and mobile filter sheet.
- `/azure-devops` Board mode becomes the default connected view. Its project,
  team, and board selectors share one board view model with a multi-column
  desktop DnD composition and a focused single-column mobile composition.
- Desktop card moves are optimistic and roll back on failure. Card editing uses
  a dialog on wider layouts and a full-height, safe-area-aware mobile surface;
  the mobile editor includes an explicit column picker instead of requiring
  touch drag.
- Task PR summary integration through a provider-tagged view model; Azure
  detail remains in Azure-specific components.
- A shared integration availability invalidation channel updates every consumer
  after configuration mutations while retaining periodic health polling.
- A shared source-control repository picker merges GitHub, GitLab, and Azure
  discovery and dispatches branch reads to the selected provider.
- Azure browse presets and saved views reuse the interaction model of GitHub and
  GitLab, with raw WIQL contained in an Advanced disclosure.
- The page hydrates portable Azure preferences before selecting defaults,
  persists only valid user changes, and falls back deterministically when a
  remembered provider ID is stale.
- Board cards and work-item rows open a desktop `Dialog`. Phone uses a dedicated
  full-height `Drawer` with a fixed header/action area and one internally
  scrolling detail/discussion body. Shared hooks own detail, assignment, move,
  quick-action, and error state.
- Work-item detail is read-only except for Assign to me, Unassign, and board
  column/split-state controls. A visible Task menu exposes workspace-configured
  actions and existing linked Kandev tasks.
- Azure settings adds Default queries, Quick actions, Work-item watches, and
  Pull-request watches using shared settings cards, watcher enable drafts,
  reset dialogs, responsive tables/cards, and save coordination.
- No required action may be hover-only or desktop-only.

## Tests

- Go table tests for URL validation, PAT headers, API errors, WIQL batching,
  PR conversion, workspace isolation, persistence, and route status codes.
- Go service tests for repository/task association validation and restart
  persistence.
- TypeScript unit tests for API request/response normalization and pure filter
  or status helpers.
- Playwright desktop and `mobile-chrome` flows using the Azure mock controller:
  connect, browse work items, browse PRs, and open PR feedback.
- Security review of secret isolation, URL/SSRF validation, response-size
  bounds, and error redaction before final verification.
- Go tests for provider-neutral task inputs, canonical remote URLs, and
  credential cleanup around Azure clone processes.
- Component and Playwright coverage for immediate availability, Enabled chips,
  provider grouping, preset/saved-view behavior, and mobile parity.
- Go REST/service/controller tests for team and board discovery, backlog-order
  hydration, dynamic column/done fields, allowlisted JSON Patch generation,
  revision conflicts, and mock mutation.
- TypeScript API/hook/view-model tests for dependent selector resets, board
  grouping, optimistic move rollback, conflict refresh, and normalized card
  updates.
- Playwright desktop and `mobile-chrome` flows for initial board load,
  assignment and column changes, reload persistence, mobile focused-column
  navigation, detail containment, and absence of document horizontal overflow.
- Go tests for immediate save-time auth health, work-item detail/comment
  pagination, PAT identity resolution, planning-field normalization, constrained
  patch operations, task/work-item associations, preset normalization, watcher
  generation/reservation safety, cleanup, and poll errors.
- User settings and frontend hook tests for per-user/per-workspace preference
  hydration, queued optimistic writes, stale provider fallback, and no browser
  storage dependency.
- Component tests for read-only detail, section retries, Assign to me/Unassign,
  quick-action payloads, linked-task indicators, preset editors, and responsive
  watcher controls.
- Playwright desktop and `mobile-chrome` flows for restored filters, detail and
  discussion, assignment/column changes, quick task creation, immediate
  availability, default-query/action customization, and watcher create/run/reset.

## Verification

- `rtk make -C apps/backend fmt`
- `rtk go test ./internal/azuredevops/...` from `apps/backend`
- `rtk make -C apps/backend test`
- `rtk make -C apps/backend lint`
- Task files define the exact focused Go and Vitest commands for each new
  behavior; run those during TDD before the corresponding task is marked done.
- `pnpm e2e:run --host --project chromium -- e2e/tests/integrations/azure-devops.spec.ts` from `apps/web`
- `pnpm e2e:run --host --project mobile-chrome -- e2e/tests/integrations/mobile-azure-devops.spec.ts e2e/tests/task/mobile-create-task-remote-repo.spec.ts` from `apps/web`

## Risks

- Azure organization URLs are an outbound-request boundary. V1 accepts only
  canonical HTTPS `dev.azure.com/<organization>` URLs to avoid an SSRF-capable
  arbitrary host setting.
- WIQL returns references rather than hydrated work items and Azure caps batch
  retrieval at 200 IDs; ordering and partial omissions require explicit tests.
- Azure reviewer votes and branch policies do not map one-to-one to GitHub
  reviews and checks. Only summary states are shared.
- Existing task PR UI is GitHub-heavy. The implementation must extract only the
  smallest provider-tagged presentation contract required for Azure, not begin
  a broad GitHub/GitLab refactor.
- The task creation API and branch loader are GitHub-named today. Compatibility
  fields must remain accepted while internal contracts become provider-neutral.
- Azure PAT clone auth must not leak through process arguments, persisted remote
  URLs, executor metadata, structured logs, or agent-visible environment state.
- Remote executors receive credential-free clone URLs. A private Azure repo is
  guaranteed to clone through the backend materialization path; remote executor
  push/clone credentials remain separately configured.
- Existing workspace PATs may have only Work Items Read. Board reads continue
  to work, while mutations can return 403 until the user replaces the PAT with
  Work Items Read & write; the UI must preserve readable board data and show
  reconnect guidance.
- Board column and done field reference names are provider data and can differ
  by team/process. The browser sends column IDs only; the backend must derive
  and validate all provider-native patch paths and values.
- Azure board snapshots can exceed one work-item batch and can contain deleted
  references. Preserve backlog order, omit missing items, and keep the
  remaining board usable.
- Azure work-item types expose different estimate fields. Normalize only the
  documented planning-field allowlist and omit unavailable values instead of
  guessing a universal Effort field.
- Discussion uses a preview-version Azure endpoint and opaque continuation
  tokens. Keep paging isolated behind the Azure client contract.
- The PAT represents one Azure identity for the whole workspace; Assign to me
  means that identity, which may differ from the signed-in Kandev user's email.
- Portable preference writes can race rapid filter changes. Use one queued
  patch stream and ignore stale loads so an older response cannot overwrite the
  most recent in-memory selection.
- Watchers can fan out task creation. Reuse generation reservations, profile
  dependency checks, 60-second minimum polling, and `max_inflight_tasks`
  throttling before enabling them.

## Task Waves

Wave 1: backend foundation

- [x] [Task 01: Workspace configuration](task-01-workspace-configuration.md)
- [x] [Task 02: REST client](task-02-rest-client.md)

Wave 2: backend product reads

- [x] [Task 03: Work-item and PR services](task-03-read-services.md)
- [x] [Task 04: Task PR persistence and backend wiring](task-04-task-pr-wiring.md)

Wave 3: frontend

- [x] [Task 05: Frontend data and settings](task-05-frontend-settings.md)
- [x] [Task 06: Responsive browse and task PR UI](task-06-frontend-browse.md)

Wave 4: integrated validation

- [x] [Task 07: E2E, security review, and documentation](task-07-e2e-security-docs.md)

Wave 5: integration navigation and Azure browse UX

- [x] [Task 08: Immediate availability and integration identity](task-08-availability-and-identity.md)
- [x] [Task 09: Azure presets and saved views](task-09-azure-presets.md)

Wave 6: provider-neutral repository selection

- [x] [Task 10: Remote repository contracts and discovery](task-10-remote-repository-contracts.md)
- [x] [Task 11: Secure Azure repository materialization](task-11-secure-azure-clone.md)
- [x] [Task 12: Unified task repository picker](task-12-unified-repository-picker.md)

Wave 7: integrated validation

- [x] [Task 13: Cross-provider E2E, security review, and documentation](task-13-enhancement-validation.md)

Wave 8: editable Azure board backend

- [x] [Task 14: Board discovery and snapshots](task-14-board-discovery.md)
- [x] [Task 15: Revision-safe work-item mutations](task-15-board-mutations.md)

Wave 9: responsive board UI

- [x] [Task 16: Editable desktop and mobile board](task-16-board-ui.md)

Wave 10: board validation and documentation

- [x] [Task 17: Board E2E, docs, and verification](task-17-board-validation.md)

Wave 11: connection, preference, and work-item contracts

- [x] [Task 18: Immediate connection activation](task-18-immediate-activation.md)
- [x] [Task 19: Portable Azure browse preferences](task-19-browse-preferences.md)
- [x] [Task 20: Work-item detail contracts](task-20-work-item-detail.md)
- [x] [Task 21: Constrained work-item mutations](task-21-work-item-mutations.md)
- [x] [Task 22: Task work-item associations](task-22-task-work-item-links.md)
- [ ] [Task 23: Azure provider presets](task-23-provider-presets.md)

Wave 12: watcher backend

- [ ] [Task 24: Azure watcher persistence](task-24-watcher-persistence.md)
- [ ] [Task 25: Azure watcher polling](task-25-watcher-polling.md)
- [ ] [Task 26: Azure watcher dispatch](task-26-watcher-dispatch.md)

Wave 13: responsive frontend

- [ ] [Task 27: Responsive work-item detail](task-27-work-item-detail-ui.md)
- [ ] [Task 28: Azure automation settings](task-28-automation-settings.md)

Wave 14: integrated validation

- [ ] [Task 29: Azure enhancement validation](task-29-enhancement-validation.md)

Tasks within a wave are listed separately for ownership clarity but should run
sequentially in the current workspace when they touch the same package or state
composition files.
