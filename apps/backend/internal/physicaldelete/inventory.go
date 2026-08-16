package physicaldelete

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type ProtectedResource struct {
	ID        string
	Kind      ResourceKind
	State     string
	Path      string
	RootPath  string
	CommonDir string
}

type CleanupAnchor struct {
	ID              string
	OperationID     string
	State           string
	Path            string
	RepositoryID    string
	Branch          string
	HeadOID         string
	SnapshotDigest  string
	SnapshotVersion int
	ResourceKind    string
	ResourceID      string
	TaskID          string
	ManagedRootKey  string
	AnchorRevision  int
	validated       bool
}

type ProvisionalProtection struct {
	LeaseID             string
	State               string
	TaskOwnerID         string
	SessionOwnerID      string
	CreationOperationID string
	ManagedRootID       string
	Path                string
	RootPath            string
	CommonDir           string
}

// Inventory is the complete protection view assembled from the writer DB.
// Each source is kept as a separate field so a future consumer cannot silently
// narrow the protection set to active worktrees alone.
type Inventory struct {
	Complete                bool
	ActiveWorktrees         []ProtectedResource
	TaskEnvironments        []ProtectedResource
	EnvironmentRepositories []ProtectedResource
	ExecutorWorktrees       []ProtectedResource
	WorkspaceGroups         []ProtectedResource
	QuarantineEntries       []ProtectedResource
	CleanupAnchors          []CleanupAnchor
	ProvisionalLeases       []ProvisionalProtection
}

type inventorySnapshot struct {
	digest     string
	paths      []string
	commonDirs []string
	anchors    []CleanupAnchor
	inventory  Inventory
}

func (inventory Inventory) validate() (inventorySnapshot, error) {
	if !inventory.Complete {
		return inventorySnapshot{}, ErrInventoryIncomplete
	}
	seen := make(map[string]struct{})
	leasePaths := make(map[string]struct{})
	leaseCommonDirs := make(map[string]struct{})
	leaseOperations := make(map[string]struct{})
	paths := make([]string, 0)
	commonDirs := make([]string, 0)
	digest := sha256.New()
	groups := [][]ProtectedResource{
		inventory.ActiveWorktrees,
		inventory.TaskEnvironments,
		inventory.EnvironmentRepositories,
		inventory.ExecutorWorktrees,
		inventory.WorkspaceGroups,
		inventory.QuarantineEntries,
	}
	for _, group := range groups {
		for _, row := range group {
			if err := validateProtectedResource(row); err != nil {
				return inventorySnapshot{}, err
			}
			identity := string(row.Kind) + "\x00" + row.ID
			if _, ok := seen[identity]; ok {
				return inventorySnapshot{}, fmt.Errorf("%w: duplicate %s", ErrInventoryIncomplete, identity)
			}
			seen[identity] = struct{}{}
			paths = appendPath(paths, row.Path, row.RootPath)
			writeResourceDigest(digest, row)
		}
	}
	for _, lease := range inventory.ProvisionalLeases {
		if err := validateProvisionalProtection(lease); err != nil {
			return inventorySnapshot{}, err
		}
		identity := string(ResourceKindProvisional) + "\x00" + lease.LeaseID
		if _, ok := seen[identity]; ok {
			return inventorySnapshot{}, fmt.Errorf("%w: duplicate provisional lease %s", ErrInventoryIncomplete, lease.LeaseID)
		}
		seen[identity] = struct{}{}
		if _, ok := leasePaths[lease.Path]; ok {
			return inventorySnapshot{}, fmt.Errorf("%w: duplicate provisional path %s", ErrInventoryIncomplete, lease.Path)
		}
		if _, ok := leaseCommonDirs[lease.CommonDir]; ok {
			return inventorySnapshot{}, fmt.Errorf("%w: duplicate provisional common directory %s", ErrInventoryIncomplete, lease.CommonDir)
		}
		if _, ok := leaseOperations[lease.CreationOperationID]; ok {
			return inventorySnapshot{}, fmt.Errorf("%w: duplicate provisional operation %s", ErrInventoryIncomplete, lease.CreationOperationID)
		}
		leasePaths[lease.Path] = struct{}{}
		leaseCommonDirs[lease.CommonDir] = struct{}{}
		leaseOperations[lease.CreationOperationID] = struct{}{}
		paths = appendPath(paths, lease.Path, lease.RootPath)
		commonDirs = append(commonDirs, lease.CommonDir)
		digest.Write([]byte("provisional\x00" + lease.LeaseID + "\x00" + lease.State + "\x00" +
			lease.TaskOwnerID + "\x00" + lease.SessionOwnerID + "\x00" + lease.CreationOperationID +
			"\x00" + lease.ManagedRootID + "\x00" + lease.Path + "\x00" + lease.RootPath + "\x00" + lease.CommonDir + "\n"))
	}
	for _, anchor := range inventory.CleanupAnchors {
		if err := validateCleanupAnchor(anchor); err != nil {
			return inventorySnapshot{}, err
		}
		identity := string(ResourceKindCleanupAnchor) + "\x00" + anchor.ID
		if _, ok := seen[identity]; ok {
			return inventorySnapshot{}, fmt.Errorf("%w: duplicate cleanup anchor %s", ErrInventoryIncomplete, identity)
		}
		seen[identity] = struct{}{}
		digest.Write([]byte(identity + "\x00" + anchor.SnapshotDigest + "\n"))
		paths = append(paths, anchor.Path)
	}
	sum := digest.Sum(nil)
	return inventorySnapshot{
		digest:     hex.EncodeToString(sum),
		paths:      compactPaths(paths),
		commonDirs: compactPaths(commonDirs),
		anchors:    append([]CleanupAnchor(nil), inventory.CleanupAnchors...),
		inventory:  inventory,
	}, nil
}

