package models

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ArchivedResourceReconcileSnapshotVersion = 2
	ArchivedResourceReconcileSnapshotKind    = "archived_resource_reconcile"
	ArchivedResourceReconcileSnapshotPhase   = "db_unbound_retained"
	ArchivedResourceRetentionAnchorVersion   = 1
	ArchivedResourceReconcileResourceKind    = "git_worktree"

	ArchivedResourceReconcileMaxSnapshotBytes = 64 * 1024
	ArchivedResourceReconcileMaxAssociations  = 128
)

var (
	ErrArchivedResourceSnapshotInvalid  = errors.New("archived resource reconcile snapshot is invalid")
	ErrArchivedResourceSnapshotTooLarge = errors.New("archived resource reconcile snapshot exceeds size limit")
)

// ArchivedResourceReconcileSnapshot is the canonical, durable DB-only
// reconciliation contract. It intentionally contains metadata only. Filesystem
// identity and contents are never persisted in this snapshot.
type ArchivedResourceReconcileSnapshot struct {
	SchemaVersion   int                                      `json:"schema_version"`
	Kind            string                                   `json:"kind"`
	Phase           string                                   `json:"phase"`
	Immutable       ArchivedResourceReconcileImmutable       `json:"immutable"`
	RetentionAnchor ArchivedResourceReconcileRetentionAnchor `json:"retention_anchor"`
	Result          ArchivedResourceReconcileSnapshotResult  `json:"result"`
}

type ArchivedResourceReconcileImmutable struct {
	OriginTaskID   string                                 `json:"origin_task_id"`
	ArchivedAt     string                                 `json:"archived_at"`
	ManagedRootKey string                                 `json:"managed_root_key"`
	Target         ArchivedResourceReconcileTarget        `json:"target"`
	Associations   []ArchivedResourceReconcileAssociation `json:"associations"`
}

type ArchivedResourceReconcileTarget struct {
	WorktreeID     string `json:"worktree_id"`
	RepositoryID   string `json:"repository_id"`
	RepositoryPath string `json:"repository_path"`
	GitCommonDir   string `json:"git_common_dir"`
	WorktreePath   string `json:"worktree_path"`
	Branch         string `json:"branch"`
	HeadOID        string `json:"head_oid"`
}

