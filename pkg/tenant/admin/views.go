package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// The views the management routes answer with. Each is built field by field from
// a root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there.

// TenantAdminTenant is one tenant as the management routes show it. It carries
// the name the request surface addresses it by, the kind, the account that
// owns the organization, and its state, spelled the way tenant.Status spells
// it: active, disabled or deleted. A disabled tenant also carries who disabled
// it, spelled the way tenant.DisabledBy spells the source: operator or account.
//
// It carries no identifier: the identifier is internal, and a client addresses
// the tenant by the name alone.
type TenantAdminTenant struct {
	_ struct{} `description:"One tenant as the management routes render it: the name, the kind, the organization profile, the owner account, the state, the creation time and who disabled it while it is disabled."`

	Name         string `json:"name" required:"true" description:"The name the request surface addresses the tenant by, and the one every view and path uses."`
	Kind         string `json:"kind" required:"true" enum:"business,personal" description:"The tenant's kind: business for an ordinary tenant, personal for the single-person space a host derives for an account. A client branches on this value, never on the name."`
	DisplayName  string `json:"displayName" required:"true" description:"The organization's display name. Empty when its profile names none; a surface falls back to the name then."`
	Description  string `json:"description" required:"true" description:"The organization's description. Empty when its profile names none."`
	ContactEmail string `json:"contactEmail" required:"true" description:"The organization's contact address. Empty when its profile names none. It is the organization's address, not a login identity."`
	Website      string `json:"website" required:"true" description:"The organization's absolute http or https address. Empty when its profile names none."`
	OwnerAccount string `json:"ownerAccount" required:"true" description:"The account that owns the organization, from the roster row's owner column. Empty for a tenant that carries no owner, such as the platform's own tenant. This is a platform-domain detail; no member-facing view carries it."`
	Status       string `json:"status" required:"true" enum:"active,disabled,deleted" description:"The tenant's state: active is served, disabled is not and may be served again, deleted is a tombstone that is never served again."`
	// CreatedAt is when the roster recorded the registration. Every row carries
	// one, so the field is always present and a surface needs no fallback.
	CreatedAt time.Time `json:"createdAt" required:"true" description:"When the tenant was registered, as an RFC 3339 instant in UTC."`
	// DisabledBy is present only while Status is disabled. The field is left
	// out otherwise, so a source never appears on a row whose state it does not
	// describe.
	DisabledBy string `json:"disabledBy,omitempty" enum:"operator,account" description:"Who disabled the tenant: operator through a status change, or account through the account's own state. Present only while the state is disabled."`
}

// tenantView is the name the handlers and their tests use for the view. It is an
// name, so the fragment publishes the component under the name the hand-written
// fragment used: TenantAdminTenant.
type tenantView = TenantAdminTenant

// tenantViewOf builds the view field by field from a root-package value. The
// owner is the roster row's own owner column, rendered as the account
// identifier it stores; a tenant with no owner renders it empty, so the field is
// always present. The disable source is rendered whenever the view spells the
// state as disabled,
// which includes a value this build does not know: that value reads as disabled
// everywhere else, so the source it carries belongs with it. Rendering it only
// for the literal StatusDisabled would leave such a row showing a disabled state
// and no reason, which is the contradiction the view exists to avoid.
func tenantViewOf(t tenant.Tenant, kind tenant.Kind) TenantAdminTenant {
	view := TenantAdminTenant{
		Name:         t.Name,
		Kind:         string(kind),
		DisplayName:  t.Profile.DisplayName,
		Description:  t.Profile.Description,
		ContactEmail: t.Profile.ContactEmail,
		Website:      t.Profile.Website,
		OwnerAccount: t.OwnerAccount,
		Status:       t.Status.String(),
		CreatedAt:    t.CreatedAt.UTC(),
	}
	if t.Status.String() == tenant.StatusDisabled.String() {
		view.DisabledBy = t.DisabledBy.String()
	}
	return view
}

