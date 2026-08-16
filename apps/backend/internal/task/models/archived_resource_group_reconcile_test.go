package models

import (
	"strings"
	"testing"
	"time"
)

func TestArchivedResourceGroupReconcileSnapshotCanonicalRoundTrip(t *testing.T) {
	immutable := archivedResourceGroupImmutableFixture()
	_, raw, identity, err := NewArchivedResourceGroupReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceGroupReconcileSnapshot: %v", err)
	}
	decoded, decodedIdentity, err := DecodeArchivedResourceGroupReconcileSnapshot(raw)
	if err != nil {
		t.Fatalf("DecodeArchivedResourceGroupReconcileSnapshot: %v", err)
	}
	if decodedIdentity != identity {
		t.Fatalf("identity drifted:\n got %#v\nwant %#v", decodedIdentity, identity)
	}
	if decoded.SchemaVersion != ArchivedResourceGroupReconcileSnapshotVersion {
		t.Fatalf("schema version = %d, want %d", decoded.SchemaVersion, ArchivedResourceGroupReconcileSnapshotVersion)
	}
	if !strings.HasPrefix(identity.OperationID, "archived-resource-group-reconcile:") {
		t.Fatalf("operation id prefix wrong: %q", identity.OperationID)
	}
}

func TestArchivedResourceGroupReconcileBounds(t *testing.T) {
	for _, maxCount := range []int{
		ArchivedResourceReconcileMaxTasks + 1,
		ArchivedResourceReconcileMaxBranches + 1,
		ArchivedResourceReconcileMaxAssociations + 1,
	} {
		immutable := archivedResourceGroupImmutableFixture()
		for i := 0; i < maxCount; i++ {
			immutable.Tasks = append(immutable.Tasks, ArchivedResourceGroupReconcileTask{
				TaskID:     "task-extra",
				ArchivedAt: time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
			})
		}
		if _, _, _, err := NewArchivedResourceGroupReconcileSnapshot(immutable); err == nil {
			t.Fatalf("group snapshot accepted %d tasks", maxCount)
		}
	}
}

func TestArchivedResourceGroupReconcileParticipantValidation(t *testing.T) {
	immutable := archivedResourceGroupImmutableFixture()
	immutable.Associations[0].TaskID = "task-orphan"
	if _, _, _, err := NewArchivedResourceGroupReconcileSnapshot(immutable); err == nil {
		t.Fatal("group snapshot accepted association whose owner is not in the task inventory")
	}
}

func TestArchivedResourceGroupReconcileBranchValidation(t *testing.T) {
	immutable := archivedResourceGroupImmutableFixture()
	immutable.Associations[0].WorktreeBranch = "branch-orphan"
	if _, _, _, err := NewArchivedResourceGroupReconcileSnapshot(immutable); err == nil {
		t.Fatal("group snapshot accepted association whose branch is not in the branch inventory")
	}
}

func archivedResourceGroupImmutableFixture() ArchivedResourceGroupReconcileImmutable {
	rootKey, _ := ArchivedResourceManagedRootKey("/tmp/kandev/worktree")
	taskArchivedAt := time.Date(2026, 8, 12, 1, 2, 3, 4, time.UTC).Format(time.RFC3339Nano)
	coordinatorID := "task-coordinator"
	return ArchivedResourceGroupReconcileImmutable{
		CoordinatorTaskID: coordinatorID,
		Tasks: []ArchivedResourceGroupReconcileTask{
			{TaskID: coordinatorID, ArchivedAt: taskArchivedAt},
		},
		ManagedRootKey: rootKey,
		Target: ArchivedResourceGroupReconcileTarget{
			WorktreeID:     "worktree-shared",
			RepositoryID:   "repository-company",
			RepositoryPath: "/tmp/kandev/repo",
			GitCommonDir:   "/tmp/kandev/repo/.git",
			WorktreePath:   "/tmp/kandev/worktree",
		},
		Branches: []ArchivedResourceReconcileBranch{
			{Branch: "feature/synthetic", HeadOID: strings.Repeat("a", 40)},
		},
		Associations: []ArchivedResourceReconcileAssociation{
			{
				AssociationID:  "association-coordinator",
				TaskID:         coordinatorID,
				SessionID:      "env-coordinator",
				WorktreeID:     "worktree-shared",
				RepositoryID:   "repository-company",
				BranchSlug:     "feature/synthetic",
				WorktreePath:   "/tmp/kandev/worktree",
				WorktreeBranch: "feature/synthetic",
				Status:         "active",
				CreatedAt:      time.Date(2026, 8, 12, 1, 0, 0, 1, time.UTC).Format(time.RFC3339Nano),
				UpdatedAt:      time.Date(2026, 8, 12, 1, 1, 0, 2, time.UTC).Format(time.RFC3339Nano),
			},
		},
	}
}
