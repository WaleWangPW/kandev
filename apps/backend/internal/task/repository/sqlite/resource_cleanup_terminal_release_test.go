package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

func TestArchivedResourceReleaseAbsentRequiresExactRetainedAnchor(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	// No anchor exists: the release admission must fail.
	job := releaseAbsentJobFixture(t, "task-release", "/tmp/release/path", "operation-missing", "digest-missing")
	if _, err := repo.ReleaseAbsentArchivedResourceAnchor(ctx, job); !errors.Is(err, ErrArchivedResourceReleaseTargetNotRetained) {
		t.Fatalf("missing anchor release error = %v, want ErrArchivedResourceReleaseTargetNotRetained", err)
	}
}

func TestArchivedResourceReleaseAbsentSucceedsWhenInventoryIsClean(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-release")
	now := time.Now().UTC()
	retainedAnchor := seedRetainedAnchor(t, repo, "task-release", "/tmp/release/path", "operation-anchor", "digest-anchor", now)
	job := releaseAbsentJobFixtureWithAnchor(t, retainedAnchor, now)

	admission, err := repo.ReleaseAbsentArchivedResourceAnchor(ctx, job)
	if err != nil {
		t.Fatalf("ReleaseAbsentArchivedResourceAnchor: %v", err)
	}
	if admission == nil || admission.Job == nil {
		t.Fatal("admission missing job")
	}
	if admission.Job.State != models.TaskResourceCleanupStateReleased {
		t.Fatalf("released state = %q, want released", admission.Job.State)
	}
	if admission.Job.CompletedAt == nil {
		t.Fatal("released anchor has no CompletedAt")
	}
	if admission.Job.AnchorRevision != models.ArchivedResourceRetentionAnchorVersion {
		t.Fatalf("released anchor revision = %d", admission.Job.AnchorRevision)
	}
}

func TestArchivedResourceReleaseAbsentFailsWhenPathStillReferenced(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-release")
	now := time.Now().UTC()
	retainedAnchor := seedRetainedAnchor(t, repo, "task-release", "/tmp/release/path", "operation-anchor", "digest-anchor", now)
	if _, err := repo.db.Exec(`
		INSERT INTO task_environment_repos (id, task_environment_id, repository_id, branch_slug, worktree_id, worktree_path, worktree_branch, position, status, created_at, updated_at)
		VALUES (?, ?, ?, '', 'wt-other', ?, ?, 0, 'active', ?, ?)
	`, "env-repo-row", "env-other", "repository-company", "/tmp/release/path", "feature/synthetic", now, now); err != nil {
		t.Fatalf("seed retained repo: %v", err)
	}
	job := releaseAbsentJobFixtureWithAnchor(t, retainedAnchor, now)
	if _, err := repo.ReleaseAbsentArchivedResourceAnchor(ctx, job); !errors.Is(err, ErrArchivedResourceReleaseTargetNotRetained) {
		t.Fatalf("inventory-referenced release error = %v, want ErrArchivedResourceReleaseTargetNotRetained", err)
	}
}

func TestArchivedResourceReleaseAbsentRejectsMismatchedHeaders(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	now := time.Now().UTC()
	retainedAnchor := seedRetainedAnchor(t, repo, "task-release", "/tmp/release/path", "operation-anchor", "digest-anchor", now)
	job := releaseAbsentJobFixtureWithAnchor(t, retainedAnchor, now)
	job.OperationID = "different-operation-id"
	if _, err := repo.ReleaseAbsentArchivedResourceAnchor(ctx, job); err == nil {
		t.Fatal("mismatched header release accepted")
	}
}

