# Desensitization Report

## Scope

A desensitization scan is performed against every file in the overlay
(excluding the verifier itself, its helper Go source, and the patch series,
which contains intentional source diffs). The scan is implemented in
`verifier/verify.sh` and is part of the verification contract: any high-risk
match fails the verifier with exit code `15`.

## Patterns scanned

| Label | Pattern |
|-------|---------|
| private-key | `BEGIN <words> PRIVATE KEY` |
| aws-access-key | `AKIA[0-9A-Z]{16}` |
| aws-secret-key | `aws_secret_access_key\s*=` |
| github-token | `gh[pousr]_[A-Za-z0-9]{36,}` |
| slack-token | `xox[baprs]-[A-Za-z0-9-]{10,}` |
| openai-key | `sk-[A-Za-z0-9]{20,}T3BlbkFJ[A-Za-z0-9]{20,}` |
| leaked-home-path | `/Users/weihongwang/<path>` outside the verifier itself |
| leaked-email | any RFC-822-shaped address other than the documented commit author (`396218656@qq.com`) |

## Intentional inclusions

- `396218656@qq.com` is the documented commit author of `c70fd4fb7` and
  appears in `manifest.json`, `docs/source-acceptance-receipt.md`, and every
  patch `From:` header. The scan explicitly filters this one address.
- The patch series is excluded from the secret-pattern scan because the
  patches contain source diffs that may legitimately include test fixtures
  with example tokens, URLs, or paths.
- The verifier script and `tree-hash.go` are excluded from the home-path
  scan because they necessarily reference the operator's filesystem layout
  when computing `REPO_ROOT`.

## Run history

| Run | Date | Result |
|-----|------|--------|
| Initial build | 2026-08-18 | clean |
| Subject tampering simulation | 2026-08-18 | fails closed (exit 14) |
| Checksum tampering simulation | 2026-08-18 | fails closed (exit 11) |
| Patch apply failure simulation | 2026-08-18 | fails closed (exit 14) |
| Tree hash mismatch simulation | 2026-08-18 | fails closed (exit 12) |

## Conclusion

The overlay contents are desensitized. The verifier enforces this
contract and fails closed on any future regression.

## Disclosure

No third-party security disclosure is required. The overlay is private.
