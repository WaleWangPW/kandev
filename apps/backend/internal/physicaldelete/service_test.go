package physicaldelete

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func completeInventory() Inventory { return Inventory{Complete: true} }

func testService(t *testing.T, inventory Inventory) *Service {
	t.Helper()
	service, err := New(Config{
		Inventory: InventorySourceFunc(func(context.Context) (Inventory, error) {
			return inventory, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}

func testRequest(t *testing.T, action Action, path string, anchor *Anchor) Request {
	t.Helper()
	return Request{
		Action:    action,
		Authority: AuthorityLifecycle,
		Resource: Resource{
			Kind:   ResourceKindManagedRoot,
			ID:     "resource-1",
			Path:   path,
			Anchor: anchor,
		},
	}
}

func provisionalCreateRequest(root, target, commonDir, resourceID, operationID string) CreateRequest {
	return CreateRequest{
		Authority: AuthorityMaterializer,
		Identity: ProvisionalIdentity{
			TaskOwnerID:         "task-owner-1",
			SessionOwnerID:      "session-owner-1",
			CreationOperationID: operationID,
			ManagedRootID:       "managed-root-1",
			CanonicalPath:       target,
			CommonDir:           commonDir,
		},
		Resource: Resource{
			Kind:      ResourceKindProvisional,
			ID:        resourceID,
			Path:      target,
			RootPath:  root,
			CommonDir: commonDir,
		},
	}
}

type doneProbeContext struct {
	done     chan struct{}
	observed chan struct{}
	observe  sync.Once
	cancel   sync.Once
}

func newDoneProbeContext() *doneProbeContext {
	return &doneProbeContext{done: make(chan struct{}), observed: make(chan struct{})}
}

func (c *doneProbeContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *doneProbeContext) Done() <-chan struct{} {
	c.observe.Do(func() { close(c.observed) })
	return c.done
}

func (c *doneProbeContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func (c *doneProbeContext) Value(any) any { return nil }

func (c *doneProbeContext) Cancel() { c.cancel.Do(func() { close(c.done) }) }

func scopedRequest(action Action, root, target, commonDir string) Request {
	return Request{
		Action:    action,
		Authority: AuthorityLifecycle,
		Resource: Resource{
			Kind:      ResourceKindManagedRoot,
			ID:        "ordinary-1",
			Path:      target,
			RootPath:  root,
			CommonDir: commonDir,
		},
	}
}

func TestAdmissionDeniesEveryManagedRootExecutor(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	anchor, err := CaptureAnchor(target)
	if err != nil {
		t.Fatalf("CaptureAnchor: %v", err)
	}
	service := testService(t, completeInventory())
	actions := []Action{
		ActionRegisteredWorktreeRemove,
		ActionBranchDelete,
		ActionRecursiveRootRemove,
		ActionQuarantine,
		ActionRestore,
		ActionPurge,
		ActionParentRemove,
	}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			receipt, err := service.Execute(context.Background(), testRequest(t, action, target, &anchor))
			if !errors.Is(err, ErrExecutorUnavailable) {
				t.Fatalf("Execute error = %v, want ErrExecutorUnavailable", err)
			}
			if receipt.Decision != DecisionDeny || receipt.Reason != DenialExecutorUnavailable {
				t.Fatalf("receipt = %+v, want unavailable denial", receipt)
			}
			if receipt.Mutated {
				t.Fatal("denied executor reported a mutation")
			}
			if _, err := os.Lstat(target); err != nil {
				t.Fatalf("target changed after denied action: %v", err)
			}
		})
	}
}

func TestAdmissionFailsClosedOnIncompleteOrMalformedInventory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	anchor, err := CaptureAnchor(target)
	if err != nil {
		t.Fatalf("CaptureAnchor: %v", err)
	}
	tests := []struct {
		name      string
		inventory Inventory
		want      error
	}{
		{name: "incomplete", inventory: Inventory{}, want: ErrInventoryIncomplete},
		{
			name: "unknown row state",
			inventory: Inventory{
				Complete: true,
				ActiveWorktrees: []ProtectedResource{{
					ID: "wt-1", Kind: ResourceKindRegisteredWorktree, Path: filepath.Join(root, "live"), State: "future_state",
				}},
			},
			want: ErrInventoryIncomplete,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := testService(t, tt.inventory)
			receipt, err := service.Execute(context.Background(), testRequest(t, ActionRecursiveRootRemove, target, &anchor))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Execute error = %v, want %v", err, tt.want)
			}
			if receipt.Mutated || receipt.Decision != DecisionDeny {
				t.Fatalf("receipt = %+v, want zero-mutation denial", receipt)
			}
			if _, err := os.Lstat(target); err != nil {
				t.Fatalf("target changed after inventory denial: %v", err)
			}
		})
	}
}

