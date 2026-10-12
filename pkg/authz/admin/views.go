package admin

import (
	"time"

	"github.com/vislake/speed/pkg/authz"
)

// The views the management routes answer with. Each is built field by field from
// a root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there.

// permissionView is one declared permission as the catalog route shows it.
type permissionView struct {
	Permission string `json:"permission"`
}

func permissionViewOf(p authz.Permission) permissionView {
	return permissionView{Permission: string(p)}
}

// roleView is one role as the role routes show it.
type roleView struct {
	Tenant         string    `json:"tenant"`
	Role           string    `json:"role"`
	Builtin        bool      `json:"builtin"`
	DescriptionKey string    `json:"description_key"`
	CreatedAt      time.Time `json:"created_at"`
}

func roleViewOf(r authz.RoleRecord) roleView {
	return roleView{
		Tenant:         r.Tenant,
		Role:           string(r.Role),
		Builtin:        r.Builtin,
		DescriptionKey: r.DescriptionKey,
		CreatedAt:      r.CreatedAt.UTC(),
	}
}

// grantView is one permission grant as the grant routes show it. The resource
// and the action are the pair the engine matches on; the permission a route
// names is translated to it on the way in.
type grantView struct {
	Tenant    string    `json:"tenant"`
	Role      string    `json:"role"`
	Resource  string    `json:"resource"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"created_at"`
}

func grantViewOf(g authz.GrantRecord) grantView {
	return grantView{
		Tenant:    g.Tenant,
		Role:      string(g.Role),
		Resource:  g.Resource,
		Action:    g.Action,
		CreatedAt: g.CreatedAt.UTC(),
	}
}

// bindingView is one role binding as the binding routes show it.
type bindingView struct {
	Tenant    string    `json:"tenant"`
	Account   string    `json:"account"`
	Role      string    `json:"role"`
	GrantedAt time.Time `json:"granted_at"`
}

func bindingViewOf(b authz.Binding) bindingView {
	return bindingView{
		Tenant:    b.Tenant,
		Account:   b.Account,
		Role:      string(b.Role),
		GrantedAt: b.GrantedAt.UTC(),
	}
}