func TestCancelStaleArchivedResourcePendingMoveRemovesOnlyTargetRow(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-cancel")
	insertTask(t, repo.db, "task-sibling")
	now := time.Now().UTC()
	pending := seedPrisitinePendingMove(t, repo, "task-cancel", "wt-cancel", now)
	other := seedPrisitinePendingMove(t, repo, "task-sibling", "wt-keep", now)

	cancelled, err := repo.CancelStaleArchivedResourcePendingMove(ctx, pending)
	if err != nil {
		t.Fatalf("CancelStaleArchivedResourcePendingMove: %v", err)
	}
	if !cancelled {
		t.Fatal("expected exactly one cancellation")
	}

	current, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, pending.OperationID)
	if err != nil {
		t.Fatalf("read cancelled job: %v", err)
	}
	if current.State != models.TaskResourceCleanupStateCancelled {
		t.Fatalf("cancelled state = %q, want cancelled", current.State)
	}
	if current.ActiveScopeKey != nil {
		t.Fatalf("cancelled active_scope_key = %v, want nil", current.ActiveScopeKey)
	}

	keep, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, other.OperationID)
	if err != nil {
		t.Fatalf("read sibling: %v", err)
	}
	if keep.State != models.TaskResourceCleanupStatePending {
		t.Fatalf("sibling state = %q, want pending", keep.State)
	}
	if keep.ActiveScopeKey == nil {
		t.Fatal("sibling lost its active_scope_key")
	}
}

func TestCancelStaleArchivedResourcePendingMoveRejectsDrift(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-cancel")
	now := time.Now().UTC()
	pending := seedPrisitinePendingMove(t, repo, "task-cancel", "wt-cancel", now)
	pending.SnapshotDigest = "tampered-digest"
	cancelled, err := repo.CancelStaleArchivedResourcePendingMove(ctx, pending)
	if err == nil {
		t.Fatalf("tampered digest accepted: cancelled=%v", cancelled)
	}
	if !errors.Is(err, models.ErrArchivedResourceSnapshotInvalid) {
		t.Fatalf("tampered digest error = %v, want ErrArchivedResourceSnapshotInvalid", err)
	}
}

func TestCancelStaleArchivedResourcePendingMoveIsNoOpForAlreadyCancelledRow(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-cancel")
	now := time.Now().UTC()
	pending := seedPrisitinePendingMove(t, repo, "task-cancel", "wt-cancel", now)
	cancelled, err := repo.CancelStaleArchivedResourcePendingMove(ctx, pending)
	if err != nil || !cancelled {
		t.Fatalf("first cancel: cancelled=%v err=%v", cancelled, err)
	}
	cancelledAgain, err := repo.CancelStaleArchivedResourcePendingMove(ctx, pending)
	if err != nil {
		t.Fatalf("replay cancel err: %v", err)
	}
	if cancelledAgain {
		t.Fatal("replay cancel reported success")
	}
}

// helpers

func seedRetainedAnchor(
	t *testing.T,
	repo *Repository,
	taskID, worktreePath, operationID, digest string,
	now time.Time,
) *models.TaskResourceCleanupJob {
	t.Helper()
	completed := now
	rootKey, err := models.ArchivedResourceReleaseManagedRootKey(worktreePath)
	if err != nil {
		t.Fatalf("managed root key: %v", err)
	}
	job := &models.TaskResourceCleanupJob{
		ID:               "job-anchor",
		OperationID:      operationID,
		TaskID:           taskID,
		Trigger:          models.TaskResourceCleanupTriggerReconcile,
		State:            models.TaskResourceCleanupStateRetained,
		ResourceSnapshot: `{"anchor":true}`,
		SnapshotVersion:  models.ArchivedResourceReconcileSnapshotVersion,
		SnapshotDigest:   digest,
		ResourceKind:     models.ArchivedResourceReconcileResourceKind,
		ResourceID:       "wt-anchor",
		ManagedRootKey:   rootKey,
		AnchorRevision:   models.ArchivedResourceRetentionAnchorVersion,
		CreatedAt:        now,
		UpdatedAt:        now,
		CompletedAt:      &completed,
	}
	if err := repo.CreateTaskResourceCleanupJob(context.Background(), job); err != nil {
		t.Fatalf("create retained anchor: %v", err)
	}
	return job
}