func TestAdmissionRejectsProtectedOverlapAndInvalidAnchor(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	child := filepath.Join(target, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	anchor, err := CaptureAnchor(target)
	if err != nil {
		t.Fatalf("CaptureAnchor: %v", err)
	}
	service := testService(t, Inventory{
		Complete: true,
		ActiveWorktrees: []ProtectedResource{{
			ID: "live-1", Kind: ResourceKindRegisteredWorktree, Path: filepath.Join(root, "live"), State: "active",
		}},
	})
	protected, err := service.Execute(context.Background(), testRequest(t, ActionRecursiveRootRemove, filepath.Join(root, "live", "nested"), nil))
	if !errors.Is(err, ErrProtectedResource) || protected.Reason != DenialProtected {
		t.Fatalf("protected overlap = (%+v, %v), want protected denial", protected, err)
	}
	if err := os.Remove(filepath.Join(target, "child")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	receipt, err := service.Execute(context.Background(), testRequest(t, ActionRecursiveRootRemove, target, &anchor))
	if !errors.Is(err, ErrAnchorMismatch) || receipt.Reason != DenialAnchorMismatch {
		t.Fatalf("changed anchor = (%+v, %v), want anchor denial", receipt, err)
	}
}

func TestAdmissionAppliesManagedRootPolicyBeforeDisabledExecutor(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	target := filepath.Join(managed, "child")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	anchor, err := CaptureAnchor(target)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewRootPolicy([]string{managed})
	if err != nil {
		t.Fatalf("NewRootPolicy: %v", err)
	}
	service, err := New(Config{
		Inventory:  InventorySourceFunc(func(context.Context) (Inventory, error) { return completeInventory(), nil }),
		RootPolicy: &policy,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	receipt, err := service.Execute(context.Background(), testRequest(t, ActionRecursiveRootRemove, target, &anchor))
	if !errors.Is(err, ErrProtectedResource) || receipt.Reason != DenialProtected {
		t.Fatalf("managed-root policy = (%+v, %v), want protected denial", receipt, err)
	}
}

func TestCaptureAnchorRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := CaptureAnchor(link); !errors.Is(err, ErrSymlinkPath) {
		t.Fatalf("CaptureAnchor symlink error = %v, want ErrSymlinkPath", err)
	}
}

func TestCanonicalPathLocksOverlapAncestorsAndAliases(t *testing.T) {
	root := t.TempDir()
	registry := NewLockRegistry()
	ancestor := filepath.Join(root, "a")
	if err := os.Mkdir(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := registry.Acquire(context.Background(), []string{ancestor}, nil)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first()
	child := filepath.Join(ancestor, "child")
	started := make(chan struct{})
	secondDone := make(chan struct{})
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	var secondRelease func()
	var secondErr error
	go func() {
		close(started)
		secondRelease, secondErr = registry.Acquire(attemptCtx, []string{filepath.Join(root, "a", ".", "child")}, nil)
		close(secondDone)
	}()
	<-started
	cancelAttempt()
	<-secondDone
	if secondErr == nil {
		secondRelease()
		t.Fatal("descendant lock acquired while ancestor was held")
	}
	first()
	secondRelease, secondErr = registry.Acquire(context.Background(), []string{child}, nil)
	if secondErr != nil {
		t.Fatalf("second acquire after release: %v", secondErr)
	}
	secondRelease()

	alias := filepath.Join(root, "a", "..", "a")
	third, err := registry.Acquire(context.Background(), []string{child}, nil)
	if err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	defer third()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.Acquire(ctx, []string{alias}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled alias acquire error = %v, want context.Canceled", err)
	}
	commonRelease, err := registry.Acquire(context.Background(), []string{filepath.Join(root, "other")}, []string{ancestor})
	if err != nil {
		t.Fatalf("common-dir acquire: %v", err)
	}
	defer commonRelease()
	commonCtx, commonCancel := context.WithCancel(context.Background())
	commonCancel()
	if _, err := registry.Acquire(commonCtx, []string{filepath.Join(root, "other-2")}, []string{filepath.Join(root, "a", ".")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled common-dir alias acquire error = %v, want context.Canceled", err)
	}
}

func TestProvisionalExecuteLockBlocksBegin(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	commonDir := filepath.Join(root, "common")
	inventoryEntered := make(chan struct{})
	releaseInventory := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseInventory) }) }
	defer release()
	service, err := New(Config{
		Inventory: InventorySourceFunc(func(context.Context) (Inventory, error) {
			close(inventoryEntered)
			<-releaseInventory
			return completeInventory(), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	executeDone := make(chan struct{})
	go func() {
		_, _ = service.Execute(context.Background(), scopedRequest(ActionRecursiveRootRemove, root, target, commonDir))
		close(executeDone)
	}()
	<-inventoryEntered

	probe := newDoneProbeContext()
	beginResult := make(chan struct {
		lease ProvisionalLease
		err   error
	}, 1)
	go func() {
		lease, err := service.BeginProvisional(probe, provisionalCreateRequest(root, target, commonDir, "p-lock", "create-lock"))
		beginResult <- struct {
			lease ProvisionalLease
			err   error
		}{lease: lease, err: err}
	}()
	select {
	case result := <-beginResult:
		t.Fatalf("BeginProvisional crossed Execute lock = (%s, %v)", result.lease.ID(), result.err)
	case <-probe.observed:
		probe.Cancel()
	}
	result := <-beginResult
	if !errors.Is(result.err, ErrLockUnavailable) || !errors.Is(result.err, context.Canceled) {
		t.Fatalf("BeginProvisional while Execute holds lock = (%s, %v), want lock/cancellation denial", result.lease.ID(), result.err)
	}
	if result.lease.ID() != "" {
		t.Fatalf("BeginProvisional returned lease %q while Execute held the lock", result.lease.ID())
	}
	if got := provisionalLeaseCount(service); got != 0 {
		t.Fatalf("registry lease count = %d, want zero after lock denial", got)
	}
	release()
	<-executeDone
}

func TestProvisionalReserveThenExecuteSnapshotSeesLease(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	commonDir := filepath.Join(root, "common")
	inventoryEntered := make(chan struct{})
	releaseInventory := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseInventory) }) }
	service, err := New(Config{
		Inventory: InventorySourceFunc(func(context.Context) (Inventory, error) {
			close(inventoryEntered)
			<-releaseInventory
			return completeInventory(), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer release()
	lease, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, target, commonDir, "p-snapshot", "create-snapshot"))
	if err != nil {
		t.Fatalf("BeginProvisional: %v", err)
	}
	if lease.ID() == "" {
		t.Fatal("BeginProvisional returned an empty lease")
	}
	executeResult := make(chan struct {
		receipt Receipt
		err     error
	}, 1)
	go func() {
		receipt, err := service.Execute(context.Background(), scopedRequest(ActionRecursiveRootRemove, root, target, commonDir))
		executeResult <- struct {
			receipt Receipt
			err     error
		}{receipt: receipt, err: err}
	}()
	<-inventoryEntered
	release()
	result := <-executeResult
	if !errors.Is(result.err, ErrProtectedResource) || result.receipt.Reason != DenialProtected {
		t.Fatalf("Execute after provisional reserve = (%+v, %v), want protected denial", result.receipt, result.err)
	}
	if result.receipt.InventoryDigest == "" || result.receipt.Mutated {
		t.Fatalf("receipt = %+v, want lease-bearing inventory and no mutation", result.receipt)
	}
}

func TestProvisionalCancelledWaiterLeavesRegistryUnchanged(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	commonDir := filepath.Join(root, "common")
	service := testService(t, completeInventory())
	release, err := service.locks.Acquire(context.Background(), []string{target}, []string{commonDir})
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer release()
	probe := newDoneProbeContext()
	beginResult := make(chan struct {
		lease ProvisionalLease
		err   error
	}, 1)
	go func() {
		lease, err := service.BeginProvisional(probe, provisionalCreateRequest(root, target, commonDir, "p-cancel", "create-cancel"))
		beginResult <- struct {
			lease ProvisionalLease
			err   error
		}{lease: lease, err: err}
	}()
	select {
	case result := <-beginResult:
		t.Fatalf("cancelled waiter crossed held lock = (%s, %v)", result.lease.ID(), result.err)
	case <-probe.observed:
		probe.Cancel()
	}
	result := <-beginResult
	if !errors.Is(result.err, ErrLockUnavailable) || !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancelled BeginProvisional = (%s, %v), want lock/cancellation denial", result.lease.ID(), result.err)
	}
	if result.lease.ID() != "" {
		t.Fatalf("cancelled waiter returned lease %q", result.lease.ID())
	}
	if got := provisionalLeaseCount(service); got != 0 {
		t.Fatalf("registry lease count = %d, want zero after cancellation", got)
	}
}

func provisionalLeaseCount(service *Service) int {
	release := service.leases.lock()
	defer release()
	return len(service.leases.leases)
}

func TestProvisionalRollbackBindsIdentityButExecutorCannotMutate(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	service := testService(t, completeInventory())
	lease, err := service.BeginProvisional(context.Background(), CreateRequest{
		Authority: AuthorityMaterializer,
		Identity: ProvisionalIdentity{
			TaskOwnerID:         "task-owner-1",
			SessionOwnerID:      "session-owner-1",
			CreationOperationID: "create-p-1",
			ManagedRootID:       "managed-root-1",
			CanonicalPath:       target,
			CommonDir:           filepath.Join(root, "common"),
		},
		Resource: Resource{
			Kind:      ResourceKindProvisional,
			ID:        "p-1",
			Path:      target,
			RootPath:  root,
			CommonDir: filepath.Join(root, "common"),
		},
	})
	if err != nil {
		t.Fatalf("BeginProvisional: %v", err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := lease.Bind(context.Background()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	request, err := lease.RollbackRequest()
	if err != nil {
		t.Fatalf("RollbackRequest: %v", err)
	}
	receipt, err := service.Execute(context.Background(), request)
	if !errors.Is(err, ErrExecutorUnavailable) || receipt.Reason != DenialExecutorUnavailable {
		t.Fatalf("rollback = (%+v, %v), want unavailable denial", receipt, err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("provisional target was mutated: %v", err)
	}
}

func TestProvisionalDualLeaseRejectsSequentialCreator(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	commonDir := filepath.Join(root, "common")
	service := testService(t, completeInventory())
	first, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, target, commonDir, "p-1", "create-p-1"))
	if err != nil {
		t.Fatalf("first BeginProvisional: %v", err)
	}
	second, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, target, commonDir, "p-2", "create-p-2"))
	if !errors.Is(err, ErrProvisionalLease) {
		t.Fatalf("second BeginProvisional error = %v, want ErrProvisionalLease", err)
	}
	if second.ID() != "" {
		t.Fatalf("second creator received lease %q", second.ID())
	}
	if first.ID() == "" {
		t.Fatal("first creator did not receive a lease")
	}
}

func TestProvisionalDualLeaseRejectsPathOrCommonClaim(t *testing.T) {
	for _, test := range []struct {
		name string
		path func(string) (string, string, string, string)
	}{
		{
			name: "same canonical path",
			path: func(root string) (string, string, string, string) {
				target := filepath.Join(root, "same-target")
				return target, filepath.Join(root, "common-1"), target, filepath.Join(root, "common-2")
			},
		},
		{
			name: "same common directory",
			path: func(root string) (string, string, string, string) {
				commonDir := filepath.Join(root, "same-common")
				return filepath.Join(root, "target-1"), commonDir, filepath.Join(root, "target-2"), commonDir
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			firstTarget, firstCommon, secondTarget, secondCommon := test.path(root)
			service := testService(t, completeInventory())
			if _, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, firstTarget, firstCommon, "p-1", "create-p-1")); err != nil {
				t.Fatalf("first BeginProvisional: %v", err)
			}
			second, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, secondTarget, secondCommon, "p-2", "create-p-2"))
			if !errors.Is(err, ErrProvisionalLease) {
				t.Fatalf("second BeginProvisional error = %v, want ErrProvisionalLease", err)
			}
			if second.ID() != "" {
				t.Fatalf("second creator received lease %q", second.ID())
			}
		})
	}
}

