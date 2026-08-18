# Test Results

## Verifier run

Run from the overlay directory:

```bash
env -u KANDEV_TEST_POSTGRES_DSN \
  upstream-overlays/kandev-v0.88.0-pinwei-semantic-port/verifier/verify.sh
```

Result (2026-08-18): **PASS** (exit code 0). The verifier binds:

| Binding | Manifest value | Verifier check |
|---------|----------------|----------------|
| repository.id | `6e37a00b-e078-4b6d-a4eb-40b88ea59145` | matches |
| task08_base_branch | `feature/v0-88-task08-migrati-dkl` | matches |
| head_commit | `c70fd4fb7d77d3f679f2639ab992c6868240e773` | matches |
| tree_object | `91c5b69ebe797c1152ea627b07a25722a33f4e0c` | matches |
| parent_commit | `7e987c0a7bb06f68c9f395c5cff7e38672d72d1a` | matches |
| subject | `docs(plan): record Task08 verification receipt` | matches |
| canonical_base_commit | `cab9eaf19d997bb4c8020dd263ddc60d5b035b64` | exists in local repo |
| patch_count_required | 22 | 22 patches found |
| patch_order_subjects | 22 entries | all subjects match |

## Reproduced tree hash

After applying the 22-patch series to a fresh `git archive` extract of
`cab9eaf19d997bb4c8020dd263ddc60d5b035b64`, the verifier recomputed
the tree hash with `git add -A -f && git write-tree`:

```
91c5b69ebe797c1152ea627b07a25722a33f4e0c
```

This matches `manifest.json`'s `source_acceptance.tree_object` exactly.

## Source-tree freshness check

```bash
git -C /Users/weihongwang/.kandev/tasks/v0-88-task09-overlay_2c5pseq8/kandev-central-physical-delete-admission-20260812 \
    rev-parse HEAD
# c70fd4fb7d77d3f679f2639ab992c6868240e773

git -C /Users/weihongwang/.kandev/tasks/v0-88-task09-overlay_2c5pseq8/kandev-central-physical-delete-admission-20260812 \
    rev-parse HEAD^{tree}
# 91c5b69ebe797c1152ea627b07a25722a33f4e0c
```

Both match the manifest.

## Source compile gate (sanity)

```bash
cd apps/backend
env -u KANDEV_TEST_POSTGRES_DSN go test ./... -run '^$' -count=1
```

Result (2026-08-18): **PASS**. All packages compile. No test was run because of the `-run '^$'` filter; this is the compile-only gate.

## Source vet gate (sanity)

```bash
cd apps/backend
env -u KANDEV_TEST_POSTGRES_DSN go vet ./...
```

Result (2026-08-18): **PASS** (exit code 0).

## Source diff-check gate (sanity)

```bash
git -C /Users/weihongwang/.kandev/tasks/v0-88-task09-overlay_2c5pseq8/kandev-central-physical-delete-admission-20260812 \
    diff --check
```

Result (2026-08-18): **PASS** (exit code 0). The Task09 commit only adds new
files inside `upstream-overlays/kandev-v0.88.0-pinwei-semantic-port/`, so
the diff against the parent commit is purely additive.

## Fail-closed simulations

| Tampering | Detected at | Verifier exit |
|-----------|-------------|---------------|
| Subject line rewritten to BOGUS | Subject validation | 14 |
| Patch file truncated to garbage | Checksum gate | 11 |
| Patch hunk deleted | `git apply --check` | 14 |
| Patch content line altered (apply succeeds, tree drifts) | Tree hash gate | 12 |

All four tampering simulations closed with the documented exit code.

## What was NOT run

- **PostgreSQL tests**: `KANDEV_TEST_POSTGRES_DSN` was explicitly unset;
  no PostgreSQL instance was contacted. PostgreSQL runtime is reported
  `NOT_RUN`.
- **Frontend tests**: The verifier, the manifest, and the patches do not
  touch the web frontend tree. Frontend tests are inherited from Task08
  and were not re-run by Task09.
- **Push / PR / staging / live runtime**: explicitly forbidden by the
  Task09 user instruction. The task halts after a clean immutable commit.
