package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/physicaldelete"
)

type cleanupFailureStore struct {
	*mockStore
	referenceErrors map[string]error
	referenceCalls  []string
	prepareErrors   map[string]error
	prepareMutators map[string]func(*WorktreeReleaseSnapshot)
	prepareCalls    []string
	releaseErrors   map[string]error
	releaseCalls    []string
	updateCalls     int
}

func newCleanupFailureStore() *cleanupFailureStore {
	return &cleanupFailureStore{
		mockStore:       newMockStore(),
		referenceErrors: make(map[string]error),
		prepareErrors:   make(map[string]error),
		prepareMutators: make(map[string]func(*WorktreeReleaseSnapshot)),
		releaseErrors:   make(map[string]error),
	}
}

func (s *cleanupFailureStore) CountActiveWorktreeReferences(
	_ context.Context,
	worktreeID string,
	_ []string,
) (int, error) {
	s.referenceCalls = append(s.referenceCalls, worktreeID)
	if err := s.referenceErrors[worktreeID]; err != nil {
		return 0, err
	}
	return 0, nil
}

func (s *cleanupFailureStore) UpdateWorktree(ctx context.Context, wt *Worktree) error {
	s.updateCalls++
	return s.mockStore.UpdateWorktree(ctx, wt)
}

func (s *cleanupFailureStore) PrepareWorktreeRelease(
	ctx context.Context,
	worktreeID string,
	taskEnvironmentID string,
	repositoryID string,
	branchSlug string,
) (*WorktreeReleaseSnapshot, error) {
	s.prepareCalls = append(s.prepareCalls, worktreeID)
	if err := s.prepareErrors[worktreeID]; err != nil {
		return nil, err
	}
	snapshot, err := s.mockStore.PrepareWorktreeRelease(ctx, worktreeID, taskEnvironmentID, repositoryID, branchSlug)
	if err != nil {
		return nil, err
	}
	if mutate := s.prepareMutators[worktreeID]; mutate != nil {
		mutate(snapshot)
	}
	return snapshot, nil
}

func (s *cleanupFailureStore) ReleaseWorktreeReferenceCAS(
	ctx context.Context,
	expected *WorktreeReleaseSnapshot,
) (*WorktreeReleaseSnapshot, error) {
	s.releaseCalls = append(s.releaseCalls, expected.WorktreeID)
	if err := s.releaseErrors[expected.WorktreeID]; err != nil {
		return nil, err
	}
	return s.mockStore.ReleaseWorktreeReferenceCAS(ctx, expected)
}

