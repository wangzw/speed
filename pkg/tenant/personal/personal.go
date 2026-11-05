// Package personal is the tenant capability's personal-tenant module: it
// makes every user account own a personal tenant and drives that tenant
// through the account's life.
//
// The tenant is written inside the account's own transaction through
// authn/provisioning.AccountProvisioning, so an account and the space it owns
// either both come into being or neither does. The identity's alias is the
// account's username, which is how a personal organization is addressed on the
// request surface; a user account always has a username, and the seam refuses
// one that carries none. The service account owns no organization and the seam
// writes nothing for it.
//
// The module also implements authn.AccountLifecycle: after the account's
// transaction has committed the create hook completes anything the seam left
// unfinished, the two status hooks move the tenant with the account, and the
// deletion hook winds the tenant down. Those hooks are idempotent and are the
// retry path; the transaction seam is the authority.
//
// The module is always in force when an assembly names it: there is no
// configuration switch, because a user account without its personal tenant is
// not a state the product has.
package personal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that assembles personal tenants.
const ModuleName = "org.personal"

// FeatureName is the static feature this module serves. It is present whenever
// the module is in force; there is no switch that withdraws it.
const FeatureName = ModuleName

// IDPrefix is the fixed prefix of every personal tenant identifier. The rest
// is the account identifier, so the string is deterministic and carries no
// personal data. The prefix is reserved in the username namespace, so no
// account's alias can read as one of these identifiers.
const IDPrefix = "personal-"

// TenantID derives the personal tenant identifier from the account. The
// identifier is internal and deterministic; the tenant's alias is the
// account's username and is written separately.
func TenantID(account authn.AccountID) tenant.ID {
	return tenant.ID(IDPrefix + string(account))
}

// IsPersonalTenantID reports whether an identifier is a personal tenant's.
//
// It is the one place the prefix is compared. The host's write guards and the
// seed's skip call it, so the derivation has one statement rather than several
// that can drift apart. A caller that has the account in hand compares
// TenantID's answer exactly instead; a prefix test would mark another account's
// personal tenant personal too.
func IsPersonalTenantID(id string) bool {
	return strings.HasPrefix(id, IDPrefix)
}

// personalTenantAdminRole is the role the module writes in every personal
// tenant.
const personalTenantAdminRole = authz.TenantAdminRole

// legacyPersonalOwnerRole is the role the old personal-tenant model wrote. The
// ensure removes it, with its grants and bindings, once the tenant-admin role
// and the account's binding are in place.
const legacyPersonalOwnerRole = authz.Role("owner")

// personalTenantAdminDescriptionKey is the description key written with the
// tenant-admin role.
const personalTenantAdminDescriptionKey = "authz.role.tenantAdmin"

// personalMemberSource is the membership source the seam and the ensure write.
// The account module publishes it on the membership event; "personal" names this
// module's own provisioning rather than an administrator, an invitation or an
// import.
const personalMemberSource = "personal"

// personalBusinessPermissions is the set of business-resource permissions a
// personal tenant's tenant-admin may hold. It is written out in code and not
// derived from the process catalog, so a permission a module declares later
// does not join the role on its own. It is empty in this skeleton.
var personalBusinessPermissions = []authz.Permission{}

// personalExcludedPermissions is the part of the business-resource face a
// personal tenant's tenant-admin must never hold: membership management and
// invitations would let its holder add accounts, and identity-provider
// management would let it manage SSO.
var personalExcludedPermissions = []authz.Permission{
	authz.PermissionMembershipManage,
	authz.PermissionIDPManage,
}

// personalTenantAdminPermissions is the allowlist the ensure converges the
// tenant-admin role's grants to.
func personalTenantAdminPermissions(catalog authz.Catalog) []authz.Permission {
	return personalAllowlist(catalog, personalBusinessPermissions)
}

// personalAllowlist derives the personal tenant-admin allowlist from the
// business-resource set: (business ∩ TenantGoverned) − excluded.
func personalAllowlist(catalog authz.Catalog, business []authz.Permission) []authz.Permission {
	governed := make(map[authz.Permission]bool, len(catalog.TenantGoverned()))
	for _, permission := range catalog.TenantGoverned() {
		governed[permission] = true
	}
	out := make([]authz.Permission, 0, len(business))
	for _, permission := range business {
		if !governed[permission] {
			continue
		}
		if slices.Contains(personalExcludedPermissions, permission) {
			continue
		}
		out = append(out, permission)
	}
	return out
}

// The audit vocabulary the module writes, one action per tenant-side write.
const (
	personalCreateAction   = "org.personal.tenant.create"
	personalDeleteAction   = "org.personal.tenant.delete"
	personalOutcomeSuccess = "success"
	personalOutcomeFailed  = "failed"
	personalDetailActionID = "action_id"
	personalDetailAccount  = "account"
	personalDetailTenant   = "tenant"
)