func TestProvisionalConcurrentCreatorYieldsOneLease(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	commonDir := filepath.Join(root, "common")
	service := testService(t, completeInventory())
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	type result struct {
		lease ProvisionalLease
		err   error
	}
	results := make(chan result, 2)
	for i := 1; i <= 2; i++ {
		operationID := "create-concurrent-" + string(rune('0'+i))
		go func(operationID string) {
			ready <- struct{}{}
			<-start
			lease, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, target, commonDir, operationID, operationID))
			results <- result{lease: lease, err: err}
		}(operationID)
	}
	<-ready
	<-ready
	close(start)
	var successes, failures int
	for range 2 {
		outcome := <-results
		if outcome.err == nil {
			successes++
			if outcome.lease.ID() == "" {
				t.Fatal("successful concurrent creator returned an empty lease")
			}
			continue
		}
		failures++
		if !errors.Is(outcome.err, ErrProvisionalLease) {
			t.Fatalf("concurrent creator error = %v, want ErrProvisionalLease", outcome.err)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent creators = successes %d failures %d, want one each", successes, failures)
	}
}

func TestProvisionalIdentityDriftFailsClosed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	commonDir := filepath.Join(root, "common")
	service := testService(t, completeInventory())
	lease, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, target, commonDir, "p-1", "create-p-1"))
	if err != nil {
		t.Fatalf("BeginProvisional: %v", err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := lease.Bind(context.Background()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	base, err := lease.RollbackRequest()
	if err != nil {
		t.Fatalf("RollbackRequest: %v", err)
	}
	mutations := map[string]func(*Request){
		"task owner":         func(request *Request) { request.Identity.TaskOwnerID = "other-task-owner" },
		"session owner":      func(request *Request) { request.Identity.SessionOwnerID = "other-session-owner" },
		"creation operation": func(request *Request) { request.Identity.CreationOperationID = "other-operation" },
		"managed root":       func(request *Request) { request.Identity.ManagedRootID = "other-managed-root" },
		"canonical path":     func(request *Request) { request.Identity.CanonicalPath = filepath.Join(root, "other") },
		"common directory":   func(request *Request) { request.Identity.CommonDir = filepath.Join(root, "other-common") },
		"resource root path": func(request *Request) { request.Resource.RootPath = filepath.Join(root, "other-root") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			receipt, err := service.Execute(context.Background(), request)
			if !errors.Is(err, ErrProvisionalLease) {
				t.Fatalf("Execute error = %v, want ErrProvisionalLease", err)
			}
			if receipt.Mutated || receipt.Reason != DenialAnchorMismatch {
				t.Fatalf("receipt = %+v, want fail-closed anchor denial", receipt)
			}
		})
	}
}

func TestProvisionalUnknownRegistryStateFailsClosed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	service := testService(t, completeInventory())
	lease, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, target, filepath.Join(root, "common"), "p-1", "create-p-1"))
	if err != nil {
		t.Fatalf("BeginProvisional: %v", err)
	}
	release := service.leases.lock()
	service.leases.leases[lease.ID()].status = provisionalLeaseStatus("future")
	release()
	receipt, err := service.Execute(context.Background(), testRequest(t, ActionRecursiveRootRemove, target, nil))
	if !errors.Is(err, ErrInventoryIncomplete) || receipt.Reason != DenialInventoryIncomplete {
		t.Fatalf("unknown provisional state = (%+v, %v), want inventory denial", receipt, err)
	}
}