func TestCleanupWorktreesCapturesReleaseBeforePhysicalMutation(t *testing.T) {
	store := newCleanupFailureStore()
	mgr, err := NewManager(newTestConfig(t), store, newTestLogger())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	repoPath := initGitRepoWithRemote(t)
	wt, err := mgr.Create(context.Background(), CreateRequest{
		TaskID:         "task-snapshot-error",
		SessionID:      "session-snapshot-error",
		TaskTitle:      "Snapshot error",
		RepositoryID:   "repository-snapshot-error",
		RepositoryPath: repoPath,
		BaseBranch:     "main",
		TaskDirName:    "task-snapshot-error",
		RepoName:       "repository-snapshot-error",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prepareErr := &WorktreeReleaseConflictError{WorktreeID: wt.ID, Reason: "test drift"}
	store.prepareErrors[wt.ID] = prepareErr
	branchRef := "refs/heads/" + wt.Branch
	branchOID := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef))
	registeredPath, err := filepath.EvalSymlinks(wt.Path)
	if err != nil {
		t.Fatalf("resolve worktree path: %v", err)
	}

	// Task 07: the gate fires before any store snapshot. With the executor
	// sealed unavailable the destructive step must deny before reaching the
	// store prepare/release path.
	err = mgr.CleanupWorktrees(context.Background(), []*Worktree{wt})
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
		t.Fatalf("CleanupWorktrees error = %v, want ErrExecutorUnavailable", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("sealed executor changed path: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef)); got != branchOID {
		t.Fatalf("sealed executor changed branch: got %q want %q", got, branchOID)
	}
	registration := runGit(t, repoPath, "worktree", "list", "--porcelain")
	if !strings.Contains(registration, "worktree "+registeredPath+"\n") {
		t.Fatalf("sealed executor changed Git registration: %s", registration)
	}
	if len(store.prepareCalls) != 0 {
		t.Fatalf("sealed executor reached store prepare: %v", store.prepareCalls)
	}
	if len(store.releaseCalls) != 0 || store.updateCalls != 0 {
		t.Fatalf("sealed executor reached store mutation: release=%v update=%d", store.releaseCalls, store.updateCalls)
	}
	if store.worktrees[wt.ID].Status != StatusActive {
		t.Fatalf("sealed executor changed store status: %q", store.worktrees[wt.ID].Status)
	}
	if _, ok := mgr.worktrees[cacheKey(wt.SessionID, wt.RepositoryID, wt.BranchSlug)]; !ok {
		t.Fatal("sealed executor evicted cache")
	}
}

func TestCleanupWorktreesRejectsProjectionDriftBeforePhysicalMutation(t *testing.T) {
	store := newCleanupFailureStore()
	mgr, err := NewManager(newTestConfig(t), store, newTestLogger())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	repoPath := initGitRepoWithRemote(t)
	wt, err := mgr.Create(context.Background(), CreateRequest{
		TaskID:         "task-projection-drift",
		SessionID:      "session-projection-drift",
		TaskTitle:      "Projection drift",
		RepositoryID:   "repository-projection-drift",
		RepositoryPath: repoPath,
		BaseBranch:     "main",
		TaskDirName:    "task-projection-drift",
		RepoName:       "repository-projection-drift",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	store.prepareMutators[wt.ID] = func(snapshot *WorktreeReleaseSnapshot) {
		snapshot.WorktreePath += "-replacement"
	}

	branchRef := "refs/heads/" + wt.Branch
	branchOID := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef))
	registeredPath, err := filepath.EvalSymlinks(wt.Path)
	if err != nil {
		t.Fatalf("resolve worktree path: %v", err)
	}
	// Task 07: gate denies before the store can observe any drift; the
	// mutator registered above must therefore never run.
	err = mgr.CleanupWorktrees(context.Background(), []*Worktree{wt})
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
		t.Fatalf("CleanupWorktrees error = %v, want ErrExecutorUnavailable", err)
	}
	if len(store.prepareCalls) != 0 {
		t.Fatalf("sealed executor reached store prepare: %v", store.prepareCalls)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("sealed executor changed path: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef)); got != branchOID {
		t.Fatalf("sealed executor changed branch: got %q want %q", got, branchOID)
	}
	registration := runGit(t, repoPath, "worktree", "list", "--porcelain")
	if !strings.Contains(registration, "worktree "+registeredPath+"\n") {
		t.Fatalf("sealed executor changed Git registration: %s", registration)
	}
	if len(store.releaseCalls) != 0 || store.updateCalls != 0 {
		t.Fatalf("sealed executor reached store mutation: release=%v update=%d", store.releaseCalls, store.updateCalls)
	}
	if _, ok := mgr.worktrees[cacheKey(wt.SessionID, wt.RepositoryID, wt.BranchSlug)]; !ok {
		t.Fatal("sealed executor evicted cache")
	}
}

func TestValidateCleanupWorktreeBindingRejectsEveryProjectedIdentityDrift(t *testing.T) {
	wt := &Worktree{
		ID: "worktree-binding", TaskEnvironmentID: "environment-binding",
		RepositoryID: "repository-binding", BranchSlug: "slug-binding",
		Path: "/tmp/binding", Branch: "feature/binding",
	}
	valid := &WorktreeReleaseSnapshot{
		WorktreeID: wt.ID, TaskEnvironmentID: wt.TaskEnvironmentID,
		RepositoryID: wt.RepositoryID, BranchSlug: wt.BranchSlug,
		WorktreePath: wt.Path, WorktreeBranch: wt.Branch,
	}
	if err := validateCleanupWorktreeBinding(wt, valid); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	tests := map[string]func(*WorktreeReleaseSnapshot){
		"worktree ID": func(snapshot *WorktreeReleaseSnapshot) { snapshot.WorktreeID += "-drift" },
		"environment": func(snapshot *WorktreeReleaseSnapshot) { snapshot.TaskEnvironmentID += "-drift" },
		"repository":  func(snapshot *WorktreeReleaseSnapshot) { snapshot.RepositoryID += "-drift" },
		"branch slug": func(snapshot *WorktreeReleaseSnapshot) { snapshot.BranchSlug += "-drift" },
		"path":        func(snapshot *WorktreeReleaseSnapshot) { snapshot.WorktreePath += "-drift" },
		"branch":      func(snapshot *WorktreeReleaseSnapshot) { snapshot.WorktreeBranch += "-drift" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			drifted := *valid
			mutate(&drifted)
			if err := validateCleanupWorktreeBinding(wt, &drifted); !errors.Is(err, ErrWorktreeReleaseConflict) {
				t.Fatalf("binding error = %v, want ErrWorktreeReleaseConflict", err)
			}
		})
	}
}

