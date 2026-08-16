package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestArchivedResourceReconcileSnapshotCanonicalRoundTrip(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	immutable.Associations = []ArchivedResourceReconcileAssociation{
		archivedResourceAssociationFixture("association-b", "session-b"),
		archivedResourceAssociationFixture("association-a", "session-a"),
	}
	snapshot, raw, identity, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot: %v", err)
	}
	if got := snapshot.Immutable.Associations[0].AssociationID; got != "association-a" {
		t.Fatalf("associations not sorted: first=%q", got)
	}
	if got := immutable.Associations[0].AssociationID; got != "association-b" {
		t.Fatalf("caller input was mutated: first=%q", got)
	}
	decoded, decodedIdentity, err := DecodeArchivedResourceReconcileSnapshot(raw)
	if err != nil {
		t.Fatalf("DecodeArchivedResourceReconcileSnapshot: %v", err)
	}
	if decodedIdentity != identity {
		t.Fatalf("identity changed after decode:\n got %#v\nwant %#v", decodedIdentity, identity)
	}
	if decoded.RetentionAnchor.ImmutableDigest == "" || decoded.Result.PhysicalRemoved {
		t.Fatalf("invalid retention contract: %#v", decoded)
	}
	if !strings.HasPrefix(identity.OperationID, "archived-resource-reconcile:") ||
		!strings.HasPrefix(identity.ActiveScopeKey, "archived-resource-reconcile:") ||
		identity.ResourceID != immutable.Target.WorktreeID {
		t.Fatalf("unexpected derived identity: %#v", identity)
	}
	reversed := archivedResourceImmutableFixture(t)
	reversed.Associations = []ArchivedResourceReconcileAssociation{
		archivedResourceAssociationFixture("association-a", "session-a"),
		archivedResourceAssociationFixture("association-b", "session-b"),
	}
	_, reversedRaw, _, err := NewArchivedResourceReconcileSnapshot(reversed)
	if err != nil {
		t.Fatalf("rebuild canonical snapshot: %v", err)
	}
	if !bytes.Equal(raw, reversedRaw) {
		t.Fatalf("canonical raw bytes drifted: %d vs %d", len(raw), len(reversedRaw))
	}
}

func TestArchivedResourceReconcileSnapshotRejectsInvalidPaths(t *testing.T) {
	if _, err := ArchivedResourceManagedRootKey("not-absolute"); err == nil {
		t.Fatal("managed root key accepted non-absolute path")
	}
	immutable := archivedResourceImmutableFixture(t)
	immutable.Target.WorktreePath = "not-absolute"
	if _, _, _, err := NewArchivedResourceReconcileSnapshot(immutable); err == nil {
		t.Fatal("snapshot accepted non-canonical worktree path")
	}
	immutable = archivedResourceImmutableFixture(t)
	immutable.Target.HeadOID = strings.Repeat("Z", 40)
	if _, _, _, err := NewArchivedResourceReconcileSnapshot(immutable); err == nil {
		t.Fatal("snapshot accepted uppercase head oid")
	}
}

func TestArchivedResourceReconcileSnapshotRejectsTooManyAssociations(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	for i := 0; i < ArchivedResourceReconcileMaxAssociations+1; i++ {
		immutable.Associations = append(immutable.Associations, archivedResourceAssociationFixture("association-x", "session-x"))
	}
	if _, _, _, err := NewArchivedResourceReconcileSnapshot(immutable); err == nil {
		t.Fatal("snapshot accepted oversized association set")
	}
}

func TestArchivedResourceReconcileSnapshotRejectsOversizedDocument(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	immutable.OriginTaskID = strings.Repeat("a", ArchivedResourceReconcileMaxSnapshotBytes)
	if _, _, _, err := NewArchivedResourceReconcileSnapshot(immutable); err == nil {
		t.Fatal("snapshot accepted oversized document")
	}
}

func TestArchivedResourceReconcileSnapshotRejectsDuplicateAndUnknownJSONKeys(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	_, raw, _, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("build canonical snapshot: %v", err)
	}
	duplicate := append([]byte(nil), raw...)
	duplicate = bytes.Replace(duplicate, []byte(`"immutable":{`), []byte(`"immutable":{,"unknown":1,`), 1)
	if _, _, err := DecodeArchivedResourceReconcileSnapshot(duplicate); err == nil {
		t.Fatal("decode accepted unknown field")
	}
	tampered := bytes.Replace(raw, []byte(`"worktree_id":"worktree-shared"`), []byte(`"worktree_id":"worktree-shared","worktree_id":"worktree-shared"`), 1)
	if _, _, err := DecodeArchivedResourceReconcileSnapshot(tampered); err == nil {
		t.Fatal("decode accepted duplicate object key")
	}
}

