package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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

	err = mgr.CleanupWorktrees(context.Background(), []*Worktree{wt})
	if !errors.Is(err, ErrWorktreeReleaseConflict) {
		t.Fatalf("CleanupWorktrees error = %v, want generation conflict", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("snapshot failure changed path: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef)); got != branchOID {
		t.Fatalf("snapshot failure changed branch: got %q want %q", got, branchOID)
	}
	registration := runGit(t, repoPath, "worktree", "list", "--porcelain")
	if !strings.Contains(registration, "worktree "+registeredPath+"\n") {
		t.Fatalf("snapshot failure changed Git registration: %s", registration)
	}
	if len(store.releaseCalls) != 0 || store.updateCalls != 0 {
		t.Fatalf("snapshot failure reached store mutation: release=%v update=%d", store.releaseCalls, store.updateCalls)
	}
	if store.worktrees[wt.ID].Status != StatusActive {
		t.Fatalf("snapshot failure changed store status: %q", store.worktrees[wt.ID].Status)
	}
	if _, ok := mgr.worktrees[cacheKey(wt.SessionID, wt.RepositoryID, wt.BranchSlug)]; !ok {
		t.Fatal("snapshot failure evicted cache")
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
	err = mgr.CleanupWorktrees(context.Background(), []*Worktree{wt})
	if !errors.Is(err, ErrWorktreeReleaseConflict) {
		t.Fatalf("CleanupWorktrees error = %v, want generation conflict", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("projection drift changed path: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef)); got != branchOID {
		t.Fatalf("projection drift changed branch: got %q want %q", got, branchOID)
	}
	registration := runGit(t, repoPath, "worktree", "list", "--porcelain")
	if !strings.Contains(registration, "worktree "+registeredPath+"\n") {
		t.Fatalf("projection drift changed Git registration: %s", registration)
	}
	if len(store.releaseCalls) != 0 || store.updateCalls != 0 {
		t.Fatalf("projection drift reached store mutation: release=%v update=%d", store.releaseCalls, store.updateCalls)
	}
	if _, ok := mgr.worktrees[cacheKey(wt.SessionID, wt.RepositoryID, wt.BranchSlug)]; !ok {
		t.Fatal("projection drift evicted cache")
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

	err = mgr.CleanupWorktrees(context.Background(), []*Worktree{wt})
	if !errors.Is(err, ErrWorktreeReleaseConflict) {
		t.Fatalf("CleanupWorktrees error = %v, want generation conflict", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("physical removal did not complete before CAS conflict: %v", err)
	}
	if store.worktrees[wt.ID].Status != StatusActive {
		t.Fatalf("CAS conflict changed store status: %q", store.worktrees[wt.ID].Status)
	}
	if _, ok := mgr.worktrees[cacheKey(wt.SessionID, wt.RepositoryID, wt.BranchSlug)]; !ok {
		t.Fatal("CAS conflict evicted cache")
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
	err = mgr.CleanupWorktrees(ctx, []*Worktree{wt})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CleanupWorktrees error = %v, want context cancellation", err)
	}

	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("failed removal changed directory state: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, repoPath, "rev-parse", branchRef)); got != branchOID {
		t.Fatalf("failed removal changed branch: got %q, want %q", got, branchOID)
	}
	registration = runGit(t, repoPath, "worktree", "list", "--porcelain")
	if !strings.Contains(registration, "worktree "+registeredPath+"\n") {
		t.Fatalf("failed removal changed worktree registration: %s", registration)
	}
	stored, ok := store.worktrees[wt.ID]
	if !ok {
		t.Fatal("failed removal dropped worktree store entry")
	}
	if stored.Status != StatusActive || stored.DeletedAt != nil {
		t.Fatalf("failed removal changed store state: status=%q deleted_at=%v", stored.Status, stored.DeletedAt)
	}
	if store.updateCalls != 0 {
		t.Fatalf("failed removal updated store %d times, want 0", store.updateCalls)
	}
	mgr.mu.RLock()
	cached, ok := mgr.worktrees[cacheID]
	mgr.mu.RUnlock()
	if !ok || cached != wt {
		t.Fatalf("failed removal changed cache entry: present=%v value=%p want=%p", ok, cached, wt)
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

	err = mgr.CleanupWorktrees(context.Background(), []*Worktree{first, success, last})
	if !errors.Is(err, firstFailure) {
		t.Fatalf("CleanupWorktrees error = %v, want first failure", err)
	}
	if errors.Is(err, lastFailure) {
		t.Fatalf("CleanupWorktrees returned last failure instead of first: %v", err)
	}
	if want := []string{first.ID, success.ID, last.ID}; !reflect.DeepEqual(store.referenceCalls, want) {
		t.Fatalf("reference checks = %v, want %v", store.referenceCalls, want)
	}

	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("first failed worktree path changed: %v", err)
	}
	if _, err := os.Stat(last.Path); err != nil {
		t.Fatalf("last failed worktree path changed: %v", err)
	}
	if _, err := os.Stat(success.Path); !os.IsNotExist(err) {
		t.Fatalf("successful worktree path still exists: %v", err)
	}
	if store.worktrees[first.ID].Status != StatusActive || store.worktrees[last.ID].Status != StatusActive {
		t.Fatalf("failed worktree store state changed: first=%q last=%q",
			store.worktrees[first.ID].Status, store.worktrees[last.ID].Status)
	}
	if store.worktrees[success.ID].Status != StatusDeleted {
		t.Fatalf("successful worktree store status = %q, want %q", store.worktrees[success.ID].Status, StatusDeleted)
	}

	mgr.mu.RLock()
	_, firstCached := mgr.worktrees[cacheKey(first.SessionID, first.RepositoryID, first.BranchSlug)]
	_, successCached := mgr.worktrees[cacheKey(success.SessionID, success.RepositoryID, success.BranchSlug)]
	_, lastCached := mgr.worktrees[cacheKey(last.SessionID, last.RepositoryID, last.BranchSlug)]
	mgr.mu.RUnlock()
	if !firstCached || successCached || !lastCached {
		t.Fatalf("cache state after mixed batch: first=%v success=%v last=%v, want true/false/true",
			firstCached, successCached, lastCached)
	}
}
