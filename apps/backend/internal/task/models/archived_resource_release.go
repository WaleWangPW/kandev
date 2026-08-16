package models

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ArchivedResourceReleaseSnapshotVersion = 2
	ArchivedResourceReleaseSnapshotKind    = "archived_resource_release"
	ArchivedResourceReleaseSnapshotPhase   = "db_unbound_released"
)

const ArchivedResourceReleaseMaxSnapshotBytes = 16 * 1024

var (
	ErrArchivedResourceReleaseInvalid  = errors.New("archived resource release snapshot is invalid")
	ErrArchivedResourceReleaseTooLarge = errors.New("archived resource release snapshot exceeds size limit")
)

// ArchivedResourceReleaseSnapshot is the canonical, durable, metadata-only
// admission request for absent-target release. It binds exactly one retained
// v2 anchor (operation_id + digest + task + target identity) to the canonical
// physical path. The release admission proves the physical path is absent
// from inventory and the Git worktree registration is absent before the
// anchor transitions from retained to released.
type ArchivedResourceReleaseSnapshot struct {
	SchemaVersion int                                 `json:"schema_version"`
	Kind          string                              `json:"kind"`
	Phase         string                              `json:"phase"`
	Immutable     ArchivedResourceReleaseImmutable    `json:"immutable"`
	Release       ArchivedResourceReleaseReleaseProof `json:"release"`
}

type ArchivedResourceReleaseImmutable struct {
	AnchorOperationID  string `json:"anchor_operation_id"`
	AnchorDigest       string `json:"anchor_digest"`
	AnchorTaskID       string `json:"anchor_task_id"`
	AnchorWorktreeID   string `json:"anchor_worktree_id"`
	AnchorRepository   string `json:"anchor_repository_id"`
	AnchorBranch       string `json:"anchor_branch"`
	AnchorHeadOID      string `json:"anchor_head_oid"`
	AnchorWorktreePath string `json:"anchor_worktree_path"`
	AnchorGitCommonDir string `json:"anchor_git_common_dir"`
}

// ArchivedResourceReleaseReleaseProof is the sealed admission proof that the
// retained target is absent. PhysicalPath is the canonical absolute path the
// admission verifies; GitWorktreeRegistration is the exact Git worktree
// registration (path + branch + head_oid) that admission verifies as absent.
// ReleasedAt is the RFC3339Nano UTC stamp the released anchor will carry.
type ArchivedResourceReleaseReleaseProof struct {
	PhysicalPath            string                                 `json:"physical_path"`
	GitWorktreeRegistration ArchivedResourceReleaseGitRegistration `json:"git_worktree_registration"`
	ReleasedAt              string                                 `json:"released_at"`
}

type ArchivedResourceReleaseGitRegistration struct {
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	HeadOID      string `json:"head_oid"`
}

// ArchivedResourceReleaseIdentity is the redundant header set bound alongside
// the resource_snapshot. Every field is derived from canonical snapshot bytes
// so an admission cannot self-sign identity without re-encoding.
type ArchivedResourceReleaseIdentity struct {
	SnapshotDigest        string
	AnchorOperationID     string
	AnchorDigest          string
	OperationID           string
	ActiveScopeKey        string
	ResourceKind          string
	ResourceID            string
	ManagedRootKey        string
	CanonicalWorktreePath string
}

// ArchivedResourceReleaseAdmission is the durable receipt returned by the
// release writer after a successful admission. Reason is metadata only.
type ArchivedResourceReleaseAdmission struct {
	Job    *TaskResourceCleanupJob
	Reason string
}