func releaseAbsentJobFixture(
	t *testing.T,
	taskID, worktreePath, operationID, digest string,
) *models.TaskResourceCleanupJob {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	headOID := strings.Repeat("a", 40)
	immutable := models.ArchivedResourceReleaseImmutable{
		AnchorOperationID:  operationID,
		AnchorDigest:       digest,
		AnchorTaskID:       taskID,
		AnchorWorktreeID:   "wt-anchor",
		AnchorRepository:   "repository-company",
		AnchorBranch:       "feature/synthetic",
		AnchorHeadOID:      headOID,
		AnchorWorktreePath: worktreePath,
		AnchorGitCommonDir: "/tmp/release/.git",
	}
	release := models.ArchivedResourceReleaseReleaseProof{
		PhysicalPath: worktreePath,
		GitWorktreeRegistration: models.ArchivedResourceReleaseGitRegistration{
			WorktreePath: worktreePath,
			Branch:       "feature/synthetic",
			HeadOID:      headOID,
		},
		ReleasedAt: now,
	}
	_, raw, identity, err := models.NewArchivedResourceReleaseSnapshot(immutable, release)
	if err != nil {
		t.Fatalf("build release snapshot: %v", err)
	}
	managedRootKey, err := models.ArchivedResourceReleaseManagedRootKey(worktreePath)
	if err != nil {
		t.Fatalf("managed root key: %v", err)
	}
	utcNow := time.Now().UTC()
	return &models.TaskResourceCleanupJob{
		ID:               identity.OperationID,
		OperationID:      identity.OperationID,
		TaskID:           taskID,
		Trigger:          models.TaskResourceCleanupTriggerReconcile,
		State:            models.TaskResourceCleanupStatePending,
		ResourceSnapshot: string(raw),
		SnapshotVersion:  models.ArchivedResourceReconcileSnapshotVersion,
		SnapshotDigest:   identity.SnapshotDigest,
		ResourceKind:     identity.ResourceKind,
		ResourceID:       identity.ResourceID,
		ManagedRootKey:   managedRootKey,
		AnchorRevision:   0,
		CreatedAt:        utcNow,
		UpdatedAt:        utcNow,
	}
}

func releaseAbsentJobFixtureWithAnchor(
	t *testing.T,
	anchor *models.TaskResourceCleanupJob,
	now time.Time,
) *models.TaskResourceCleanupJob {
	t.Helper()
	return releaseAbsentJobFixture(
		t,
		anchor.TaskID,
		"/tmp/release/path",
		anchor.OperationID,
		anchor.SnapshotDigest,
	)
}

func seedPrisitinePendingMove(
	t *testing.T,
	repo *Repository,
	taskID, worktreeID string,
	now time.Time,
) *models.TaskResourceCleanupJob {
	t.Helper()
	worktreePath := "/tmp/pending/worktree-" + worktreeID
	rootKey, err := models.ArchivedResourceReleaseManagedRootKey(worktreePath)
	if err != nil {
		t.Fatalf("managed root key: %v", err)
	}
	immutable := models.ArchivedResourceReconcileImmutable{
		OriginTaskID:   taskID,
		ArchivedAt:     now.Format(time.RFC3339Nano),
		ManagedRootKey: rootKey,
		Target: models.ArchivedResourceReconcileTarget{
			WorktreeID:     worktreeID,
			RepositoryID:   "repository-company",
			RepositoryPath: "/tmp/pending/repo",
			GitCommonDir:   "/tmp/pending/repo/.git",
			WorktreePath:   worktreePath,
			Branch:         "feature/pending",
			HeadOID:        strings.Repeat("b", 40),
		},
		Associations: []models.ArchivedResourceReconcileAssociation{
			{
				AssociationID:  "association-" + worktreeID,
				TaskID:         taskID,
				SessionID:      "session-" + worktreeID,
				WorktreeID:     worktreeID,
				RepositoryID:   "repository-company",
				BranchSlug:     "feature/pending",
				WorktreePath:   worktreePath,
				WorktreeBranch: "feature/pending",
				Status:         "active",
				CreatedAt:      now.Format(time.RFC3339Nano),
				UpdatedAt:      now.Format(time.RFC3339Nano),
			},
		},
	}
	snapshot, raw, identity, err := models.NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("build pending move snapshot: %v", err)
	}
	job := models.NewArchivedResourceReconcileJob(snapshot, raw, identity)
	job.ID = "job-" + worktreeID
	job.TaskID = taskID
	job.State = models.TaskResourceCleanupStatePending
	job.CreatedAt = now
	job.UpdatedAt = now
	if err := repo.CreateTaskResourceCleanupJob(context.Background(), job); err != nil {
		t.Fatalf("create pending move: %v", err)
	}
	return job
}

var _ = filepath.Join
