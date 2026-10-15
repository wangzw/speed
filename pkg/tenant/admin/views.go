package admin

import (
	"github.com/vislake/speed/pkg/tenant"
)

// The views the management routes answer with. Each is built field by field from
// a root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there.

// tenantView is one tenant as the management routes show it. It carries the
// tenant's identifier, the alias the request surface addresses it by, and its
// state, spelled the way tenant.Status spells it: active or disabled.
type tenantView struct {
	ID     string `json:"id"`
	Alias  string `json:"alias"`
	Status string `json:"status"`
}

func tenantViewOf(t tenant.Tenant) tenantView {
	return tenantView{
		ID:     string(t.ID),
		Alias:  t.Alias,
		Status: t.Status.String(),
	}
}