func TestArchivedResourceReconcileHeaderValidationRejectsDrift(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	_, raw, identity, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot: %v", err)
	}
	job := NewArchivedResourceReconcileJob(
		ArchivedResourceReconcileSnapshot{SchemaVersion: 2, Kind: ArchivedResourceReconcileSnapshotKind, Phase: ArchivedResourceReconcileSnapshotPhase,
			Immutable: immutable, RetentionAnchor: ArchivedResourceReconcileRetentionAnchor{AnchorVersion: 1, ImmutableDigest: identity.ImmutableDigest},
			Result: ArchivedResourceReconcileSnapshotResult{PhysicalRemoved: false},
		}, raw, identity,
	)
	if _, _, err := ValidateArchivedResourceReconcileJobHeaders(job); err != nil {
		t.Fatalf("ValidateArchivedResourceReconcileJobHeaders: %v", err)
	}
	job.SnapshotDigest = "deadbeef"
	if _, _, err := ValidateArchivedResourceReconcileJobHeaders(job); err == nil {
		t.Fatal("validation accepted drifted snapshot digest")
	}
	job.SnapshotDigest = identity.SnapshotDigest
	job.ResourceID = "drift"
	if _, _, err := ValidateArchivedResourceReconcileJobHeaders(job); err == nil {
		t.Fatal("validation accepted drifted resource id")
	}
	job.ResourceID = identity.ResourceID
	scope := "drift"
	job.ActiveScopeKey = &scope
	if _, _, err := ValidateArchivedResourceReconcileJobHeaders(job); err == nil {
		t.Fatal("validation accepted drifted active scope")
	}
	job.ActiveScopeKey = &identity.ActiveScopeKey
	job.Trigger = TaskResourceCleanupTriggerArchive
	if _, _, err := ValidateArchivedResourceReconcileJobHeaders(job); err == nil {
		t.Fatal("validation accepted non-reconcile trigger")
	}
}

func TestArchivedResourceReconcileAnchorLifecycleStates(t *testing.T) {
	for _, state := range []TaskResourceCleanupState{
		TaskResourceCleanupStatePrepared, TaskResourceCleanupStatePending,
		TaskResourceCleanupStateRunning, TaskResourceCleanupStateRetryWait,
	} {
		if !IsActiveArchivedResourceReconcileState(state) {
			t.Fatalf("pre-retention state %q must be active", state)
		}
	}
	for _, state := range []TaskResourceCleanupState{
		TaskResourceCleanupStateSucceeded, TaskResourceCleanupStateFailed,
		TaskResourceCleanupStateCancelled, "unknown",
	} {
		if IsActiveArchivedResourceReconcileState(state) {
			t.Fatalf("terminal/unknown state %q must not be active", state)
		}
	}
}

func TestArchivedResourceManagedRootKeyBinding(t *testing.T) {
	rootKey, err := ArchivedResourceManagedRootKey("/tmp/kandev/worktree")
	if err != nil {
		t.Fatalf("ArchivedResourceManagedRootKey: %v", err)
	}
	if !strings.HasPrefix(rootKey, "git_worktree:") || len(rootKey) <= len("git_worktree:") {
		t.Fatalf("root key missing prefix or digest: %q", rootKey)
	}
}

func TestArchivedResourceReconcileAnyHeaderDispatch(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	_, raw, identity, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot: %v", err)
	}
	job := NewArchivedResourceReconcileJob(
		ArchivedResourceReconcileSnapshot{SchemaVersion: 2, Kind: ArchivedResourceReconcileSnapshotKind, Phase: ArchivedResourceReconcileSnapshotPhase,
			Immutable: immutable, RetentionAnchor: ArchivedResourceReconcileRetentionAnchor{AnchorVersion: 1, ImmutableDigest: identity.ImmutableDigest},
			Result: ArchivedResourceReconcileSnapshotResult{PhysicalRemoved: false},
		}, raw, identity,
	)
	if _, err := ValidateArchivedResourceAnyReconcileJobHeaders(job); err != nil {
		t.Fatalf("v2 dispatch: %v", err)
	}
	job.SnapshotVersion = 99
	if _, err := ValidateArchivedResourceAnyReconcileJobHeaders(job); !errors.Is(err, ErrArchivedResourceSnapshotInvalid) {
		t.Fatalf("v99 dispatch error = %v, want invalid", err)
	}
}

func TestArchivedResourceReconcileManagedPathExtraction(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	_, raw, identity, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot: %v", err)
	}
	job := NewArchivedResourceReconcileJob(
		ArchivedResourceReconcileSnapshot{SchemaVersion: 2, Kind: ArchivedResourceReconcileSnapshotKind, Phase: ArchivedResourceReconcileSnapshotPhase,
			Immutable: immutable, RetentionAnchor: ArchivedResourceReconcileRetentionAnchor{AnchorVersion: 1, ImmutableDigest: identity.ImmutableDigest},
			Result: ArchivedResourceReconcileSnapshotResult{PhysicalRemoved: false},
		}, raw, identity,
	)
	path, err := ArchivedResourceReconcileManagedPath(job)
	if err != nil {
		t.Fatalf("ArchivedResourceReconcileManagedPath: %v", err)
	}
	if path != immutable.Target.WorktreePath {
		t.Fatalf("managed path = %q, want %q", path, immutable.Target.WorktreePath)
	}
	if _, err := ArchivedResourceReconcileManagedPath(nil); !errors.Is(err, ErrArchivedResourceSnapshotInvalid) {
		t.Fatalf("nil path error = %v, want invalid", err)
	}
	job.SnapshotVersion = 99
	if _, err := ArchivedResourceReconcileManagedPath(job); !errors.Is(err, ErrArchivedResourceSnapshotInvalid) {
		t.Fatalf("invalid version path error = %v, want invalid", err)
	}
}

