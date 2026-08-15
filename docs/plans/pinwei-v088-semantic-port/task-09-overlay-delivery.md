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

Pending.
