# Security Review

## Scope

This overlay is a private, offline handoff. It contains no runtime
configurations, no live database connections, and no production secrets.
The security review verifies that the artifact bundle is safe to ship,
inspect, and verify in a fresh, isolated worktree without leaking
credentials or exposing the operator environment.

## Out-of-scope by design

The overlay deliberately excludes:

- The full source tree (only the 22-patch mailbox series is shipped).
- Any runtime configuration files (`profiles.yaml`, `*.env`, etc.).
- Any local database dump or SQLite snapshot.
- Any worktree state, agent runtime state, or session metadata.

## Threat model

### T1. Secret leakage into the overlay

- **Asset**: private SSH keys, cloud credentials, API tokens.
- **Surface**: every overlay file (`manifest.json`, `patches/*.patch`,
  `docs/*.md`, `verifier/*.sh`, `verifier/*.go`).
- **Mitigation**: desensitization scan in `verifier/verify.sh` for
  AWS access-key prefixes, GitHub/Slack/OpenAI tokens, PEM private-key
  headers, and operator home paths. The scan is exhaustive and must report
  zero hits for the verifier to pass.

### T2. Patch tampering

- **Asset**: the 22-patch mailbox series; checksum integrity of every file.
- **Surface**: an attacker could swap or rewrite a patch file.
- **Mitigation**: SHA-256 checksums in `checksums.sha256` are verified by
  `sha256sum -c checksums.sha256 --status` before any patch is applied.
  Any mismatch fails closed with exit code `11`.

### T3. Tree-object drift

- **Asset**: the accepted source tree hash `91c5b69ebe797c1152ea627b07a25722a33f4e0c`.
- **Surface**: a patch that fails to reproduce the exact accepted tree
  silently allows tampering to slip through.
- **Mitigation**: the verifier re-computes the tree hash from a fresh
  archive of the canonical base commit and applied patches; the result
  must equal the manifest's `tree_object`. Any drift fails closed with
  exit code `12`.

### T4. Parent/HEAD subject drift

- **Asset**: parent commit, HEAD commit, and HEAD subject.
- **Surface**: an overlay that swaps in a different `HEAD` while keeping
  the same tree hash.
- **Mitigation**: the verifier reads `git cat-file -p <head>` and
  verifies the `tree`, `parent`, and first non-blank subject line
  against the manifest. Any drift fails closed with exit code `13`.

### T5. Patch apply failure

- **Asset**: ability to detect a corrupted or truncated patch.
- **Surface**: a partial patch that produces a working tree but not
  the expected tree hash.
- **Mitigation**: every patch is `git apply --check`-ed before being
  applied; `git apply` is then re-run for real. Any check failure
  fails closed with exit code `14`.

### T6. Runtime / database contact

- **Asset**: isolation guarantee. The overlay must not trigger a live
  database, network call, or runtime.
- **Surface**: a verifier script that accidentally shells out to a
  remote tool, an unconfigured executor, or `pg_isready`.
- **Mitigation**: the verifier script is a pure shell + `git` pipeline.
  Required environment variables that would enable a database contact
  (`KANDEV_TEST_POSTGRES_DSN`, `DATABASE_URL`, `PGHOST`, `PGUSER`) are
  explicitly checked at the end of the run; any non-empty value fails
  closed with exit code `20`.

## Residual risk

The overlay trusts the operator to:

1. Run `verify.sh` from a fresh, isolated worktree with no shared
   `~/.ssh`, no `gh` auth, no `aws` auth.
2. Verify that the local source repo's HEAD tree is the same value
   bound in `manifest.json` (`91c5b69ebe797c1152ea627b07a25722a33f4e0c`)
   before shipping.

These checks are documented in `docs/source-acceptance-receipt.md`.

## Disclosure

No third-party security disclosure is required. The overlay is private.
