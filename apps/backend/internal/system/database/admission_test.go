package database

import (
	"context"

	"github.com/kandev/kandev/internal/physicaldelete"
)

// permissiveAdmission is a controllable physicaldelete.Admission for the
// factory-reset tests. Task 07 binds every destructive step behind the
// sealed central gate; tests that want the historical wipe side-effects
// use newPermissiveAdmission, and tests that want the fail-closed contract
// use newDeniedAdmission. Both helpers share the same shape so the test
// fixture only has to swap the field.
type permissiveAdmission struct {
	denied bool
}

func (p permissiveAdmission) BeginProvisional(_ context.Context, _ physicaldelete.CreateRequest) (physicaldelete.ProvisionalLease, error) {
	return physicaldelete.ProvisionalLease{}, nil
}

func (p permissiveAdmission) Execute(_ context.Context, req physicaldelete.Request) (physicaldelete.Receipt, error) {
	if p.denied {
		return physicaldelete.Receipt{
			Action:       req.Action,
			ResourceKind: req.Resource.Kind,
			ResourceID:   req.Resource.ID,
			Reason:       physicaldelete.DenialExecutorUnavailable,
		}, physicaldelete.ErrExecutorUnavailable
	}
	return physicaldelete.Receipt{
		Action: req.Action, ResourceKind: req.Resource.Kind,
		ResourceID: req.Resource.ID, Mutated: false,
	}, nil
}

func newPermissiveAdmissionForReset() physicaldelete.Admission {
	return permissiveAdmission{}
}

func newDeniedAdmissionForReset() physicaldelete.Admission {
	return permissiveAdmission{denied: true}
}
