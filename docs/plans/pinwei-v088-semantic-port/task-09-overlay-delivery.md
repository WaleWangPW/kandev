---
id: "09-overlay-delivery"
title: "Freeze and deliver the private v0.88 overlay"
status: pending
wave: 8
depends_on: ["08-migration-verification"]
plan: "plan.md"
spec: "../../specs/tasks/archived-resource-safety/spec.md"
---

# Task 09: Freeze and deliver the private v0.88 overlay

- **Acceptance:** Independent read-only review binds parent/HEAD/tree/path closure and reproduces all security-critical task checks from the fixed commit object.
- **Acceptance:** A minimal private overlay contains only manifest, exact binary/full-index patch, source/receipt/security docs, and verifier; fresh archive application yields the accepted source tree.
- **Acceptance:** One normal private push and one Draft PR read back the exact head; no public push, Ready, merge, rebase, or force-push occurs.
- **Verification:** Run the overlay's checksum-bound verifier from a fresh archive with `KANDEV_TEST_POSTGRES_DSN` unset, then read back the private branch and Draft PR head/state.
- **Files likely touched:** private `upstream-overlays/kandev-v0.88.0-pinwei-semantic-port/` package only; source worktree is read-only after freeze.
- **Dependencies:** Task 08.
- **Parallelism:** sequential fixed-object handoff.
- **Inputs:** accepted source commit and all verification receipts.

## Results

2026-08-18 local overlay packaging and verifier receipt (no runtime,
database, physical action, push, or PR):

- Repository binding: `repository_id=6e37a00b-e078-4b6d-a4eb-40b88ea59145`,
  `task08_base_branch=feature/v0-88-task08-migrati-dkl`,
  `task09_overlay_branch=feature/v0-88-task09-overlay-fj3`,
  `canonical_base_commit=cab9eaf19d997bb4c8020dd263ddc60d5b035b64` (`v0.88.0`).
- Source acceptance: `head_commit=c70fd4fb7d77d3f679f2639ab992c6868240e773`,
  `tree_object=91c5b69ebe797c1152ea627b07a25722a33f4e0c`,
  `parent_commit=7e987c0a7bb06f68c9f395c5cff7e38672d72d1a`,
  subject `docs(plan): record Task08 verification receipt`. All four values
  were re-bound from the local source worktree (`git cat-file -p HEAD`)
  before any artifact was generated.
- Overlay layout under `upstream-overlays/kandev-v0.88.0-pinwei-semantic-port/`:
  `manifest.json`, `checksums.sha256`, `patches/` (22 mailbox-format
  binary+full-index patches generated with `git format-patch --binary
  --full-index` over `cab9eaf..c70fd4fb7`), `verifier/verify.sh`,
  `verifier/tree-hash.go`, `docs/source-acceptance-receipt.md`,
  `docs/security-review.md`, `docs/desensitization-report.md`,
  `docs/test-results.md`. Total 30 files; no other paths in the source
  tree are touched.
- Verifier behavior (`env -u KANDEV_TEST_POSTGRES_DSN
  upstream-overlays/kandev-v0.88.0-pinwei-semantic-port/verifier/verify.sh`):
  exit code `0` (PASS). The verifier checksums every overlay file, applies
  all 22 patches in numeric order against a fresh `git archive` extract of
  `cab9eaf19d997bb4c8020dd263ddc60d5b035b64`, recomputes the tree hash with
  `git add -A -f && git write-tree`, and confirms the result equals
  `91c5b69ebe797c1152ea627b07a25722a33f4e0c` byte-for-byte. All 22
  `Subject:` lines match the manifest's `patch_order_subjects` list.
- Fail-closed simulations (each with regenerated checksums): subject-line
  rewrite fails closed with exit `14`; patch truncation fails closed with
  exit `11`; missing-hunk deletion fails closed with exit `14`; tree-drift
  content edit fails closed with exit `12`. Documented in
  `docs/test-results.md`.
- Desensitization scan: clean for all shipped overlay files. The scan
  detects AWS/GitHub/Slack/OpenAI/SSH-key shapes, operator-home paths,
  and email-shaped strings; it explicitly allows the documented commit
  author (`396218656@qq.com`) which appears in `manifest.json`, the
  receipt, and every patch `From:` header. No third-party security
  disclosure is required.
- Source compile gate (sanity):
  `cd apps/backend && env -u KANDEV_TEST_POSTGRES_DSN go test ./... -run
  '^$' -count=1` PASS. `go vet ./...` PASS. `git diff --check` PASS.
- PostgreSQL: NOT_RUN. `KANDEV_TEST_POSTGRES_DSN` was explicitly unset;
  no PostgreSQL instance was contacted.
- Branch and HEAD readback (the user-authorized second half of the
  verification command, run locally without push): the local source
  worktree's `git rev-parse HEAD` returns
  `c70fd4fb7d77d3f679f2639ab992c6868240e773` and
  `git rev-parse HEAD^{tree}` returns
  `91c5b69ebe797c1152ea627b07a25722a33f4e0c`; both match the manifest.
- Push, PR, staging, runtime, live DB, flag changes, and restarts are
  explicitly out of scope and were not performed. The task halts at a
  clean immutable local commit.
