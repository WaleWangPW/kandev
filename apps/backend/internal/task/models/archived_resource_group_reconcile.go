package models

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ArchivedResourceGroupReconcileSnapshotVersion = 3
	ArchivedResourceGroupReconcileSnapshotKind    = "archived_resource_group_reconcile"

	ArchivedResourceReconcileMaxTasks    = 128
	ArchivedResourceReconcileMaxBranches = 128
)

// ArchivedResourceGroupReconcileSnapshot binds the complete logical ownership
// of one worktree. It contains metadata only and reuses the cleanup-job anchor.
type ArchivedResourceGroupReconcileSnapshot struct {
	SchemaVersion   int                                      `json:"schema_version"`
	Kind            string                                   `json:"kind"`
	Phase           string                                   `json:"phase"`
	Immutable       ArchivedResourceGroupReconcileImmutable  `json:"immutable"`
	RetentionAnchor ArchivedResourceReconcileRetentionAnchor `json:"retention_anchor"`
	Result          ArchivedResourceReconcileSnapshotResult  `json:"result"`
}

type ArchivedResourceGroupReconcileImmutable struct {
	CoordinatorTaskID string                                 `json:"coordinator_task_id"`
	Tasks             []ArchivedResourceGroupReconcileTask   `json:"tasks"`
	ManagedRootKey    string                                 `json:"managed_root_key"`
	Target            ArchivedResourceGroupReconcileTarget   `json:"target"`
	Branches          []ArchivedResourceReconcileBranch      `json:"branches"`
	Associations      []ArchivedResourceReconcileAssociation `json:"associations"`
}

type ArchivedResourceGroupReconcileTask struct {
	TaskID     string `json:"task_id"`
	ArchivedAt string `json:"archived_at"`
}

type ArchivedResourceReconcileBranch struct {
	Branch  string `json:"branch"`
	HeadOID string `json:"head_oid"`
}

type ArchivedResourceGroupReconcileTarget struct {
	WorktreeID     string `json:"worktree_id"`
	RepositoryID   string `json:"repository_id"`
	RepositoryPath string `json:"repository_path"`
	GitCommonDir   string `json:"git_common_dir"`
	WorktreePath   string `json:"worktree_path"`
}

func NewArchivedResourceGroupReconcileJob(
	snapshot ArchivedResourceGroupReconcileSnapshot,
	raw []byte,
	identity ArchivedResourceReconcileIdentity,
) *TaskResourceCleanupJob {
	activeScope := identity.ActiveScopeKey
	return &TaskResourceCleanupJob{
		OperationID:      identity.OperationID,
		TaskID:           snapshot.Immutable.CoordinatorTaskID,
		Trigger:          TaskResourceCleanupTriggerReconcile,
		State:            TaskResourceCleanupStatePending,
		ResourceSnapshot: string(raw),
		SnapshotVersion:  ArchivedResourceGroupReconcileSnapshotVersion,
		SnapshotDigest:   identity.SnapshotDigest,
		ResourceKind:     identity.ResourceKind,
		ResourceID:       identity.ResourceID,
		ManagedRootKey:   identity.ManagedRootKey,
		AnchorRevision:   0,
		ActiveScopeKey:   &activeScope,
	}
}

func ValidateArchivedResourceGroupReconcileJobHeaders(
	job *TaskResourceCleanupJob,
) (ArchivedResourceGroupReconcileSnapshot, ArchivedResourceReconcileIdentity, error) {
	if job == nil {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: nil cleanup job", ErrArchivedResourceSnapshotInvalid)
	}
	snapshot, identity, err := DecodeArchivedResourceGroupReconcileSnapshot([]byte(job.ResourceSnapshot))
	if err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	if job.Trigger != TaskResourceCleanupTriggerReconcile ||
		job.TaskID != snapshot.Immutable.CoordinatorTaskID ||
		job.OperationID != identity.OperationID ||
		job.SnapshotVersion != ArchivedResourceGroupReconcileSnapshotVersion ||
		job.SnapshotDigest != identity.SnapshotDigest ||
		job.ResourceKind != identity.ResourceKind ||
		job.ResourceID != identity.ResourceID ||
		job.ManagedRootKey != identity.ManagedRootKey ||
		job.ActiveScopeKey == nil || *job.ActiveScopeKey != identity.ActiveScopeKey {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: cleanup job headers do not match group snapshot", ErrArchivedResourceSnapshotInvalid)
	}
	if err := validateArchivedResourceAnchorLifecycle(job); err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	return snapshot, identity, nil
}

