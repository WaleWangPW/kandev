package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/kandev/kandev/internal/physicaldelete"
)

func ensureDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o700)
}

func ensureAnchor(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte("anchor"), 0o600)
}

// rollbackAdmissionSpy records the (action, authority) pairs the central
// admission service is asked to execute. It simulates the production
// Execute flow without touching the filesystem so the deferred rollback
// guard can be asserted directly.
type rollbackAdmissionSpy struct {
	executed atomic.Int32
	last     atomic.Value
}

func (s *rollbackAdmissionSpy) BeginProvisional(ctx context.Context, req physicaldelete.CreateRequest) (physicaldelete.ProvisionalLease, error) {
	return physicaldelete.ProvisionalLease{}, errors.New("rollback spy cannot begin provisional")
}

func (s *rollbackAdmissionSpy) Execute(ctx context.Context, req physicaldelete.Request) (physicaldelete.Receipt, error) {
	s.executed.Add(1)
	s.last.Store(req)
	return physicaldelete.Receipt{}, nil
}

// TestRollbackProvisionOnExit_RoutesLeaseToCentralAdmission pins the
// deferred guard added to (Manager).createInTaskDir. The legacy guard
// merely closed the lease, which dropped the reservation but did not
// drive the central admission rollback path. After a persist failure
// the lease is bound (the worktree is on disk) and the rollback
// request must be forwarded to the admission service so the orphan
// worktree can be removed.
//
// The helper is package-private so we drive it directly: a bound
// lease must dispatch Execute at least once; a not-yet-bound lease
// must not.
func TestRollbackProvisionOnExit_RoutesLeaseToCentralAdmission(t *testing.T) {
	ctx := context.Background()
	spy := &rollbackAdmissionSpy{}
	commonDir := t.TempDir()
	canonicalPath := filepath.Join(t.TempDir(), "provisional-target")

	// Bound lease: drive a real physicaldelete.Service through the
	// minimum path that lands the lease in the bound state so the
	// rollback guard can observe it. The path must exist before Bind
	// captures the anchor, but it must not exist before BeginProvisional
	// reserves the inventory slot — so create the parent directory only
	// up front, then run BeginProvisional, then materialize the target,
	// then Bind.
	if err := ensureDir(canonicalPath); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}
	service, err := physicaldelete.New(physicaldelete.Config{
		Inventory: physicaldelete.InventorySourceFunc(func(context.Context) (physicaldelete.Inventory, error) {
			return physicaldelete.Inventory{Complete: true}, nil
		}),
	})
	if err != nil {
		t.Fatalf("physicaldelete.New: %v", err)
	}
	begin, err := service.BeginProvisional(ctx, physicaldelete.CreateRequest{
		Authority: physicaldelete.AuthorityWorktree,
		Identity: physicaldelete.ProvisionalIdentity{
			TaskOwnerID:         "task-bind",
			SessionOwnerID:      "session-bind",
			CreationOperationID: "op-bind",
			ManagedRootID:       "task-dir-bind",
			CanonicalPath:       canonicalPath,
			CommonDir:           commonDir,
		},
		Resource: physicaldelete.Resource{
			Kind:      physicaldelete.ResourceKindProvisional,
			ID:        canonicalPath,
			Path:      canonicalPath,
			RootPath:  canonicalPath,
			CommonDir: commonDir,
		},
	})
	if err != nil {
		t.Fatalf("BeginProvisional: %v", err)
	}
	if err := ensureAnchor(canonicalPath); err != nil {
		t.Fatalf("ensureAnchor: %v", err)
	}
	if err := begin.Bind(ctx); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	rollbackProvisionOnExit(ctx, newTestLogger(), spy, &begin, canonicalPath, "test")
	if spy.executed.Load() == 0 {
		t.Fatal("expected Execute call for bound lease, got none")
	}

	// Reservation-only lease: rollback guard must not dispatch a
	// spurious Execute when the worktree was never materialized.
	spy2 := &rollbackAdmissionSpy{}
	reservePath := filepath.Join(t.TempDir(), "reserve-target")
	begin2, err := service.BeginProvisional(ctx, physicaldelete.CreateRequest{
		Authority: physicaldelete.AuthorityWorktree,
		Identity: physicaldelete.ProvisionalIdentity{
			TaskOwnerID:         "task-reserve",
			SessionOwnerID:      "session-reserve",
			CreationOperationID: "op-reserve",
			ManagedRootID:       "task-dir-reserve",
			CanonicalPath:       reservePath,
			CommonDir:           commonDir,
		},
		Resource: physicaldelete.Resource{
			Kind:      physicaldelete.ResourceKindProvisional,
			ID:        reservePath,
			Path:      reservePath,
			RootPath:  reservePath,
			CommonDir: commonDir,
		},
	})
	if err != nil {
		t.Fatalf("BeginProvisional reserve: %v", err)
	}
	rollbackProvisionOnExit(ctx, newTestLogger(), spy2, &begin2, reservePath, "test-reserve")
	if spy2.executed.Load() != 0 {
		t.Fatalf("expected no Execute call for reserved-only lease, got %d", spy2.executed.Load())
	}
}