// NewArchivedResourceReleaseSnapshot validates and canonicalizes the
// metadata-only release request. The returned raw bytes are the only accepted
// representation for the resource_snapshot column.
func NewArchivedResourceReleaseSnapshot(
	immutable ArchivedResourceReleaseImmutable,
	release ArchivedResourceReleaseReleaseProof,
) (ArchivedResourceReleaseSnapshot, []byte, ArchivedResourceReleaseIdentity, error) {
	if err := validateArchivedResourceReleaseImmutable(immutable); err != nil {
		return ArchivedResourceReleaseSnapshot{}, nil, ArchivedResourceReleaseIdentity{}, err
	}
	if err := validateArchivedResourceReleaseProof(release); err != nil {
		return ArchivedResourceReleaseSnapshot{}, nil, ArchivedResourceReleaseIdentity{}, err
	}
	if immutable.AnchorWorktreePath != release.PhysicalPath {
		return ArchivedResourceReleaseSnapshot{}, nil, ArchivedResourceReleaseIdentity{}, fmt.Errorf("%w: physical path does not bind anchor worktree_path", ErrArchivedResourceReleaseInvalid)
	}
	if immutable.AnchorWorktreePath != release.GitWorktreeRegistration.WorktreePath ||
		immutable.AnchorBranch != release.GitWorktreeRegistration.Branch ||
		immutable.AnchorHeadOID != release.GitWorktreeRegistration.HeadOID {
		return ArchivedResourceReleaseSnapshot{}, nil, ArchivedResourceReleaseIdentity{}, fmt.Errorf("%w: git registration does not bind anchor target", ErrArchivedResourceReleaseInvalid)
	}
	snapshot := ArchivedResourceReleaseSnapshot{
		SchemaVersion: ArchivedResourceReleaseSnapshotVersion,
		Kind:          ArchivedResourceReleaseSnapshotKind,
		Phase:         ArchivedResourceReleaseSnapshotPhase,
		Immutable:     immutable,
		Release:       release,
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ArchivedResourceReleaseSnapshot{}, nil, ArchivedResourceReleaseIdentity{}, fmt.Errorf("%w: encode snapshot: %v", ErrArchivedResourceReleaseInvalid, err)
	}
	if len(raw) > ArchivedResourceReleaseMaxSnapshotBytes {
		return ArchivedResourceReleaseSnapshot{}, nil, ArchivedResourceReleaseIdentity{}, ErrArchivedResourceReleaseTooLarge
	}
	identity := archivedResourceReleaseIdentity(snapshot, raw)
	return snapshot, raw, identity, nil
}