// The module's refusals.
var (
	// ErrTenantUnavailable refuses an account whose personal tenant row exists
	// and is not enabled.
	ErrTenantUnavailable = errors.New("the personal tenant exists and is not enabled")
	// ErrIdentifierUnavailable refuses an identifier or alias the personal
	// tenant cannot take.
	ErrIdentifierUnavailable = errors.New("the personal tenant's identifier or alias is unavailable")
)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The roster and its transaction-aware write, the policy manager and its
// transaction-aware surface, the seeding surface, the permission catalog and
// the audit capability are required: the seam writes the first five, the
// lifecycle writes the audit row, and a command that names this module without
// one of them cannot run it.
//
// The module provides the account provisioning seam, the account lifecycle and
// the tenant kind resolver. It provides the resolver rather than requiring one
// because the module itself is the one place that knows which identifiers are
// personal. An assembly without this module has no personal tenant, and
// tenant.self's optional resolver then reads every tenant as business.
//
// The account module requires the provisioning seam this module provides, so
// the membership read, the deletion removal and the transaction-aware
// membership write are taken up in Init, not declared in Requires: a
// requirement in the other direction would be a cycle. The optional
// capabilities are taken up there too because their modules may sit downstream
// of the account module.
//
// The feature is static. A user account's personal tenant is part of what the
// account is, so there is no configuration item that turns it off.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*tenantcatalog.TxCatalog)(nil)},
			{Token: (*authzpolicy.TxManager)(nil)},
			{Token: (*authz.Manager)(nil)},
			{Token: (*authz.Seeder)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.AccountLifecycle)(nil)},
			{Token: (*provisioning.AccountProvisioning)(nil)},
			{Token: (*tenant.KindResolver)(nil)},
		},
		Resources: []any{
			feature.Spec{Name: FeatureName},
		},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},
	}
}

// product is the module's instance.
type product struct {
	roster tenant.Catalog
	// txRoster and txManager are the transaction-aware writes the provisioning
	// seam runs on the account's transaction.
	txRoster  tenantcatalog.TxCatalog
	txManager authzpolicy.TxManager
	// membershipWriter is the account module's transaction-aware membership
	// write. It is taken up in Init because the account module requires the
	// provisioning seam this module provides.
	membershipWriter provisioning.MembershipWriter
	memberships      authn.Memberships
	// membershipCleanup is the account-deletion removal path, which does not ask
	// the membership guard.
	membershipCleanup authn.MembershipCleanup
	manager           authz.Manager
	seeder            authz.Seeder
	catalog           authz.Catalog
	audit             audit.Audit
	logger            *slog.Logger
	// accounts is the account read surface; nil in an assembly without one.
	// The event compensation reads the account's current state through it, and
	// the backfill reads the account's username to name the tenant's alias.
	accounts accountReader
	// bus is where the compensation subscribes; nil leaves the synchronous
	// hooks as the only route.
	bus  event.Bus
	seen *seenEvents
}

// accountReader is the part of authn.Accounts this module reads.
type accountReader interface {
	Get(ctx context.Context, id authn.AccountID) (authn.Account, error)
	Identifiers(ctx context.Context, id authn.AccountID) ([]authn.Identifier, error)
}

// productOf narrows an instance to this module's product.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// newProduct takes up what the descriptor requires.
func newProduct(reg *core.Registry) (*product, error) {
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, err)
	}
	txRoster, err := core.Resolve[tenantcatalog.TxCatalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the transaction-aware tenant roster: %w", ModuleName, err)
	}
	txManager, err := core.Resolve[authzpolicy.TxManager](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the transaction-aware policy manager: %w", ModuleName, err)
	}
	manager, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy manager: %w", ModuleName, err)
	}
	seeder, err := core.Resolve[authz.Seeder](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy seeding surface: %w", ModuleName, err)
	}
	catalog, err := core.Resolve[authz.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the permission catalog: %w", ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", ModuleName, err)
	}
	return &product{
		roster:    roster,
		txRoster:  txRoster,
		txManager: txManager,
		manager:   manager,
		seeder:    seeder,
		catalog:   catalog,
		audit:     auditor,
		logger:    log.Default(),
		seen:      newSeenEvents(seenEventsCapacity),
	}, nil
}

// wire takes up the optional capabilities and subscribes the compensation.
func (p *product) wire(reg *core.Registry) error {
	var err error
	if delivered, loggerErr := core.Resolve[log.Logger](reg); loggerErr == nil {
		p.logger = delivered.Named(ModuleName)
	}
	if bus, found, busErr := optional[event.Bus](reg, "the event bus"); busErr != nil {
		return busErr
	} else if found {
		p.bus = bus
	}
	if p.memberships, err = core.Resolve[authn.Memberships](reg); err != nil {
		return fmt.Errorf("%s: taking up the memberships: %w", ModuleName, err)
	}
	if p.membershipCleanup, err = core.Resolve[authn.MembershipCleanup](reg); err != nil {
		return fmt.Errorf("%s: taking up the account-deletion membership removal: %w", ModuleName, err)
	}
	if p.membershipWriter, err = core.Resolve[provisioning.MembershipWriter](reg); err != nil {
		return fmt.Errorf("%s: taking up the transaction-aware membership write: %w", ModuleName, err)
	}
	if accounts, found, accountsErr := optional[authn.Accounts](reg, "the account read surface"); accountsErr != nil {
		return accountsErr
	} else if found {
		p.accounts = accounts
	}
	p.subscribe()
	return nil
}

// optional takes up a capability whose absence is a legal assembly.
func optional[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

// seenEventsCapacity bounds the set of event identifiers remembered to tell a
// second delivery from a new event.
const seenEventsCapacity = 4096

// seenEvents remembers the identifiers of the events handled lately, bounded:
// when it is full the oldest is forgotten.
type seenEvents struct {
	mu       sync.Mutex
	capacity int
	order    []string
	byID     map[string]struct{}
}

func newSeenEvents(capacity int) *seenEvents {
	return &seenEvents{capacity: capacity, byID: make(map[string]struct{})}
}

func (s *seenEvents) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.byID[id]
	return ok
}

func (s *seenEvents) add(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; ok {
		return
	}
	for len(s.order) >= s.capacity {
		delete(s.byID, s.order[0])
		s.order = s.order[1:]
	}
	s.order = append(s.order, id)
	s.byID[id] = struct{}{}
}