// TenantTransferView is one owner-transfer request as the requester's surfaces
// show it. It names the target by display name and username, never by the
// internal account identifier: a target the account store does not resolve
// leaves both fields empty.
type TenantTransferView struct {
	_ struct{} `description:"One pending or finished owner-transfer request on the organization's own surface."`

	ID                string    `json:"id" required:"true" description:"The request identifier the target and the canonical paths address."`
	TargetDisplayName string    `json:"targetDisplayName" required:"true" description:"The target account's display name. Empty when the account store does not resolve it."`
	TargetUsername    string    `json:"targetUsername" required:"true" description:"The target account's username. Empty when the account store does not resolve it."`
	RequestedAt       time.Time `json:"requestedAt" required:"true" description:"When the request was made, as an RFC 3339 instant in UTC."`
	ExpiresAt         time.Time `json:"expiresAt" required:"true" description:"When the confirmation window closes, as an RFC 3339 instant in UTC."`
	Status            string    `json:"status" required:"true" enum:"pending,confirmed,declined,cancelled,expired" description:"The request's state. Only pending requests can be cancelled or confirmed."`
}

// MyTransferView is one owner-transfer request as the target's account-level
// surface shows it. It names the organization by its name and carries no
// account or tenant identifier.
type MyTransferView struct {
	_ struct{} `description:"One owner-transfer request addressed to the caller."`

	ID          string    `json:"id" required:"true" description:"The request identifier."`
	Tenant      string    `json:"tenant" required:"true" description:"The organization whose ownership the request is about, by its name."`
	RequestedAt time.Time `json:"requestedAt" required:"true" description:"When the request was made, as an RFC 3339 instant in UTC."`
	ExpiresAt   time.Time `json:"expiresAt" required:"true" description:"When the confirmation window closes, as an RFC 3339 instant in UTC."`
	Status      string    `json:"status" required:"true" enum:"pending,confirmed,declined,cancelled,expired" description:"The request's state. Only pending requests can be confirmed or declined."`
}

// transferViewOf builds the requester's view and resolves the target's two
// names. The account identifier is never a fallback: an absent account, one
// that does not parse and a static-table subject leave both fields empty. A
// read that fails otherwise is a 503 with no cause on the wire.
func (p *product) transferViewOf(ctx context.Context, request tenant.TransferRequest) (TenantTransferView, error) {
	view := TenantTransferView{
		ID:          request.ID,
		RequestedAt: request.RequestedAt.UTC(),
		ExpiresAt:   request.ExpiresAt.UTC(),
		Status:      string(request.Status),
	}
	if p.accounts == nil || request.To == "" {
		return view, nil
	}
	id, err := authn.ParseAccountID(request.To)
	if err != nil || id.IsStatic() {
		return view, nil
	}
	account, err := p.accounts.Get(ctx, id)
	if err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return view, nil
		}
		p.logger.Error(ModuleName+": the transfer target's account could not be read, so the target is unresolved",
			slog.String("cause", err.Error()))
		return TenantTransferView{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	view.TargetDisplayName = account.DisplayName
	identifiers, err := p.accounts.IdentifiersMany(ctx, []authn.AccountID{id})
	if err != nil {
		p.logger.Error(ModuleName+": the transfer target's identifiers could not be read, so the username is unresolved",
			slog.String("cause", err.Error()))
		return TenantTransferView{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	for _, identifier := range identifiers[id] {
		if identifier.Kind == authn.IdentifierUsername {
			view.TargetUsername = identifier.Value
			break
		}
	}
	return view, nil
}

// myTransferViewOf builds the target's view, resolving the organization's name.
func (p *product) myTransferViewOf(ctx context.Context, request tenant.TransferRequest) (MyTransferView, error) {
	name, err := p.nameOf(ctx, request.Tenant)
	if err != nil {
		return MyTransferView{}, err
	}
	return MyTransferView{
		ID:          request.ID,
		Tenant:      name,
		RequestedAt: request.RequestedAt.UTC(),
		ExpiresAt:   request.ExpiresAt.UTC(),
		Status:      string(request.Status),
	}, nil
}
