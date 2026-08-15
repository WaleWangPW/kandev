package worktree

import (
	"context"

	"github.com/kandev/kandev/internal/physicaldelete"
)

func init() {
	testAdmissionFactory = func() physicaldelete.Admission {
		svc, err := physicaldelete.New(physicaldelete.Config{
			Inventory: physicaldelete.InventorySourceFunc(func(context.Context) (physicaldelete.Inventory, error) {
				return physicaldelete.Inventory{Complete: true}, nil
			}),
		})
		if err != nil {
			panic(err)
		}
		return svc
	}
}
