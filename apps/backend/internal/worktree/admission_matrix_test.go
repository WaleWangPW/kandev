package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/physicaldelete"
	"github.com/kandev/kandev/internal/system/storage"
)

// denyAdmissionFactory returns a real physicaldelete.Service configured to
// deny every Execute call via the sealed filesystem executor. It still
// admits provisional leases so the manager can construct a worktree for
// the test fixture.
func denyAdmissionFactory() physicaldelete.Admission {
	admission, _ := physicaldelete.New(physicaldelete.Config{
		Inventory: physicaldelete.InventorySourceFunc(func(context.Context) (physicaldelete.Inventory, error) {
			return physicaldelete.Inventory{Complete: true}, nil
		}),
	})
	return admission
}

// TestAdmissionGateMatrix_ConsumerEntryFailClosed verifies Task 07's
// fail-closed contract across every managed-root consumer the worktree
// manager owns. Each test creates the prerequisite worktree, then issues
// the consumer call with a deliberately sealed admission and asserts the
// destructive step is denied before the underlying filesystem mutation.
func TestAdmissionGateMatrix_ConsumerEntryFailClosed(t *testing.T) {
	t.Run("RemoveByID denies before any filesystem mutation", func(t *testing.T) {
		mgr, wt := createWorktreeForAdmissionMatrix(t, "task-removebyid", "session-removebyid")
		defer cleanupAdmissionMatrixWorktree(t, wt)
		mgr.SetAdmission(denyEverythingAdmission{})

		err := mgr.RemoveByID(context.Background(), wt.ID, false)
		if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
			t.Fatalf("RemoveByID error = %v, want ErrExecutorUnavailable", err)
		}
		if _, statErr := os.Stat(wt.Path); statErr != nil {
			t.Fatalf("sealed executor removed worktree path %s: %v", wt.Path, statErr)
		}
	})
	t.Run("CleanupWorktrees denies before any filesystem mutation", func(t *testing.T) {
		mgr, wt := createWorktreeForAdmissionMatrix(t, "task-cleanup", "session-cleanup")
		defer cleanupAdmissionMatrixWorktree(t, wt)
		mgr.SetAdmission(denyEverythingAdmission{})

		err := mgr.CleanupWorktrees(context.Background(), []*Worktree{wt})
		if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
			t.Fatalf("CleanupWorktrees error = %v, want ErrExecutorUnavailable", err)
		}
		if _, statErr := os.Stat(wt.Path); statErr != nil {
			t.Fatalf("sealed executor removed worktree path %s: %v", wt.Path, statErr)
		}
	})
	t.Run("PruneQuarantinedWorkspace denies before Git registration mutation", func(t *testing.T) {
		mgr, wt := createWorktreeForAdmissionMatrix(t, "task-prune", "session-prune")
		defer cleanupAdmissionMatrixWorktree(t, wt)
		mgr.SetAdmission(denyEverythingAdmission{})

		// macOS /private prefix may be added when git resolves the worktree
		// path. Resolve symlinks before comparing to avoid false negatives.
		resolved, err := filepath.EvalSymlinks(wt.Path)
		if err != nil {
			t.Fatalf("resolve worktree path: %v", err)
		}
		registeredBefore := strings.Contains(
			runGit(t, wt.RepositoryPath, "worktree", "list", "--porcelain"),
			"worktree "+resolved,
		)
		if !registeredBefore {
			t.Fatalf("test fixture missing worktree registration for %s", resolved)
		}

		err = mgr.PruneQuarantinedWorkspace(
			context.Background(),
			storage.QuarantineEntry{TaskID: wt.TaskID},
		)
		if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
			t.Fatalf("PruneQuarantinedWorkspace error = %v, want ErrExecutorUnavailable", err)
		}
		registeredAfter := strings.Contains(
			runGit(t, wt.RepositoryPath, "worktree", "list", "--porcelain"),
			"worktree "+resolved,
		)
		if !registeredAfter {
			t.Fatalf("sealed executor removed Git registration for %s", resolved)
		}
	})
	t.Run("recreate denies before removing existing directory", func(t *testing.T) {
		mgr, wt := createWorktreeForAdmissionMatrix(t, "task-recreate", "session-recreate")
		defer cleanupAdmissionMatrixWorktree(t, wt)
		mgr.SetAdmission(denyEverythingAdmission{})

		_, err := mgr.recreate(context.Background(), wt, CreateRequest{
			SessionID:      wt.SessionID,
			TaskID:         wt.TaskID,
			RepositoryID:   wt.RepositoryID,
			RepositoryPath: wt.RepositoryPath,
		})
		if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
			t.Fatalf("recreate error = %v, want ErrExecutorUnavailable", err)
		}
		if _, statErr := os.Stat(wt.Path); statErr != nil {
			t.Fatalf("sealed executor removed recreate target %s: %v", wt.Path, statErr)
		}
	})
	t.Run("CleanupPlainFolder denies before removing path", func(t *testing.T) {
		tasksRoot := canonicalTempDir(t)
		target := filepath.Join(tasksRoot, "task-plain", "folder")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("mkdir target: %v", err)
		}
		canary := filepath.Join(target, "keep")
		if err := os.WriteFile(canary, []byte("data"), 0o600); err != nil {
			t.Fatalf("write canary: %v", err)
		}
		cleaner := newHandoffCleanerForMatrix(t, tasksRoot)
		cleaner.SetAdmission(denyEverythingAdmission{})

		err := cleaner.CleanupPlainFolder(context.Background(), target)
		if !errors.Is(err, physicaldelete.ErrExecutorUnavailable) {
			t.Fatalf("CleanupPlainFolder error = %v, want ErrExecutorUnavailable", err)
		}
		if _, statErr := os.Stat(canary); statErr != nil {
			t.Fatalf("sealed executor removed plain-folder payload: %v", statErr)
		}
	})
}

