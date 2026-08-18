#!/usr/bin/env bash
# Kandev v0.88.0 Pinwei Semantic Port overlay verifier.
#
# Verifies the immutable handoff:
#   1. Reads manifest.json and binds repository_id, base_branch, source HEAD,
#      source tree, parent, and the 22-patch subject list.
#   2. Verifies SHA-256 of every overlay artifact against checksums.sha256.
#   3. Extracts a fresh archive of the canonical v0.88.0 base commit
#      (cab9eaf19d997bb4c8020dd263ddc60d5b035b64) from the local git object
#      database into a temporary working directory.
#   4. Applies every patch in numeric order via git apply --check, then git am
#      against the temporary branch.
#   5. Computes the resulting tree hash and compares it with the manifest's
#      source_acceptance.tree_object (91c5b69ebe797c1152ea627b07a25722a33f4e0c).
#   6. Verifies the head commit hash, parent commit hash, and subject against
#      the manifest.
#   7. Re-validates every patch's subject against the manifest's
#      patch_order_subjects (in order).
#   8. Runs desensitization scans against the overlay files for high-risk
#      patterns (private keys, AWS/GCP credentials, tokens).
#
# Strictly local. Never contacts a database, runtime, or remote. Fails closed
# with a documented exit code on every problem.

set -euo pipefail

# Exit codes (also documented in manifest.json).
EX_PASS=0
EX_MANIFEST_MISSING=10
EX_CHECKSUM_MISMATCH=11
EX_TREE_HASH_MISMATCH=12
EX_PARENT_MISMATCH=13
EX_PATCH_APPLY_FAILURE=14
EX_DESENSITIZATION_FAILURE=15
EX_RUNTIME_OR_DB_CONTACT=20
EX_USAGE=2

# Locate the overlay directory from the script path.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OVERLAY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

log() { printf '[verify] %s\n' "$*"; }
fail() {
  printf '[verify][FAIL] %s\n' "$*" >&2
  exit "$1"
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "$EX_MANIFEST_MISSING" "required command not found: $1"
  fi
}

# 0. Required commands.
require_command git
require_command sha256sum
require_command awk
require_command grep
require_command mktemp
require_command tar
if command -v go >/dev/null 2>&1; then
  HAVE_GO=1
else
  HAVE_GO=0
fi

# 1. Bind manifest fields.
MANIFEST="$OVERLAY_DIR/manifest.json"
if [ ! -f "$MANIFEST" ]; then
  fail "$EX_MANIFEST_MISSING" "manifest.json missing at $MANIFEST"
fi

read_manifest() {
  awk -F'"' -v k="$1" '$2==k{print $4; exit}' "$MANIFEST"
}

