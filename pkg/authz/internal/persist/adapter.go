// Package persist is the thin adapter the policy engine stores its policy through, converting between the engine's rules and the policy tables.
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
// changed one row at a time and never rewritten wholesale.
var ErrNotImplemented = errors.New("authz/persist: SavePolicy is not implemented; the policy is changed one row at a time")

type grantRow struct {
	tenant.Model
	Role      string    `gorm:"column:role;primaryKey"`
	Resource  string    `gorm:"column:resource;primaryKey"`
	Action    string    `gorm:"column:action;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (grantRow) TableName() string { return "authz_grants" }

type bindingRow struct {
	tenant.Model
	AccountID string    `gorm:"column:account_id;primaryKey"`
	Role      string    `gorm:"column:role;primaryKey"`
	GrantedAt time.Time `gorm:"column:granted_at;not null"`
}

func (bindingRow) TableName() string { return "authz_bindings" }

// Adapter stores the policy engine's rules in authz_grants and authz_bindings.
// A load reads every tenant's rows; a write names its own row's tenant and is
// refused when that tenant is not the caller's.
type Adapter struct {
	session *gorm.DB
	caller  tenant.ID
}

// New returns an adapter over handle that checks every write against caller.
// handle builds one session whose context is marked cross-tenant over ctx, and
// every statement goes through it. caller is the tenant a write acts on: a row
// whose domain is another tenant is refused with authz.ErrTenantMismatch. A read
// ignores caller; an adapter built for reading alone binds the empty identifier,
// and every write through it is refused.
func New(ctx context.Context, handle *gorm.DB, caller tenant.ID) *Adapter {
	return &Adapter{
		session: handle.Session(&gorm.Session{Context: tenant.CrossTenant(ctx)}),
		caller:  caller,
	}
}

// LoadPolicy reads every tenant's grants and bindings into m, one rule at a
// time: a grant row becomes the p rule {role, domain, resource, action} and a
// binding row the g rule {account, role, domain}. A rule that will not load is
// reported by the policy type it belongs to and never by its values.
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

// SavePolicy is not implemented; see ErrNotImplemented.
func (a *Adapter) SavePolicy(casbinmodel.Model) error { return ErrNotImplemented }

// AddPolicy writes one rule. A p rule is {role, domain, resource, action} and
// lands in authz_grants; a g rule is {account, role, domain} and lands in
// authz_bindings. A domain that is not the caller's is refused with
// authz.ErrTenantMismatch. A rule already there is a no-op.
func (a *Adapter) AddPolicy(sec, ptype string, rule []string) error {
	row, err := a.newRow(ptype, rule, time.Now().UTC())
	if err != nil {
		return err
	}
	return a.session.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error
}

// RemovePolicy deletes the one rule named, addressed by the row's whole key.
func (a *Adapter) RemovePolicy(sec, ptype string, rule []string) error {
	row, err := a.newRow(ptype, rule, time.Time{})
	if err != nil {
		return err
	}
	return a.session.Delete(row).Error
}

// RemoveFilteredPolicy deletes every rule of ptype whose fields from fieldIndex
// match the given values: an empty value matches any. The removal is held to the
// caller's tenant, and a filter naming another tenant's domain is refused with
// authz.ErrTenantMismatch rather than run.
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

func ruleColumns(ptype string) ([]string, any, bool) {
	switch ptype {
	case "p":
		return []string{"role", "tenant_id", "resource", "action"}, &grantRow{}, true
	case "g":
		return []string{"account_id", "role", "tenant_id"}, &bindingRow{}, true
	}
	return nil, nil, false
}

func domainIndex(ptype string) int {
	if ptype == "g" {
		return 2
	}
	return 1
}

func unknownPolicyType(ptype string) error {
	return fmt.Errorf("authz/persist: no table holds policy type %q", ptype)
}
