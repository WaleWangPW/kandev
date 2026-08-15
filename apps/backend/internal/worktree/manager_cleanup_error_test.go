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
	updateCalls     int
}

func newCleanupFailureStore() *cleanupFailureStore {
	return &cleanupFailureStore{
		mockStore:       newMockStore(),
		referenceErrors: make(map[string]error),
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
