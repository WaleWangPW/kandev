package physicaldelete

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type provisionalLeaseStatus string

const (
	provisionalLeaseReserved provisionalLeaseStatus = "reserved"
	provisionalLeaseBound    provisionalLeaseStatus = "bound"
)

type provisionalState struct {
	id        string
	authority Authority
	identity  ProvisionalIdentity
	resource  Resource
	anchor    *Anchor
	status    provisionalLeaseStatus
}

type provisionalRegistry struct {
	mu              syncMutex
	leases          map[string]*provisionalState
	pathClaims      map[string]string
	commonClaims    map[string]string
	operationClaims map[string]string
}

// syncMutex is a tiny private alias that keeps the registry's lock ownership
// explicit in this file without exposing mutable state to callers.
type syncMutex struct{ mu chan struct{} }

func newProvisionalRegistry() *provisionalRegistry {
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	return &provisionalRegistry{
		mu:              syncMutex{mu: lock},
		leases:          make(map[string]*provisionalState),
		pathClaims:      make(map[string]string),
		commonClaims:    make(map[string]string),
		operationClaims: make(map[string]string),
	}
}

func (r *provisionalRegistry) lock() func() {
	<-r.mu.mu
	return func() { r.mu.mu <- struct{}{} }
}

type ProvisionalLease struct {
	id        string
	authority Authority
	identity  ProvisionalIdentity
	resource  Resource
	registry  *provisionalRegistry
}

func (l ProvisionalLease) ID() string { return l.id }

func (l ProvisionalLease) Identity() ProvisionalIdentity { return l.identity }

// Close releases a bound or reserved provisional lease. It is idempotent;
// once closed, the lease cannot authorize a rollback request.
func (l *ProvisionalLease) Close() error {
	if l == nil || l.registry == nil || l.id == "" {
		return nil
	}
	release := l.registry.lock()
	defer release()
	state, ok := l.registry.leases[l.id]
	if !ok || state == nil || !sameProvisionalLeaseDescriptor(state, l) {
		return nil
	}
	delete(l.registry.leases, l.id)
	delete(l.registry.pathClaims, state.identity.CanonicalPath)
	delete(l.registry.commonClaims, state.identity.CommonDir)
	delete(l.registry.operationClaims, state.identity.CreationOperationID)
	return nil
}

func (l *ProvisionalLease) Bind(ctx context.Context) error {
	if l == nil || l.registry == nil || l.id == "" {
		return ErrProvisionalLease
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	anchor, err := CaptureAnchor(l.resource.Path)
	if err != nil {
		return fmt.Errorf("%w: bind: %v", ErrProvisionalLease, err)
	}
	release := l.registry.lock()
	defer release()
	state, ok := l.registry.leases[l.id]
	if !ok || state == nil || !sameProvisionalLeaseDescriptor(state, l) {
		return ErrProvisionalLease
	}
	switch state.status {
	case provisionalLeaseBound:
		return ErrProvisionalLeaseUsed
	case provisionalLeaseReserved:
		state.anchor = &anchor
		state.status = provisionalLeaseBound
		return nil
	default:
		return ErrProvisionalLease
	}
}

func (l ProvisionalLease) RollbackRequest() (Request, error) {
	if l.registry == nil || l.id == "" {
		return Request{}, ErrProvisionalLease
	}
	release := l.registry.lock()
	state, ok := l.registry.leases[l.id]
	if !ok || state == nil || !sameProvisionalLeaseDescriptor(state, &l) ||
		state.status != provisionalLeaseBound || state.anchor == nil {
		release()
		return Request{}, fmt.Errorf("%w: lease is not bound", ErrProvisionalLease)
	}
	resource := state.resource
	resource.Anchor = state.anchor
	resource.LeaseID = l.id
	identity := state.identity
	release()
	return Request{
		Action:    ActionProvisionalRollback,
		Authority: state.authority,
		Executor:  ExecutorFilesystem,
		Resource:  resource,
		Identity:  identity,
		lease:     &l,
	}, nil
}

func (s *Service) BeginProvisional(ctx context.Context, request CreateRequest) (ProvisionalLease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ProvisionalLease{}, err
	}
	if !knownAuthority(request.Authority) {
		return ProvisionalLease{}, fmt.Errorf("%w: unknown authority", ErrInvalidRequest)
	}
	resource, err := normalizeResource(request.Resource)
	if err != nil {
		return ProvisionalLease{}, err
	}
	if resource.Kind != ResourceKindProvisional {
		return ProvisionalLease{}, fmt.Errorf("%w: provisional lease has kind %q", ErrInvalidRequest, resource.Kind)
	}
	identity, err := normalizeProvisionalIdentity(request.Identity, resource)
	if err != nil {
		return ProvisionalLease{}, err
	}
	paths, commonDirs := requestLockPaths(Request{Resource: resource})
	releaseLocks, err := s.locks.Acquire(ctx, paths, commonDirs)
	if err != nil {
		return ProvisionalLease{}, err
	}
	defer releaseLocks()
	if _, err := os.Lstat(resource.Path); err == nil {
		return ProvisionalLease{}, ErrProvisionalTarget
	} else if !errors.Is(err, os.ErrNotExist) {
		return ProvisionalLease{}, fmt.Errorf("%w: inspect target: %v", ErrProvisionalLease, err)
	}
	id := uuid.NewString()
	state := &provisionalState{
		id:        id,
		authority: request.Authority,
		identity:  identity,
		resource:  resource,
		status:    provisionalLeaseReserved,
	}
	if err := s.leases.reserve(state); err != nil {
		return ProvisionalLease{}, err
	}
	if err := ctx.Err(); err != nil {
		s.leases.invalidate(id)
		return ProvisionalLease{}, err
	}
	return ProvisionalLease{
		id:        id,
		authority: request.Authority,
		identity:  identity,
		resource:  resource,
		registry:  s.leases,
	}, nil
}

