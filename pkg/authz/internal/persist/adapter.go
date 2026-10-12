// Package persist is the thin adapter the policy engine stores its policy
// through. It converts between the policy rows Casbin matches on and the rows
// of authz_grants and authz_bindings, each carrying its tenant in a real column
// of its own table rather than inside the policy string.
//
// It implements github.com/casbin/casbin/v3's persist.Adapter. That interface's
// five methods carry neither a context nor a tenant, so the two facts the tables
// need — the cross-tenant mark a load is issued under, and the tenant a write is
// checked against — are both bound once, when the adapter is built: see New.
package persist

import (
	"context"
	"errors"
	"fmt"
	"time"

	casbinmodel "github.com/casbin/casbin/v3/model"
	casbinpersist "github.com/casbin/casbin/v3/persist"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// ErrNotImplemented reports that SavePolicy was called. The policy tables are
// changed one row at a time through the management surface and never rewritten
// wholesale, so the clear-and-rewrite SavePolicy performs is left unimplemented:
// a full rewrite would drop a write that landed between its read and its write.
// It is this package's own error, not one of the root package's sentinels.
var ErrNotImplemented = errors.New("authz/persist: SavePolicy is not implemented; the policy is changed one row at a time")

// grantRow is one p policy: a role holding a permission in a domain. It is the
// row shape of authz_grants, whose tenant column leads the key.
type grantRow struct {
	tenant.Model
	Role      string    `gorm:"column:role;primaryKey"`
	Resource  string    `gorm:"column:resource;primaryKey"`
	Action    string    `gorm:"column:action;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

// TableName is the table the p policies live in.
func (grantRow) TableName() string { return "authz_grants" }

// bindingRow is one g policy: an account holding a role in a domain. It is the
// row shape of authz_bindings, whose tenant column leads the key.
type bindingRow struct {
	tenant.Model
	AccountID string    `gorm:"column:account_id;primaryKey"`
	Role      string    `gorm:"column:role;primaryKey"`
	GrantedAt time.Time `gorm:"column:granted_at;not null"`
}

// TableName is the table the g policies live in.
func (bindingRow) TableName() string { return "authz_bindings" }

// Adapter stores the policy engine's rules in authz_grants and authz_bindings.
//
// Every statement it issues carries the cross-tenant mark, established once in
// New: a load reads every tenant's rows, which is what the deciding process
// holds, and a write states its row's tenant itself rather than being filtered
// to one. What the mark lifts is the condition and the fill, not the column, so
// each written row still names a tenant, and caller is that tenant: a write
// whose domain is not the caller's is refused before it reaches the database.
type Adapter struct {
	session *gorm.DB
	caller  tenant.ID
}

// New returns an adapter over handle that checks every write against caller.
//
// handle is used to build one session whose context is marked cross-tenant with
// tenant.CrossTenant over ctx, and every statement the adapter issues goes
// through that session. The mark is bound here rather than at each call site
// because the persist.Adapter methods carry no context and its caller is
// Casbin's own Enforcer.LoadPolicy, which carries none either — there is no
// call site that could apply it. ctx is the deadline and cancellation the
// statements inherit: a load bounded by a startup timeout ends with it. A
// reload builds a fresh adapter so that the context it loads under is the one
// it was triggered with.
//
// caller is the tenant a write acts on. A policy row whose domain is another
// tenant is refused with authz.ErrTenantMismatch rather than written: the mark
// says the call intends to reach every tenant's rows, not that any row may be
// written into any tenant. A read ignores caller and crosses tenants. An
// adapter built for reading alone binds the empty identifier, and every write
// through it is refused.
func New(ctx context.Context, handle *gorm.DB, caller tenant.ID) *Adapter {
	return &Adapter{
		session: handle.Session(&gorm.Session{Context: tenant.CrossTenant(ctx)}),
		caller:  caller,
	}
}

// LoadPolicy reads every tenant's grants and bindings into m, one rule at a
// time: a grant row becomes the p rule {role, domain, resource, action} and a
// binding row the g rule {account, role, domain}. The read is this package's
// one statement against a security-sensitive table that is meant to cross
// tenants, and it goes through the session's mark, without which the filter
// would refuse it.
//
// A rule that will not load is reported by the policy type it belongs to and
// never by its values, so no grant's rule and no binding's role reaches the
// caller's error or the log it writes it to.
func (a *Adapter) LoadPolicy(m casbinmodel.Model) error {
	var grants []grantRow
	if err := a.session.Order("tenant_id, role, resource, action").Find(&grants).Error; err != nil {
		return fmt.Errorf("authz/persist: loading the grants: %w", err)
	}
	for _, g := range grants {
		rule := []string{"p", g.Role, string(g.TenantID), g.Resource, g.Action}
		if err := casbinpersist.LoadPolicyArray(rule, m); err != nil {
			return fmt.Errorf("authz/persist: loading a p rule: %w", err)
		}
	}

	var bindings []bindingRow
	if err := a.session.Order("tenant_id, account_id, role").Find(&bindings).Error; err != nil {
		return fmt.Errorf("authz/persist: loading the bindings: %w", err)
	}
	for _, b := range bindings {
		rule := []string{"g", b.AccountID, b.Role, string(b.TenantID)}
		if err := casbinpersist.LoadPolicyArray(rule, m); err != nil {
			return fmt.Errorf("authz/persist: loading a g rule: %w", err)
		}
	}
	return nil
}

// SavePolicy is not implemented. The policy tables are changed one row at a
// time, never rewritten wholesale; see ErrNotImplemented.
func (a *Adapter) SavePolicy(casbinmodel.Model) error { return ErrNotImplemented }

// AddPolicy writes one rule. A p rule is {role, domain, resource, action} and
// lands in authz_grants; a g rule is {account, role, domain} and lands in
// authz_bindings. The domain becomes the row's tenant column, and a domain that
// is not the caller's is refused with authz.ErrTenantMismatch before any
// statement is issued.
//
// A rule already there is left as it is rather than reported as a conflict: the
// write is keyed by the rule's whole key, so the same rule written twice names
// the same row, and the intent of a second write is that the row be there, which
// it already is. This is how the management surface's PUT routes and the host's
// role provisioner — both documented as idempotent, "a second run adds no row" —
// behave: a repeated grant or binding is a no-op, not a duplicate-key failure.
func (a *Adapter) AddPolicy(sec, ptype string, rule []string) error {
	row, err := a.newRow(ptype, rule, time.Now().UTC())
	if err != nil {
		return err
	}
	return a.session.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error
}

// RemovePolicy deletes the one rule named. It is addressed by the row's whole
// key, so under the composite key the tenant column leads, exactly that row is
// removed and no other tenant's is reached.
func (a *Adapter) RemovePolicy(sec, ptype string, rule []string) error {
	row, err := a.newRow(ptype, rule, time.Time{})
	if err != nil {
		return err
	}
	return a.session.Delete(row).Error
}

// RemoveFilteredPolicy deletes every rule of ptype whose fields from fieldIndex
// match the given values, the way Casbin's field filter reads them: an empty
// value matches any, and the fields are the rule's own positions. The removal
// is held to the caller's tenant, and a filter naming another tenant's domain
// is refused with authz.ErrTenantMismatch rather than run.
func (a *Adapter) RemoveFilteredPolicy(sec, ptype string, fieldIndex int, fieldValues ...string) error {
	columns, target, ok := ruleColumns(ptype)
	if !ok {
		return unknownPolicyType(ptype)
	}
	if a.caller == "" {
		return fmt.Errorf("%w: a filtered removal names no tenant to act on and the caller none",
			authz.ErrTenantMismatch)
	}
	query := a.session.Where("tenant_id = ?", string(a.caller))
	for i, value := range fieldValues {
		at := fieldIndex + i
		if at < 0 || at >= len(columns) {
			return fmt.Errorf("authz/persist: the %s filter reaches field %d, past the rule's %d",
				ptype, at, len(columns))
		}
		if value == "" {
			continue
		}
		if columns[at] == "tenant_id" {
			if tenant.ID(value) != a.caller {
				return fmt.Errorf("%w: the filter names tenant %q, the caller %q",
					authz.ErrTenantMismatch, value, a.caller)
			}
			continue
		}
		query = query.Where(columns[at]+" = ?", value)
	}
	return query.Delete(target).Error
}

// newRow builds the row a p or g rule writes, with the tenant column filled
// from the rule's domain after checking it against the caller. It reads the
// rule by its position: a p rule is {role, domain, resource, action}, a g rule
// {account, role, domain}. The timestamp is written only for a create; a delete
// addresses the row by the rest of its key and leaves them unset.
func (a *Adapter) newRow(ptype string, rule []string, stamp time.Time) (any, error) {
	columns, _, ok := ruleColumns(ptype)
	if !ok {
		return nil, unknownPolicyType(ptype)
	}
	if len(rule) != len(columns) {
		return nil, fmt.Errorf("authz/persist: a %s rule carries %d fields, want %d", ptype, len(rule), len(columns))
	}
	domain := rule[domainIndex(ptype)]
	if tenant.ID(domain) != a.caller {
		return nil, fmt.Errorf("%w: the rule names tenant %q, the caller %q",
			authz.ErrTenantMismatch, domain, a.caller)
	}
	switch ptype {
	case "p":
		return &grantRow{
			Model:     tenant.Model{TenantID: tenant.ID(domain)},
			Role:      rule[0],
			Resource:  rule[2],
			Action:    rule[3],
			CreatedAt: stamp,
		}, nil
	case "g":
		return &bindingRow{
			Model:     tenant.Model{TenantID: tenant.ID(domain)},
			AccountID: rule[0],
			Role:      rule[1],
			GrantedAt: stamp,
		}, nil
	}
	return nil, unknownPolicyType(ptype)
}

// ruleColumns maps a policy type's rule positions onto the columns of its
// table, and names the model a statement against it is built on. The p rule is
// {role, domain, resource, action} over authz_grants and the g rule
// {account, role, domain} over authz_bindings; the domain always lands on the
// tenant column.
func ruleColumns(ptype string) ([]string, any, bool) {
	switch ptype {
	case "p":
		return []string{"role", "tenant_id", "resource", "action"}, &grantRow{}, true
	case "g":
		return []string{"account_id", "role", "tenant_id"}, &bindingRow{}, true
	}
	return nil, nil, false
}

// domainIndex is the rule position the domain sits at: the second of a p rule
// and the third of a g rule.
func domainIndex(ptype string) int {
	if ptype == "g" {
		return 2
	}
	return 1
}

// unknownPolicyType reports a policy type this adapter has no table for.
func unknownPolicyType(ptype string) error {
	return fmt.Errorf("authz/persist: no table holds policy type %q", ptype)
}