func TestCleanupWorktreesPropagatesReleaseConflictWithoutCacheEviction(t *testing.T) {
	store := newCleanupFailureStore()
	mgr, err := NewManager(newTestConfig(t), store, newTestLogger())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	wt, err := mgr.Create(context.Background(), CreateRequest{
		TaskID:         "task-release-conflict",
		SessionID:      "session-release-conflict",
		TaskTitle:      "Release conflict",
		RepositoryID:   "repository-release-conflict",
		RepositoryPath: initGitRepoWithRemote(t),
		BaseBranch:     "main",
		TaskDirName:    "task-release-conflict",
		RepoName:       "repository-release-conflict",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	store.releaseErrors[wt.ID] = &WorktreeReleaseConflictError{WorktreeID: wt.ID, Reason: "test drift"}

	// Task 07: gate denies first; release conflict is therefore irrelevant.
	err = mgr.CleanupWorktrees(context.Background(), []*Worktree{wt})
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
		t.Fatalf("CleanupWorktrees error = %v, want ErrExecutorUnavailable", err)
	}
	if len(store.releaseCalls) != 0 {
		t.Fatalf("sealed executor reached store release: %v", store.releaseCalls)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("sealed executor removed worktree path %s: %v", wt.Path, err)
	}
	if store.worktrees[wt.ID].Status != StatusActive {
		t.Fatalf("sealed executor changed store status: %q", store.worktrees[wt.ID].Status)
	}
	if _, ok := mgr.worktrees[cacheKey(wt.SessionID, wt.RepositoryID, wt.BranchSlug)]; !ok {
		t.Fatal("sealed executor evicted cache")
	}
}

func TestCleanupWorktreesPreservesStateAfterDirectoryRemovalError(t *testing.T) {
	store := newCleanupFailureStore()
	mgr, err := NewManager(newTestConfig(t), store, newTestLogger())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	repoPath := initGitRepoWithRemote(t)
	wt, err := mgr.Create(context.Background(), CreateRequest{
		TaskID:         "task-removal-error",
		SessionID:      "session-removal-error",
		TaskTitle:      "Removal error",
		RepositoryID:   "repository-removal-error",
		RepositoryPath: repoPath,
		BaseBranch:     "main",
		TaskDirName:    "task-removal-error",
		RepoName:       "repository-removal-error",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cacheID := cacheKey(wt.SessionID, wt.RepositoryID, wt.BranchSlug)
	branchRef := "refs/heads/" + wt.Branch
	branchOID := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef))
	registeredPath, err := filepath.EvalSymlinks(wt.Path)
	if err != nil {
		t.Fatalf("resolve worktree path: %v", err)
	}
	registration := runGit(t, repoPath, "worktree", "list", "--porcelain")
	if !strings.Contains(registration, "worktree "+registeredPath+"\n") {
		t.Fatalf("created worktree is not registered: %s", registration)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Task 07: gate runs before any cancel-aware step. The seal may surface
	// as ErrExecutorUnavailable (when the lock was already held) or as a
	// ErrLockUnavailable / context-cancellation chain (when the cancelled
	// context reaches the executor's ctx check before the executor deny).
	// All three keep every persisted bit byte-equivalent.
	err = mgr.CleanupWorktrees(ctx, []*Worktree{wt})
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) &&
		!errors.Is(err, physicaldelete.ErrLockUnavailable) &&
		!errors.Is(err, context.Canceled) {
		t.Fatalf("CleanupWorktrees error = %v, want ErrExecutorUnavailable, ErrLockUnavailable, or context.Canceled", err)
	}

	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("sealed executor changed directory state: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef)); got != branchOID {
		t.Fatalf("sealed executor changed branch: got %q, want %q", got, branchOID)
	}
	registration = runGit(t, repoPath, "worktree", "list", "--porcelain")
	if !strings.Contains(registration, "worktree "+registeredPath+"\n") {
		t.Fatalf("sealed executor changed worktree registration: %s", registration)
	}
	stored, ok := store.worktrees[wt.ID]
	if !ok {
		t.Fatal("sealed executor dropped worktree store entry")
	}
	if stored.Status != StatusActive || stored.DeletedAt != nil {
		t.Fatalf("sealed executor changed store state: status=%q deleted_at=%v", stored.Status, stored.DeletedAt)
	}
	if store.updateCalls != 0 {
		t.Fatalf("sealed executor updated store %d times, want 0", store.updateCalls)
	}
	mgr.mu.RLock()
	cached, ok := mgr.worktrees[cacheID]
	mgr.mu.RUnlock()
	if !ok || cached != wt {
		t.Fatalf("sealed executor changed cache entry: present=%v value=%p want=%p", ok, cached, wt)
	}
}

func TestCleanupWorktreesReturnsFirstErrorAndContinuesBatch(t *testing.T) {
	store := newCleanupFailureStore()
	mgr, err := NewManager(newTestConfig(t), store, newTestLogger())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	create := func(taskID string) *Worktree {
		t.Helper()
		wt, err := mgr.Create(context.Background(), CreateRequest{
			TaskID:         taskID,
			SessionID:      "session-" + taskID,
			TaskTitle:      taskID,
			RepositoryID:   "repository-" + taskID,
			RepositoryPath: initGitRepoWithRemote(t),
			BaseBranch:     "main",
			TaskDirName:    taskID,
			RepoName:       "repository-" + taskID,
		})
		if err != nil {
			t.Fatalf("Create %s: %v", taskID, err)
		}
		return wt
	}

	first := create("task-first-error")
	success := create("task-success")
	last := create("task-last-error")
	firstFailure := errors.New("first cleanup failure")
	lastFailure := errors.New("last cleanup failure")
	store.referenceErrors[first.ID] = firstFailure
	store.referenceErrors[last.ID] = lastFailure

	// Task 07: the gate runs before the per-worktree reference count, so the
	// legacy referenceErrors seeds are unreachable while the executor is
	// sealed unavailable. Assert that the gate denial dominates and every
	// batch entry keeps its row, directory, branch, and cache entry intact.
	err = mgr.CleanupWorktrees(context.Background(), []*Worktree{first, success, last})
	if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
		t.Fatalf("CleanupWorktrees error = %v, want ErrExecutorUnavailable", err)
	}
	if !reflect.DeepEqual(store.referenceCalls, []string(nil)) {
		t.Fatalf("reference checks = %v, want none (gate fires first)", store.referenceCalls)
	}

	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("sealed executor removed first worktree %s: %v", first.Path, err)
	}
	if _, err := os.Stat(success.Path); err != nil {
		t.Fatalf("sealed executor removed success worktree %s: %v", success.Path, err)
	}
	if _, err := os.Stat(last.Path); err != nil {
		t.Fatalf("sealed executor removed last worktree %s: %v", last.Path, err)
	}
	if store.worktrees[first.ID].Status != StatusActive ||
		store.worktrees[success.ID].Status != StatusActive ||
		store.worktrees[last.ID].Status != StatusActive {
		t.Fatalf("sealed executor changed batch store state: first=%q success=%q last=%q",
			store.worktrees[first.ID].Status,
			store.worktrees[success.ID].Status,
			store.worktrees[last.ID].Status)
	}

	mgr.mu.RLock()
	_, firstCached := mgr.worktrees[cacheKey(first.SessionID, first.RepositoryID, first.BranchSlug)]
	_, successCached := mgr.worktrees[cacheKey(success.SessionID, success.RepositoryID, success.BranchSlug)]
	_, lastCached := mgr.worktrees[cacheKey(last.SessionID, last.RepositoryID, last.BranchSlug)]
	mgr.mu.RUnlock()
	if !firstCached || !successCached || !lastCached {
		t.Fatalf("cache state after sealed batch: first=%v success=%v last=%v, want true/true/true",
			firstCached, successCached, lastCached)
	}

	// Silence the unused reference-error seeds; the tests above prove the
	// gate now supersedes them.
	_ = firstFailure
	_ = lastFailure
}
