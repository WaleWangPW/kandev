package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestArchivedResourceReleaseSnapshotCanonicalRoundTrip(t *testing.T) {
	immutable, release := archivedResourceReleaseFixture(t)
	snapshot, raw, identity, err := NewArchivedResourceReleaseSnapshot(immutable, release)
	if err != nil {
		t.Fatalf("NewArchivedResourceReleaseSnapshot: %v", err)
	}
	if snapshot.SchemaVersion != ArchivedResourceReleaseSnapshotVersion ||
		snapshot.Kind != ArchivedResourceReleaseSnapshotKind ||
		snapshot.Phase != ArchivedResourceReleaseSnapshotPhase {
		t.Fatalf("canonical contract violated: %#v", snapshot)
	}
	decoded, decodedIdentity, err := DecodeArchivedResourceReleaseSnapshot(raw)
	if err != nil {
		t.Fatalf("DecodeArchivedResourceReleaseSnapshot: %v", err)
	}
	if decodedIdentity != identity {
		t.Fatalf("identity drift after decode:\n got %#v\nwant %#v", decodedIdentity, identity)
	}
	reEncoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(raw, reEncoded) {
		t.Fatal("canonical raw bytes drifted across decode")
	}
}

func TestArchivedResourceReleaseSnapshotRejectsProofAndIdentityDrift(t *testing.T) {
	immutable, release := archivedResourceReleaseFixture(t)
	release.PhysicalPath = "/tmp/kandev/worktree-other"
	if _, _, _, err := NewArchivedResourceReleaseSnapshot(immutable, release); !errors.Is(err, ErrArchivedResourceReleaseInvalid) {
		t.Fatalf("mismatched physical path accepted: %v", err)
	}
	immutable, release = archivedResourceReleaseFixture(t)
	release.GitWorktreeRegistration.Branch = "feature/other"
	if _, _, _, err := NewArchivedResourceReleaseSnapshot(immutable, release); !errors.Is(err, ErrArchivedResourceReleaseInvalid) {
		t.Fatalf("mismatched git registration branch accepted: %v", err)
	}
	immutable, release = archivedResourceReleaseFixture(t)
	immutable.AnchorHeadOID = strings.Repeat("Z", 40)
	if _, _, _, err := NewArchivedResourceReleaseSnapshot(immutable, release); !errors.Is(err, ErrArchivedResourceReleaseInvalid) {
		t.Fatalf("uppercase head oid accepted: %v", err)
	}
}

func TestArchivedResourceReleaseSnapshotRejectsMissingFields(t *testing.T) {
	immutable, release := archivedResourceReleaseFixture(t)
	immutable.AnchorWorktreePath = "not-absolute"
	if _, _, _, err := NewArchivedResourceReleaseSnapshot(immutable, release); !errors.Is(err, ErrArchivedResourceReleaseInvalid) {
		t.Fatalf("non-absolute path accepted: %v", err)
	}
	immutable, release = archivedResourceReleaseFixture(t)
	release.ReleasedAt = "2026-08-12 10:00:00"
	if _, _, _, err := NewArchivedResourceReleaseSnapshot(immutable, release); err == nil {
		t.Fatal("non-canonical released_at accepted")
	}
}

func TestArchivedResourceReleaseSnapshotDecodeRejectsTrailingJSON(t *testing.T) {
	immutable, release := archivedResourceReleaseFixture(t)
	_, raw, _, err := NewArchivedResourceReleaseSnapshot(immutable, release)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := DecodeArchivedResourceReleaseSnapshot(append(raw, ' ', 'x')); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestArchivedResourceReleaseManagedRootKeyCanonicalisesWorktreePath(t *testing.T) {
	key, err := ArchivedResourceReleaseManagedRootKey("/tmp/kandev/worktree")
	if err != nil {
		t.Fatalf("ArchivedResourceReleaseManagedRootKey: %v", err)
	}
	if !strings.HasPrefix(key, "git_worktree:") || len(key) <= len("git_worktree:") {
		t.Fatalf("managed root key shape wrong: %q", key)
	}
	if _, err := ArchivedResourceReleaseManagedRootKey("relative"); err == nil {
		t.Fatal("relative path accepted")
	}
}

func archivedResourceReleaseFixture(t *testing.T) (ArchivedResourceReleaseImmutable, ArchivedResourceReleaseReleaseProof) {
	t.Helper()
	now := time.Date(2026, 8, 12, 1, 2, 3, 4, time.UTC).Format(time.RFC3339Nano)
	headOID := strings.Repeat("a", 40)
	return ArchivedResourceReleaseImmutable{
			AnchorOperationID:  "archived-resource-reconcile:abc",
			AnchorDigest:       "digest",
			AnchorTaskID:       "task-archived",
			AnchorWorktreeID:   "worktree-shared",
			AnchorRepository:   "repository-company",
			AnchorBranch:       "feature/synthetic",
			AnchorHeadOID:      headOID,
			AnchorWorktreePath: "/tmp/kandev/worktree",
			AnchorGitCommonDir: "/tmp/kandev/repo/.git",
		}, ArchivedResourceReleaseReleaseProof{
			PhysicalPath: "/tmp/kandev/worktree",
			GitWorktreeRegistration: ArchivedResourceReleaseGitRegistration{
				WorktreePath: "/tmp/kandev/worktree",
				Branch:       "feature/synthetic",
				HeadOID:      headOID,
			},
			ReleasedAt: now,
		}
}
