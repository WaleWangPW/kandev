package physicaldelete

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type LockRegistry struct {
	mu     sync.Mutex
	paths  map[string]struct{}
	common map[string]struct{}
	notify chan struct{}
}

// RootPolicy names control roots that are never eligible for an ordinary
// disjoint operation. Its paths are canonicalized once at construction and
// compared with the same ancestor/descendant rule as admission locks.
type RootPolicy struct{ paths []string }

func NewRootPolicy(paths []string) (RootPolicy, error) {
	canonical, err := canonicalLockPaths(paths)
	if err != nil {
		return RootPolicy{}, fmt.Errorf("%w: root policy: %v", ErrInvalidRequest, err)
	}
	for _, path := range canonical {
		if err := ensureNoSymlinkPath(path); err != nil {
			return RootPolicy{}, fmt.Errorf("%w: root policy: %v", ErrInvalidRequest, err)
		}
	}
	return RootPolicy{paths: canonical}, nil
}

func (p RootPolicy) protects(resource Resource) bool {
	return resourcesOverlap(resource, p.paths)
}

func NewLockRegistry() *LockRegistry {
	return &LockRegistry{
		paths:  make(map[string]struct{}),
		common: make(map[string]struct{}),
		notify: make(chan struct{}),
	}
}

// Acquire takes all target locks as one set. Target locks overlap for equal,
// ancestor, and descendant paths. Common-directory locks are exact canonical
// identities and are kept in a separate namespace from target paths.
func (r *LockRegistry) Acquire(ctx context.Context, paths, commonDirs []string) (func(), error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil registry", ErrLockUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	canonicalPaths, err := canonicalLockPaths(paths)
	if err != nil {
		return nil, fmt.Errorf("%w: path: %v", ErrLockUnavailable, err)
	}
	canonicalCommon, err := canonicalLockPaths(commonDirs)
	if err != nil {
		return nil, fmt.Errorf("%w: common directory: %v", ErrLockUnavailable, err)
	}
	for {
		r.mu.Lock()
		if !r.conflicts(canonicalPaths, canonicalCommon) {
			for _, path := range canonicalPaths {
				r.paths[path] = struct{}{}
			}
			for _, path := range canonicalCommon {
				r.common[path] = struct{}{}
			}
			waiter := &lockRelease{
				registry: r,
				paths:    canonicalPaths,
				common:   canonicalCommon,
			}
			r.mu.Unlock()
			return waiter.Release, nil
		}
		wait := r.notify
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, errors.Join(ErrLockUnavailable, ctx.Err())
		case <-wait:
		}
	}
}

type lockRelease struct {
	registry *LockRegistry
	paths    []string
	common   []string
	once     sync.Once
}

func (l *lockRelease) Release() {
	if l == nil || l.registry == nil {
		return
	}
	l.once.Do(func() {
		l.registry.mu.Lock()
		for _, path := range l.paths {
			delete(l.registry.paths, path)
		}
		for _, path := range l.common {
			delete(l.registry.common, path)
		}
		close(l.registry.notify)
		l.registry.notify = make(chan struct{})
		l.registry.mu.Unlock()
	})
}

func (r *LockRegistry) conflicts(paths, commonDirs []string) bool {
	for _, path := range paths {
		for held := range r.paths {
			if pathsOverlap(path, held) {
				return true
			}
		}
	}
	for _, path := range commonDirs {
		if _, held := r.common[path]; held {
			return true
		}
	}
	return false
}

func canonicalLockPaths(paths []string) ([]string, error) {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		canonical, err := canonicalLockPath(path)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result, nil
}

// CanonicalLockPathForTest exposes canonicalLockPath for tests that need
// to seed fixtures with the same canonical form the admission will produce.
func CanonicalLockPathForTest(path string) (string, error) {
	return canonicalLockPath(path)
}

func canonicalLockPath(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("empty or NUL path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(abs)
	for current := clean; ; current = filepath.Dir(current) {
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr == nil {
			relative, relErr := filepath.Rel(current, clean)
			if relErr != nil {
				return "", relErr
			}
			return filepath.Clean(filepath.Join(resolved, relative)), nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", resolveErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return clean, nil
		}
	}
}

func pathsOverlap(left, right string) bool {
	if left == right {
		return true
	}
	leftToRight, leftErr := filepath.Rel(left, right)
	rightToLeft, rightErr := filepath.Rel(right, left)
	return (leftErr == nil && isDescendant(leftToRight)) ||
		(rightErr == nil && isDescendant(rightToLeft))
}

func isDescendant(relative string) bool {
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func lockIdentity(path, commonDir string) LockIdentity {
	return LockIdentity{
		PathKey:      digestPath(path),
		CommonDirKey: digestPath(commonDir),
	}
}

func digestPath(path string) string {
	if path == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(path))
	return hex.EncodeToString(digest[:])
}