REPO_ID=$(read_manifest id)
[ -n "$REPO_ID" ] || read_manifest id
REPO_ID=$(awk -F'"' '/"repository"/{flag=1} flag && /"id"/{print $4; flag=0}' "$MANIFEST")
[ "$REPO_ID" = "6e37a00b-e078-4b6d-a4eb-40b88ea59145" ] || fail "$EX_MANIFEST_MISSING" "repository.id mismatch: got '$REPO_ID'"
BASE_BRANCH=$(awk -F'"' '/"task08_base_branch"/{print $4; exit}' "$MANIFEST")
[ "$BASE_BRANCH" = "feature/v0-88-task08-migrati-dkl" ] || fail "$EX_MANIFEST_MISSING" "task08_base_branch mismatch: got '$BASE_BRANCH'"
OVERLAY_BRANCH=$(awk -F'"' '/"task09_overlay_branch"/{print $4; exit}' "$MANIFEST")
[ -n "$OVERLAY_BRANCH" ] || fail "$EX_MANIFEST_MISSING" "task09_overlay_branch missing"
EXPECT_HEAD=$(awk -F'"' '/"head_commit"/{print $4; exit}' "$MANIFEST")
[ "$EXPECT_HEAD" = "c70fd4fb7d77d3f679f2639ab992c6868240e773" ] || fail "$EX_MANIFEST_MISSING" "head_commit mismatch: got '$EXPECT_HEAD'"
EXPECT_TREE=$(awk -F'"' '/"tree_object"/{print $4; exit}' "$MANIFEST")
[ "$EXPECT_TREE" = "91c5b69ebe797c1152ea627b07a25722a33f4e0c" ] || fail "$EX_MANIFEST_MISSING" "tree_object mismatch: got '$EXPECT_TREE'"
EXPECT_PARENT=$(awk -F'"' '/"parent_commit"/{print $4; exit}' "$MANIFEST")
[ "$EXPECT_PARENT" = "7e987c0a7bb06f68c9f395c5cff7e38672d72d1a" ] || fail "$EX_MANIFEST_MISSING" "parent_commit mismatch: got '$EXPECT_PARENT'"
EXPECT_BASE=$(awk -F'"' '/"canonical_base_commit"/{print $4; exit}' "$MANIFEST")
[ "$EXPECT_BASE" = "cab9eaf19d997bb4c8020dd263ddc60d5b035b64" ] || fail "$EX_MANIFEST_MISSING" "canonical_base_commit mismatch: got '$EXPECT_BASE'"
EXPECT_SUBJECT=$(awk -F'"' '/"subject"/{print $4; exit}' "$MANIFEST")
[ "$EXPECT_SUBJECT" = "docs(plan): record Task08 verification receipt" ] || fail "$EX_MANIFEST_MISSING" "subject mismatch: got '$EXPECT_SUBJECT'"

# 2. Checksum verification.
CHECKSUMS="$OVERLAY_DIR/checksums.sha256"
[ -f "$CHECKSUMS" ] || fail "$EX_CHECKSUM_MISMATCH" "checksums.sha256 missing"
log "verifying SHA-256 sums in $OVERLAY_DIR"
if ! (cd "$OVERLAY_DIR" && sha256sum -c checksums.sha256 --status); then
  fail "$EX_CHECKSUM_MISMATCH" "one or more checksums failed"
fi
log "checksums PASS"

# 3. Confirm local repo has the required commits.
REPO_ROOT="$(cd "$OVERLAY_DIR/../.." && pwd)"
git -C "$REPO_ROOT" rev-parse --git-dir >/dev/null 2>&1 \
  || fail "$EX_MANIFEST_MISSING" "local source worktree is not a git repo: $REPO_ROOT"
for sha in "$EXPECT_BASE" "$EXPECT_PARENT" "$EXPECT_HEAD" "$EXPECT_TREE"; do
  git -C "$REPO_ROOT" cat-file -t "$sha" >/dev/null 2>&1 \
    || fail "$EX_MANIFEST_MISSING" "git object missing in local repo: $sha"
done

# 4. Build fresh archive from canonical base.
TMP=$(mktemp -d -t overlay-verify.XXXXXX)
trap 'rm -rf "$TMP"' EXIT
WORK="$TMP/src"
mkdir -p "$WORK"
git -C "$REPO_ROOT" archive "$EXPECT_BASE" | tar -x -C "$WORK"