func validateProvisionalProtection(lease ProvisionalProtection) error {
	for name, value := range map[string]string{
		"lease id":           lease.LeaseID,
		"task owner":         lease.TaskOwnerID,
		"session owner":      lease.SessionOwnerID,
		"creation operation": lease.CreationOperationID,
		"managed root":       lease.ManagedRootID,
		"canonical path":     lease.Path,
		"root path":          lease.RootPath,
		"common directory":   lease.CommonDir,
	} {
		if value == "" || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("%w: provisional %s is malformed", ErrInventoryIncomplete, name)
		}
	}
	if lease.State != string(provisionalLeaseReserved) && lease.State != string(provisionalLeaseBound) {
		return fmt.Errorf("%w: %w: unknown provisional state %q", ErrInventoryIncomplete, ErrUnknownInventory, lease.State)
	}
	path, err := canonicalLockPath(lease.Path)
	if err != nil || path != lease.Path {
		return fmt.Errorf("%w: provisional canonical path is not canonical", ErrInventoryIncomplete)
	}
	if err := ensureNoSymlinkPath(path); err != nil {
		return fmt.Errorf("%w: provisional path: %v", ErrInventoryIncomplete, err)
	}
	root, err := canonicalLockPath(lease.RootPath)
	if err != nil || root != lease.RootPath {
		return fmt.Errorf("%w: provisional root path is not canonical", ErrInventoryIncomplete)
	}
	if err := ensureNoSymlinkPath(root); err != nil {
		return fmt.Errorf("%w: provisional root: %v", ErrInventoryIncomplete, err)
	}
	common, err := canonicalLockPath(lease.CommonDir)
	if err != nil || common != lease.CommonDir {
		return fmt.Errorf("%w: provisional common directory is not canonical", ErrInventoryIncomplete)
	}
	if err := ensureNoSymlinkPath(common); err != nil {
		return fmt.Errorf("%w: provisional common directory: %v", ErrInventoryIncomplete, err)
	}
	return nil
}

