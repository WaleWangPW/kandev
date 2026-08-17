package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/physicaldelete"
)

// HandoffCleaner is the office task-handoffs cleanup adapter that
// satisfies task/service.WorkspaceCleaner. It threads the managed-root
// guard before any destructive operation, refuses paths outside the
// configured kandev-managed roots, and delegates the actual disk
// removal to the existing Manager.
//
// Wired in cmd/kandev: handoffSvc.SetWorkspaceCleaner(NewHandoffCleaner(worktreeMgr, log)).
type HandoffCleaner struct {
	manager *Manager
	logger  *logger.Logger
	// extraRoots is the optional list of additional directories the
	// cleaner is allowed to remove (e.g. the office multi-repo root,
	// plain folder root). Worktree paths are validated separately
	// against the manager's TasksBasePath.
	extraRoots []string
	// admission is the sealed central gate that every destructive
	// handoff cleanup step must funnel through. nil is fail-closed.
	admission physicaldelete.Admission
}

// NewHandoffCleaner constructs the cleaner. extraRoots is appended to
// the manager's tasks base path; pass office-specific managed roots
// (multi-repo / plain-folder) when wiring.
func NewHandoffCleaner(mgr *Manager, log *logger.Logger, extraRoots ...string) *HandoffCleaner {
	return &HandoffCleaner{
		manager:    mgr,
		logger:     log.WithFields(zap.String("component", "handoff-cleaner")),
		extraRoots: extraRoots,
	}
}

// SetAdmission wires the sealed central admission gate that every
// destructive handoff cleanup step must funnel through. Production
// wiring in backendapp provides the shared physicaldelete.Service.
func (c *HandoffCleaner) SetAdmission(admission physicaldelete.Admission) {
	c.admission = admission
}

// gateManagedRootDeletion submits one sealed admission request for a
// managed-root physical action. Missing admission or the deliberately
// sealed executor returns a typed denial so the caller short-circuits
// before any filesystem mutation.
func (c *HandoffCleaner) gateManagedRootDeletion(
	ctx context.Context,
	authority physicaldelete.Authority,
	action physicaldelete.Action,
	resourceKind physicaldelete.ResourceKind,
	resourceID, path string,
) (physicaldelete.Receipt, error) {
	if c.admission == nil {
		return physicaldelete.Receipt{}, fmt.Errorf(
			"handoff cleaner: physical-delete admission is not configured")
	}
	if path == "" {
		return physicaldelete.Receipt{}, fmt.Errorf(
			"handoff cleaner: %s requires a managed-root path", action)
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return physicaldelete.Receipt{}, fmt.Errorf(
			"handoff cleaner: canonicalize %s path %s: %w", action, path, err)
	}
	anchor, anchorErr := physicaldelete.CaptureAnchor(absPath)
	resource := physicaldelete.Resource{
		Kind:     resourceKind,
		ID:       resourceID,
		Path:     absPath,
		RootPath: absPath,
	}
	if anchorErr == nil {
		resource.Anchor = &anchor
	}
	request := physicaldelete.Request{
		Action:    action,
		Authority: authority,
		Resource:  resource,
	}
	receipt, err := c.admission.Execute(ctx, request)
	if err == nil {
		return receipt, nil
	}
	switch {
	case errors.Is(err, physicaldelete.ErrExecutorUnavailable),
		errors.Is(err, physicaldelete.ErrInvalidRequest),
		errors.Is(err, physicaldelete.ErrInventoryIncomplete),
		errors.Is(err, physicaldelete.ErrLockUnavailable),
		errors.Is(err, physicaldelete.ErrProtectedResource),
		errors.Is(err, physicaldelete.ErrAnchorMismatch):
		return receipt, err
	default:
		return receipt, err
	}
}