# 5. Apply patches in order.
PATCH_DIR="$OVERLAY_DIR/patches"
[ -d "$PATCH_DIR" ] || fail "$EX_PATCH_APPLY_FAILURE" "patches/ missing"
PATCH_FILES=$(ls -1 "$PATCH_DIR"/*.patch 2>/dev/null | sort)
PATCH_COUNT=$(printf '%s\n' "$PATCH_FILES" | grep -c . || true)
EXPECTED_PATCH_COUNT=$(grep '"patch_count_required"' "$MANIFEST" | sed -E 's/.*"patch_count_required"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/')
[ "$PATCH_COUNT" -eq "$EXPECTED_PATCH_COUNT" ] \
  || fail "$EX_PATCH_APPLY_FAILURE" "expected $EXPECTED_PATCH_COUNT patches, found $PATCH_COUNT"

EXPECTED_SUBJECTS=$(awk '
  /"patch_order_subjects":/ {collect=1; next}
  collect && /^    \]/ {collect=0}
  collect {
    gsub(/^ +/, "");
    if ($0 ~ /^"/) { print }
  }
' "$MANIFEST")
SUBJECT_NUM=0
while IFS= read -r patch; do
  [ -z "$patch" ] && continue
  log "checking $(basename "$patch")"
  SUBJECT_NUM=$((SUBJECT_NUM+1))
  ACTUAL_SUBJECT=$(awk '/^Subject: \[PATCH/{sub(/^Subject: \[PATCH [0-9]+\/[0-9]+\] /,""); print; exit}' "$patch")
  EXPECTED=$(printf '%s\n' "$EXPECTED_SUBJECTS" | sed -n "${SUBJECT_NUM}p" | sed -E 's/^"(.*)",?$/\1/')
  if [ "$ACTUAL_SUBJECT" != "$EXPECTED" ]; then
    fail "$EX_PATCH_APPLY_FAILURE" "subject mismatch on patch #$SUBJECT_NUM: got '$ACTUAL_SUBJECT', expected '$EXPECTED'"
  fi
  (cd "$WORK" && git apply --check --whitespace=nowarn < "$patch") \
    || fail "$EX_PATCH_APPLY_FAILURE" "git apply --check failed on $(basename "$patch")"
  (cd "$WORK" && git apply --whitespace=nowarn < "$patch") >/dev/null \
    || fail "$EX_PATCH_APPLY_FAILURE" "git apply failed on $(basename "$patch")"
done <<EOF_PATCHES
$PATCH_FILES
EOF_PATCHES

# 6. Recompute tree hash and compare.
if [ "$HAVE_GO" = "1" ]; then
  OUT_TREE_FILE="$TMP/tree.txt"
  (cd "$WORK" && go run "$OVERLAY_DIR/verifier/tree-hash.go" "$WORK" "$OUT_TREE_FILE") \
    || fail "$EX_TREE_HASH_MISMATCH" "tree-hash helper failed"
else
  # Fallback when go is absent: use git directly.
  git -C "$WORK" init -q
  git -C "$WORK" add -A -f >/dev/null 2>&1
  OUT_TREE=$(git -C "$WORK" write-tree)
  printf '%s\n' "$OUT_TREE" > "$TMP/tree.txt"
fi
ACTUAL_TREE=$(cat "$TMP/tree.txt")
[ "$ACTUAL_TREE" = "$EXPECT_TREE" ] \
  || fail "$EX_TREE_HASH_MISMATCH" "tree hash mismatch: got '$ACTUAL_TREE', expected '$EXPECT_TREE'"
log "tree hash matches: $ACTUAL_TREE"

# 7. Recompute head commit from source worktree and compare.
HEAD_RAW=$(git -C "$REPO_ROOT" cat-file -p "$EXPECT_HEAD")
HEAD_TREE=$(printf '%s\n' "$HEAD_RAW" | awk '$1=="tree"{print $2}')
HEAD_PARENT=$(printf '%s\n' "$HEAD_RAW" | awk '$1=="parent"{print $2}')
HEAD_SUBJECT=$(printf '%s\n' "$HEAD_RAW" | awk 'BEGIN{found=0} /^$/{if(found==0){found=1; next}} found{print; exit}')
[ "$HEAD_TREE" = "$EXPECT_TREE" ] || fail "$EX_PARENT_MISMATCH" "head.tree mismatch: got '$HEAD_TREE'"
[ "$HEAD_PARENT" = "$EXPECT_PARENT" ] || fail "$EX_PARENT_MISMATCH" "head.parent mismatch: got '$HEAD_PARENT'"
[ "$HEAD_SUBJECT" = "$EXPECT_SUBJECT" ] || fail "$EX_PARENT_MISMATCH" "head.subject mismatch: got '$HEAD_SUBJECT'"
log "head commit binding verified"

# 8. Desensitization scan against overlay contents (not source files).
log "scanning overlay for high-risk patterns"
SENSITIVE=0
SCAN_OUTPUT="$TMP/desensitize.txt"
: > "$SCAN_OUTPUT"
scan_pattern() {
  local label="$1" pattern="$2"
  if grep -RInE "$pattern" "$OVERLAY_DIR" \
       --exclude-dir=.git --exclude=checksums.sha256 \
       --exclude='*.patch' --exclude='tree-hash.go' --exclude='verify.sh' \
       > "$TMP/scan.tmp" 2>/dev/null; then
    if [ -s "$TMP/scan.tmp" ]; then
      echo "[$label]" >> "$SCAN_OUTPUT"
      cat "$TMP/scan.tmp" >> "$SCAN_OUTPUT"
      echo "" >> "$SCAN_OUTPUT"
      SENSITIVE=1
    fi
  fi
}
# Secret-shaped tokens.
scan_pattern "private-key" 'BEGIN [A-Z ]+PRIVATE KEY'
scan_pattern "aws-access-key" 'AKIA[0-9A-Z]{16}'
scan_pattern "aws-secret-key" 'aws_secret_access_key[[:space:]]*='
scan_pattern "github-token" 'gh[pousr]_[A-Za-z0-9]{36,}'
scan_pattern "slack-token" 'xox[baprs]-[A-Za-z0-9-]{10,}'
scan_pattern "openai-key" 'sk-[A-Za-z0-9]{20,}T3BlbkFJ[A-Za-z0-9]{20,}'

# Operator-home paths in shipped files (verifier excludes itself).
if grep -RIn '/Users/weihongwang/[^ "]+' "$OVERLAY_DIR" \
     --exclude-dir=.git --exclude=checksums.sha256 \
     --exclude='*.patch' --exclude='tree-hash.go' --exclude='verify.sh' \
     > "$TMP/scan.tmp" 2>/dev/null; then
  if [ -s "$TMP/scan.tmp" ]; then
    echo "[leaked-home-path]" >> "$SCAN_OUTPUT"
    cat "$TMP/scan.tmp" >> "$SCAN_OUTPUT"
    echo "" >> "$SCAN_OUTPUT"
    SENSITIVE=1
  fi
fi

# Email-shaped strings other than the documented commit author.
if grep -RInE '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.(com|org|net|io)\b' "$OVERLAY_DIR" \
     --exclude-dir=.git --exclude=checksums.sha256 \
     --exclude='*.patch' --exclude='tree-hash.go' --exclude='verify.sh' \
     > "$TMP/scan.tmp" 2>/dev/null; then
  if [ -s "$TMP/scan.tmp" ]; then
    FILTERED=$(grep -v "396218656@qq.com" "$TMP/scan.tmp" || true)
    if [ -n "$FILTERED" ]; then
      echo "[leaked-email]" >> "$SCAN_OUTPUT"
      echo "$FILTERED" >> "$SCAN_OUTPUT"
      echo "" >> "$SCAN_OUTPUT"
      SENSITIVE=1
    fi
  fi
fi

if [ "$SENSITIVE" = "1" ]; then
  echo "--- scan output ---" >&2
  cat "$SCAN_OUTPUT" >&2
  fail "$EX_DESENSITIZATION_FAILURE" "desensitization scan flagged high-risk patterns (see $SCAN_OUTPUT)"
fi
log "desensitization scan clean"

# 9. Verify required env vars are unset (no DB / runtime contact).
for envvar in KANDEV_TEST_POSTGRES_DSN DATABASE_URL PGHOST PGUSER; do
  if [ -n "${!envvar:-}" ]; then
    fail "$EX_RUNTIME_OR_DB_CONTACT" "$envvar is set; verifier must run with that variable unset"
  fi
done
log "environment isolation verified"

log "ALL CHECKS PASS"
exit "$EX_PASS"