func validateProtectedResource(row ProtectedResource) error {
	if row.ID == "" || strings.IndexByte(row.ID, 0) >= 0 {
		return fmt.Errorf("%w: protected row has invalid id", ErrInventoryIncomplete)
	}
	if !knownResourceKind(row.Kind) {
		return fmt.Errorf("%w: %w: protected row has unknown kind %q", ErrInventoryIncomplete, ErrUnknownInventory, row.Kind)
	}
	if !knownInventoryState(row.Kind, row.State) {
		return fmt.Errorf("%w: %w: protected row has unknown state %q", ErrInventoryIncomplete, ErrUnknownInventory, row.State)
	}
	path, err := canonicalLockPath(row.Path)
	if err != nil {
		return fmt.Errorf("%w: protected row path: %v", ErrInventoryIncomplete, err)
	}
	if err := ensureNoSymlinkPath(path); err != nil {
		return fmt.Errorf("%w: protected row path: %v", ErrInventoryIncomplete, err)
	}
	if row.RootPath != "" {
		root, err := canonicalLockPath(row.RootPath)
		if err != nil {
			return fmt.Errorf("%w: protected row root: %v", ErrInventoryIncomplete, err)
		}
		if err := ensureNoSymlinkPath(root); err != nil {
			return fmt.Errorf("%w: protected row root: %v", ErrInventoryIncomplete, err)
		}
	}
	if row.CommonDir != "" {
		if _, err := canonicalLockPath(row.CommonDir); err != nil {
			return fmt.Errorf("%w: protected row common dir: %v", ErrInventoryIncomplete, err)
		}
	}
	return nil
}

func validateCleanupAnchor(anchor CleanupAnchor) error {
	// The v2/v3 decoder (Task 05) is the only layer allowed to mark a row
	// validated. Every validated anchor must carry the canonical identity
	// fields the release admission binds to; unknown / malformed rows
	// remain fail-closed.
	if !anchor.validated {
		return fmt.Errorf("%w: strict cleanup anchor is not validated", ErrInventoryIncomplete)
	}
	if anchor.ID == "" || anchor.OperationID == "" || anchor.TaskID == "" ||
		anchor.RepositoryID == "" || anchor.Branch == "" || anchor.HeadOID == "" ||
		anchor.ManagedRootKey == "" || anchor.Path == "" ||
		anchor.SnapshotDigest == "" || anchor.SnapshotVersion <= 0 {
		return fmt.Errorf("%w: strict cleanup anchor identity is incomplete", ErrInventoryIncomplete)
	}
	if anchor.AnchorRevision < 0 {
		return fmt.Errorf("%w: anchor revision is negative", ErrInventoryIncomplete)
	}
	return nil
}

func writeResourceDigest(digest interface{ Write([]byte) (int, error) }, row ProtectedResource) {
	digest.Write([]byte(string(row.Kind) + "\x00" + row.ID + "\x00" + row.State + "\x00" + row.Path + "\x00" + row.RootPath + "\x00" + row.CommonDir + "\n"))
}

func appendPath(paths []string, values ...string) []string {
	for _, value := range values {
		if value != "" {
			paths = append(paths, value)
		}
	}
	return paths
}

func compactPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		canonical, err := canonicalLockPath(path)
		if err != nil || canonical == "" {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result
}

func knownResourceKind(kind ResourceKind) bool {
	switch kind {
	case ResourceKindRegisteredWorktree,
		ResourceKindBranch,
		ResourceKindManagedRoot,
		ResourceKindQuarantine,
		ResourceKindParent,
		ResourceKindProvisional,
		ResourceKindTaskEnvironment,
		ResourceKindEnvironmentRepo,
		ResourceKindExecutorWorktree,
		ResourceKindWorkspaceGroup,
		ResourceKindQuarantineEntry,
		ResourceKindCleanupAnchor:
		return true
	default:
		return false
	}
}

func knownInventoryState(kind ResourceKind, state string) bool {
	switch kind {
	case ResourceKindRegisteredWorktree, ResourceKindEnvironmentRepo:
		return state == "active" || state == "merged" || state == "deleted"
	case ResourceKindTaskEnvironment:
		return state == "creating" || state == "ready" || state == "stopped" || state == "failed"
	case ResourceKindExecutorWorktree:
		switch state {
		case "prepared", "starting", "running", "ready", "failed", "stopped", "completed":
			return true
		default:
			return false
		}
	case ResourceKindWorkspaceGroup:
		return state == "active" || state == "cleanup_pending" || state == "cleaned" || state == "cleanup_failed"
	case ResourceKindQuarantineEntry:
		return state == "quarantined" || state == "restored" || state == "deleted" || state == "failed"
	default:
		return false
	}
}