// TestAdmissionGateMatrix_MissingAdmissionFailsClosed verifies that a
// nil admission also denies every consumer so the production wiring is
// never silent.
func TestAdmissionGateMatrix_MissingAdmissionFailsClosed(t *testing.T) {
	t.Run("removeWorktree refuses nil admission", func(t *testing.T) {
		mgr, wt := createWorktreeForAdmissionMatrix(t, "task-niladmission", "session-niladmission")
		defer cleanupAdmissionMatrixWorktree(t, wt)
		mgr.SetAdmission(nil)
		// Do NOT wire an admission; the manager reports fail-closed on every
		// destructive entry rather than continuing in an unauthenticated state.
		err := mgr.RemoveByID(context.Background(), wt.ID, false)
		if err == nil {
			t.Fatal("nil admission must fail closed at removeWorktree")
		}
		if !strings.Contains(err.Error(), "physical-delete admission") {
			t.Fatalf("error = %v, want admission-unconfigured sentinel", err)
		}
	})
	t.Run("CleanupPlainFolder refuses nil admission", func(t *testing.T) {
		tasksRoot := canonicalTempDir(t)
		target := filepath.Join(tasksRoot, "task-nil", "folder")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("mkdir target: %v", err)
		}
		cleaner := newHandoffCleanerForMatrix(t, tasksRoot)
		// No SetAdmission call — nil admission must fail closed.
		err := cleaner.CleanupPlainFolder(context.Background(), target)
		if err == nil {
			t.Fatal("nil admission must fail closed at CleanupPlainFolder")
		}
		if !strings.Contains(err.Error(), "physical-delete admission") {
			t.Fatalf("error = %v, want admission-unconfigured sentinel", err)
		}
	})
}

// denyEverythingAdmission surfaces the sealed-executor denial so every
// consumer test can prove the gate fires before any filesystem or Git
// mutation. The deny gate is deliberate: a test that uses a working
// admission would only verify the inventory pipeline, not the
// fail-closed contract the central gate is supposed to guarantee.
type denyEverythingAdmission struct{}

