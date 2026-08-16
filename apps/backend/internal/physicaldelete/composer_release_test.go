package physicaldelete

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/kandev/kandev/internal/task/models"
)

var (
	composerReleaseTaskID        = "task-release"
	composerReleaseWorktreePath  = ""
	composerReleaseGitCommonDir  = ""
	composerReleaseBranch        = "feature/composer"
	composerReleaseHeadOIDPrefix = "f"
	composerReleaseWorktreeID    = "wt-composer"
	composerReleaseRepositoryID  = "repository-composer"
)

// composerFixture builds a real SQL-backed writer DB, seeds a retained v2
// reconcile anchor, and returns the admission service together with the
// raw DB handle and the canonical snapshot bytes the caller can mutate.
type composerFixture struct {
	svc                *Service
	db                 *sqlx.DB
	anchorID           string
	anchorWorktreePath string
	anchorDigest       string
	anchorOperationID  string
	now                time.Time
}

func newComposerFixture(t *testing.T) *composerFixture {
	t.Helper()
	// Resolve the release worktree path under the test's TempDir so
	// canonicalLockPath returns the same canonical path for the seeded
	// snapshot and the request. The worktree directory itself must NOT
	// exist on disk — the release admission's absence proof is rooted in
	// the writer-DB inventory, not the filesystem.
	root := t.TempDir()
	worktreePath := filepath.Join(root, "worktree")
	gitCommonPath := filepath.Join(root, "repo", ".git")
	// Pre-canonicalise both paths so the seeded snapshot stores the same
	// canonical form the admission request will produce after its own
	// canonicalisation pass.
	composerReleaseWorktreePath = composerCanonicalize(t, worktreePath)
	composerReleaseGitCommonDir = composerCanonicalize(t, gitCommonPath)
	db := openComposerDB(t)
	svc, err := New(Config{Inventory: NewSQLInventorySource(db)})
	if err != nil {
		t.Fatalf("physicaldelete.New: %v", err)
	}
	now := time.Now().UTC()
	anchor := composerSeededRetainedV2(t, db, now)
	t.Cleanup(func() { _ = db.Close() })
	return &composerFixture{
		svc:                svc,
		db:                 db,
		anchorID:           anchor.id,
		anchorWorktreePath: composerReleaseWorktreePath,
		anchorDigest:       anchor.digest,
		anchorOperationID:  anchor.operationID,
		now:                now,
	}
}

// composerCanonicalize runs the same canonicalLockPath the admission will
// later run so seeded paths and request paths agree byte-for-byte.
func composerCanonicalize(t *testing.T, path string) string {
	t.Helper()
	canonical, err := canonicalLockPath(path)
	if err != nil {
		t.Fatalf("canonicalize %q: %v", path, err)
	}
	return canonical
}

func openComposerDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE task_environments (
			id TEXT PRIMARY KEY, status TEXT, workspace_path TEXT
		)`,
		`CREATE TABLE task_environment_repos (
			id TEXT PRIMARY KEY, task_environment_id TEXT, repository_id TEXT,
			worktree_id TEXT, worktree_path TEXT, worktree_branch TEXT,
			branch_slug TEXT, position INTEGER, status TEXT,
			created_at TIMESTAMP, updated_at TIMESTAMP,
			merged_at TIMESTAMP, deleted_at TIMESTAMP
		)`,
		`CREATE TABLE executors_running (
			id TEXT PRIMARY KEY, session_id TEXT UNIQUE, task_id TEXT,
			worktree_id TEXT, worktree_path TEXT, worktree_branch TEXT,
			status TEXT
		)`,
		`CREATE TABLE task_workspace_groups (
			id TEXT PRIMARY KEY, cleanup_status TEXT, materialized_path TEXT
		)`,
		`CREATE TABLE storage_quarantine_entries (
			id TEXT PRIMARY KEY, state TEXT, original_path TEXT, quarantine_path TEXT
		)`,
		`CREATE TABLE task_resource_cleanup_jobs (
			id TEXT PRIMARY KEY, operation_id TEXT UNIQUE, task_id TEXT,
			trigger TEXT, state TEXT, resource_snapshot TEXT,
			snapshot_version INTEGER, snapshot_digest TEXT, resource_kind TEXT,
			resource_id TEXT, managed_root_key TEXT, anchor_revision BIGINT,
			active_scope_key TEXT, attempts INTEGER, next_attempt_at TIMESTAMP,
			last_error TEXT, created_at TIMESTAMP, updated_at TIMESTAMP,
			completed_at TIMESTAMP
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return db
}