// DecodeArchivedResourceReleaseSnapshot is the strict decoder used to read a
// previously persisted release snapshot from the database.
func DecodeArchivedResourceReleaseSnapshot(
	raw []byte,
) (ArchivedResourceReleaseSnapshot, ArchivedResourceReleaseIdentity, error) {
	if len(raw) == 0 {
		return ArchivedResourceReleaseSnapshot{}, ArchivedResourceReleaseIdentity{}, fmt.Errorf("%w: empty document", ErrArchivedResourceReleaseInvalid)
	}
	if len(raw) > ArchivedResourceReleaseMaxSnapshotBytes {
		return ArchivedResourceReleaseSnapshot{}, ArchivedResourceReleaseIdentity{}, ErrArchivedResourceReleaseTooLarge
	}
	if err := rejectDuplicateReleaseJSONKeys(raw); err != nil {
		return ArchivedResourceReleaseSnapshot{}, ArchivedResourceReleaseIdentity{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot ArchivedResourceReleaseSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return ArchivedResourceReleaseSnapshot{}, ArchivedResourceReleaseIdentity{}, fmt.Errorf("%w: decode: %v", ErrArchivedResourceReleaseInvalid, err)
	}
	if err := requireReleaseJSONEOF(decoder); err != nil {
		return ArchivedResourceReleaseSnapshot{}, ArchivedResourceReleaseIdentity{}, err
	}
	if err := validateArchivedResourceReleaseSnapshot(snapshot); err != nil {
		return ArchivedResourceReleaseSnapshot{}, ArchivedResourceReleaseIdentity{}, err
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return ArchivedResourceReleaseSnapshot{}, ArchivedResourceReleaseIdentity{}, fmt.Errorf("%w: re-encode: %v", ErrArchivedResourceReleaseInvalid, err)
	}
	if !bytes.Equal(raw, canonical) {
		return ArchivedResourceReleaseSnapshot{}, ArchivedResourceReleaseIdentity{}, fmt.Errorf("%w: document is not canonical", ErrArchivedResourceReleaseInvalid)
	}
	return snapshot, archivedResourceReleaseIdentity(snapshot, raw), nil
}

// ArchivedResourceReleaseManagedRootKey derives the canonical managed root
// identity from the anchor's canonical worktree_path.
func ArchivedResourceReleaseManagedRootKey(worktreePath string) (string, error) {
	if err := validateReleaseCanonicalAbsolutePath("worktree_path", worktreePath); err != nil {
		return "", err
	}
	return "git_worktree:" + sha256Hex([]byte(worktreePath)), nil
}

func archivedResourceReleaseIdentity(
	snapshot ArchivedResourceReleaseSnapshot,
	raw []byte,
) ArchivedResourceReleaseIdentity {
	digest := sha256Hex(raw)
	managedRootKey, _ := ArchivedResourceReleaseManagedRootKey(snapshot.Immutable.AnchorWorktreePath)
	return ArchivedResourceReleaseIdentity{
		SnapshotDigest:        digest,
		AnchorOperationID:     snapshot.Immutable.AnchorOperationID,
		AnchorDigest:          snapshot.Immutable.AnchorDigest,
		OperationID:           "archived-resource-release:" + digest,
		ActiveScopeKey:        "archived-resource-release:" + sha256Hex([]byte(snapshot.Immutable.AnchorOperationID+"\x00"+snapshot.Immutable.AnchorDigest+"\x00"+digest)),
		ResourceKind:          ArchivedResourceReconcileResourceKind,
		ResourceID:            snapshot.Immutable.AnchorWorktreeID,
		ManagedRootKey:        managedRootKey,
		CanonicalWorktreePath: snapshot.Immutable.AnchorWorktreePath,
	}
}

func validateArchivedResourceReleaseSnapshot(snapshot ArchivedResourceReleaseSnapshot) error {
	if snapshot.SchemaVersion != ArchivedResourceReleaseSnapshotVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrArchivedResourceReleaseInvalid, ArchivedResourceReleaseSnapshotVersion)
	}
	if snapshot.Kind != ArchivedResourceReleaseSnapshotKind {
		return fmt.Errorf("%w: unexpected kind", ErrArchivedResourceReleaseInvalid)
	}
	if snapshot.Phase != ArchivedResourceReleaseSnapshotPhase {
		return fmt.Errorf("%w: unexpected phase", ErrArchivedResourceReleaseInvalid)
	}
	if err := validateArchivedResourceReleaseImmutable(snapshot.Immutable); err != nil {
		return err
	}
	if err := validateArchivedResourceReleaseProof(snapshot.Release); err != nil {
		return err
	}
	if snapshot.Immutable.AnchorWorktreePath != snapshot.Release.PhysicalPath ||
		snapshot.Immutable.AnchorWorktreePath != snapshot.Release.GitWorktreeRegistration.WorktreePath ||
		snapshot.Immutable.AnchorBranch != snapshot.Release.GitWorktreeRegistration.Branch ||
		snapshot.Immutable.AnchorHeadOID != snapshot.Release.GitWorktreeRegistration.HeadOID {
		return fmt.Errorf("%w: release proof does not bind anchor identity", ErrArchivedResourceReleaseInvalid)
	}
	return nil
}