func TestProvisionalMalformedClaimFailsClosed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "provisional")
	commonDir := filepath.Join(root, "common")
	service := testService(t, completeInventory())
	lease, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, target, commonDir, "p-1", "create-p-1"))
	if err != nil {
		t.Fatalf("BeginProvisional: %v", err)
	}
	release := service.leases.lock()
	delete(service.leases.pathClaims, service.leases.leases[lease.ID()].identity.CanonicalPath)
	release()
	receipt, err := service.Execute(context.Background(), testRequest(t, ActionRecursiveRootRemove, target, nil))
	if !errors.Is(err, ErrInventoryIncomplete) || receipt.Reason != DenialInventoryIncomplete {
		t.Fatalf("malformed provisional claim = (%+v, %v), want inventory denial", receipt, err)
	}
}

func TestProvisionalBindCannotCaptureAnotherLeaseTarget(t *testing.T) {
	root := t.TempDir()
	firstTarget := filepath.Join(root, "first")
	secondTarget := filepath.Join(root, "second")
	service := testService(t, completeInventory())
	first, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, firstTarget, filepath.Join(root, "common-first"), "p-1", "create-p-1"))
	if err != nil {
		t.Fatalf("first BeginProvisional: %v", err)
	}
	second, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, secondTarget, filepath.Join(root, "common-second"), "p-2", "create-p-2"))
	if err != nil {
		t.Fatalf("second BeginProvisional: %v", err)
	}
	if err := os.Mkdir(firstTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	forged := second
	forged.resource.Path = firstTarget
	if err := forged.Bind(context.Background()); !errors.Is(err, ErrProvisionalLease) {
		t.Fatalf("forged Bind error = %v, want ErrProvisionalLease", err)
	}
	if err := first.Bind(context.Background()); err != nil {
		t.Fatalf("first Bind after rejected capture: %v", err)
	}
}