// ValidateArchivedResourceAnyReconcileJobHeaders dispatches by the redundant
// version header, then verifies the raw snapshot independently.
func ValidateArchivedResourceAnyReconcileJobHeaders(
	job *TaskResourceCleanupJob,
) (ArchivedResourceReconcileIdentity, error) {
	if job == nil {
		return ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: nil cleanup job", ErrArchivedResourceSnapshotInvalid)
	}
	switch job.SnapshotVersion {
	case ArchivedResourceReconcileSnapshotVersion:
		_, identity, err := ValidateArchivedResourceReconcileJobHeaders(job)
		return identity, err
	case ArchivedResourceGroupReconcileSnapshotVersion:
		_, identity, err := ValidateArchivedResourceGroupReconcileJobHeaders(job)
		return identity, err
	default:
		return ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: unsupported reconcile snapshot version", ErrArchivedResourceSnapshotInvalid)
	}
}

// ArchivedResourceReconcileManagedPath validates either supported snapshot and
// returns its exact canonical worktree path for the physical protection layer.
func ArchivedResourceReconcileManagedPath(job *TaskResourceCleanupJob) (string, error) {
	if job == nil {
		return "", fmt.Errorf("%w: nil cleanup job", ErrArchivedResourceSnapshotInvalid)
	}
	switch job.SnapshotVersion {
	case ArchivedResourceReconcileSnapshotVersion:
		snapshot, _, err := ValidateArchivedResourceReconcileJobHeaders(job)
		return snapshot.Immutable.Target.WorktreePath, err
	case ArchivedResourceGroupReconcileSnapshotVersion:
		snapshot, _, err := ValidateArchivedResourceGroupReconcileJobHeaders(job)
		return snapshot.Immutable.Target.WorktreePath, err
	default:
		return "", fmt.Errorf("%w: unsupported reconcile snapshot version", ErrArchivedResourceSnapshotInvalid)
	}
}

func NewArchivedResourceGroupReconcileSnapshot(
	immutable ArchivedResourceGroupReconcileImmutable,
) (ArchivedResourceGroupReconcileSnapshot, []byte, ArchivedResourceReconcileIdentity, error) {
	immutable.Tasks = append([]ArchivedResourceGroupReconcileTask(nil), immutable.Tasks...)
	immutable.Branches = append([]ArchivedResourceReconcileBranch(nil), immutable.Branches...)
	immutable.Associations = append([]ArchivedResourceReconcileAssociation(nil), immutable.Associations...)
	sort.Slice(immutable.Tasks, func(i, j int) bool { return immutable.Tasks[i].TaskID < immutable.Tasks[j].TaskID })
	sort.Slice(immutable.Branches, func(i, j int) bool { return groupBranchLess(immutable.Branches[i], immutable.Branches[j]) })
	sort.Slice(immutable.Associations, func(i, j int) bool {
		return archivedAssociationLess(immutable.Associations[i], immutable.Associations[j])
	})
	if len(immutable.Tasks) > 0 {
		immutable.CoordinatorTaskID = immutable.Tasks[0].TaskID
	}
	if err := validateArchivedResourceGroupImmutable(immutable); err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, nil, ArchivedResourceReconcileIdentity{}, err
	}
	immutableBytes, err := json.Marshal(immutable)
	if err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, nil, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: encode group immutable: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	immutableDigest := sha256Hex(immutableBytes)
	snapshot := ArchivedResourceGroupReconcileSnapshot{
		SchemaVersion: ArchivedResourceGroupReconcileSnapshotVersion,
		Kind:          ArchivedResourceGroupReconcileSnapshotKind,
		Phase:         ArchivedResourceReconcileSnapshotPhase,
		Immutable:     immutable,
		RetentionAnchor: ArchivedResourceReconcileRetentionAnchor{
			AnchorVersion: ArchivedResourceRetentionAnchorVersion, ImmutableDigest: immutableDigest,
		},
		Result: ArchivedResourceReconcileSnapshotResult{PhysicalRemoved: false},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, nil, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: encode group snapshot: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	if len(raw) > ArchivedResourceReconcileMaxSnapshotBytes {
		return ArchivedResourceGroupReconcileSnapshot{}, nil, ArchivedResourceReconcileIdentity{}, ErrArchivedResourceSnapshotTooLarge
	}
	return snapshot, raw, archivedResourceGroupReconcileIdentity(snapshot, raw, immutableDigest), nil
}

