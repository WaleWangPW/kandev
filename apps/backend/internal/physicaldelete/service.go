package physicaldelete

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Service struct {
	inventory InventorySource
	locks     *LockRegistry
	leases    *provisionalRegistry
	root      RootPolicy
	now       func() time.Time
	fs        filesystemExecutor
	git       gitExecutor
}

func New(config Config) (*Service, error) {
	if config.Inventory == nil {
		return nil, fmt.Errorf("%w: inventory source is required", ErrInventoryIncomplete)
	}
	locks := config.Locks
	if locks == nil {
		locks = NewLockRegistry()
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		inventory: config.Inventory,
		locks:     locks,
		leases:    newProvisionalRegistry(),
		root:      rootPolicy(config.RootPolicy),
		now:       now,
	}, nil
}

func NewAdmission(config Config) (Admission, error) { return New(config) }

func (s *Service) Execute(ctx context.Context, request Request) (Receipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	receipt := Receipt{
		Decision:     DecisionDeny,
		Action:       request.Action,
		ResourceKind: request.Resource.Kind,
		ResourceID:   request.Resource.ID,
		At:           s.now(),
	}
	normalized, spec, err := normalizeRequest(request)
	if err != nil {
		return withReason(receipt, reasonForError(err)), err
	}
	receipt.Action = normalized.Action
	receipt.ResourceKind = normalized.Resource.Kind
	receipt.ResourceID = normalized.Resource.ID
	receipt.Executor = spec.executor
	paths, commonDirs := requestLockPaths(normalized)
	release, err := s.locks.Acquire(ctx, paths, commonDirs)
	if err != nil {
		return withReason(receipt, DenialLockUnavailable), errors.Join(ErrLockUnavailable, err)
	}
	defer release()
	receipt.Locks = lockIdentities(normalized)
	snapshot, err := s.loadInventory(ctx)
	if err != nil {
		return withReason(receipt, DenialInventoryIncomplete), err
	}
	receipt.InventoryDigest = snapshot.digest
	if err := verifyAnchors(normalized); err != nil {
		return withReason(receipt, reasonForError(err)), err
	}
	if normalized.Action == ActionProvisionalRollback {
		if err := s.verifyProvisional(normalized); err != nil {
			return withReason(receipt, reasonForError(err)), err
		}
	}
	if s.root.protects(normalized.Resource) {
		return withReason(receipt, DenialProtected), ErrProtectedResource
	}
	for _, child := range normalized.Children {
		if s.root.protects(child) {
			return withReason(receipt, DenialProtected), ErrProtectedResource
		}
	}
	if normalized.Action != ActionProvisionalRollback && inventoryProtects(normalized, snapshot.paths, snapshot.commonDirs) {
		return withReason(receipt, DenialProtected), ErrProtectedResource
	}
	return s.executeUnavailable(ctx, normalized, receipt)
}

func rootPolicy(policy *RootPolicy) RootPolicy {
	if policy == nil {
		return RootPolicy{}
	}
	return *policy
}

func (s *Service) loadInventory(ctx context.Context) (inventorySnapshot, error) {
	inventory, err := s.inventory.Load(ctx)
	if err != nil {
		if errors.Is(err, ErrInventoryIncomplete) {
			return inventorySnapshot{}, err
		}
		return inventorySnapshot{}, errors.Join(ErrInventoryIncomplete, err)
	}
	provisional, err := s.leases.protections()
	if err != nil {
		return inventorySnapshot{}, err
	}
	inventory.ProvisionalLeases = append(inventory.ProvisionalLeases, provisional...)
	snapshot, err := inventory.validate()
	if err != nil {
		return inventorySnapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) executeUnavailable(ctx context.Context, request Request, receipt Receipt) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return withReason(receipt, DenialLockUnavailable), err
	}
	switch actionExecutor(request.Action) {
	case ExecutorFilesystem:
		return s.fs.execute(ctx, request, receipt)
	case ExecutorGit:
		return s.git.execute(ctx, request, receipt)
	default:
		return withReason(receipt, DenialInvalidRequest), fmt.Errorf("%w: unknown executor", ErrInvalidRequest)
	}
}

func requestLockPaths(request Request) ([]string, []string) {
	paths := []string{request.Resource.Path}
	common := []string{}
	if request.Resource.RootPath != "" {
		paths = append(paths, request.Resource.RootPath)
	}
	if request.Resource.CommonDir != "" {
		common = append(common, request.Resource.CommonDir)
	}
	for _, child := range request.Children {
		paths = append(paths, child.Path)
		if child.RootPath != "" {
			paths = append(paths, child.RootPath)
		}
		if child.CommonDir != "" {
			common = append(common, child.CommonDir)
		}
	}
	return paths, common
}

func lockIdentities(request Request) []LockIdentity {
	locks := []LockIdentity{lockIdentity(request.Resource.Path, request.Resource.CommonDir)}
	for _, child := range request.Children {
		locks = append(locks, lockIdentity(child.Path, child.CommonDir))
	}
	return locks
}

func inventoryProtects(request Request, protected, protectedCommonDirs []string) bool {
	if resourcesOverlap(request.Resource, protected) || commonDirProtected(request.Resource, protectedCommonDirs) {
		return true
	}
	for _, child := range request.Children {
		if resourcesOverlap(child, protected) || commonDirProtected(child, protectedCommonDirs) {
			return true
		}
	}
	return false
}

func commonDirProtected(resource Resource, protected []string) bool {
	if resource.CommonDir == "" {
		return false
	}
	for _, commonDir := range protected {
		if resource.CommonDir == commonDir {
			return true
		}
	}
	return false
}

func withReason(receipt Receipt, reason DenialReason) Receipt {
	receipt.Reason = reason
	return receipt
}

func reasonForError(err error) DenialReason {
	switch {
	case errors.Is(err, ErrInventoryIncomplete), errors.Is(err, ErrUnknownInventory):
		return DenialInventoryIncomplete
	case errors.Is(err, ErrAnchorMismatch), errors.Is(err, ErrSymlinkPath), errors.Is(err, ErrProvisionalLease):
		return DenialAnchorMismatch
	case errors.Is(err, ErrProtectedResource):
		return DenialProtected
	case errors.Is(err, ErrLockUnavailable):
		return DenialLockUnavailable
	default:
		return DenialInvalidRequest
	}
}

var _ Admission = (*Service)(nil)
