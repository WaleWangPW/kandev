// Package physicaldelete centralizes the final admission gate for mutations
// beneath Kandev-managed roots. This release deliberately keeps every
// managed-root executor unavailable and fail-closed.
package physicaldelete

import (
	"context"
	"errors"
	"os"
	"time"
)

var (
	ErrInvalidRequest          = errors.New("physicaldelete: invalid request")
	ErrInventoryIncomplete     = errors.New("physicaldelete: inventory is incomplete")
	ErrUnknownInventory        = errors.New("physicaldelete: inventory contains an unknown row")
	ErrAnchorMismatch          = errors.New("physicaldelete: resource anchor does not match")
	ErrProtectedResource       = errors.New("physicaldelete: resource is protected")
	ErrExecutorUnavailable     = errors.New("physicaldelete: executor is unavailable")
	ErrSymlinkPath             = errors.New("physicaldelete: managed path contains a symlink")
	ErrLockUnavailable         = errors.New("physicaldelete: lock acquisition failed")
	ErrProvisionalLease        = errors.New("physicaldelete: provisional lease is invalid")
	ErrProvisionalTarget       = errors.New("physicaldelete: provisional target already exists")
	ErrProvisionalLeaseUsed    = errors.New("physicaldelete: provisional lease is already bound")
	ErrProvisionalTargetExists = ErrProvisionalTarget
	// ErrReleaseNotAdmitted is returned when the absent-target release cannot
	// prove that the targeted retained anchor exists, the physical path is
	// absent from inventory, and the Git worktree registration is absent.
	ErrReleaseNotAdmitted = errors.New("physicaldelete: absent-target release was not admitted")
)

type Action string

const (
	ActionProvisionalRollback      Action = "provisional_rollback"
	ActionRegisteredWorktreeRemove Action = "registered_worktree_remove"
	ActionBranchDelete             Action = "branch_delete"
	ActionRecursiveRootRemove      Action = "recursive_root_remove"
	ActionQuarantine               Action = "quarantine"
	ActionRestore                  Action = "restore"
	ActionPurge                    Action = "purge"
	ActionParentRemove             Action = "parent_remove"
	// ActionReleaseAbsent is the metadata-only release admission for a
	// retained target whose physical path and Git worktree registration are
	// already absent. It produces no physical or Git mutation.
	ActionReleaseAbsent Action = "release_absent"

	ActionRegisteredWorktreeRemoval Action = ActionRegisteredWorktreeRemove
	ActionBranchRemoval             Action = ActionBranchDelete
	ActionRecursiveRemoval          Action = ActionRecursiveRootRemove
	ActionQuarantineRoot            Action = ActionQuarantine
	ActionRestoreRoot               Action = ActionRestore
	ActionPurgeRoot                 Action = ActionPurge
	ActionParentRemoval             Action = ActionParentRemove
)

type ResourceKind string

const (
	ResourceKindRegisteredWorktree ResourceKind = "registered_worktree"
	ResourceKindBranch             ResourceKind = "branch"
	ResourceKindManagedRoot        ResourceKind = "managed_root"
	ResourceKindQuarantine         ResourceKind = "quarantine"
	ResourceKindParent             ResourceKind = "parent"
	ResourceKindProvisional        ResourceKind = "provisional"
	ResourceKindTaskEnvironment    ResourceKind = "task_environment"
	ResourceKindEnvironmentRepo    ResourceKind = "environment_repository"
	ResourceKindExecutorWorktree   ResourceKind = "executor_worktree"
	ResourceKindWorkspaceGroup     ResourceKind = "workspace_group"
	ResourceKindQuarantineEntry    ResourceKind = "quarantine_entry"
	ResourceKindCleanupAnchor      ResourceKind = "cleanup_anchor"
)

type Authority string

const (
	AuthorityLifecycle    Authority = "lifecycle"
	AuthorityWorktree     Authority = "worktree"
	AuthorityStorage      Authority = "storage"
	AuthorityOffice       Authority = "office"
	AuthorityHandoff      Authority = "handoff"
	AuthorityMaterializer Authority = "materializer"
	AuthorityFactoryReset Authority = "factory_reset"
	AuthorityAdmin        Authority = "admin"
)