func DecodeArchivedResourceGroupReconcileSnapshot(
	raw []byte,
) (ArchivedResourceGroupReconcileSnapshot, ArchivedResourceReconcileIdentity, error) {
	if len(raw) == 0 {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: empty document", ErrArchivedResourceSnapshotInvalid)
	}
	if len(raw) > ArchivedResourceReconcileMaxSnapshotBytes {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, ErrArchivedResourceSnapshotTooLarge
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot ArchivedResourceGroupReconcileSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: decode group: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	if err := validateArchivedResourceGroupSnapshot(snapshot); err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: group document is not canonical", ErrArchivedResourceSnapshotInvalid)
	}
	immutableBytes, err := json.Marshal(snapshot.Immutable)
	if err != nil {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: encode group immutable: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	immutableDigest := sha256Hex(immutableBytes)
	if snapshot.RetentionAnchor.ImmutableDigest != immutableDigest {
		return ArchivedResourceGroupReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: immutable digest mismatch", ErrArchivedResourceSnapshotInvalid)
	}
	return snapshot, archivedResourceGroupReconcileIdentity(snapshot, raw, immutableDigest), nil
}

func archivedResourceGroupReconcileIdentity(
	snapshot ArchivedResourceGroupReconcileSnapshot,
	raw []byte,
	immutableDigest string,
) ArchivedResourceReconcileIdentity {
	snapshotDigest := sha256Hex(raw)
	return ArchivedResourceReconcileIdentity{
		SnapshotDigest: snapshotDigest, ImmutableDigest: immutableDigest,
		OperationID: "archived-resource-group-reconcile:" + snapshotDigest,
		ActiveScopeKey: "archived-resource-group-reconcile:" + sha256Hex([]byte(
			snapshot.Immutable.Target.WorktreeID+"\x00"+immutableDigest,
		)),
		ResourceKind:   ArchivedResourceReconcileResourceKind,
		ResourceID:     snapshot.Immutable.Target.WorktreeID,
		ManagedRootKey: snapshot.Immutable.ManagedRootKey,
	}
}

func validateArchivedResourceGroupSnapshot(snapshot ArchivedResourceGroupReconcileSnapshot) error {
	if snapshot.SchemaVersion != ArchivedResourceGroupReconcileSnapshotVersion ||
		snapshot.Kind != ArchivedResourceGroupReconcileSnapshotKind ||
		snapshot.Phase != ArchivedResourceReconcileSnapshotPhase ||
		snapshot.RetentionAnchor.AnchorVersion != ArchivedResourceRetentionAnchorVersion ||
		snapshot.Result.PhysicalRemoved {
		return fmt.Errorf("%w: group snapshot lifecycle fields are invalid", ErrArchivedResourceSnapshotInvalid)
	}
	return validateArchivedResourceGroupImmutable(snapshot.Immutable)
}

func validateArchivedResourceGroupImmutable(immutable ArchivedResourceGroupReconcileImmutable) error {
	if len(immutable.Tasks) == 0 || len(immutable.Tasks) > ArchivedResourceReconcileMaxTasks ||
		len(immutable.Branches) == 0 || len(immutable.Branches) > ArchivedResourceReconcileMaxBranches ||
		len(immutable.Associations) == 0 || len(immutable.Associations) > ArchivedResourceReconcileMaxAssociations {
		return fmt.Errorf("%w: group inventory count is out of bounds", ErrArchivedResourceSnapshotInvalid)
	}
	if err := validateGroupTarget(immutable); err != nil {
		return err
	}
	tasks, err := validateGroupTasks(immutable)
	if err != nil {
		return err
	}
	branches, err := validateGroupBranches(immutable)
	if err != nil {
		return err
	}
	return validateGroupAssociations(immutable, tasks, branches)
}

func validateGroupTarget(immutable ArchivedResourceGroupReconcileImmutable) error {
	if err := validateOpaque("coordinator_task_id", immutable.CoordinatorTaskID); err != nil {
		return err
	}
	if err := validateOpaque("managed_root_key", immutable.ManagedRootKey); err != nil {
		return err
	}
	target := immutable.Target
	if err := validateOpaque("worktree_id", target.WorktreeID); err != nil {
		return err
	}
	if err := validateOpaque("repository_id", target.RepositoryID); err != nil {
		return err
	}
	for name, value := range map[string]string{"repository_path": target.RepositoryPath, "git_common_dir": target.GitCommonDir, "worktree_path": target.WorktreePath} {
		if err := validateCanonicalAbsolutePath(name, value); err != nil {
			return err
		}
	}
	rootKey, err := ArchivedResourceManagedRootKey(target.WorktreePath)
	if err != nil || rootKey != immutable.ManagedRootKey {
		return fmt.Errorf("%w: managed_root_key does not bind group worktree_path", ErrArchivedResourceSnapshotInvalid)
	}
	return nil
}

func validateGroupTasks(immutable ArchivedResourceGroupReconcileImmutable) (map[string]struct{}, error) {
	tasks := make(map[string]struct{}, len(immutable.Tasks))
	for index, task := range immutable.Tasks {
		if err := validateOpaque("group.task_id", task.TaskID); err != nil {
			return nil, err
		}
		if err := validateCanonicalUTC("group.archived_at", task.ArchivedAt); err != nil {
			return nil, err
		}
		if index > 0 && immutable.Tasks[index-1].TaskID >= task.TaskID {
			return nil, fmt.Errorf("%w: group tasks are not uniquely sorted", ErrArchivedResourceSnapshotInvalid)
		}
		tasks[task.TaskID] = struct{}{}
	}
	if immutable.CoordinatorTaskID != immutable.Tasks[0].TaskID {
		return nil, fmt.Errorf("%w: coordinator is not the first canonical task", ErrArchivedResourceSnapshotInvalid)
	}
	return tasks, nil
}

func validateGroupBranches(immutable ArchivedResourceGroupReconcileImmutable) (map[string]struct{}, error) {
	branches := make(map[string]struct{}, len(immutable.Branches))
	for index, branch := range immutable.Branches {
		if err := validateOpaque("group.branch", branch.Branch); err != nil {
			return nil, err
		}
		if len(branch.HeadOID) != 40 || strings.ToLower(branch.HeadOID) != branch.HeadOID {
			return nil, fmt.Errorf("%w: group head_oid must be 40 lowercase hex characters", ErrArchivedResourceSnapshotInvalid)
		}
		if _, err := hex.DecodeString(branch.HeadOID); err != nil {
			return nil, fmt.Errorf("%w: group head_oid must be hexadecimal", ErrArchivedResourceSnapshotInvalid)
		}
		if index > 0 && !groupBranchLess(immutable.Branches[index-1], branch) {
			return nil, fmt.Errorf("%w: group branches are not uniquely sorted", ErrArchivedResourceSnapshotInvalid)
		}
		branches[branch.Branch] = struct{}{}
	}
	return branches, nil
}

func validateGroupAssociations(
	immutable ArchivedResourceGroupReconcileImmutable,
	tasks map[string]struct{},
	branches map[string]struct{},
) error {
	seenIDs := make(map[string]struct{}, len(immutable.Associations))
	seenKeys := make(map[string]struct{}, len(immutable.Associations))
	usedTasks := make(map[string]struct{}, len(tasks))
	usedBranches := make(map[string]struct{}, len(branches))
	for index, association := range immutable.Associations {
		if index > 0 && !archivedAssociationLess(immutable.Associations[index-1], association) {
			return fmt.Errorf("%w: group associations are not uniquely sorted", ErrArchivedResourceSnapshotInvalid)
		}
		if _, ok := seenIDs[association.AssociationID]; ok {
			return fmt.Errorf("%w: duplicate group association_id", ErrArchivedResourceSnapshotInvalid)
		}
		key := association.SessionID + "\x00" + association.WorktreeID
		if _, ok := seenKeys[key]; ok {
			return fmt.Errorf("%w: duplicate group association key", ErrArchivedResourceSnapshotInvalid)
		}
		seenIDs[association.AssociationID], seenKeys[key] = struct{}{}, struct{}{}
		if err := validateGroupAssociation(immutable, association, tasks, branches); err != nil {
			return err
		}
		usedTasks[association.TaskID], usedBranches[association.WorktreeBranch] = struct{}{}, struct{}{}
	}
	if len(usedTasks) != len(tasks) || len(usedBranches) != len(branches) {
		return fmt.Errorf("%w: group task or branch inventory is not fully used", ErrArchivedResourceSnapshotInvalid)
	}
	return nil
}

func validateGroupAssociation(
	immutable ArchivedResourceGroupReconcileImmutable,
	association ArchivedResourceReconcileAssociation,
	tasks map[string]struct{},
	branches map[string]struct{},
) error {
	for name, value := range map[string]string{"association_id": association.AssociationID, "task_id": association.TaskID, "session_id": association.SessionID, "worktree_id": association.WorktreeID, "repository_id": association.RepositoryID, "worktree_branch": association.WorktreeBranch, "status": association.Status} {
		if err := validateOpaque(name, value); err != nil {
			return err
		}
	}
	if err := validateOptionalOpaque("branch_slug", association.BranchSlug); err != nil {
		return err
	}
	if err := validateCanonicalAbsolutePath("association.worktree_path", association.WorktreePath); err != nil {
		return err
	}
	if err := validateCanonicalUTC("association.created_at", association.CreatedAt); err != nil {
		return err
	}
	if err := validateCanonicalUTC("association.updated_at", association.UpdatedAt); err != nil {
		return err
	}
	_, taskOK := tasks[association.TaskID]
	_, branchOK := branches[association.WorktreeBranch]
	if !taskOK || !branchOK || association.Status != "active" ||
		association.WorktreeID != immutable.Target.WorktreeID ||
		association.RepositoryID != immutable.Target.RepositoryID ||
		association.WorktreePath != immutable.Target.WorktreePath {
		return fmt.Errorf("%w: group association does not match immutable inventory", ErrArchivedResourceSnapshotInvalid)
	}
	return nil
}

func groupBranchLess(left, right ArchivedResourceReconcileBranch) bool {
	if left.Branch != right.Branch {
		return left.Branch < right.Branch
	}
	return left.HeadOID < right.HeadOID
}