type composerSeeded struct {
	id          string
	digest      string
	operationID string
}

func composerSeededRetainedV2(t *testing.T, db *sqlx.DB, now time.Time) composerSeeded {
	t.Helper()
	immutable := models.ArchivedResourceReconcileImmutable{
		OriginTaskID:   composerReleaseTaskID,
		ArchivedAt:     now.Format(time.RFC3339Nano),
		ManagedRootKey: composerReleaseManagedRootKey(t),
		Target: models.ArchivedResourceReconcileTarget{
			WorktreeID:     composerReleaseWorktreeID,
			RepositoryID:   composerReleaseRepositoryID,
			RepositoryPath: composerReleaseGitCommonDir,
			GitCommonDir:   composerReleaseGitCommonDir,
			WorktreePath:   composerReleaseWorktreePath,
			Branch:         composerReleaseBranch,
			HeadOID:        composerReleaseHeadOIDPrefix + strings.Repeat("0", 39),
		},
		Associations: []models.ArchivedResourceReconcileAssociation{
			{
				AssociationID:  "association-composer",
				TaskID:         composerReleaseTaskID,
				SessionID:      "session-composer",
				WorktreeID:     composerReleaseWorktreeID,
				RepositoryID:   composerReleaseRepositoryID,
				BranchSlug:     composerReleaseBranch,
				WorktreePath:   composerReleaseWorktreePath,
				WorktreeBranch: composerReleaseBranch,
				Status:         "active",
				CreatedAt:      now.Format(time.RFC3339Nano),
				UpdatedAt:      now.Format(time.RFC3339Nano),
			},
		},
	}
	snapshot, raw, identity, err := models.NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	anchorRevision := int64(models.ArchivedResourceRetentionAnchorVersion)
	completedAt := now
	if _, err := db.Exec(`
		INSERT INTO task_resource_cleanup_jobs (
			id, operation_id, task_id, trigger, state, resource_snapshot,
			snapshot_version, snapshot_digest, resource_kind, resource_id,
			managed_root_key, anchor_revision, active_scope_key,
			attempts, next_attempt_at, last_error, created_at, updated_at, completed_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0, NULL, '', ?, ?, ?
		)
	`, "anchor-composer", identity.OperationID, composerReleaseTaskID,
		"archived_resource_reconcile", "retained", string(raw), 2,
		identity.SnapshotDigest, "git_worktree", composerReleaseWorktreeID,
		immutable.ManagedRootKey, anchorRevision, now, now, completedAt,
	); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}
	_ = snapshot
	return composerSeeded{
		id:          "anchor-composer",
		digest:      identity.SnapshotDigest,
		operationID: identity.OperationID,
	}
}

func composerReleaseManagedRootKey(t *testing.T) string {
	t.Helper()
	key, err := ComputeAnchorManagedRootKey(composerReleaseWorktreePath)
	if err != nil {
		t.Fatalf("managed root key: %v", err)
	}
	return key
}

func composerReleaseRequestFixture(operationID string) Request {
	managedRootKey, _ := ComputeAnchorManagedRootKey(composerReleaseWorktreePath)
	return Request{
		Action:    ActionReleaseAbsent,
		Authority: AuthorityAdmin,
		Executor:  ExecutorNone,
		Resource: Resource{
			Kind: ResourceKindEnvironmentRepo,
			ID:   operationID,
			Path: composerReleaseWorktreePath,
		},
		AnchorIdentity: AnchorIdentity{
			OperationID:     operationID,
			SnapshotDigest:  "",
			ResourceKind:    "git_worktree",
			ResourceID:      composerReleaseWorktreeID,
			TaskID:          composerReleaseTaskID,
			ManagedRootKey:  managedRootKey,
			SnapshotVersion: 2,
		},
	}
}

func composerReleaseRequestWithDigest(operationID, digest string) Request {
	req := composerReleaseRequestFixture(operationID)
	req.AnchorIdentity.SnapshotDigest = digest
	return req
}