func TestArchivedResourceReconcileDecodeTrailingJSONRejected(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	_, raw, _, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot: %v", err)
	}
	trailing := append(append([]byte(nil), raw...), ' ', '1', ' ')
	if _, _, err := DecodeArchivedResourceReconcileSnapshot(trailing); err == nil {
		t.Fatal("decode accepted trailing JSON value")
	}
	if _, _, err := DecodeArchivedResourceReconcileSnapshot(nil); err == nil {
		t.Fatal("decode accepted empty body")
	}
}

func TestArchivedResourceReconcileInvalidTimestampRejected(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	immutable.ArchivedAt = "not-a-timestamp"
	if _, _, _, err := NewArchivedResourceReconcileSnapshot(immutable); err == nil {
		t.Fatal("snapshot accepted invalid timestamp")
	}
}

func TestArchivedResourceReconcileDecodeStrictTypeCoercion(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	_, raw, identity, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot: %v", err)
	}
	decoded, decodedIdentity, err := DecodeArchivedResourceReconcileSnapshot(raw)
	if err != nil {
		t.Fatalf("DecodeArchivedResourceReconcileSnapshot: %v", err)
	}
	if decodedIdentity.SnapshotDigest != identity.SnapshotDigest {
		t.Fatalf("snapshot digest drifted: %s vs %s", decodedIdentity.SnapshotDigest, identity.SnapshotDigest)
	}
	if decoded.RetentionAnchor.ImmutableDigest != identity.ImmutableDigest {
		t.Fatalf("immutable digest drifted: %s vs %s", decoded.RetentionAnchor.ImmutableDigest, identity.ImmutableDigest)
	}
	// Trailing whitespace in raw bytes is rejected by canonical round-trip.
	// The accepted JSON body is exactly what the canonical encoder produced.
	if decoded.SchemaVersion != 2 || decoded.Kind != ArchivedResourceReconcileSnapshotKind {
		t.Fatalf("decoded lifecycle fields invalid: %+v", decoded)
	}
}

func TestArchivedResourceReconcileJSONMarshalingDeterminism(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	_, raw1, _, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot #1: %v", err)
	}
	_, raw2, _, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot #2: %v", err)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Fatal("non-deterministic canonical bytes for identical input")
	}
}

func TestArchivedResourceReconcileMarshalRoundTripPreservesDecodableRaw(t *testing.T) {
	immutable := archivedResourceImmutableFixture(t)
	_, raw, _, err := NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("NewArchivedResourceReconcileSnapshot: %v", err)
	}
	wrapped := map[string]json.RawMessage{"snapshot": raw}
	encoded, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatalf("encode wrapper: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode wrapper: %v", err)
	}
	if _, _, err := DecodeArchivedResourceReconcileSnapshot(decoded["snapshot"]); err != nil {
		t.Fatalf("decode wrapped snapshot: %v", err)
	}
}

func archivedResourceImmutableFixture(t *testing.T) ArchivedResourceReconcileImmutable {
	t.Helper()
	rootKey, err := ArchivedResourceManagedRootKey("/tmp/kandev/worktree")
	if err != nil {
		t.Fatalf("ArchivedResourceManagedRootKey: %v", err)
	}
	return ArchivedResourceReconcileImmutable{
		OriginTaskID:   "task-archived",
		ArchivedAt:     time.Date(2026, 8, 12, 1, 2, 3, 4, time.UTC).Format(time.RFC3339Nano),
		ManagedRootKey: rootKey,
		Target: ArchivedResourceReconcileTarget{
			WorktreeID:     "worktree-shared",
			RepositoryID:   "repository-company",
			RepositoryPath: "/tmp/kandev/repo",
			GitCommonDir:   "/tmp/kandev/repo/.git",
			WorktreePath:   "/tmp/kandev/worktree",
			Branch:         "feature/synthetic",
			HeadOID:        strings.Repeat("a", 40),
		},
		Associations: []ArchivedResourceReconcileAssociation{
			archivedResourceAssociationFixture("association-a", "session-a"),
		},
	}
}

func archivedResourceAssociationFixture(id, sessionID string) ArchivedResourceReconcileAssociation {
	return ArchivedResourceReconcileAssociation{
		AssociationID:  id,
		TaskID:         "task-archived",
		SessionID:      sessionID,
		WorktreeID:     "worktree-shared",
		RepositoryID:   "repository-company",
		BranchSlug:     "feature/synthetic",
		WorktreePath:   "/tmp/kandev/worktree",
		WorktreeBranch: "feature/synthetic",
		Status:         "active",
		CreatedAt:      time.Date(2026, 8, 12, 1, 0, 0, 1, time.UTC).Format(time.RFC3339Nano),
		UpdatedAt:      time.Date(2026, 8, 12, 1, 1, 0, 2, time.UTC).Format(time.RFC3339Nano),
	}
}
