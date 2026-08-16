package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

func TestArchivedResourceEnvironmentRetirementRemovesExactRepositoryRows(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-retire")
	env := seedStoppedEnvironment(t, repo, "task-retire", "env-stopped")
	row := seedEnvironmentRepo(t, repo, env.ID, "repo-row")

	job := environmentRetirementJobFixture(t, env, []models.ArchivedResourceEnvironmentRetirementRepository{
		archivedResourceRetirementRepoFixture(row),
	})

	identity, err := repo.RetireStaleArchivedResourceEnvironmentReference(ctx, job)
	if err != nil {
		t.Fatalf("RetireStaleArchivedResourceEnvironmentReference: %v", err)
	}
	if identity == nil {
		t.Fatal("expected non-nil identity")
	}

	var remaining int
	if err := repo.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM task_environment_repos WHERE id = ?", row.ID).Scan(&remaining); err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining rows = %d, want 0", remaining)
	}
}

func TestArchivedResourceEnvironmentRetirementFailsClosedWhenWorkspaceGroupStateUnknown(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-retire")
	env := seedStoppedEnvironment(t, repo, "task-retire", "env-stopped")
	row := seedEnvironmentRepo(t, repo, env.ID, "repo-row")
	if _, err := repo.db.Exec(`
		CREATE TABLE IF NOT EXISTS task_workspace_groups (
			id TEXT PRIMARY KEY,
			cleanup_status TEXT NOT NULL DEFAULT 'active'
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := repo.db.Exec(`INSERT INTO task_workspace_groups VALUES ('group-bad', 'future')`); err != nil {
		t.Fatalf("seed bad state: %v", err)
	}
	job := environmentRetirementJobFixture(t, env, []models.ArchivedResourceEnvironmentRetirementRepository{
		archivedResourceRetirementRepoFixture(row),
	})
	if _, err := repo.RetireStaleArchivedResourceEnvironmentReference(ctx, job); !errors.Is(err, ErrArchivedResourceEnvironmentRetirementNotAdmitted) {
		t.Fatalf("unknown workspace group state error = %v, want ErrArchivedResourceEnvironmentRetirementNotAdmitted", err)
	}
	var remaining int
	if err := repo.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM task_environment_repos WHERE id = ?", row.ID).Scan(&remaining); err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("row was deleted despite fail-closed: remaining=%d", remaining)
	}
}

func TestArchivedResourceEnvironmentRetirementFailsClosedOnIdentityDrift(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-retire")
	env := seedStoppedEnvironment(t, repo, "task-retire", "env-stopped")
	row := seedEnvironmentRepo(t, repo, env.ID, "repo-row")

	job := environmentRetirementJobFixture(t, env, []models.ArchivedResourceEnvironmentRetirementRepository{
		archivedResourceRetirementRepoFixture(row),
	})
	job.TaskID = "task-other"
	if _, err := repo.RetireStaleArchivedResourceEnvironmentReference(ctx, job); err == nil {
		t.Fatal("drifted task_id accepted")
	}
}

func TestArchivedResourceEnvironmentRetirementFailsClosedWhenEnvironmentNotStopped(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()

	insertTask(t, repo.db, "task-retire")
	env := seedStoppedEnvironment(t, repo, "task-retire", "env-stopped")
	row := seedEnvironmentRepo(t, repo, env.ID, "repo-row")
	if _, err := repo.db.Exec(`UPDATE task_environments SET status = 'ready' WHERE id = ?`, env.ID); err != nil {
		t.Fatalf("flip status: %v", err)
	}
	job := environmentRetirementJobFixture(t, env, []models.ArchivedResourceEnvironmentRetirementRepository{
		archivedResourceRetirementRepoFixture(row),
	})
	if _, err := repo.RetireStaleArchivedResourceEnvironmentReference(ctx, job); !errors.Is(err, ErrArchivedResourceEnvironmentRetirementIdentityDrift) {
		t.Fatalf("non-stopped env error = %v, want ErrArchivedResourceEnvironmentRetirementIdentityDrift", err)
	}
}

func seedStoppedEnvironment(t *testing.T, repo *Repository, taskID, envID string) *models.TaskEnvironment {
	t.Helper()
	now := time.Now().UTC()
	env := &models.TaskEnvironment{
		ID:                envID,
		TaskID:            taskID,
		ExecutorType:      string(models.ExecutorTypeWorktree),
		ExecutorID:        "executor-test",
		ExecutorProfileID: "profile-test",
		Status:            models.TaskEnvironmentStatusStopped,
		WorkspacePath:     "/tmp/env/worktree",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := repo.CreateTaskEnvironment(context.Background(), env); err != nil {
		t.Fatalf("create env: %v", err)
	}
	return env
}

func seedEnvironmentRepo(t *testing.T, repo *Repository, envID, repoID string) *models.TaskEnvironmentRepo {
	t.Helper()
	now := time.Now().UTC()
	row := &models.TaskEnvironmentRepo{
		ID:                repoID + "-row",
		TaskEnvironmentID: envID,
		RepositoryID:      repoID,
		BranchSlug:        "feature/synthetic",
		WorktreeID:        "wt-" + repoID,
		WorktreePath:      "/tmp/env/worktree",
		WorktreeBranch:    "feature/synthetic",
		Position:          0,
		Status:            "active",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := repo.CreateTaskEnvironmentRepo(context.Background(), row); err != nil {
		t.Fatalf("create env repo: %v", err)
	}
	return row
}

func archivedResourceRetirementRepoFixture(row *models.TaskEnvironmentRepo) models.ArchivedResourceEnvironmentRetirementRepository {
	return models.ArchivedResourceEnvironmentRetirementRepository{
		ID:             row.ID,
		RepositoryID:   row.RepositoryID,
		BranchSlug:     row.BranchSlug,
		WorktreeID:     row.WorktreeID,
		WorktreePath:   row.WorktreePath,
		WorktreeBranch: row.WorktreeBranch,
		Position:       row.Position,
		Status:         row.Status,
		CreatedAt:      row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      row.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func environmentRetirementJobFixture(
	t *testing.T,
	env *models.TaskEnvironment,
	repos []models.ArchivedResourceEnvironmentRetirementRepository,
) *models.TaskResourceCleanupJob {
	t.Helper()
	immutable := models.ArchivedResourceEnvironmentRetirementImmutable{
		EnvironmentID:     env.ID,
		TaskID:            env.TaskID,
		EnvironmentStatus: "stopped",
		Repositories:      repos,
	}
	proof := models.ArchivedResourceEnvironmentRetirementProof{
		RetiredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	_, raw, identity, err := models.NewArchivedResourceEnvironmentRetirementSnapshot(immutable, proof)
	if err != nil {
		t.Fatalf("build retirement snapshot: %v", err)
	}
	managedRootKey, _ := models.ArchivedResourceEnvironmentRetirementManagedRootKey(repos[0].WorktreePath)
	utcNow := time.Now().UTC()
	return &models.TaskResourceCleanupJob{
		ID:               identity.OperationID,
		OperationID:      identity.OperationID,
		TaskID:           env.TaskID,
		Trigger:          models.TaskResourceCleanupTriggerReconcile,
		State:            models.TaskResourceCleanupStatePending,
		ResourceSnapshot: string(raw),
		SnapshotVersion:  models.ArchivedResourceEnvironmentRetirementSnapshotVersion,
		SnapshotDigest:   identity.SnapshotDigest,
		ResourceKind:     identity.ResourceKind,
		ResourceID:       identity.ResourceID,
		ManagedRootKey:   managedRootKey,
		AnchorRevision:   0,
		CreatedAt:        utcNow,
		UpdatedAt:        utcNow,
	}
}