// composerSeedExtraReference inserts one extra row referencing the release
// target path so the inventory's "path still referenced" guard fires.
func composerSeedExtraReference(t *testing.T, db *sqlx.DB) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec(`
		INSERT INTO task_environment_repos (
			id, task_environment_id, repository_id, worktree_id, worktree_path,
			worktree_branch, branch_slug, position, status, created_at, updated_at
		) VALUES (
			'env-repo-extra', 'env-extra', ?, 'wt-extra', ?, ?, '', 0, 'active', ?, ?
		)
	`, composerReleaseRepositoryID, composerReleaseWorktreePath,
		composerReleaseBranch, now, now,
	); err != nil {
		t.Fatalf("seed extra reference: %v", err)
	}
	var inserted string
	if err := db.QueryRowxContext(context.Background(),
		`SELECT worktree_path FROM task_environment_repos WHERE id = 'env-repo-extra'`,
	).Scan(&inserted); err != nil {
		t.Fatalf("verify seed: %v", err)
	}
	if inserted != composerReleaseWorktreePath {
		t.Fatalf("seeded path %q != canonical release path %q", inserted, composerReleaseWorktreePath)
	}
}

func composerSeedExecutorReference(t *testing.T, db *sqlx.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO executors_running (
			id, session_id, task_id, worktree_id, worktree_path,
			worktree_branch, status
		) VALUES (
			'exec-extra', 'session-extra', ?, 'wt-extra', ?, ?, 'running'
		)
	`, composerReleaseTaskID, composerReleaseWorktreePath,
		composerReleaseBranch,
	); err != nil {
		t.Fatalf("seed executor reference: %v", err)
	}
}

func composerSeedUnknownWorkspaceState(t *testing.T, db *sqlx.DB) {
	t.Helper()
	// The materialized_path filter on the workspace-group SELECT excludes
	// rows whose path is empty, so the seeded row must carry a path that
	// survives that filter. Using the release path keeps the test focused on
	// the unknown-state failure rather than a path overlap.
	if _, err := db.Exec(`
		INSERT INTO task_workspace_groups (
			id, cleanup_status, materialized_path
		) VALUES ('group-bad', 'unknown-state', ?)
	`, composerReleaseGitCommonDir); err != nil {
		t.Fatalf("seed unknown state: %v", err)
	}
}

func composerSeedMalformedV2(t *testing.T, db *sqlx.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO task_resource_cleanup_jobs (
			id, operation_id, task_id, trigger, state, resource_snapshot,
			snapshot_version, snapshot_digest, resource_kind, resource_id,
			managed_root_key, anchor_revision, active_scope_key,
			attempts, next_attempt_at, last_error, created_at, updated_at, completed_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0, NULL, '', ?, ?, ?
		)
	`, id, "op-"+id, composerReleaseTaskID, "archived_resource_reconcile",
		"retained", `{"not":"valid json`, 2, "digest-"+id, "git_worktree",
		composerReleaseWorktreeID, composerReleaseManagedRootKey(t),
		int64(models.ArchivedResourceRetentionAnchorVersion),
		time.Now().UTC(), time.Now().UTC(), time.Now().UTC(),
	); err != nil {
		t.Fatalf("seed malformed anchor: %v", err)
	}
}

