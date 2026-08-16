package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

func TestArchivedResourcePendingMoveCancelDisabledByDefault(t *testing.T) {
	svc, _, _ := createTestService(t)
	_, err := svc.CancelStaleArchivedResourcePendingMove(context.Background(), ArchivedResourcePendingMoveCancelRequest{
		PendingMoveID:        "job-pending",
		PendingMoveOperation: "operation-pending",
		SessionID:            "session-terminal",
		SnapshotVersion:      models.ArchivedResourceReconcileSnapshotVersion,
		SnapshotDigest:       "digest",
		ResourceKind:         models.ArchivedResourceReconcileResourceKind,
		ResourceID:           "wt-pending",
		ManagedRootKey:       "git_worktree:pending",
		ResourceSnapshot:     `{}`,
		TaskID:               "task-pending",
	})
	if !errors.Is(err, ErrArchivedResourcePendingMoveDisabled) {
		t.Fatalf("disabled cancel error = %v, want ErrArchivedResourcePendingMoveDisabled", err)
	}
}

func TestArchivedResourceReleaseAbsentDisabledByDefault(t *testing.T) {
	svc, _, _ := createTestService(t)
	_, err := svc.ReleaseAbsentArchivedResourceTarget(context.Background(), ArchivedResourceReleaseRequest{
		AnchorOperationID:  "operation-anchor",
		AnchorDigest:       "digest",
		AnchorTaskID:       "task-anchor",
		AnchorWorktreeID:   "wt-anchor",
		AnchorRepository:   "repository-company",
		AnchorBranch:       "feature/synthetic",
		AnchorHeadOID:      strings.Repeat("a", 40),
		AnchorWorktreePath: "/tmp/release/path",
		AnchorGitCommonDir: "/tmp/release/.git",
		ReleasedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	})
	if !errors.Is(err, ErrArchivedResourceReleaseDisabled) {
		t.Fatalf("disabled release error = %v, want ErrArchivedResourceReleaseDisabled", err)
	}
}

func TestArchivedResourceEnvironmentRetirementDisabledByDefault(t *testing.T) {
	svc, _, _ := createTestService(t)
	_, err := svc.RetireStaleArchivedResourceEnvironmentReference(context.Background(), ArchivedResourceEnvironmentRetirementRequest{
		EnvironmentID:     "env-stopped",
		TaskID:            "task-archived",
		EnvironmentStatus: "stopped",
		Repositories: []ArchivedResourceEnvironmentRetirementRepositoryRequest{
			{
				ID:             "repo-row",
				RepositoryID:   "repository-company",
				WorktreeID:     "wt-repo",
				WorktreeBranch: "feature/synthetic",
				Position:       0,
				Status:         "active",
				CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
				UpdatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
		RetiredAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if !errors.Is(err, ErrArchivedResourceEnvironmentRetirementDisabled) {
		t.Fatalf("disabled retirement error = %v, want ErrArchivedResourceEnvironmentRetirementDisabled", err)
	}
}

func TestArchivedResourceTerminalRequestShapeRejectsMissingFields(t *testing.T) {
	if err := validateArchivedResourcePendingMoveRequest(ArchivedResourcePendingMoveCancelRequest{}); !errors.Is(err, ErrArchivedResourcePendingMoveInvalid) {
		t.Fatalf("pending move validation = %v, want ErrArchivedResourcePendingMoveInvalid", err)
	}
	if err := validateArchivedResourceReleaseRequest(ArchivedResourceReleaseRequest{}); !errors.Is(err, ErrArchivedResourceReleaseInvalid) {
		t.Fatalf("release validation = %v, want ErrArchivedResourceReleaseInvalid", err)
	}
	if err := validateArchivedResourceEnvironmentRetirementRequest(ArchivedResourceEnvironmentRetirementRequest{}); !errors.Is(err, ErrArchivedResourceEnvironmentRetirementInvalid) {
		t.Fatalf("retirement validation = %v, want ErrArchivedResourceEnvironmentRetirementInvalid", err)
	}
}
