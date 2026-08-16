package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestArchivedResourceEnvironmentRetirementSnapshotCanonicalRoundTrip(t *testing.T) {
	immutable, proof := archivedResourceRetirementFixture(t)
	snapshot, raw, identity, err := NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof)
	if err != nil {
		t.Fatalf("NewArchivedResourceEnvironmentRetirementSnapshot: %v", err)
	}
	if snapshot.SchemaVersion != ArchivedResourceEnvironmentRetirementSnapshotVersion ||
		snapshot.Kind != ArchivedResourceEnvironmentRetirementSnapshotKind ||
		snapshot.Phase != ArchivedResourceEnvironmentRetirementSnapshotPhase {
		t.Fatalf("canonical contract violated: %#v", snapshot)
	}
	decoded, decodedIdentity, err := DecodeArchivedResourceEnvironmentRetirementSnapshot(raw)
	if err != nil {
		t.Fatalf("DecodeArchivedResourceEnvironmentRetirementSnapshot: %v", err)
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

func TestArchivedResourceEnvironmentRetirementSnapshotSortsRepositoriesAndRejectsDuplicates(t *testing.T) {
	immutable, proof := archivedResourceRetirementFixture(t)
	immutable.Repositories = []ArchivedResourceEnvironmentRetirementRepository{
		archivedResourceRetirementRepositoryFixture("repo-b", "wt-b"),
		archivedResourceRetirementRepositoryFixture("repo-a", "wt-a"),
	}
	snapshot, _, _, err := NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof)
	if err != nil {
		t.Fatalf("sort: %v", err)
	}
	if got := snapshot.Immutable.Repositories[0].ID; got != "repo-a-row" {
		t.Fatalf("repositories not sorted: first=%q", got)
	}
	immutable, proof = archivedResourceRetirementFixture(t)
	immutable.Repositories = []ArchivedResourceEnvironmentRetirementRepository{
		archivedResourceRetirementRepositoryFixture("repo-a", "wt-a"),
		archivedResourceRetirementRepositoryFixture("repo-a", "wt-a"),
	}
	if _, _, _, err := NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof); !errors.Is(err, ErrArchivedResourceEnvironmentRetirementInvalid) {
		t.Fatalf("duplicate row accepted: %v", err)
	}
}

func TestArchivedResourceEnvironmentRetirementSnapshotRejectsInvalidStatusesAndTimestamps(t *testing.T) {
	immutable, proof := archivedResourceRetirementFixture(t)
	immutable.EnvironmentStatus = "ready"
	if _, _, _, err := NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof); !errors.Is(err, ErrArchivedResourceEnvironmentRetirementInvalid) {
		t.Fatalf("invalid environment status accepted: %v", err)
	}
	immutable, proof = archivedResourceRetirementFixture(t)
	immutable.Repositories[0].Status = "future"
	if _, _, _, err := NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof); !errors.Is(err, ErrArchivedResourceEnvironmentRetirementInvalid) {
		t.Fatalf("invalid repository status accepted: %v", err)
	}
	immutable, proof = archivedResourceRetirementFixture(t)
	immutable.Repositories[0].CreatedAt = "2026-08-12 10:00:00"
	if _, _, _, err := NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof); !errors.Is(err, ErrArchivedResourceEnvironmentRetirementInvalid) {
		t.Fatalf("non-canonical created_at accepted: %v", err)
	}
}

func TestArchivedResourceEnvironmentRetirementSnapshotDecodeRejectsTrailingJSON(t *testing.T) {
	immutable, proof := archivedResourceRetirementFixture(t)
	_, raw, _, err := NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, err := DecodeArchivedResourceEnvironmentRetirementSnapshot(append(raw, ' ', 'y')); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func archivedResourceRetirementFixture(t *testing.T) (ArchivedResourceEnvironmentRetirementImmutable, ArchivedResourceEnvironmentRetirementProof) {
	t.Helper()
	now := time.Date(2026, 8, 12, 1, 2, 3, 4, time.UTC).Format(time.RFC3339Nano)
	return ArchivedResourceEnvironmentRetirementImmutable{
			EnvironmentID:     "env-stopped",
			TaskID:            "task-archived",
			EnvironmentStatus: "stopped",
			Repositories: []ArchivedResourceEnvironmentRetirementRepository{
				archivedResourceRetirementRepositoryFixture("repo-a", "wt-a"),
			},
		}, ArchivedResourceEnvironmentRetirementProof{
			RetiredAt: now,
		}
}

func archivedResourceRetirementRepositoryFixture(repositoryID, worktreeID string) ArchivedResourceEnvironmentRetirementRepository {
	createdAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	updatedAt := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	return ArchivedResourceEnvironmentRetirementRepository{
		ID:             repositoryID + "-row",
		RepositoryID:   repositoryID,
		BranchSlug:     "feature/synthetic",
		WorktreeID:     worktreeID,
		WorktreePath:   "/tmp/kandev/worktree",
		WorktreeBranch: "feature/synthetic",
		Position:       0,
		Status:         "deleted",
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		DeletedAt:      updatedAt,
	}
}

func TestArchivedResourceEnvironmentRetirementManagedRootKeyIgnoresEmptyPath(t *testing.T) {
	key, err := ArchivedResourceEnvironmentRetirementManagedRootKey("")
	if err != nil {
		t.Fatalf("empty path: %v", err)
	}
	if key != "" {
		t.Fatalf("expected empty key for empty path: %q", key)
	}
	if _, err := ArchivedResourceEnvironmentRetirementManagedRootKey("relative"); err == nil {
		t.Fatal("relative path accepted")
	}
}

var _ = strings.HasPrefix // keep strings import
