package service

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

func TestArchivedResourceReconcileDisabledByDefault(t *testing.T) {
	svc, _, _ := createTestService(t)
	if svc.archivedResourceReconcileEnabled() {
		t.Fatal("archived resource reconcile must be disabled by default")
	}
}

func TestArchivedResourceReconcileEnableAndDisableToggle(t *testing.T) {
	svc, _, _ := createTestService(t)
	svc.SetArchivedResourceFeatures(true, false)
	if !svc.archivedResourceReconcileEnabled() {
		t.Fatal("reconcile flag not enabled after SetArchivedResourceFeatures")
	}
	svc.SetArchivedResourceFeatures(false, true)
	if svc.archivedResourceReconcileEnabled() {
		t.Fatal("reconcile flag still enabled after disable")
	}
}

func TestArchivedResourceReconcileEnabledWithRepo(t *testing.T) {
	svc, _, _ := createTestService(t)
	svc.SetArchivedResourceFeatures(true, false)
	if !svc.archivedResourceReconcileEnabled() {
		t.Fatal("reconcile flag not enabled")
	}
	if _, err := svc.archivedResourceReconcileRepo(); err != nil {
		t.Fatalf("reconcile repo unavailable: %v", err)
	}
	svc.StartArchivedResourceReconcileWorker(context.Background())
	svc.StopArchivedResourceReconcileWorker()
}

func TestArchivedResourceReconcileRequestShapeInvalid(t *testing.T) {
	if err := validateArchivedResourceAssociationCount(ArchivedResourceReconcileRequest{}); err == nil {
		t.Fatal("empty association count accepted")
	}
	req := ArchivedResourceGroupReconcileRequest{}
	if _, err := validateArchivedResourceGroupRequestShape(req); err == nil {
		t.Fatal("empty group request accepted")
	}
}

func TestCanonicalTimeEqual(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 1, time.UTC)
	if !canonicalTimeEqual(now.Format(time.RFC3339Nano), now) {
		t.Fatal("canonical time rejected matching instant")
	}
	notNano, _ := time.Parse(time.RFC3339, "2026-08-12T00:00:00Z")
	if canonicalTimeEqual(notNano.Format(time.RFC3339), now) {
		t.Fatal("non-nanosecond timestamp accepted")
	}
	if canonicalTimeEqual("not-a-time", now) {
		t.Fatal("invalid timestamp accepted")
	}
}

func TestIsCanonicalUTCTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 1, time.UTC)
	if !isCanonicalUTCTimestamp(now.Format(time.RFC3339Nano)) {
		t.Fatal("canonical UTC timestamp rejected")
	}
	if isCanonicalUTCTimestamp("not-a-time") {
		t.Fatal("invalid timestamp accepted")
	}
}

func TestArchivedResourceReconcileRequestShapeRejectsDuplicateTask(t *testing.T) {
	req := ArchivedResourceGroupReconcileRequest{
		ExpectedTasks: []ArchivedResourceGroupTaskRequest{
			{TaskID: "task-1"},
			{TaskID: "task-1"},
		},
		ExpectedBranches: []ArchivedResourceGroupBranchRequest{{Branch: "feature/x", HeadOID: strings.Repeat("a", 40)}},
		Target: ArchivedResourceGroupReconcileTargetRequest{
			WorktreeID:     "wt-1",
			RepositoryID:   "repo-1",
			RepositoryPath: "/tmp/repo",
			GitCommonDir:   "/tmp/repo/.git",
			WorktreePath:   "/tmp/worktree",
			Associations: []ArchivedResourceGroupReconcileAssociationRequest{
				{AssociationID: "assoc-1", TaskID: "task-1", SessionID: "sess-1", Branch: "feature/x", CreatedAt: "2026-08-12T00:00:00.000000001Z", UpdatedAt: "2026-08-12T00:01:00.000000002Z"},
			},
		},
	}
	if _, err := validateArchivedResourceGroupRequestShape(req); err == nil {
		t.Fatal("group with duplicate task accepted")
	}
}

func TestArchivedResourceValidationActiveStates(t *testing.T) {
	for _, state := range []models.TaskResourceCleanupState{
		models.TaskResourceCleanupStateRetained,
		models.TaskResourceCleanupStateBlocked,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStateRetryWait,
	} {
		if !models.IsActiveArchivedResourceReconcileState(state) {
			t.Fatalf("active state %q rejected", state)
		}
	}
}

func TestArchivedResourceReconcileLocked(t *testing.T) {
	svc, _, _ := createTestService(t)
	lock1 := svc.archivedResourceTaskLock("task-A")
	lock2 := svc.archivedResourceTaskLock("task-B")
	if lock1 == lock2 {
		t.Fatal("distinct task ids must produce distinct locks")
	}
	again := svc.archivedResourceTaskLock("task-A")
	if again != lock1 {
		t.Fatal("lock must be stable for the same task id")
	}
}

func TestWithArchivedResourceTaskLocksInvalid(t *testing.T) {
	svc, _, _ := createTestService(t)
	if err := svc.withArchivedResourceTaskLocks(nil, func() error { return nil }); err == nil {
		t.Fatal("empty task ids accepted")
	}
	if err := svc.withArchivedResourceTaskLocks([]string{"task-A"}, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if err := svc.withArchivedResourceTaskLocks([]string{"task-A", "task-A"}, func() error { return nil }); err == nil {
		t.Fatal("duplicate task ids accepted")
	}
}

func TestWithArchivedResourceTaskLocksSerializes(t *testing.T) {
	svc, _, _ := createTestService(t)
	var executed int32
	barrier := make(chan struct{})
	released := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = svc.withArchivedResourceTaskLocks([]string{"task-A", "task-B"}, func() error {
			close(barrier)
			<-released
			atomic.AddInt32(&executed, 1)
			return nil
		})
		close(done)
	}()
	<-barrier
	if atomic.LoadInt32(&executed) != 0 {
		t.Fatal("callback executed before lock was acquired by other goroutine")
	}
	close(released)
	<-done
	if atomic.LoadInt32(&executed) != 1 {
		t.Fatalf("callback did not complete: executed=%d", atomic.LoadInt32(&executed))
	}
}
