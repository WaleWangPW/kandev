package models

import (
	"bytes"
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
	ArchivedResourceEnvironmentRetirementSnapshotVersion = 2
	ArchivedResourceEnvironmentRetirementSnapshotKind    = "archived_resource_environment_retirement"
	ArchivedResourceEnvironmentRetirementSnapshotPhase   = "db_unbound_retired"

	ArchivedResourceEnvironmentRetirementMaxSnapshotBytes = 16 * 1024
	ArchivedResourceEnvironmentRetirementMaxRepositories  = 16
)

var (
	ErrArchivedResourceEnvironmentRetirementInvalid  = errors.New("archived resource environment retirement snapshot is invalid")
	ErrArchivedResourceEnvironmentRetirementTooLarge = errors.New("archived resource environment retirement snapshot exceeds size limit")
)

// ArchivedResourceEnvironmentRetirementSnapshot is the canonical, durable,
// metadata-only retirement request. It binds one exact stopped/failed task
// environment, its complete sorted repository reference set, and the exact
// seven-field per-repository generation (the same fields that bound the v0.88
// release snapshot in docs/specs/tasks/archived-resource-safety/spec.md).
//
// The retirement admission only succeeds when every active v2/v3 reconcile
// anchor confirms the environment is not a participant and every workspace
// group inventory state is one of the four accepted known states.
type ArchivedResourceEnvironmentRetirementSnapshot struct {
	SchemaVersion int                                            `json:"schema_version"`
	Kind          string                                         `json:"kind"`
	Phase         string                                         `json:"phase"`
	Immutable     ArchivedResourceEnvironmentRetirementImmutable `json:"immutable"`
	Retirement    ArchivedResourceEnvironmentRetirementProof     `json:"retirement"`
}

type ArchivedResourceEnvironmentRetirementImmutable struct {
	EnvironmentID     string                                            `json:"environment_id"`
	TaskID            string                                            `json:"task_id"`
	EnvironmentStatus string                                            `json:"environment_status"`
	Repositories      []ArchivedResourceEnvironmentRetirementRepository `json:"repositories"`
}