// ArchivedResourceReconcileAssociation binds the exact generation of one
// task_environment_repos row. The snapshot SessionID slot carries the row's
// task_environment_id — the stable v0.88 participant identity that the
// repository loaders derive for the same row.
type ArchivedResourceReconcileAssociation struct {
	AssociationID  string `json:"association_id"`
	TaskID         string `json:"task_id"`
	SessionID      string `json:"session_id"`
	WorktreeID     string `json:"worktree_id"`
	RepositoryID   string `json:"repository_id"`
	BranchSlug     string `json:"branch_slug"`
	WorktreePath   string `json:"worktree_path"`
	WorktreeBranch string `json:"worktree_branch"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type ArchivedResourceReconcileRetentionAnchor struct {
	AnchorVersion   int    `json:"anchor_version"`
	ImmutableDigest string `json:"immutable_digest"`
}

type ArchivedResourceReconcileSnapshotResult struct {
	PhysicalRemoved bool `json:"physical_removed"`
}

// ArchivedResourceReconcileIdentity contains the redundant typed headers that
// are stored beside resource_snapshot. Every field is derived from canonical
// snapshot bytes and can therefore be checked without trusting a caller.
type ArchivedResourceReconcileIdentity struct {
	SnapshotDigest  string
	ImmutableDigest string
	OperationID     string
	ActiveScopeKey  string
	ResourceKind    string
	ResourceID      string
	ManagedRootKey  string
}

type ArchivedResourceReconcileAdmission struct {
	Job     *TaskResourceCleanupJob
	Created bool
}

type ArchivedResourceReconcileCompletion struct {
	Job                 *TaskResourceCleanupJob
	AssociationsUnbound int
	Replayed            bool
}

// NewArchivedResourceReconcileJob builds the only valid initial durable job
// representation for a canonical reconcile snapshot.
func NewArchivedResourceReconcileJob(
	snapshot ArchivedResourceReconcileSnapshot,
	raw []byte,
	identity ArchivedResourceReconcileIdentity,
) *TaskResourceCleanupJob {
	activeScope := identity.ActiveScopeKey
	return &TaskResourceCleanupJob{
		OperationID:      identity.OperationID,
		TaskID:           snapshot.Immutable.OriginTaskID,
		Trigger:          TaskResourceCleanupTriggerReconcile,
		State:            TaskResourceCleanupStatePending,
		ResourceSnapshot: string(raw),
		SnapshotVersion:  ArchivedResourceReconcileSnapshotVersion,
		SnapshotDigest:   identity.SnapshotDigest,
		ResourceKind:     identity.ResourceKind,
		ResourceID:       identity.ResourceID,
		ManagedRootKey:   identity.ManagedRootKey,
		AnchorRevision:   0,
		ActiveScopeKey:   &activeScope,
	}
}

// ValidateArchivedResourceReconcileJobHeaders treats resource_snapshot as the
// authority and checks every redundant job header against it.
func ValidateArchivedResourceReconcileJobHeaders(
	job *TaskResourceCleanupJob,
) (ArchivedResourceReconcileSnapshot, ArchivedResourceReconcileIdentity, error) {
	if job == nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: nil cleanup job", ErrArchivedResourceSnapshotInvalid)
	}
	snapshot, identity, err := DecodeArchivedResourceReconcileSnapshot([]byte(job.ResourceSnapshot))
	if err != nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	if job.Trigger != TaskResourceCleanupTriggerReconcile ||
		job.TaskID != snapshot.Immutable.OriginTaskID ||
		job.OperationID != identity.OperationID ||
		job.SnapshotVersion != ArchivedResourceReconcileSnapshotVersion ||
		job.SnapshotDigest != identity.SnapshotDigest ||
		job.ResourceKind != identity.ResourceKind ||
		job.ResourceID != identity.ResourceID ||
		job.ManagedRootKey != identity.ManagedRootKey ||
		job.ActiveScopeKey == nil || *job.ActiveScopeKey != identity.ActiveScopeKey {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: cleanup job headers do not match snapshot", ErrArchivedResourceSnapshotInvalid)
	}
	if err := validateArchivedResourceAnchorLifecycle(job); err != nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	return snapshot, identity, nil
}

func IsActiveArchivedResourceReconcileState(state TaskResourceCleanupState) bool {
	switch state {
	case TaskResourceCleanupStatePrepared,
		TaskResourceCleanupStatePending,
		TaskResourceCleanupStateRunning,
		TaskResourceCleanupStateRetryWait,
		TaskResourceCleanupStateRetained,
		TaskResourceCleanupStateBlocked:
		return true
	default:
		return false
	}
}

func validateArchivedResourceAnchorLifecycle(job *TaskResourceCleanupJob) error {
	if !IsActiveArchivedResourceReconcileState(job.State) {
		return fmt.Errorf("%w: reconcile job state %q is not an active anchor", ErrArchivedResourceSnapshotInvalid, job.State)
	}
	switch job.State {
	case TaskResourceCleanupStatePrepared,
		TaskResourceCleanupStatePending,
		TaskResourceCleanupStateRunning,
		TaskResourceCleanupStateRetryWait:
		if job.AnchorRevision != 0 || job.CompletedAt != nil {
			return fmt.Errorf("%w: pre-retention state has terminal anchor fields", ErrArchivedResourceSnapshotInvalid)
		}
	case TaskResourceCleanupStateRetained:
		if job.AnchorRevision != ArchivedResourceRetentionAnchorVersion || job.CompletedAt == nil || job.CompletedAt.IsZero() {
			return fmt.Errorf("%w: retained state requires revision one and completion time", ErrArchivedResourceSnapshotInvalid)
		}
	case TaskResourceCleanupStateBlocked:
		if job.AnchorRevision < 0 || job.AnchorRevision > ArchivedResourceRetentionAnchorVersion ||
			(job.AnchorRevision == 0 && job.CompletedAt != nil) ||
			(job.AnchorRevision == ArchivedResourceRetentionAnchorVersion && (job.CompletedAt == nil || job.CompletedAt.IsZero())) {
			return fmt.Errorf("%w: blocked anchor lifecycle fields are inconsistent", ErrArchivedResourceSnapshotInvalid)
		}
	}
	return nil
}

// NewArchivedResourceReconcileSnapshot validates and canonicalizes a snapshot.
// Associations are sorted in a copy so caller-owned input is never mutated.
func NewArchivedResourceReconcileSnapshot(
	immutable ArchivedResourceReconcileImmutable,
) (ArchivedResourceReconcileSnapshot, []byte, ArchivedResourceReconcileIdentity, error) {
	immutable.Associations = append([]ArchivedResourceReconcileAssociation(nil), immutable.Associations...)
	sort.Slice(immutable.Associations, func(i, j int) bool {
		return archivedAssociationLess(immutable.Associations[i], immutable.Associations[j])
	})
	if err := validateArchivedResourceImmutable(immutable); err != nil {
		return ArchivedResourceReconcileSnapshot{}, nil, ArchivedResourceReconcileIdentity{}, err
	}
	immutableBytes, err := json.Marshal(immutable)
	if err != nil {
		return ArchivedResourceReconcileSnapshot{}, nil, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: encode immutable: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	immutableDigest := sha256Hex(immutableBytes)
	snapshot := ArchivedResourceReconcileSnapshot{
		SchemaVersion: ArchivedResourceReconcileSnapshotVersion,
		Kind:          ArchivedResourceReconcileSnapshotKind,
		Phase:         ArchivedResourceReconcileSnapshotPhase,
		Immutable:     immutable,
		RetentionAnchor: ArchivedResourceReconcileRetentionAnchor{
			AnchorVersion:   ArchivedResourceRetentionAnchorVersion,
			ImmutableDigest: immutableDigest,
		},
		Result: ArchivedResourceReconcileSnapshotResult{PhysicalRemoved: false},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ArchivedResourceReconcileSnapshot{}, nil, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: encode snapshot: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	if len(raw) > ArchivedResourceReconcileMaxSnapshotBytes {
		return ArchivedResourceReconcileSnapshot{}, nil, ArchivedResourceReconcileIdentity{}, ErrArchivedResourceSnapshotTooLarge
	}
	identity := archivedResourceReconcileIdentity(snapshot, raw, immutableDigest)
	return snapshot, raw, identity, nil
}

// DecodeArchivedResourceReconcileSnapshot accepts canonical JSON only. It
// rejects duplicate and unknown keys before deriving the typed headers.
func DecodeArchivedResourceReconcileSnapshot(
	raw []byte,
) (ArchivedResourceReconcileSnapshot, ArchivedResourceReconcileIdentity, error) {
	if len(raw) == 0 {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: empty document", ErrArchivedResourceSnapshotInvalid)
	}
	if len(raw) > ArchivedResourceReconcileMaxSnapshotBytes {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, ErrArchivedResourceSnapshotTooLarge
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot ArchivedResourceReconcileSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: decode: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	if err := validateArchivedResourceSnapshot(snapshot); err != nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, err
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: re-encode: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	if !bytes.Equal(raw, canonical) {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: document is not canonical", ErrArchivedResourceSnapshotInvalid)
	}
	immutableBytes, err := json.Marshal(snapshot.Immutable)
	if err != nil {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: encode immutable: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	immutableDigest := sha256Hex(immutableBytes)
	if snapshot.RetentionAnchor.ImmutableDigest != immutableDigest {
		return ArchivedResourceReconcileSnapshot{}, ArchivedResourceReconcileIdentity{}, fmt.Errorf("%w: immutable digest mismatch", ErrArchivedResourceSnapshotInvalid)
	}
	return snapshot, archivedResourceReconcileIdentity(snapshot, raw, immutableDigest), nil
}

func ArchivedResourceManagedRootKey(worktreePath string) (string, error) {
	if err := validateCanonicalAbsolutePath("worktree_path", worktreePath); err != nil {
		return "", err
	}
	return "git_worktree:" + sha256Hex([]byte(worktreePath)), nil
}

func archivedResourceReconcileIdentity(
	snapshot ArchivedResourceReconcileSnapshot,
	raw []byte,
	immutableDigest string,
) ArchivedResourceReconcileIdentity {
	snapshotDigest := sha256Hex(raw)
	return ArchivedResourceReconcileIdentity{
		SnapshotDigest:  snapshotDigest,
		ImmutableDigest: immutableDigest,
		OperationID:     "archived-resource-reconcile:" + snapshotDigest,
		ActiveScopeKey: "archived-resource-reconcile:" + sha256Hex([]byte(
			snapshot.Immutable.OriginTaskID+"\x00"+snapshot.Immutable.Target.WorktreeID+"\x00"+immutableDigest,
		)),
		ResourceKind:   ArchivedResourceReconcileResourceKind,
		ResourceID:     snapshot.Immutable.Target.WorktreeID,
		ManagedRootKey: snapshot.Immutable.ManagedRootKey,
	}
}

func validateArchivedResourceSnapshot(snapshot ArchivedResourceReconcileSnapshot) error {
	if snapshot.SchemaVersion != ArchivedResourceReconcileSnapshotVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrArchivedResourceSnapshotInvalid, ArchivedResourceReconcileSnapshotVersion)
	}
	if snapshot.Kind != ArchivedResourceReconcileSnapshotKind {
		return fmt.Errorf("%w: unexpected kind", ErrArchivedResourceSnapshotInvalid)
	}
	if snapshot.Phase != ArchivedResourceReconcileSnapshotPhase {
		return fmt.Errorf("%w: unexpected phase", ErrArchivedResourceSnapshotInvalid)
	}
	if snapshot.RetentionAnchor.AnchorVersion != ArchivedResourceRetentionAnchorVersion {
		return fmt.Errorf("%w: unexpected anchor version", ErrArchivedResourceSnapshotInvalid)
	}
	if snapshot.Result.PhysicalRemoved {
		return fmt.Errorf("%w: physical_removed must be false", ErrArchivedResourceSnapshotInvalid)
	}
	return validateArchivedResourceImmutable(snapshot.Immutable)
}

func validateArchivedResourceImmutable(immutable ArchivedResourceReconcileImmutable) error {
	if err := validateOpaque("origin_task_id", immutable.OriginTaskID); err != nil {
		return err
	}
	if err := validateCanonicalUTC("archived_at", immutable.ArchivedAt); err != nil {
		return err
	}
	if err := validateOpaque("managed_root_key", immutable.ManagedRootKey); err != nil {
		return err
	}
	target := immutable.Target
	for name, value := range map[string]string{
		"worktree_id":   target.WorktreeID,
		"repository_id": target.RepositoryID,
		"branch":        target.Branch,
	} {
		if err := validateOpaque(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{
		"repository_path": target.RepositoryPath,
		"git_common_dir":  target.GitCommonDir,
		"worktree_path":   target.WorktreePath,
	} {
		if err := validateCanonicalAbsolutePath(name, value); err != nil {
			return err
		}
	}
	expectedManagedRootKey, err := ArchivedResourceManagedRootKey(target.WorktreePath)
	if err != nil || immutable.ManagedRootKey != expectedManagedRootKey {
		return fmt.Errorf("%w: managed_root_key does not bind worktree_path", ErrArchivedResourceSnapshotInvalid)
	}
	if len(target.HeadOID) != 40 || strings.ToLower(target.HeadOID) != target.HeadOID {
		return fmt.Errorf("%w: head_oid must be 40 lowercase hex characters", ErrArchivedResourceSnapshotInvalid)
	}
	if _, err := hex.DecodeString(target.HeadOID); err != nil {
		return fmt.Errorf("%w: head_oid must be hexadecimal", ErrArchivedResourceSnapshotInvalid)
	}
	if len(immutable.Associations) == 0 || len(immutable.Associations) > ArchivedResourceReconcileMaxAssociations {
		return fmt.Errorf("%w: association count must be between 1 and %d", ErrArchivedResourceSnapshotInvalid, ArchivedResourceReconcileMaxAssociations)
	}
	seen := make(map[string]struct{}, len(immutable.Associations))
	seenKeys := make(map[string]struct{}, len(immutable.Associations))
	for i, association := range immutable.Associations {
		if i > 0 && archivedAssociationLess(association, immutable.Associations[i-1]) {
			return fmt.Errorf("%w: associations are not canonically sorted", ErrArchivedResourceSnapshotInvalid)
		}
		if _, ok := seen[association.AssociationID]; ok {
			return fmt.Errorf("%w: duplicate association_id", ErrArchivedResourceSnapshotInvalid)
		}
		seen[association.AssociationID] = struct{}{}
		associationKey := association.SessionID + "\x00" + association.WorktreeID
		if _, ok := seenKeys[associationKey]; ok {
			return fmt.Errorf("%w: duplicate session/worktree association key", ErrArchivedResourceSnapshotInvalid)
		}
		seenKeys[associationKey] = struct{}{}
		if err := validateArchivedAssociation(immutable, association); err != nil {
			return err
		}
	}
	return nil
}

func validateArchivedAssociation(
	immutable ArchivedResourceReconcileImmutable,
	association ArchivedResourceReconcileAssociation,
) error {
	for name, value := range map[string]string{
		"association_id":  association.AssociationID,
		"task_id":         association.TaskID,
		"session_id":      association.SessionID,
		"worktree_id":     association.WorktreeID,
		"repository_id":   association.RepositoryID,
		"worktree_branch": association.WorktreeBranch,
		"status":          association.Status,
	} {
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
	if association.Status != "active" ||
		association.TaskID != immutable.OriginTaskID ||
		association.WorktreeID != immutable.Target.WorktreeID ||
		association.RepositoryID != immutable.Target.RepositoryID ||
		association.WorktreePath != immutable.Target.WorktreePath ||
		association.WorktreeBranch != immutable.Target.Branch {
		return fmt.Errorf("%w: association does not match immutable target", ErrArchivedResourceSnapshotInvalid)
	}
	return nil
}

func archivedAssociationLess(left, right ArchivedResourceReconcileAssociation) bool {
	if left.AssociationID != right.AssociationID {
		return left.AssociationID < right.AssociationID
	}
	if left.SessionID != right.SessionID {
		return left.SessionID < right.SessionID
	}
	return left.UpdatedAt < right.UpdatedAt
}

func validateOpaque(name, value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%w: %s is empty or not trimmed", ErrArchivedResourceSnapshotInvalid, name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a control character", ErrArchivedResourceSnapshotInvalid, name)
		}
	}
	return nil
}

func validateOptionalOpaque(name, value string) error {
	if value == "" {
		return nil
	}
	return validateOpaque(name, value)
}

func validateCanonicalAbsolutePath(name, value string) error {
	if !utf8.ValidString(value) || value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value ||
		filepath.Dir(value) == value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%w: %s must be a canonical absolute path", ErrArchivedResourceSnapshotInvalid, name)
	}
	return nil
}

func validateCanonicalUTC(name, value string) error {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return fmt.Errorf("%w: %s must be canonical RFC3339Nano UTC", ErrArchivedResourceSnapshotInvalid, name)
	}
	return nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return fmt.Errorf("%w: %v", ErrArchivedResourceSnapshotInvalid, err)
	}
	return requireJSONEOF(decoder)
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("object did not terminate")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("array did not terminate")
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delim)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("%w: trailing JSON: %v", ErrArchivedResourceSnapshotInvalid, err)
		}
		return fmt.Errorf("%w: trailing JSON value %v", ErrArchivedResourceSnapshotInvalid, token)
	}
	return nil
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