func (r *provisionalRegistry) reserve(state *provisionalState) error {
	if r == nil || state == nil || state.id == "" {
		return ErrProvisionalLease
	}
	release := r.lock()
	defer release()
	if _, exists := r.leases[state.id]; exists {
		return ErrProvisionalLease
	}
	if _, exists := r.pathClaims[state.identity.CanonicalPath]; exists {
		return ErrProvisionalLease
	}
	if _, exists := r.commonClaims[state.identity.CommonDir]; exists {
		return ErrProvisionalLease
	}
	if _, exists := r.operationClaims[state.identity.CreationOperationID]; exists {
		return ErrProvisionalLease
	}
	r.leases[state.id] = state
	r.pathClaims[state.identity.CanonicalPath] = state.id
	r.commonClaims[state.identity.CommonDir] = state.id
	r.operationClaims[state.identity.CreationOperationID] = state.id
	return nil
}

func (r *provisionalRegistry) invalidate(id string) {
	if r == nil || id == "" {
		return
	}
	release := r.lock()
	defer release()
	state, ok := r.leases[id]
	if !ok || state == nil || state.status != provisionalLeaseReserved {
		return
	}
	delete(r.leases, id)
	delete(r.pathClaims, state.identity.CanonicalPath)
	delete(r.commonClaims, state.identity.CommonDir)
	delete(r.operationClaims, state.identity.CreationOperationID)
}

func (r *provisionalRegistry) protections() ([]ProvisionalProtection, error) {
	if r == nil {
		return nil, ErrInventoryIncomplete
	}
	release := r.lock()
	defer release()
	if r.leases == nil || r.pathClaims == nil || r.commonClaims == nil || r.operationClaims == nil {
		return nil, fmt.Errorf("%w: provisional registry claims are malformed", ErrInventoryIncomplete)
	}
	for path, leaseID := range r.pathClaims {
		state, ok := r.leases[leaseID]
		if !ok || state == nil || state.identity.CanonicalPath != path {
			return nil, fmt.Errorf("%w: provisional path claim is malformed", ErrInventoryIncomplete)
		}
	}
	for commonDir, leaseID := range r.commonClaims {
		state, ok := r.leases[leaseID]
		if !ok || state == nil || state.identity.CommonDir != commonDir {
			return nil, fmt.Errorf("%w: provisional common-directory claim is malformed", ErrInventoryIncomplete)
		}
	}
	for operationID, leaseID := range r.operationClaims {
		state, ok := r.leases[leaseID]
		if !ok || state == nil || state.identity.CreationOperationID != operationID {
			return nil, fmt.Errorf("%w: provisional operation claim is malformed", ErrInventoryIncomplete)
		}
	}
	protections := make([]ProvisionalProtection, 0, len(r.leases))
	for _, state := range r.leases {
		if state == nil || (state.status != provisionalLeaseReserved && state.status != provisionalLeaseBound) {
			return nil, fmt.Errorf("%w: unknown provisional lease state", ErrInventoryIncomplete)
		}
		if r.pathClaims[state.identity.CanonicalPath] != state.id ||
			r.commonClaims[state.identity.CommonDir] != state.id ||
			r.operationClaims[state.identity.CreationOperationID] != state.id {
			return nil, fmt.Errorf("%w: provisional lease claims are incomplete", ErrInventoryIncomplete)
		}
		identity, err := normalizeProvisionalIdentity(state.identity, state.resource)
		if err != nil {
			return nil, fmt.Errorf("%w: provisional lease %s: %v", ErrInventoryIncomplete, state.id, err)
		}
		if state.id == "" || state.authority == "" ||
			!sameProvisionalIdentity(identity, state.identity) ||
			(state.status == provisionalLeaseBound && state.anchor == nil) {
			return nil, fmt.Errorf("%w: malformed provisional lease %s", ErrInventoryIncomplete, state.id)
		}
		protections = append(protections, ProvisionalProtection{
			LeaseID:             state.id,
			State:               string(state.status),
			TaskOwnerID:         identity.TaskOwnerID,
			SessionOwnerID:      identity.SessionOwnerID,
			CreationOperationID: identity.CreationOperationID,
			ManagedRootID:       identity.ManagedRootID,
			Path:                identity.CanonicalPath,
			RootPath:            state.resource.RootPath,
			CommonDir:           identity.CommonDir,
		})
	}
	sort.Slice(protections, func(i, j int) bool { return protections[i].LeaseID < protections[j].LeaseID })
	return protections, nil
}