func (denyEverythingAdmission) BeginProvisional(_ context.Context, _ physicaldelete.CreateRequest) (physicaldelete.ProvisionalLease, error) {
	return physicaldelete.ProvisionalLease{}, nil
}

func (denyEverythingAdmission) Execute(_ context.Context, req physicaldelete.Request) (physicaldelete.Receipt, error) {
	return physicaldelete.Receipt{
		Action:       req.Action,
		ResourceKind: req.Resource.Kind,
		ResourceID:   req.Resource.ID,
		Reason:       physicaldelete.DenialExecutorUnavailable,
	}, physicaldelete.ErrExecutorUnavailable
}

// createWorktreeForAdmissionMatrix wraps the existing manager + repo
// fixtures and runs Create with a real physicaldelete.Service configured
// to admit provisional leases while denying destructive actions. The
// caller replaces the admission before exercising the gated path. The
// returned manager shares the same mock store as the worktree so the
// subsequent RemoveByID / CleanupWorktrees calls can locate it.
func createWorktreeForAdmissionMatrix(t *testing.T, taskID, sessionID string) (*Manager, *Worktree) {
	t.Helper()
	repoPath := initGitRepoForWorktreeTest(t)
	store := newMockStore()
	mgr, err := NewManager(newTestConfig(t), store, newTestLogger())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.SetAdmission(denyAdmissionFactory())

	wt, err := mgr.Create(context.Background(), CreateRequest{
		TaskID:         taskID,
		SessionID:      sessionID,
		TaskTitle:      taskID,
		RepositoryID:   "repository-" + taskID,
		RepositoryPath: repoPath,
		BaseBranch:     "main",
		TaskDirName:    taskID,
		RepoName:       "repository-" + taskID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return mgr, wt
}

// newManagerForAdmissionMatrix returns a fresh Manager bound to a
// throwaway mock store. The caller wires its own admission. This helper
// is used by tests that do not need the worktree store to be preloaded
// (recreate, PruneQuarantinedWorkspace when the store isn't queried).
func newManagerForAdmissionMatrix(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(newTestConfig(t), newMockStore(), newTestLogger())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

// newHandoffCleanerForMatrix returns a HandoffCleaner bound to a tmp
// tasks root. The caller wires its own admission.
func newHandoffCleanerForMatrix(t *testing.T, tasksRoot string) *HandoffCleaner {
	t.Helper()
	log := newTestLogger()
	mgr := &Manager{config: Config{TasksBasePath: tasksRoot}, logger: log}
	return NewHandoffCleaner(mgr, log)
}

// cleanupAdmissionMatrixWorktree removes the underlying worktree
// directory without going through the gated manager, so test cleanup
// does not trip over the sealed executor the consumer tests rely on.
func cleanupAdmissionMatrixWorktree(t *testing.T, wt *Worktree) {
	t.Helper()
	if wt == nil || wt.Path == "" {
		return
	}
	_ = os.RemoveAll(wt.Path)
}

// permissiveMatrixAdmission is reserved for tests that want a working
// admission (provisional lease + destructive step admit). The matrix
// tests now use the real denyAdmissionFactory for sealed denials.
type permissiveMatrixAdmission struct{}

func (permissiveMatrixAdmission) BeginProvisional(_ context.Context, _ physicaldelete.CreateRequest) (physicaldelete.ProvisionalLease, error) {
	return physicaldelete.ProvisionalLease{}, nil
}

func (permissiveMatrixAdmission) Execute(_ context.Context, req physicaldelete.Request) (physicaldelete.Receipt, error) {
	return physicaldelete.Receipt{
		Action: req.Action, ResourceKind: req.Resource.Kind,
		ResourceID: req.Resource.ID, Mutated: false,
	}, nil
}