func validateArchivedResourceReleaseImmutable(immutable ArchivedResourceReleaseImmutable) error {
	for name, value := range map[string]string{
		"anchor_operation_id":   immutable.AnchorOperationID,
		"anchor_digest":         immutable.AnchorDigest,
		"anchor_task_id":        immutable.AnchorTaskID,
		"anchor_worktree_id":    immutable.AnchorWorktreeID,
		"anchor_repository_id":  immutable.AnchorRepository,
		"anchor_branch":         immutable.AnchorBranch,
		"anchor_head_oid":       immutable.AnchorHeadOID,
		"anchor_worktree_path":  immutable.AnchorWorktreePath,
		"anchor_git_common_dir": immutable.AnchorGitCommonDir,
	} {
		if err := validateReleaseOpaque(name, value); err != nil {
			return err
		}
	}
	if err := validateReleaseCanonicalAbsolutePath("anchor_worktree_path", immutable.AnchorWorktreePath); err != nil {
		return err
	}
	if err := validateReleaseCanonicalAbsolutePath("anchor_git_common_dir", immutable.AnchorGitCommonDir); err != nil {
		return err
	}
	if len(immutable.AnchorHeadOID) != 40 || strings.ToLower(immutable.AnchorHeadOID) != immutable.AnchorHeadOID {
		return fmt.Errorf("%w: anchor_head_oid must be 40 lowercase hex characters", ErrArchivedResourceReleaseInvalid)
	}
	if _, err := hex.DecodeString(immutable.AnchorHeadOID); err != nil {
		return fmt.Errorf("%w: anchor_head_oid must be hexadecimal", ErrArchivedResourceReleaseInvalid)
	}
	return nil
}

func validateArchivedResourceReleaseProof(release ArchivedResourceReleaseReleaseProof) error {
	if err := validateReleaseCanonicalAbsolutePath("physical_path", release.PhysicalPath); err != nil {
		return err
	}
	if release.PhysicalPath != release.GitWorktreeRegistration.WorktreePath {
		return fmt.Errorf("%w: git worktree_path must equal physical_path", ErrArchivedResourceReleaseInvalid)
	}
	for name, value := range map[string]string{
		"git_branch":   release.GitWorktreeRegistration.Branch,
		"git_head_oid": release.GitWorktreeRegistration.HeadOID,
	} {
		if err := validateReleaseOpaque(name, value); err != nil {
			return err
		}
	}
	if len(release.GitWorktreeRegistration.HeadOID) != 40 ||
		strings.ToLower(release.GitWorktreeRegistration.HeadOID) != release.GitWorktreeRegistration.HeadOID {
		return fmt.Errorf("%w: git registration head_oid must be 40 lowercase hex characters", ErrArchivedResourceReleaseInvalid)
	}
	if _, err := hex.DecodeString(release.GitWorktreeRegistration.HeadOID); err != nil {
		return fmt.Errorf("%w: git registration head_oid must be hexadecimal", ErrArchivedResourceReleaseInvalid)
	}
	if err := validateReleaseCanonicalUTC("released_at", release.ReleasedAt); err != nil {
		return err
	}
	return nil
}

func validateReleaseOpaque(name, value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%w: %s is empty or not trimmed", ErrArchivedResourceReleaseInvalid, name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a control character", ErrArchivedResourceReleaseInvalid, name)
		}
	}
	return nil
}

func validateReleaseCanonicalAbsolutePath(name, value string) error {
	if !utf8.ValidString(value) || value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value ||
		filepath.Dir(value) == value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%w: %s must be a canonical absolute path", ErrArchivedResourceReleaseInvalid, name)
	}
	return nil
}

func validateReleaseCanonicalUTC(name, value string) error {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return fmt.Errorf("%w: %s must be canonical RFC3339Nano UTC", ErrArchivedResourceReleaseInvalid, name)
	}
	return nil
}

func rejectDuplicateReleaseJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkReleaseJSONValue(decoder); err != nil {
		return fmt.Errorf("%w: %v", ErrArchivedResourceReleaseInvalid, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %v", err)
	}
	return nil
}

func walkReleaseJSONValue(decoder *json.Decoder) error {
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
			if err := walkReleaseJSONValue(decoder); err != nil {
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
			if err := walkReleaseJSONValue(decoder); err != nil {
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

func requireReleaseJSONEOF(decoder *json.Decoder) error {
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("%w: trailing JSON: %v", ErrArchivedResourceReleaseInvalid, err)
		}
		return fmt.Errorf("%w: trailing JSON value %v", ErrArchivedResourceReleaseInvalid, token)
	}
	return nil
}