func TestProvisionalLeaseProtectsOrdinaryAdmission(t *testing.T) {
	for _, bind := range []bool{false, true} {
		name := "reserved"
		if bind {
			name = "bound"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "provisional")
			commonDir := filepath.Join(root, "common")
			service := testService(t, completeInventory())
			lease, err := service.BeginProvisional(context.Background(), provisionalCreateRequest(root, target, commonDir, "p-1", "create-p-1"))
			if err != nil {
				t.Fatalf("BeginProvisional: %v", err)
			}
			var anchor *Anchor
			if bind {
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				captured, err := CaptureAnchor(target)
				if err != nil {
					t.Fatalf("CaptureAnchor: %v", err)
				}
				anchor = &captured
				if err := lease.Bind(context.Background()); err != nil {
					t.Fatalf("Bind: %v", err)
				}
			}
			request := testRequest(t, ActionRecursiveRootRemove, target, anchor)
			receipt, err := service.Execute(context.Background(), request)
			if !errors.Is(err, ErrProtectedResource) || receipt.Reason != DenialProtected {
				t.Fatalf("ordinary admission = (%+v, %v), want protected denial", receipt, err)
			}
			if receipt.InventoryDigest == "" || receipt.Mutated {
				t.Fatalf("receipt = %+v, want authoritative protected inventory and no mutation", receipt)
			}
		})
	}
}

func TestInventoryAnchorRequiresTask05StrictSnapshotValidation(t *testing.T) {
	service := testService(t, Inventory{Complete: true, CleanupAnchors: []CleanupAnchor{{
		ID: "cleanup-1", State: "retained", SnapshotVersion: 2,
	}}})
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	anchor, err := CaptureAnchor(target)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.Execute(context.Background(), testRequest(t, ActionRecursiveRootRemove, target, &anchor))
	if !errors.Is(err, ErrInventoryIncomplete) || receipt.Reason != DenialInventoryIncomplete {
		t.Fatalf("invalid cleanup anchor = (%+v, %v), want inventory denial", receipt, err)
	}
}
