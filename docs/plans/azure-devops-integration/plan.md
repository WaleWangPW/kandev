# Azure DevOps Integration Plan

## Scope

Implement the Azure DevOps Services integration defined in
[`../../specs/azure-devops-integration/spec.md`](../../specs/azure-devops-integration/spec.md):
workspace-scoped PAT configuration, direct REST work-item and pull-request
reads, persistent task PR links, responsive settings/browse surfaces, immediate
integration availability updates, provider-neutral remote repository selection,
server-side authenticated Azure clones, and an editable Azure Boards view. No
Azure runtime path may require `gh` or `az`.

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
  JSON Patch `/rev` test before title, assignee, tags, or board-position
  operations.
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
- Playwright desktop and `mobile-chrome` flows for initial board load, card
  editing, moving, reload persistence, mobile focused-column navigation, editor
  containment, and absence of document horizontal overflow.

## Verification

- `rtk make -C apps/backend fmt`
- `rtk go test ./internal/azuredevops/...` from `apps/backend`
- `rtk make -C apps/backend test`
- `rtk make -C apps/backend lint`
- `rtk pnpm --filter @kandev/web typecheck` from `apps`
- `rtk pnpm --filter @kandev/web test -- --run` from `apps`
- `rtk pnpm --filter @kandev/web lint` from `apps`
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

Tasks within a wave are listed separately for ownership clarity but should run
sequentially in the current workspace when they touch the same package or state
composition files.