type Executor string

const (
	ExecutorFilesystem Executor = "filesystem"
	ExecutorGit        Executor = "git"
	// ExecutorNone marks an admission that produces no physical or Git
	// mutation. The release admission routes through this executor so the
	// sealed absence proof in inventory is the only authoritative signal.
	ExecutorNone Executor = "none"
)

type Decision string

const (
	DecisionDeny   Decision = "deny"
	DecisionDenied Decision = DecisionDeny
)

type DenialReason string

const (
	DenialInvalidRequest      DenialReason = "invalid_request"
	DenialInventoryIncomplete DenialReason = "inventory_incomplete"
	DenialAnchorMismatch      DenialReason = "anchor_mismatch"
	DenialProtected           DenialReason = "protected_resource"
	DenialExecutorUnavailable DenialReason = "executor_unavailable"
	DenialLockUnavailable     DenialReason = "lock_unavailable"
	DenialReleaseNotAdmitted  DenialReason = "release_not_admitted"
)

// Anchor retains the exact Lstat identity observed before the final gate. The
// file-info value is intentionally private so callers cannot self-sign an
// identity without observing the filesystem through CaptureAnchor.
type Anchor struct {
	path string
	info os.FileInfo
}

// Resource is metadata only. It contains no executable function or fallback
// hook; the Service owns the action switch and the sealed executor boundary.
type Resource struct {
	Kind      ResourceKind
	ID        string
	Path      string
	RootPath  string
	CommonDir string
	Anchor    *Anchor
	LeaseID   string
}

// ProvisionalIdentity is the immutable admission identity for a provisional
// creation. Every field is required so a later bind cannot be detached from
// its task/session owner, creation operation, managed root, or canonical
// path/common-directory claim.
type ProvisionalIdentity struct {
	TaskOwnerID         string
	SessionOwnerID      string
	CreationOperationID string
	ManagedRootID       string
	CanonicalPath       string
	CommonDir           string
}

type Request struct {
	Action    Action
	Authority Authority
	Executor  Executor
	Force     bool
	Resource  Resource
	Children  []Resource
	Identity  ProvisionalIdentity

	lease *ProvisionalLease
}

type CreateRequest struct {
	Authority Authority
	Identity  ProvisionalIdentity
	Resource  Resource
}

type LockIdentity struct {
	PathKey      string
	CommonDirKey string
}

type Receipt struct {
	Decision        Decision
	Reason          DenialReason
	Action          Action
	ResourceKind    ResourceKind
	ResourceID      string
	Executor        Executor
	InventoryDigest string
	Locks           []LockIdentity
	Mutated         bool
	At              time.Time
}

func (r Receipt) Denied() bool { return r.Decision == DecisionDeny }

type Admission interface {
	BeginProvisional(context.Context, CreateRequest) (ProvisionalLease, error)
	Execute(context.Context, Request) (Receipt, error)
}

type InventorySource interface {
	Load(context.Context) (Inventory, error)
}

type InventorySourceFunc func(context.Context) (Inventory, error)

func (f InventorySourceFunc) Load(ctx context.Context) (Inventory, error) {
	if f == nil {
		return Inventory{}, ErrInventoryIncomplete
	}
	return f(ctx)
}

// WriterDBSnapshotReader is the narrow read-only boundary used by the
// composition layer. Its implementation must read all protection tables from
// one writer-DB consistent snapshot; a partial reader is rejected by
// Inventory.Validate.
type WriterDBSnapshotReader interface {
	LoadPhysicalDeleteInventory(context.Context) (Inventory, error)
}

type WriterDBInventorySource struct{ reader WriterDBSnapshotReader }

func NewWriterDBInventorySource(reader WriterDBSnapshotReader) *WriterDBInventorySource {
	return &WriterDBInventorySource{reader: reader}
}

func (s *WriterDBInventorySource) Load(ctx context.Context) (Inventory, error) {
	if s == nil || s.reader == nil {
		return Inventory{}, ErrInventoryIncomplete
	}
	return s.reader.LoadPhysicalDeleteInventory(ctx)
}

type Config struct {
	Inventory  InventorySource
	Locks      *LockRegistry
	RootPolicy *RootPolicy
	Now        func() time.Time
}