type ArchivedResourceEnvironmentRetirementRepository struct {
	ID             string `json:"id"`
	RepositoryID   string `json:"repository_id"`
	BranchSlug     string `json:"branch_slug,omitempty"`
	WorktreeID     string `json:"worktree_id,omitempty"`
	WorktreePath   string `json:"worktree_path,omitempty"`
	WorktreeBranch string `json:"worktree_branch,omitempty"`
	Position       int    `json:"position"`
	Status         string `json:"status,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	MergedAt       string `json:"merged_at,omitempty"`
	DeletedAt      string `json:"deleted_at,omitempty"`
}

type ArchivedResourceEnvironmentRetirementProof struct {
	RetiredAt string `json:"retired_at"`
}

// ArchivedResourceEnvironmentRetirementIdentity is the redundant header set
// bound alongside the resource_snapshot. Every field is derived from canonical
// snapshot bytes so the admission cannot self-sign identity without re-encoding.
type ArchivedResourceEnvironmentRetirementIdentity struct {
	SnapshotDigest string
	OperationID    string
	ActiveScopeKey string
	ResourceKind   string
	ResourceID     string
	ManagedRootKey string
}

// NewArchivedResourceEnvironmentRetirementSnapshot validates and
// canonicalizes the exact-set retirement request. Repositories are sorted by
// repository_id (then worktree_id) so the canonical form is deterministic.
func NewArchivedResourceEnvironmentRetirementSnapshot(
	immutable ArchivedResourceEnvironmentRetirementImmutable,
	retirement ArchivedResourceEnvironmentRetirementProof,
) (ArchivedResourceEnvironmentRetirementSnapshot, []byte, ArchivedResourceEnvironmentRetirementIdentity, error) {
	repositories := append([]ArchivedResourceEnvironmentRetirementRepository(nil), immutable.Repositories...)
	sort.Slice(repositories, func(i, j int) bool {
		if repositories[i].ID != repositories[j].ID {
			return repositories[i].ID < repositories[j].ID
		}
		if repositories[i].RepositoryID != repositories[j].RepositoryID {
			return repositories[i].RepositoryID < repositories[j].RepositoryID
		}
		if repositories[i].WorktreeID != repositories[j].WorktreeID {
			return repositories[i].WorktreeID < repositories[j].WorktreeID
		}
		return repositories[i].CreatedAt < repositories[j].CreatedAt
	})
	immutable.Repositories = repositories
	if err := validateArchivedResourceEnvironmentRetirementImmutable(immutable); err != nil {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, nil, ArchivedResourceEnvironmentRetirementIdentity{}, err
	}
	if err := validateArchivedResourceEnvironmentRetirementProof(retirement); err != nil {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, nil, ArchivedResourceEnvironmentRetirementIdentity{}, err
	}
	snapshot := ArchivedResourceEnvironmentRetirementSnapshot{
		SchemaVersion: ArchivedResourceEnvironmentRetirementSnapshotVersion,
		Kind:          ArchivedResourceEnvironmentRetirementSnapshotKind,
		Phase:         ArchivedResourceEnvironmentRetirementSnapshotPhase,
		Immutable:     immutable,
		Retirement:    retirement,
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, nil, ArchivedResourceEnvironmentRetirementIdentity{}, fmt.Errorf("%w: encode snapshot: %v", ErrArchivedResourceEnvironmentRetirementInvalid, err)
	}
	if len(raw) > ArchivedResourceEnvironmentRetirementMaxSnapshotBytes {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, nil, ArchivedResourceEnvironmentRetirementIdentity{}, ErrArchivedResourceEnvironmentRetirementTooLarge
	}
	identity := archivedResourceEnvironmentRetirementIdentity(snapshot, raw)
	return snapshot, raw, identity, nil
}

// DecodeArchivedResourceEnvironmentRetirementSnapshot is the strict decoder
// used to read a previously persisted retirement snapshot from the database.
func DecodeArchivedResourceEnvironmentRetirementSnapshot(
	raw []byte,
) (ArchivedResourceEnvironmentRetirementSnapshot, ArchivedResourceEnvironmentRetirementIdentity, error) {
	if len(raw) == 0 {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, ArchivedResourceEnvironmentRetirementIdentity{}, fmt.Errorf("%w: empty document", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if len(raw) > ArchivedResourceEnvironmentRetirementMaxSnapshotBytes {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, ArchivedResourceEnvironmentRetirementIdentity{}, ErrArchivedResourceEnvironmentRetirementTooLarge
	}
	if err := rejectDuplicateRetirementJSONKeys(raw); err != nil {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, ArchivedResourceEnvironmentRetirementIdentity{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot ArchivedResourceEnvironmentRetirementSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, ArchivedResourceEnvironmentRetirementIdentity{}, fmt.Errorf("%w: decode: %v", ErrArchivedResourceEnvironmentRetirementInvalid, err)
	}
	if err := requireRetirementJSONEOF(decoder); err != nil {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, ArchivedResourceEnvironmentRetirementIdentity{}, err
	}
	if err := validateArchivedResourceEnvironmentRetirementSnapshot(snapshot); err != nil {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, ArchivedResourceEnvironmentRetirementIdentity{}, err
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, ArchivedResourceEnvironmentRetirementIdentity{}, fmt.Errorf("%w: re-encode: %v", ErrArchivedResourceEnvironmentRetirementInvalid, err)
	}
	if !bytes.Equal(raw, canonical) {
		return ArchivedResourceEnvironmentRetirementSnapshot{}, ArchivedResourceEnvironmentRetirementIdentity{}, fmt.Errorf("%w: document is not canonical", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	return snapshot, archivedResourceEnvironmentRetirementIdentity(snapshot, raw), nil
}

// ArchivedResourceEnvironmentRetirementManagedRootKey derives the canonical
// managed root identity from the environment's first canonical worktree_path.
// Returns an empty string when the environment has no repositories.
func ArchivedResourceEnvironmentRetirementManagedRootKey(worktreePath string) (string, error) {
	if worktreePath == "" {
		return "", nil
	}
	if err := validateRetirementCanonicalAbsolutePath("worktree_path", worktreePath); err != nil {
		return "", err
	}
	return "git_worktree:" + sha256Hex([]byte(worktreePath)), nil
}

func archivedResourceEnvironmentRetirementIdentity(
	snapshot ArchivedResourceEnvironmentRetirementSnapshot,
	raw []byte,
) ArchivedResourceEnvironmentRetirementIdentity {
	digest := sha256Hex(raw)
	firstPath := ""
	if len(snapshot.Immutable.Repositories) > 0 {
		firstPath = snapshot.Immutable.Repositories[0].WorktreePath
	}
	managedRootKey, _ := ArchivedResourceEnvironmentRetirementManagedRootKey(firstPath)
	return ArchivedResourceEnvironmentRetirementIdentity{
		SnapshotDigest: digest,
		OperationID:    "archived-resource-environment-retirement:" + digest,
		ActiveScopeKey: "archived-resource-environment-retirement:" + sha256Hex([]byte(
			snapshot.Immutable.EnvironmentID+"\x00"+snapshot.Immutable.TaskID+"\x00"+digest,
		)),
		ResourceKind:   "task_environment",
		ResourceID:     snapshot.Immutable.EnvironmentID,
		ManagedRootKey: managedRootKey,
	}
}

func validateArchivedResourceEnvironmentRetirementSnapshot(snapshot ArchivedResourceEnvironmentRetirementSnapshot) error {
	if snapshot.SchemaVersion != ArchivedResourceEnvironmentRetirementSnapshotVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrArchivedResourceEnvironmentRetirementInvalid, ArchivedResourceEnvironmentRetirementSnapshotVersion)
	}
	if snapshot.Kind != ArchivedResourceEnvironmentRetirementSnapshotKind {
		return fmt.Errorf("%w: unexpected kind", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if snapshot.Phase != ArchivedResourceEnvironmentRetirementSnapshotPhase {
		return fmt.Errorf("%w: unexpected phase", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if err := validateArchivedResourceEnvironmentRetirementImmutable(snapshot.Immutable); err != nil {
		return err
	}
	return validateArchivedResourceEnvironmentRetirementProof(snapshot.Retirement)
}

func validateArchivedResourceEnvironmentRetirementImmutable(immutable ArchivedResourceEnvironmentRetirementImmutable) error {
	for name, value := range map[string]string{
		"environment_id":     immutable.EnvironmentID,
		"task_id":            immutable.TaskID,
		"environment_status": immutable.EnvironmentStatus,
	} {
		if err := validateRetirementOpaque(name, value); err != nil {
			return err
		}
	}
	switch immutable.EnvironmentStatus {
	case "stopped", "failed":
	default:
		return fmt.Errorf("%w: environment_status must be stopped or failed", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	if len(immutable.Repositories) == 0 || len(immutable.Repositories) > ArchivedResourceEnvironmentRetirementMaxRepositories {
		return fmt.Errorf("%w: repositories count must be between 1 and %d", ErrArchivedResourceEnvironmentRetirementInvalid, ArchivedResourceEnvironmentRetirementMaxRepositories)
	}
	seen := make(map[string]struct{}, len(immutable.Repositories))
	for i, repo := range immutable.Repositories {
		if i > 0 && !retirementRepositoryLess(immutable.Repositories[i-1], repo) {
			return fmt.Errorf("%w: repositories are not canonically sorted", ErrArchivedResourceEnvironmentRetirementInvalid)
		}
		if _, ok := seen[repo.ID]; ok {
			return fmt.Errorf("%w: duplicate repository row id", ErrArchivedResourceEnvironmentRetirementInvalid)
		}
		seen[repo.ID] = struct{}{}
		if err := validateRetirementRepository(repo); err != nil {
			return err
		}
	}
	return nil
}

func validateRetirementRepository(repo ArchivedResourceEnvironmentRetirementRepository) error {
	for name, value := range map[string]string{
		"id":              repo.ID,
		"repository_id":   repo.RepositoryID,
		"worktree_id":     repo.WorktreeID,
		"worktree_branch": repo.WorktreeBranch,
		"status":          repo.Status,
	} {
		if err := validateRetirementOpaque(name, value); err != nil {
			return err
		}
	}
	if err := validateRetirementOptionalOpaque("branch_slug", repo.BranchSlug); err != nil {
		return err
	}
	if repo.WorktreePath != "" {
		if err := validateRetirementCanonicalAbsolutePath("worktree_path", repo.WorktreePath); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{
		"created_at": repo.CreatedAt,
		"updated_at": repo.UpdatedAt,
	} {
		if err := validateRetirementCanonicalUTC(name, value); err != nil {
			return err
		}
	}
	if repo.MergedAt != "" {
		if err := validateRetirementCanonicalUTC("merged_at", repo.MergedAt); err != nil {
			return err
		}
	}
	if repo.DeletedAt != "" {
		if err := validateRetirementCanonicalUTC("deleted_at", repo.DeletedAt); err != nil {
			return err
		}
	}
	switch repo.Status {
	case "active", "merged", "deleted":
	default:
		return fmt.Errorf("%w: repository status must be active, merged, or deleted", ErrArchivedResourceEnvironmentRetirementInvalid)
	}
	return nil
}

func validateArchivedResourceEnvironmentRetirementProof(proof ArchivedResourceEnvironmentRetirementProof) error {
	if err := validateRetirementCanonicalUTC("retired_at", proof.RetiredAt); err != nil {
		return err
	}
	return nil
}

func retirementRepositoryLess(left, right ArchivedResourceEnvironmentRetirementRepository) bool {
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	if left.RepositoryID != right.RepositoryID {
		return left.RepositoryID < right.RepositoryID
	}
	if left.WorktreeID != right.WorktreeID {
		return left.WorktreeID < right.WorktreeID
	}
	return left.CreatedAt < right.CreatedAt
}

func validateRetirementOpaque(name, value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%w: %s is empty or not trimmed", ErrArchivedResourceEnvironmentRetirementInvalid, name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a control character", ErrArchivedResourceEnvironmentRetirementInvalid, name)
		}
	}
	return nil
}

func validateRetirementOptionalOpaque(name, value string) error {
	if value == "" {
		return nil
	}
	return validateRetirementOpaque(name, value)
}

func validateRetirementCanonicalAbsolutePath(name, value string) error {
	if !utf8.ValidString(value) || value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value ||
		filepath.Dir(value) == value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%w: %s must be a canonical absolute path", ErrArchivedResourceEnvironmentRetirementInvalid, name)
	}
	return nil
}

func validateRetirementCanonicalUTC(name, value string) error {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return fmt.Errorf("%w: %s must be canonical RFC3339Nano UTC", ErrArchivedResourceEnvironmentRetirementInvalid, name)
	}
	return nil
}

func rejectDuplicateRetirementJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkRetirementJSONValue(decoder); err != nil {
		return fmt.Errorf("%w: %v", ErrArchivedResourceEnvironmentRetirementInvalid, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %v", err)
	}
	return nil
}

func walkRetirementJSONValue(decoder *json.Decoder) error {
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
			if err := walkRetirementJSONValue(decoder); err != nil {
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
			if err := walkRetirementJSONValue(decoder); err != nil {
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

func requireRetirementJSONEOF(decoder *json.Decoder) error {
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("%w: trailing JSON: %v", ErrArchivedResourceEnvironmentRetirementInvalid, err)
		}
		return fmt.Errorf("%w: trailing JSON value %v", ErrArchivedResourceEnvironmentRetirementInvalid, token)
	}
	return nil
}
