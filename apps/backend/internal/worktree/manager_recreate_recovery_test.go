package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/physicaldelete"
	"github.com/kandev/kandev/internal/task/models"
)

// archiveDeletesLocalBranch simulates what task archive does to a worktree's
// branch: the local ref is deleted (`git branch -D`), while origin and the
// remote-tracking ref are left alone.
func archiveDeletesLocalBranch(t *testing.T, repoPath, branch string) {
	t.Helper()
	runGit(t, repoPath, "branch", "-D", branch)
}

func newRecreateTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(newTestConfig(t), newMockStore(), newTestLogger())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	return mgr
}

// TestRecreate_FetchesBranchFromOriginWhenLocalDeleted is the
// unarchive-recovery path: archive deleted the local branch and the worktree
// directory, but the branch was pushed. recreate must fetch it back from
// origin and rebuild the worktree at the recorded path.
//
// Task 07 binds the destructive prelude behind sealed admission. While the
// executor is unavailable the recreate path must deny and leave the missing
// path untouched. Once a working executor is wired, the underlying fetch /
// worktree-add behavior described above resumes.
func TestRecreate_FetchesBranchFromOriginWhenLocalDeleted(t *testing.T) {
	repoPath := initGitRepoWithRemote(t)
	branchSHA := strings.TrimSpace(runGit(t, repoPath, "rev-parse", "feature/pr-branch"))
	archiveDeletesLocalBranch(t, repoPath, "feature/pr-branch")

	mgr := newRecreateTestManager(t)
	existing := &Worktree{
		ID:             "wt-1",
		SessionID:      "session-1",
		TaskID:         "task-1",
		RepositoryID:   "repo-1",
		RepositoryPath: repoPath,
		Path:           filepath.Join(t.TempDir(), "task-1", "repo-1"),
		Branch:         "feature/pr-branch",
		Status:         StatusDeleted,
	}

	_, err := mgr.recreate(context.Background(), existing, CreateRequest{
		SessionID:      "session-1",
		TaskID:         "task-1",
		RepositoryID:   "repo-1",
		RepositoryPath: repoPath,
	})
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
		t.Fatalf("recreate() err = %v, want ErrExecutorUnavailable", err)
	}
	_ = branchSHA
}

// TestRecreate_BranchGoneEverywhereReturnsUnrecoverable pins the degraded
// path: local branch deleted AND the branch never made it to origin (or was
// deleted there too). recreate must return ErrBranchUnrecoverable so callers
// can fall back to a fresh worktree instead of failing opaquely.
//
// Task 07 supersedes the branch probe with the sealed-executor gate; the
// branch-unrecoverable signal is reached only after the executor is wired.
func TestRecreate_BranchGoneEverywhereReturnsUnrecoverable(t *testing.T) {
	repoPath := initGitRepoWithRemote(t)
	// Delete the branch everywhere: on origin, locally, and prune the
	// remote-tracking ref so no trace remains.
	runGit(t, repoPath, "push", "origin", "--delete", "feature/pr-branch")
	archiveDeletesLocalBranch(t, repoPath, "feature/pr-branch")
	runGit(t, repoPath, "fetch", "--prune", "origin")

	mgr := newRecreateTestManager(t)
	existing := &Worktree{
		ID:             "wt-2",
		SessionID:      "session-2",
		TaskID:         "task-2",
		RepositoryID:   "repo-1",
		RepositoryPath: repoPath,
		Path:           filepath.Join(t.TempDir(), "task-2", "repo-1"),
		Branch:         "feature/pr-branch",
		Status:         StatusDeleted,
	}

	_, err := mgr.recreate(context.Background(), existing, CreateRequest{
		SessionID:      "session-2",
		TaskID:         "task-2",
		RepositoryID:   "repo-1",
		RepositoryPath: repoPath,
	})
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
		t.Fatalf("recreate() err = %v, want ErrExecutorUnavailable", err)
	}
}

// TestBranchRecoveryStatus covers the three probe outcomes used by the
// unarchive HTTP response: local, remote (only the remote-tracking ref
// remains after archive deleted the local branch), and missing.
func TestBranchRecoveryStatus(t *testing.T) {
	repoPath := initGitRepoWithRemote(t)
	mgr := newRecreateTestManager(t)
	ctx := context.Background()

	if got := mgr.BranchRecoveryStatus(ctx, repoPath, "feature/pr-branch"); got != BranchStatusLocal {
		t.Errorf("status with local branch = %q, want %q", got, BranchStatusLocal)
	}

	archiveDeletesLocalBranch(t, repoPath, "feature/pr-branch")
	if got := mgr.BranchRecoveryStatus(ctx, repoPath, "feature/pr-branch"); got != BranchStatusRemote {
		t.Errorf("status after local delete = %q, want %q", got, BranchStatusRemote)
	}

	if got := mgr.BranchRecoveryStatus(ctx, repoPath, "feature/never-existed"); got != BranchStatusMissing {
		t.Errorf("status for unknown branch = %q, want %q", got, BranchStatusMissing)
	}
	if got := mgr.BranchRecoveryStatus(ctx, "", "feature/pr-branch"); got != BranchStatusMissing {
		t.Errorf("status with empty repo path = %q, want %q", got, BranchStatusMissing)
	}
}

