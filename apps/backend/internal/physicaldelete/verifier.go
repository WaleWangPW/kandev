package physicaldelete

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func CaptureAnchor(path string) (Anchor, error) {
	clean, err := cleanAbsolutePath(path)
	if err != nil {
		return Anchor{}, fmt.Errorf("%w: %v", ErrAnchorMismatch, err)
	}
	if err := rejectFinalSymlink(clean); err != nil {
		return Anchor{}, err
	}
	canonical, err := canonicalLockPath(clean)
	if err != nil {
		return Anchor{}, fmt.Errorf("%w: %v", ErrAnchorMismatch, err)
	}
	if err := ensureNoSymlinkPath(canonical); err != nil {
		return Anchor{}, err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return Anchor{}, fmt.Errorf("%w: lstat: %v", ErrAnchorMismatch, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Anchor{}, ErrSymlinkPath
	}
	return Anchor{path: canonical, info: info}, nil
}

type actionSpec struct {
	executor Executor
}

func normalizeRequest(request Request) (Request, actionSpec, error) {
	spec, ok := actionSpecs[request.Action]
	if !ok {
		return Request{}, actionSpec{}, fmt.Errorf("%w: unknown action %q", ErrInvalidRequest, request.Action)
	}
	if !knownAuthority(request.Authority) {
		return Request{}, actionSpec{}, fmt.Errorf("%w: unknown authority %q", ErrInvalidRequest, request.Authority)
	}
	normalized := request
	resource, err := normalizeResource(request.Resource)
	if err != nil {
		return Request{}, actionSpec{}, err
	}
	normalized.Resource = resource
	children := make([]Resource, len(request.Children))
	for i, child := range request.Children {
		children[i], err = normalizeResource(child)
		if err != nil {
			return Request{}, actionSpec{}, fmt.Errorf("%w: child %d: %v", ErrInvalidRequest, i, err)
		}
	}
	normalized.Children = children
	if request.Executor != "" && request.Executor != spec.executor {
		return Request{}, actionSpec{}, fmt.Errorf("%w: executor %q cannot execute %q", ErrInvalidRequest, request.Executor, request.Action)
	}
	normalized.Executor = spec.executor
	if request.Action == ActionProvisionalRollback && request.lease == nil {
		return Request{}, actionSpec{}, fmt.Errorf("%w: rollback requires a live lease", ErrProvisionalLease)
	}
	if request.Action == ActionProvisionalRollback {
		identity, err := normalizeProvisionalIdentity(request.Identity, normalized.Resource)
		if err != nil {
			return Request{}, actionSpec{}, err
		}
		normalized.Identity = identity
	}
	return normalized, spec, nil
}

func normalizeResource(resource Resource) (Resource, error) {
	if resource.ID == "" || strings.IndexByte(resource.ID, 0) >= 0 {
		return Resource{}, fmt.Errorf("%w: resource id is required", ErrInvalidRequest)
	}
	if !knownResourceKind(resource.Kind) {
		return Resource{}, fmt.Errorf("%w: unknown resource kind %q", ErrInvalidRequest, resource.Kind)
	}
	cleanPath, err := cleanAbsolutePath(resource.Path)
	if err != nil {
		return Resource{}, fmt.Errorf("%w: resource path: %v", ErrInvalidRequest, err)
	}
	if err := rejectFinalSymlink(cleanPath); err != nil {
		return Resource{}, err
	}
	path, err := canonicalLockPath(cleanPath)
	if err != nil {
		return Resource{}, fmt.Errorf("%w: resource path: %v", ErrInvalidRequest, err)
	}
	if err := ensureNoSymlinkPath(path); err != nil {
		return Resource{}, err
	}
	resource.Path = path
	if resource.RootPath != "" {
		cleanRoot, cleanErr := cleanAbsolutePath(resource.RootPath)
		if cleanErr != nil {
			return Resource{}, fmt.Errorf("%w: root path: %v", ErrInvalidRequest, cleanErr)
		}
		if err := rejectFinalSymlink(cleanRoot); err != nil {
			return Resource{}, err
		}
		resource.RootPath, err = canonicalLockPath(cleanRoot)
		if err != nil {
			return Resource{}, fmt.Errorf("%w: root path: %v", ErrInvalidRequest, err)
		}
		if err := ensureNoSymlinkPath(resource.RootPath); err != nil {
			return Resource{}, err
		}
	}
	if resource.CommonDir != "" {
		resource.CommonDir, err = canonicalLockPath(resource.CommonDir)
		if err != nil {
			return Resource{}, fmt.Errorf("%w: common directory: %v", ErrInvalidRequest, err)
		}
	}
	if resource.Anchor != nil && resource.Anchor.path != resource.Path {
		return Resource{}, fmt.Errorf("%w: anchor path does not match resource path", ErrAnchorMismatch)
	}
	return resource, nil
}

func verifyAnchors(request Request) error {
	resources := append([]Resource{request.Resource}, request.Children...)
	for _, resource := range resources {
		if err := verifyAnchor(resource.Path, resource.Anchor); err != nil {
			return err
		}
	}
	return nil
}

func verifyAnchor(path string, anchor *Anchor) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if anchor == nil {
			return nil
		}
		return fmt.Errorf("%w: target disappeared", ErrAnchorMismatch)
	}
	if err != nil {
		return fmt.Errorf("%w: lstat: %v", ErrAnchorMismatch, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlinkPath
	}
	if anchor == nil || anchor.info == nil || !os.SameFile(anchor.info, info) {
		return fmt.Errorf("%w: identity changed", ErrAnchorMismatch)
	}
	return nil
}

func ensureNoSymlinkPath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: empty path", ErrSymlinkPath)
	}
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: %s", ErrSymlinkPath, current)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: inspect %s: %v", ErrSymlinkPath, current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func cleanAbsolutePath(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("empty or NUL path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func rejectFinalSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: inspect target: %v", ErrSymlinkPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlinkPath
	}
	return nil
}

func actionExecutor(action Action) Executor { return actionSpecs[action].executor }

var actionSpecs = map[Action]actionSpec{
	ActionProvisionalRollback:      {executor: ExecutorFilesystem},
	ActionRegisteredWorktreeRemove: {executor: ExecutorGit},
	ActionBranchDelete:             {executor: ExecutorGit},
	ActionRecursiveRootRemove:      {executor: ExecutorFilesystem},
	ActionQuarantine:               {executor: ExecutorFilesystem},
	ActionRestore:                  {executor: ExecutorFilesystem},
	ActionPurge:                    {executor: ExecutorFilesystem},
	ActionParentRemove:             {executor: ExecutorFilesystem},
	ActionReleaseAbsent:            {executor: ExecutorNone},
}

func knownAuthority(authority Authority) bool {
	switch authority {
	case AuthorityLifecycle, AuthorityWorktree, AuthorityStorage, AuthorityOffice,
		AuthorityHandoff, AuthorityMaterializer, AuthorityFactoryReset, AuthorityAdmin:
		return true
	default:
		return false
	}
}

func knownExecutor(executor Executor) bool {
	return executor == ExecutorFilesystem || executor == ExecutorGit || executor == ExecutorNone
}

func resourcesOverlap(resource Resource, protected []string) bool {
	paths := []string{resource.Path}
	if resource.RootPath != "" {
		paths = append(paths, resource.RootPath)
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		for _, candidate := range protected {
			if pathsOverlap(path, candidate) {
				return true
			}
		}
	}
	return false
}