func (s *Service) verifyProvisional(request Request) error {
	if request.lease == nil || request.Resource.LeaseID == "" {
		return ErrProvisionalLease
	}
	release := s.leases.lock()
	defer release()
	state, ok := s.leases.leases[request.Resource.LeaseID]
	if !ok || state == nil || state.status != provisionalLeaseBound || state.anchor == nil {
		return ErrProvisionalLease
	}
	if !sameProvisionalLeaseDescriptor(state, request.lease) ||
		state.authority != request.Authority ||
		!sameProvisionalIdentity(state.identity, request.Identity) ||
		!sameResourceIdentity(state.resource, request.Resource) ||
		!sameAnchor(state.anchor, request.Resource.Anchor) {
		return ErrProvisionalLease
	}
	return nil
}

func normalizeProvisionalIdentity(identity ProvisionalIdentity, resource Resource) (ProvisionalIdentity, error) {
	for name, value := range map[string]string{
		"task owner":         identity.TaskOwnerID,
		"session owner":      identity.SessionOwnerID,
		"creation operation": identity.CreationOperationID,
		"managed root":       identity.ManagedRootID,
		"canonical path":     identity.CanonicalPath,
		"common directory":   identity.CommonDir,
	} {
		if value == "" || strings.IndexByte(value, 0) >= 0 {
			return ProvisionalIdentity{}, fmt.Errorf("%w: provisional %s identity is required", ErrProvisionalLease, name)
		}
	}
	if resource.RootPath == "" || resource.CommonDir == "" {
		return ProvisionalIdentity{}, fmt.Errorf("%w: provisional root/common identity is required", ErrProvisionalLease)
	}
	canonicalPath, err := canonicalLockPath(identity.CanonicalPath)
	if err != nil {
		return ProvisionalIdentity{}, fmt.Errorf("%w: canonical path: %v", ErrProvisionalLease, err)
	}
	canonicalCommonDir, err := canonicalLockPath(identity.CommonDir)
	if err != nil {
		return ProvisionalIdentity{}, fmt.Errorf("%w: common directory: %v", ErrProvisionalLease, err)
	}
	if canonicalPath != resource.Path {
		return ProvisionalIdentity{}, fmt.Errorf("%w: canonical path drift", ErrProvisionalLease)
	}
	if canonicalCommonDir != resource.CommonDir {
		return ProvisionalIdentity{}, fmt.Errorf("%w: common directory drift", ErrProvisionalLease)
	}
	identity.CanonicalPath = canonicalPath
	identity.CommonDir = canonicalCommonDir
	return identity, nil
}

func sameProvisionalIdentity(left, right ProvisionalIdentity) bool {
	return left == right
}

func sameResourceIdentity(left, right Resource) bool {
	return left.Kind == right.Kind && left.ID == right.ID && left.Path == right.Path &&
		left.RootPath == right.RootPath && left.CommonDir == right.CommonDir
}

func sameProvisionalLeaseDescriptor(state *provisionalState, lease *ProvisionalLease) bool {
	return state != nil && lease != nil && state.id == lease.id &&
		state.authority == lease.authority &&
		sameProvisionalIdentity(state.identity, lease.identity) &&
		sameResourceIdentity(state.resource, lease.resource)
}

func sameAnchor(left, right *Anchor) bool {
	return left != nil && right != nil && left.path == right.path && left.info != nil &&
		right.info != nil && os.SameFile(left.info, right.info)
}