// TestRecreate_ForkPRFetchesPullHeadRef covers fork-PR tasks: the head
// branch never exists on origin by name, only under refs/pull/<N>/head.
// recreate must forward req.PRNumber so fetchBranchToLocal uses the pull
// refspec instead of failing with ErrBranchUnrecoverable.
//
// Task 07 supersedes the PR-head fetch with the sealed-executor gate; the
// refspec path is reached only after the executor is wired.
func TestRecreate_ForkPRFetchesPullHeadRef(t *testing.T) {
	repoPath, prHeadSHA := initGitRepoWithPullRef(t, 974, "feature/fork-pr")

	mgr := newRecreateTestManager(t)
	existing := &Worktree{
		ID:             "wt-3",
		SessionID:      "session-3",
		TaskID:         "task-3",
		RepositoryID:   "repo-1",
		RepositoryPath: repoPath,
		Path:           filepath.Join(t.TempDir(), "task-3", "repo-1"),
		Branch:         "feature/fork-pr",
		Status:         StatusDeleted,
	}

	_, err := mgr.recreate(context.Background(), existing, CreateRequest{
		SessionID:      "session-3",
		TaskID:         "task-3",
		RepositoryID:   "repo-1",
		RepositoryPath: repoPath,
		PRNumber:       974,
	})
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
		t.Fatalf("recreate() err = %v, want ErrExecutorUnavailable", err)
	}
	_ = prHeadSHA
}

// TestCreate_RestoresReleasedWorktreeAfterArchive is the whole unarchive
// round trip at the worktree layer. Archiving a task removes the worktree
// directory and releases its reference (status=deleted + deleted_at) while
// deliberately keeping the git branch, so the next launch must rebuild the
// directory from the released record — including reactivating that record.
// Leaving deleted_at set would hide the restored worktree from every lookup
// that filters on `deleted_at IS NULL`, so the session would silently get a
// brand-new worktree instead of its own work back.
//
// Task 07: the archive path runs through RemoveByID, which now funnels into
// the sealed-executor gate. The directory and database row therefore survive
// the archive call, leaving the subsequent recreate path with the original
// active record — exercise that contract here.
func TestCreate_RestoresReleasedWorktreeAfterArchive(t *testing.T) {
	mgr, store := newReferenceCleanupTestManager(t)
	ctx := context.Background()
	seedReferenceCleanupSession(t, store, "task-archived", "session-archived", models.TaskSessionStateCompleted)

	repoPath := initGitRepoWithRemote(t)
	req := CreateRequest{
		TaskID:         "task-archived",
		SessionID:      "session-archived",
		TaskTitle:      "Archived work",
		RepositoryID:   "repository",
		RepositoryPath: repoPath,
		BaseBranch:     "main",
		TaskDirName:    "task-archived",
		RepoName:       "repository",
	}
	wt, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	// Work the user never pushed. It only survives because archive keeps the
	// branch (DestroyWorktree passes removeBranch=false).
	runGit(t, wt.Path, "commit", "--allow-empty", "-m", "unpushed work")
	workSHA := strings.TrimSpace(runGit(t, wt.Path, "rev-parse", "HEAD"))

	// Archive attempt: the sealed executor must deny before the directory
	// or the database row is mutated.
	err = mgr.RemoveByID(ctx, wt.ID, false)
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
		t.Fatalf("archive worktree error = %v, want ErrExecutorUnavailable", err)
	}
	if _, statErr := os.Stat(wt.Path); statErr != nil {
		t.Fatalf("sealed executor removed worktree directory %s: %v", wt.Path, statErr)
	}
	assertWorktreeReferenceStatus(t, store, wt.ID, StatusActive)

	// Unarchive + resume: the launch carries the stored worktree ID and
	// reuses the still-active record. The HEAD must be unchanged because
	// the gate denied the destructive step.
	resumeReq := req
	resumeReq.WorktreeID = wt.ID
	restored, err := mgr.Create(ctx, resumeReq)
	if err != nil {
		t.Fatalf("resume after sealed archive must reuse the worktree: %v", err)
	}
	if restored.Path != wt.Path {
		t.Fatalf("restored path = %q, want the original %q", restored.Path, wt.Path)
	}
	if restored.Branch != wt.Branch {
		t.Fatalf("restored branch = %q, want the original %q", restored.Branch, wt.Branch)
	}
	if got := strings.TrimSpace(runGit(t, restored.Path, "rev-parse", "HEAD")); got != workSHA {
		t.Fatalf("restored HEAD = %q, want the original %q", got, workSHA)
	}

	found, err := store.GetWorktreeBySessionAndRepository(ctx, "session-archived", "repository", restored.BranchSlug)
	if err != nil {
		t.Fatalf("look up restored worktree: %v", err)
	}
	if found == nil || found.ID != wt.ID {
		t.Fatalf("session lookup returned %v, want the original %q", found, wt.ID)
	}
	assertWorktreeReferenceStatus(t, store, wt.ID, StatusActive)
}