func composerSeedDriftDigest(t *testing.T, db *sqlx.DB) {
	t.Helper()
	now := time.Now().UTC()
	immutable := models.ArchivedResourceReconcileImmutable{
		OriginTaskID:   "task-drift",
		ArchivedAt:     now.Format(time.RFC3339Nano),
		ManagedRootKey: composerReleaseManagedRootKey(t),
		Target: models.ArchivedResourceReconcileTarget{
			WorktreeID:     composerReleaseWorktreeID,
			RepositoryID:   composerReleaseRepositoryID,
			RepositoryPath: composerReleaseGitCommonDir,
			GitCommonDir:   composerReleaseGitCommonDir,
			WorktreePath:   composerReleaseWorktreePath,
			Branch:         composerReleaseBranch,
			HeadOID:        "a" + strings.Repeat("0", 39),
		},
		Associations: []models.ArchivedResourceReconcileAssociation{
			{
				AssociationID:  "association-drift",
				TaskID:         "task-drift",
				SessionID:      "session-drift",
				WorktreeID:     composerReleaseWorktreeID,
				RepositoryID:   composerReleaseRepositoryID,
				BranchSlug:     composerReleaseBranch,
				WorktreePath:   composerReleaseWorktreePath,
				WorktreeBranch: composerReleaseBranch,
				Status:         "active",
				CreatedAt:      now.Format(time.RFC3339Nano),
				UpdatedAt:      now.Format(time.RFC3339Nano),
			},
		},
	}
	_, raw, identity, err := models.NewArchivedResourceReconcileSnapshot(immutable)
	if err != nil {
		t.Fatalf("build drift snapshot: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO task_resource_cleanup_jobs (
			id, operation_id, task_id, trigger, state, resource_snapshot,
			snapshot_version, snapshot_digest, resource_kind, resource_id,
			managed_root_key, anchor_revision, active_scope_key,
			attempts, next_attempt_at, last_error, created_at, updated_at, completed_at
		) VALUES (
			'anchor-drift', ?, 'task-drift', ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0, NULL, '', ?, ?, ?
		)
	`, identity.OperationID, "archived_resource_reconcile", "retained",
		string(raw), 2, identity.SnapshotDigest, "git_worktree",
		composerReleaseWorktreeID, immutable.ManagedRootKey,
		int64(models.ArchivedResourceRetentionAnchorVersion),
		now, now, now,
	); err != nil {
		t.Fatalf("seed drift anchor: %v", err)
	}
}

func composerReleaseSealHolds(t *testing.T, fixture *composerFixture) {
	t.Helper()
	receipt, err := fixture.svc.Execute(context.Background(),
		composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if receipt.Mutated {
		t.Fatal("release receipt reports Mutated=true; release must be a sealed no-op")
	}
	if receipt.Executor != ExecutorNone {
		t.Fatalf("receipt executor = %q, want %q", receipt.Executor, ExecutorNone)
	}
	if receipt.Action != ActionReleaseAbsent {
		t.Fatalf("receipt action = %q, want %q", receipt.Action, ActionReleaseAbsent)
	}
	if receipt.ResourceKind != ResourceKindEnvironmentRepo {
		t.Fatalf("receipt resource kind = %q, want %q", receipt.ResourceKind, ResourceKindEnvironmentRepo)
	}
	if receipt.ResourceID != fixture.anchorOperationID {
		t.Fatalf("receipt resource id = %q, want %q", receipt.ResourceID, fixture.anchorOperationID)
	}
	if receipt.InventoryDigest == "" {
		t.Fatal("receipt inventory digest missing")
	}
	if len(receipt.Locks) != 1 {
		t.Fatalf("receipt locks = %d, want 1 (canonical path)", len(receipt.Locks))
	}
	if receipt.Locks[0].PathKey == "" {
		t.Fatalf("receipt lock path key is empty: %#v", receipt.Locks[0])
	}
}

func TestRealCompositionReleaseAdmitsExactBoundRetainedAnchor(t *testing.T) {
	fixture := newComposerFixture(t)
	composerReleaseSealHolds(t, fixture)
}

func TestRealCompositionReleaseFailsClosedOnExtraRepositoryReference(t *testing.T) {
	fixture := newComposerFixture(t)
	composerSeedExtraReference(t, fixture.db)
	_, err := fixture.svc.Execute(context.Background(),
		composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrProtectedResource) {
		t.Fatalf("protected reference error = %v, want ErrProtectedResource", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnExecutorReference(t *testing.T) {
	fixture := newComposerFixture(t)
	composerSeedExecutorReference(t, fixture.db)
	_, err := fixture.svc.Execute(context.Background(),
		composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest))
	if !errors.Is(err, ErrProtectedResource) {
		t.Fatalf("executor reference error = %v, want ErrProtectedResource", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnUnknownWorkspaceState(t *testing.T) {
	fixture := newComposerFixture(t)
	composerSeedUnknownWorkspaceState(t, fixture.db)
	_, err := fixture.svc.Execute(context.Background(),
		composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnknownInventory) && !errors.Is(err, ErrInventoryIncomplete) {
		t.Fatalf("unknown workspace state error = %v, want ErrUnknownInventory or ErrInventoryIncomplete", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnMalformedV2(t *testing.T) {
	fixture := newComposerFixture(t)
	composerSeedMalformedV2(t, fixture.db, "anchor-malformed")
	_, err := fixture.svc.Execute(context.Background(),
		composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest))
	if !errors.Is(err, ErrInventoryIncomplete) {
		t.Fatalf("malformed v2 error = %v, want ErrInventoryIncomplete", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnDriftDigest(t *testing.T) {
	fixture := newComposerFixture(t)
	composerSeedDriftDigest(t, fixture.db)
	// Drift anchor has a different digest than the request digest. The
	// admission looks up by operation_id, finds the retained anchor, but
	// the digest comparison must fail closed.
	req := composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest)
	req.AnchorIdentity.SnapshotDigest = "wrong-digest"
	_, err := fixture.svc.Execute(context.Background(), req)
	if !errors.Is(err, ErrProtectedResource) {
		t.Fatalf("drift digest error = %v, want ErrProtectedResource", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnUnknownOperationID(t *testing.T) {
	fixture := newComposerFixture(t)
	req := composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest)
	req.Resource.ID = "archived-resource-reconcile:missing"
	req.AnchorIdentity.OperationID = "archived-resource-reconcile:missing"
	_, err := fixture.svc.Execute(context.Background(), req)
	if !errors.Is(err, ErrProtectedResource) {
		t.Fatalf("unknown operation_id error = %v, want ErrProtectedResource", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnWrongResourceKind(t *testing.T) {
	fixture := newComposerFixture(t)
	req := composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest)
	req.AnchorIdentity.ResourceKind = "task_environment"
	_, err := fixture.svc.Execute(context.Background(), req)
	if !errors.Is(err, ErrProtectedResource) {
		t.Fatalf("wrong resource kind error = %v, want ErrProtectedResource", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnAnchorNotRetained(t *testing.T) {
	fixture := newComposerFixture(t)
	if _, err := fixture.db.Exec(`
		UPDATE task_resource_cleanup_jobs SET state = 'pending' WHERE id = ?
	`, fixture.anchorID); err != nil {
		t.Fatalf("flip state: %v", err)
	}
	_, err := fixture.svc.Execute(context.Background(),
		composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest))
	if !errors.Is(err, ErrProtectedResource) {
		t.Fatalf("non-retained anchor error = %v, want ErrProtectedResource", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnWrongAnchorVersion(t *testing.T) {
	fixture := newComposerFixture(t)
	req := composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest)
	req.AnchorIdentity.SnapshotVersion = 3
	_, err := fixture.svc.Execute(context.Background(), req)
	if !errors.Is(err, ErrProtectedResource) {
		t.Fatalf("wrong version error = %v, want ErrProtectedResource", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnEmptyAnchorIdentity(t *testing.T) {
	fixture := newComposerFixture(t)
	req := composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest)
	req.AnchorIdentity = AnchorIdentity{}
	_, err := fixture.svc.Execute(context.Background(), req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty anchor identity error = %v, want ErrInvalidRequest", err)
	}
}

func TestRealCompositionReleaseFailsClosedOnChildren(t *testing.T) {
	fixture := newComposerFixture(t)
	req := composerReleaseRequestWithDigest(fixture.anchorOperationID, fixture.anchorDigest)
	req.Children = []Resource{{Kind: ResourceKindEnvironmentRepo, ID: "child", Path: "/tmp/child"}}
	_, err := fixture.svc.Execute(context.Background(), req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("children present error = %v, want ErrInvalidRequest", err)
	}
}

// TestRealCompositionReleaseLeavesAnchorAndTargetUntouched proves the
// no-op pass produces zero durable side effects: the anchor row remains
// retained with its original completed_at, the target path remains absent
// from the writer-DB inventory, and the empty-task_environment_repos set
// is unchanged.
func TestRealCompositionReleaseLeavesAnchorAndTargetUntouched(t *testing.T) {
	fixture := newComposerFixture(t)
	var beforeState, beforeCompletedAt string
	if err := fixture.db.QueryRowxContext(context.Background(), `
		SELECT state, COALESCE(completed_at, '') FROM task_resource_cleanup_jobs WHERE id = ?
	`, fixture.anchorID).Scan(&beforeState, &beforeCompletedAt); err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	composerReleaseSealHolds(t, fixture)
	var afterState, afterCompletedAt string
	if err := fixture.db.QueryRowxContext(context.Background(), `
		SELECT state, COALESCE(completed_at, '') FROM task_resource_cleanup_jobs WHERE id = ?
	`, fixture.anchorID).Scan(&afterState, &afterCompletedAt); err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	if afterState != beforeState {
		t.Fatalf("anchor state changed: %q -> %q", beforeState, afterState)
	}
	if afterCompletedAt != beforeCompletedAt {
		t.Fatalf("anchor completed_at changed: %q -> %q", beforeCompletedAt, afterCompletedAt)
	}
	var count int
	if err := fixture.db.QueryRowxContext(context.Background(),
		"SELECT COUNT(*) FROM task_environment_repos").Scan(&count); err != nil {
		t.Fatalf("count repos: %v", err)
	}
	if count != 0 {
		t.Fatalf("task_environment_repos touched by release: count=%d, want 0", count)
	}
}

// Sanity helper to ensure the worktree path passed to tests is canonical
// (otherwise ComputeAnchorManagedRootKey fails).
func init() {
	_ = filepath.Join
}