// CleanupPlainFolder removes a Kandev-owned plain folder. The path
// MUST resolve to a location under one of the configured managed
// roots; anything else is rejected up front so a corrupted
// materialized_path can never delete arbitrary user files. Task 07
// binds the destructive step behind the sealed central admission.
func (c *HandoffCleaner) CleanupPlainFolder(ctx context.Context, path string) error {
	if err := c.requireManagedRoot(path); err != nil {
		return err
	}
	if _, err := c.gateManagedRootDeletion(
		ctx,
		physicaldelete.AuthorityHandoff,
		physicaldelete.ActionRecursiveRootRemove,
		physicaldelete.ResourceKindManagedRoot,
		path,
		path,
	); err != nil {
		return fmt.Errorf("admission denied handoff cleanup of %s: %w", path, err)
	}
	c.logger.Info("cleanup plain folder", zap.String("path", path))
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// CleanupSingleRepoWorktree removes a single git worktree by ID via
// the existing Manager.RemoveByID, which already runs its own
// repository-scoped lock + git-worktree-remove + cleanup script.
//
// removeBranch is FALSE: handoffs cleanup releases the materialized
// workspace; the branch the agent created is left intact so any
// pushed PR / remote ref survives. Operators clean up branches via
// the existing branch-cleanup tooling.
func (c *HandoffCleaner) CleanupSingleRepoWorktree(ctx context.Context, worktreeID string) error {
	if c.manager == nil {
		return errors.New("worktree manager not configured")
	}
	if worktreeID == "" {
		return errors.New("worktree id is required")
	}
	c.logger.Info("cleanup single-repo worktree", zap.String("worktree_id", worktreeID))
	return c.manager.RemoveByID(ctx, worktreeID, false)
}

// CleanupMultiRepoRoot removes every per-repo worktree under a
// multi-repo task root, then removes the root directory itself. The
// root path is validated against the managed-roots set first; if the
// guard fails the per-repo removals are also skipped. Task 07 binds
// the destructive root removal behind the sealed central admission.
func (c *HandoffCleaner) CleanupMultiRepoRoot(ctx context.Context, rootPath string, worktreeIDs []string) error {
	if rootPath == "" {
		return errors.New("multi-repo root path is required")
	}
	if err := c.requireManagedRoot(rootPath); err != nil {
		return err
	}
	for _, id := range worktreeIDs {
		if id == "" {
			continue
		}
		if err := c.manager.RemoveByID(ctx, id, false); err != nil {
			c.logger.Warn("multi-repo worktree remove failed",
				zap.String("worktree_id", id), zap.Error(err))
			// Continue removing the rest; the root removal at the end
			// will reclaim any straggler files. We deliberately do not
			// abort: a partial cleanup is preferable to leaving the
			// whole tree behind.
		}
	}
	if _, err := c.gateManagedRootDeletion(
		ctx,
		physicaldelete.AuthorityHandoff,
		physicaldelete.ActionRecursiveRootRemove,
		physicaldelete.ResourceKindManagedRoot,
		rootPath,
		rootPath,
	); err != nil {
		return fmt.Errorf("admission denied handoff cleanup of %s: %w", rootPath, err)
	}
	if err := os.RemoveAll(rootPath); err != nil {
		return fmt.Errorf("remove multi-repo root %s: %w", rootPath, err)
	}
	return nil
}

// CleanupRemoteEnvironment is a stub: remote environments
// (sprites etc.) are managed by per-provider services that the
// office service does not import. The cleaner records the pending
// state via cleanup_status; provider-specific deletion is wired in a
// follow-up commit when the materializer flips owned_by_kandev for
// remote envs (today the executor does not do that).
func (c *HandoffCleaner) CleanupRemoteEnvironment(_ context.Context, provider, environmentID string) error {
	c.logger.Info("cleanup remote environment (no-op)",
		zap.String("provider", provider),
		zap.String("environment_id", environmentID))
	return nil
}

// requireManagedRoot rejects paths that do not resolve to a location
// under one of the configured managed roots. This is the belt-and-
// braces guard the spec calls out as critical: even if owned_by_kandev
// were ever wrongly set, the cleanup still cannot escape the
// kandev-managed directory tree.
//
// Symlinks are resolved on both the input path and each managed root
// so the guard accepts identities like /var/folders → /private/var/folders
// (macOS tmpdirs) consistently. When the input path no longer exists
// (e.g. cleanup is running after a partial removal), we resolve
// symlinks on the deepest parent that does exist, then re-attach the
// missing tail before checking.
func (c *HandoffCleaner) requireManagedRoot(path string) error {
	if path == "" {
		return errors.New("managed-root guard: path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("managed-root guard: absolute path: %w", err)
	}
	resolved := resolveExistingPrefix(abs)
	roots := c.managedRoots()
	if len(roots) == 0 {
		return errors.New("managed-root guard: no managed roots configured")
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rootResolved := resolveExistingPrefix(rootAbs)
		if isDescendant(rootResolved, resolved) {
			return nil
		}
	}
	return fmt.Errorf("managed-root guard: %s is not inside a kandev-managed root", path)
}

// resolveExistingPrefix evaluates symlinks on the deepest existing
// ancestor of `path` and re-attaches the missing tail. Used so the
// guard handles "the directory is about to be created" / "was just
// removed" without losing symlink-equivalence with the managed roots.
func resolveExistingPrefix(path string) string {
	cleaned := filepath.Clean(path)
	prefix := cleaned
	suffix := ""
	for {
		if r, err := filepath.EvalSymlinks(prefix); err == nil {
			if suffix == "" {
				return filepath.Clean(r)
			}
			return filepath.Clean(filepath.Join(r, suffix))
		}
		parent := filepath.Dir(prefix)
		if parent == prefix {
			return cleaned
		}
		base := filepath.Base(prefix)
		if suffix == "" {
			suffix = base
		} else {
			suffix = filepath.Join(base, suffix)
		}
		prefix = parent
	}
}

func (c *HandoffCleaner) managedRoots() []string {
	roots := make([]string, 0, 1+len(c.extraRoots))
	if c.manager != nil {
		// Reuse the worktree manager's config so the guard tracks the
		// same root the manager creates worktrees in.
		base, err := c.manager.config.ExpandedTasksBasePath()
		if err == nil && base != "" {
			roots = append(roots, base)
		}
	}
	roots = append(roots, c.extraRoots...)
	return roots
}

// isDescendant reports whether `path` is `root` or a descendant of it.
// Uses filepath.Rel to detect ".." traversal — anything that resolves
// via Rel to a relative path NOT starting with ".." is a descendant.
func isDescendant(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..") && rel != ""
}
