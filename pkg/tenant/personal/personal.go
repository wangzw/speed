// Package personal is the tenant capability's personal-tenant module: it
// makes every user account own a personal tenant, written in the account's own
// transaction, and drives that tenant through the account's life.
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

// ModuleName is this module's name in the registry.
const ModuleName = "org.personal"

// FeatureName is the static feature this module serves; it is always present.
const FeatureName = ModuleName

// IDPrefix is the prefix of every personal tenant identifier; the rest is the
// account identifier. It is reserved in the username namespace.
const IDPrefix = "personal-"

// TenantID derives the personal tenant identifier from the account.
func TenantID(account authn.AccountID) tenant.ID {
	return tenant.ID(IDPrefix + string(account))
}

// IsPersonalTenantID reports whether an identifier is a personal tenant's.
// It matches any account's personal tenant, so a caller asking whether a
// tenant is its own must compare against TenantID instead.
func IsPersonalTenantID(id string) bool {
	return strings.HasPrefix(id, IDPrefix)
}

const personalTenantAdminRole = authz.TenantAdminRole

const legacyPersonalOwnerRole = authz.Role("owner")

const personalTenantAdminDescriptionKey = "authz.role.tenantAdmin"

const personalMemberSource = "personal"

// personalBusinessPermissions is written out, not derived from the process
// catalog, so a permission a module declares later does not join the role on its own.
var personalBusinessPermissions = []authz.Permission{}

var personalExcludedPermissions = []authz.Permission{
	authz.PermissionMembershipManage,
	authz.PermissionIDPManage,
}

func personalTenantAdminPermissions(catalog authz.Catalog) []authz.Permission {
	return personalAllowlist(catalog, personalBusinessPermissions)
}

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

const (
	personalCreateAction   = "org.personal.tenant.create"
	personalDeleteAction   = "org.personal.tenant.delete"
	personalOutcomeSuccess = "success"
	personalOutcomeFailed  = "failed"
	personalDetailActionID = "action_id"
	personalDetailAccount  = "account"
	personalDetailTenant   = "tenant"
)

var (
	// ErrTenantUnavailable refuses an account whose personal tenant row exists
	// and is not enabled.
	ErrTenantUnavailable = errors.New("the personal tenant exists and is not enabled")
	// ErrIdentifierUnavailable refuses an identifier or name the personal
	// tenant cannot take.
	ErrIdentifierUnavailable = errors.New("the personal tenant's identifier or name is unavailable")
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module returns the descriptor a host registers. The module is always
// enabled, declares the static feature org.personal and mounts no route.
//
// The membership, deletion-removal and transaction-aware membership seams are
// resolved in Init, not required: the account module requires the provisioning
// seam this module provides, so a requirement would be a cycle.
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

type product struct {
	roster            tenant.Catalog
	txRoster          tenantcatalog.TxCatalog
	txManager         authzpolicy.TxManager
	membershipWriter  provisioning.MembershipWriter
	memberships       authn.Memberships
	membershipCleanup authn.MembershipCleanup
	manager           authz.Manager
	seeder            authz.Seeder
	catalog           authz.Catalog
	audit             audit.Audit
	logger            *slog.Logger
	accounts          accountReader
	bus               event.Bus
	seen              *seenEvents
}

type accountReader interface {
	Get(ctx context.Context, id authn.AccountID) (authn.Account, error)
	Identifiers(ctx context.Context, id authn.AccountID) ([]authn.Identifier, error)
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

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

const seenEventsCapacity = 4096

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
