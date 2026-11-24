package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
)

const seedModuleName = "seed"

const seedNamespace = "seed"

const accountsItem = "accounts"

const tenantsItem = "tenants"

const settingsItem = "settings"

const membersItem = "members"

const systemAdminsItem = "system-admins"

const seedMemberSource = authn.ReasonAdmin

const platformAdminRole = authz.Role("platform-admin")

const platformAdminDescriptionKey = "authz.role.platformAdmin"

const tenantAdminRole = authz.TenantAdminRole

const tenantAdminDescriptionKey = "authz.role.tenantAdmin"

const (
	seedReconcileAction  = "seed.authz.tenant-admin.reconcile"
	seedReconcileOutcome = "success"
)

type seedInput struct {
	Accounts     []seedAccount     `config:"accounts"`
	Tenants      []seedTenantEntry `config:"tenants"`
	Settings     []seedSetting     `config:"settings"`
	Members      []seedMember      `config:"members"`
	SystemAdmins []string          `config:"system-admins"`
}

type seedMember struct {
	Tenant    string `config:"tenant"`
	Account   string `config:"account"`
	Suspended bool   `config:"suspended"`
}

type seedTenantEntry struct {
	Name     string        `config:"name"`
	Admins   []string      `config:"admins"`
	Settings []seedSetting `config:"settings"`
}

type seedAdministrator struct {
	id   authn.AccountID
	name string
}

type seedSetting struct {
	Key   string `config:"key"`
	Value string `config:"value"`
}

type seedAccount struct {
	Identifier    string `config:"identifier"`
	Username      string `config:"username"`
	Password      string `config:"password"`
	PasswordHash  string `config:"password-hash"`
	DisplayName   string `config:"display-name"`
	PlatformAdmin bool   `config:"platform-admin"`
}

// check refuses an account the seed cannot write as the credential it is meant
// to be. It is this command's own check because a list carries no item metadata,
// so a misspelled field arrives here as a zero value and the account store would
// otherwise write an account with no credential and report it seeded.
func (a seedAccount) check() error {
	if strings.TrimSpace(a.Identifier) == "" {
		return errors.New("seed: an account under " + seedNamespace + "." + accountsItem +
			" names no identifier, and an account is found by the name it signs in with")
	}
	if strings.Contains(a.Identifier, "@") && strings.TrimSpace(a.Username) == "" {
		return fmt.Errorf("seed: the account %q under %s.%s is an email address and names no username, "+
			"and every account is created with a login name", a.Identifier, seedNamespace, accountsItem)
	}
	switch {
	case a.Password == "" && a.PasswordHash == "":
		return fmt.Errorf("seed: the account %q names neither a password nor a password-hash, "+
			"so it would be written with no credential and could never sign in", a.Identifier)
	case a.Password != "" && a.PasswordHash != "":
		return fmt.Errorf("seed: the account %q names a password and a password-hash, "+
			"which are alternatives", a.Identifier)
	}
	return nil
}

func validateSeedInput(in seedInput) error {
	for _, a := range in.Accounts {
		if err := a.check(); err != nil {
			return err
		}
	}
	if err := checkPlatformAdministrator(in); err != nil {
		return err
	}
	if err := checkTenants(in); err != nil {
		return err
	}
	if err := checkMembers(in); err != nil {
		return err
	}
	return checkSystemAdmins(in)
}

// checkPlatformAdministrator refuses an account list with nobody the console
// admits: the platform-administrator role is the only way to reach the
// management surface, and no later command writes a binding for an account the
// configuration does not mark.
func checkPlatformAdministrator(in seedInput) error {
	for _, a := range in.Accounts {
		if a.PlatformAdmin {
			return nil
		}
	}
	return fmt.Errorf("seed: no account under seed.%s is marked platform-admin, and a deployment "+
		"needs at least one platform administrator to reach the console; mark one entry with "+
		"platform-admin: true", accountsItem)
}

func checkTenants(in seedInput) error {
	accounts := make(map[string]bool, len(in.Accounts))
	for _, a := range in.Accounts {
		_, name, err := seedLoginName(a.Identifier)
		if err != nil {
			return err
		}
		accounts[name] = true
	}
	seenTenants := make(map[string]bool, len(in.Tenants))
	for _, t := range in.Tenants {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			return fmt.Errorf("seed: a tenant under seed.%s names no name, and a tenant is "+
				"registered by its name", tenantsItem)
		}
		if !tenant.IsName(name) {
			return fmt.Errorf("seed: the tenant %q is not a lower-case slug of 3 to 63 letters, "+
				"digits and dashes; the tenant's name is also its identifier", name)
		}
		if name == string(authz.PlatformDomain) {
			return fmt.Errorf("seed: the tenant %q is the system tenant, which the command registers "+
				"itself and must not be listed under seed.%s", name, tenantsItem)
		}
		if tenantpersonal.IsPersonalTenantID(name) {
			return fmt.Errorf("seed: the tenant %q carries the personal prefix; seed.%s lists the "+
				"deployment's business tenants, and personal tenants are built from account creation",
				name, tenantsItem)
		}
		if seenTenants[name] {
			return fmt.Errorf("seed: the tenant %q is declared twice under seed.%s", name, tenantsItem)
		}
		seenTenants[name] = true
		if len(t.Admins) == 0 {
			return fmt.Errorf("seed: the tenant %q names no administrator, and a tenant always has "+
				"a tenant-admin", name)
		}
		seenAdmins := make(map[string]bool, len(t.Admins))
		for _, admin := range t.Admins {
			if strings.TrimSpace(admin) == "" {
				return fmt.Errorf("seed: the tenant %q names an administrator with no account", name)
			}
			_, normalized, err := seedLoginName(admin)
			if err != nil {
				return fmt.Errorf("seed: the administrator %q of the tenant %q: %w", admin, name, err)
			}
			if !accounts[normalized] {
				return fmt.Errorf("seed: the administrator %q of the tenant %q is not under seed.%s; "+
					"add it there or correct the name", admin, name, accountsItem)
			}
			if seenAdmins[normalized] {
				return fmt.Errorf("seed: the account %q administers the tenant %q twice", admin, name)
			}
			seenAdmins[normalized] = true
		}
	}
	return nil
}

func checkMembers(in seedInput) error {
	tenants := make(map[string]bool, len(in.Tenants))
	for _, t := range in.Tenants {
		tenants[t.Name] = true
	}
	accounts := make(map[string]bool, len(in.Accounts))
	for _, a := range in.Accounts {
		_, name, err := seedLoginName(a.Identifier)
		if err != nil {
			return err
		}
		accounts[name] = true
	}
	seen := make(map[string]bool, len(in.Members))
	for _, m := range in.Members {
		if strings.TrimSpace(m.Tenant) == "" {
			return fmt.Errorf("seed: a member under seed.%s names no tenant, and a membership is a "+
				"tenant and an account", membersItem)
		}
		if strings.TrimSpace(m.Account) == "" {
			return fmt.Errorf("seed: the member of the tenant %q names no account", m.Tenant)
		}
		_, name, err := seedLoginName(m.Account)
		if err != nil {
			return fmt.Errorf("seed: the account named by the member of the tenant %q: %w", m.Tenant, err)
		}
		if !tenants[m.Tenant] {
			return fmt.Errorf("seed: the member %s names the tenant %q, which seed.%s does not list; "+
				"add it there or correct the name", name, m.Tenant, tenantsItem)
		}
		if !accounts[name] {
			return fmt.Errorf("seed: the member of the tenant %q names the account %q, which "+
				"seed.%s does not declare; add it there or correct the name", m.Tenant, m.Account,
				accountsItem)
		}
		pair := m.Tenant + "\x00" + name
		if seen[pair] {
			return fmt.Errorf("seed: the member %s of the tenant %q is declared twice under seed.%s",
				name, m.Tenant, membersItem)
		}
		seen[pair] = true
	}
	return nil
}

func checkSystemAdmins(in seedInput) error {
	accounts := make(map[string]bool, len(in.Accounts))
	for _, a := range in.Accounts {
		_, name, err := seedLoginName(a.Identifier)
		if err != nil {
			return err
		}
		accounts[name] = true
	}
	seen := make(map[string]bool, len(in.SystemAdmins))
	for _, admin := range in.SystemAdmins {
		if strings.TrimSpace(admin) == "" {
			return fmt.Errorf("seed: a system administrator under seed.%s names no account", systemAdminsItem)
		}
		_, normalized, err := seedLoginName(admin)
		if err != nil {
			return fmt.Errorf("seed: the system administrator %q: %w", admin, err)
		}
		if !accounts[normalized] {
			return fmt.Errorf("seed: the system administrator %q is not under seed.%s; "+
				"add it there or correct the name", admin, accountsItem)
		}
		if seen[normalized] {
			return fmt.Errorf("seed: the account %q administers the system tenant twice", admin)
		}
		seen[normalized] = true
	}
	return nil
}

// seedSchema declares the seed command's input items. The account list is one
// sensitive item, because a list carries no per-field metadata and every entry
// holds a password.
func seedSchema() config.Schema {
	return config.Schema{
		Namespace: seedNamespace,
		Mounts:    []config.Mount{{Value: &seedInput{}}},
		Items: map[string]config.Item{
			accountsItem: {
				Origins:   config.OriginPrimary | config.OriginEnv,
				Required:  true,
				Sensitive: true,
				Description: "the accounts a copy starts with, each with its login name, the username it " +
					"is created with when that login name is an email address, its credential as a " +
					"plaintext password or as a hash, and whether it holds the platform-administrator " +
					"role; at least one account must hold it",
			},
			tenantsItem: {
				Origins: config.OriginPrimary | config.OriginEnv,
				Description: "the business tenants this deployment serves, each with its name, " +
					"the accounts that administer it, and optionally the runtime settings written " +
					"into that tenant's layer; the system tenant must not be listed, each is " +
					"registered in the roster, and each named administrator's membership and " +
					"tenant-admin binding are written whenever either is missing",
			},
			settingsItem: {
				Origins: config.OriginPrimary | config.OriginEnv,
				Description: "runtime settings the deployment starts with, each written into " +
					"the global layer while that layer holds no explicit row for the key; the " +
					"settings module holds every value to its declaration and domain",
			},
			membersItem: {
				Origins: config.OriginPrimary | config.OriginEnv,
				Description: "the memberships the deployment starts with, each naming one of the " +
					"tenants under seed.tenants and one of the accounts under seed.accounts; a " +
					"member is active unless it is marked suspended",
			},
			systemAdminsItem: {
				Origins: config.OriginPrimary | config.OriginEnv,
				Description: "the accounts that administer the system tenant without the " +
					"platform-administrator role, each named by a login name under seed.accounts; " +
					"each gets a membership in the system tenant and its tenant-admin binding",
			},
		},
	}
}

func init() { core.ProcessRegistry.Register(seedModule()) }

func seedModule() core.Module {
	return core.Module{
		Name: seedModuleName,
		// The system tenant is registered in Init because the policy module's Start
		// requires the roster to carry the platform domain, and Requiring the roster
		// orders this callback after the roster's own Init.
		Requires:  []core.Requirement{{Token: (*tenant.Catalog)(nil)}},
		Resources: []any{seedSchema()},
		Init: func(ctx context.Context, reg *core.Registry, _ any) error {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return err
			}
			var in seedInput
			if err = reader.Decode(seedNamespace, &in); err != nil {
				return err
			}
			if err = validateSeedInput(in); err != nil {
				return err
			}
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return err
			}
			return seedTenant(ctx, roster, string(authz.PlatformDomain), "")
		},
	}
}

// seedDatabase writes the settings, the system tenant, the accounts, the
// business tenants with their administrators, the memberships, and the
// administration roles with their grants and bindings. It is idempotent and
// touches nothing the configuration no longer names; a missing membership is
// filled only for a declared administrator, never for seed.members.
func seedDatabase(ctx context.Context, reg *core.Registry) error {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return err
	}
	var in seedInput
	if err = reader.Decode(seedNamespace, &in); err != nil {
		return err
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return err
	}
	admin, err := core.Resolve[authn.AccountAdmin](reg)
	if err != nil {
		return err
	}
	manager, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return err
	}
	seeder, err := core.Resolve[authz.Seeder](reg)
	if err != nil {
		return err
	}
	reconciler, err := core.Resolve[authz.Reconciler](reg)
	if err != nil {
		return err
	}
	catalog, err := core.Resolve[authz.Catalog](reg)
	if err != nil {
		return err
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return err
	}
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return err
	}
	store, err := core.Resolve[settings.Store](reg)
	if err != nil {
		return err
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return err
	}
	// Every list is judged before anything is written: a list entry is a value the
	// manifest cannot hold to, so an error here must leave the settings below
	// unwritten.
	if err := validateSeedInput(in); err != nil {
		return err
	}

	// The settings first: a value the configuration names may govern how the
	// accounts below are written, the account store's password policy among them.
	if err := seedSettings(ctx, store, in.Settings); err != nil {
		return err
	}

	if err := seedTenant(ctx, roster, string(authz.PlatformDomain), ""); err != nil {
		return err
	}

	var administrators []seedAdministrator
	accountIDs := make(map[string]authn.AccountID, len(in.Accounts))
	created := make(map[string]bool, len(in.Accounts))
	for _, a := range in.Accounts {
		id, fresh, err := seedOneAccount(ctx, accounts, admin, a)
		if err != nil {
			return err
		}
		_, name, err := seedLoginName(a.Identifier)
		if err != nil {
			return err
		}
		accountIDs[name] = id
		created[name] = fresh
		if a.PlatformAdmin {
			administrators = append(administrators, seedAdministrator{id: id, name: name})
		}
	}

	// A configured tenant that is a tombstone is skipped entirely, so the seed does
	// not recreate what the deletion removed.
	deleted := make(map[string]bool, len(in.Tenants))
	for _, t := range in.Tenants {
		// The tenant's first configured administrator is its owner; a seeded tenant
		// that names none renders the generic owner label.
		owner := ""
		if len(t.Admins) > 0 {
			_, adminName, err := seedLoginName(t.Admins[0])
			if err != nil {
				return fmt.Errorf("seed: the administrator %q of %s: %w", t.Admins[0], t.Name, err)
			}
			if id, ok := accountIDs[adminName]; ok {
				owner = string(id)
			}
		}
		if err := seedTenant(ctx, roster, t.Name, owner); err != nil {
			return err
		}
		tombstone, err := tenantHoldsTombstone(ctx, roster, t.Name)
		if err != nil {
			return err
		}
		if tombstone {
			deleted[t.Name] = true
			fmt.Printf("skipped: tenant %s (deleted)\n", t.Name)
			continue
		}
		if err := seedTenantSettings(ctx, store, t.Name, t.Settings); err != nil {
			return err
		}
	}

	if err := seedMembers(ctx, memberships, in.Members, accountIDs, created, deleted); err != nil {
		return err
	}

	if err := seedAdministration(ctx, manager, seeder,
		string(authz.PlatformDomain), platformAdminRole, platformAdminDescriptionKey,
		declaredGrants(catalog)); err != nil {
		return err
	}
	if err := seedRosterAdministration(ctx, roster, manager, seeder, reconciler, auditor, catalog); err != nil {
		return err
	}

	// The platform-administrator binding is refused for an account that is not an
	// active member, so the membership is written first.
	for _, admin := range administrators {
		member, err := seedAdminMembership(ctx, memberships, string(authz.PlatformDomain),
			admin.id)
		if err != nil {
			return err
		}
		if !member {
			fmt.Printf("skipped: platform administrator %s (not an active member of system)\n", admin.name)
			continue
		}
		if err := seedBinding(ctx, manager, string(authz.PlatformDomain), platformAdminRole, admin.id); err != nil {
			return err
		}
		if err := seedBinding(ctx, manager, string(authz.PlatformDomain), tenantAdminRole, admin.id); err != nil {
			return err
		}
	}

	// A missing membership or tenant-admin binding is added on every run.
	for _, t := range in.Tenants {
		if deleted[t.Name] {
			fmt.Printf("skipped: administrators of %s (deleted tenant)\n", t.Name)
			continue
		}
		for _, adminName := range t.Admins {
			_, name, err := seedLoginName(adminName)
			if err != nil {
				return fmt.Errorf("seed: the administrator %q of the tenant %q: %w", adminName, t.Name, err)
			}
			id, ok := accountIDs[name]
			if !ok {
				// checkTenants refused this before anything was written.
				return fmt.Errorf("seed: the administrator %q of the tenant %q is not under seed.%s",
					adminName, t.Name, accountsItem)
			}
			member, err := seedAdminMembership(ctx, memberships, t.Name, id)
			if err != nil {
				return err
			}
			if !member {
				fmt.Printf("skipped: administrator %s of %s (not an active member)\n", name, t.Name)
				continue
			}
			if err := seedBinding(ctx, manager, t.Name, tenantAdminRole, id); err != nil {
				return err
			}
		}
	}

	if err := seedSystemTenantAdmins(ctx, memberships, manager, in.SystemAdmins, accountIDs); err != nil {
		return err
	}

	fmt.Print("note: no second factor is seeded; add one in account settings " +
		"before testing a write path (see AGENTS.md, \"Exercising a write path\")\n")
	return nil
}

// seedAdminMembership ensures an administrator is an active member of the
// tenant they administer: a missing row is written on every run, while an
// existing row is left as it stands. It answers whether the account is an active
// member, because the binding that follows has no effect without one.
func seedAdminMembership(ctx context.Context, memberships authn.Memberships,
	tenantName string, id authn.AccountID,
) (bool, error) {
	held, found, err := memberships.Membership(ctx, tenantName, id)
	if err != nil {
		return false, fmt.Errorf("seed: reading the membership of %s in %s: %w", id, tenantName, err)
	}
	if found {
		fmt.Printf("skipped: member %s in %s (exists)\n", id, tenantName)
		return held.Active, nil
	}
	if err := memberships.Put(ctx, authn.Membership{
		Account: id,
		Tenant:  tenantName,
		Active:  true,
		Source:  seedMemberSource,
	}); err != nil {
		return false, fmt.Errorf("seed: writing the administrator %s into %s: %w", id, tenantName, err)
	}
	fmt.Printf("seeded: member %s in %s (administrator)\n", id, tenantName)
	return true, nil
}

// seedSystemTenantAdmins is the system tenant's counterpart of the business
// tenant administrator loop, except that it never writes platform-admin.
func seedSystemTenantAdmins(ctx context.Context, memberships authn.Memberships, manager authz.Manager,
	admins []string, accountIDs map[string]authn.AccountID,
) error {
	for _, adminName := range admins {
		_, name, err := seedLoginName(adminName)
		if err != nil {
			return fmt.Errorf("seed: the system administrator %q: %w", adminName, err)
		}
		id, ok := accountIDs[name]
		if !ok {
			// checkSystemAdmins refused this before anything was written.
			return fmt.Errorf("seed: the system administrator %q is not under seed.%s",
				adminName, accountsItem)
		}
		member, err := seedAdminMembership(ctx, memberships, string(authz.PlatformDomain), id)
		if err != nil {
			return err
		}
		if !member {
			fmt.Printf("skipped: system administrator %s (not an active member)\n", name)
			continue
		}
		if err := seedBinding(ctx, manager, string(authz.PlatformDomain), tenantAdminRole, id); err != nil {
			return err
		}
	}
	return nil
}

// seedTenant registers one tenant unless the roster already holds it. A create
// that loses the identifier to another run is a skip; a name conflict held by
// another tenant is an error.
func seedTenant(ctx context.Context, roster tenant.Catalog, id, owner string) error {
	got, found, err := roster.Get(ctx, tenant.ID(id))
	if err != nil {
		return fmt.Errorf("seed: reading the tenant %s: %w", id, err)
	}
	if found {
		// A row that predates the owner rule takes the configured owner, a repair
		// and not a transfer. The repair is conditional in the statement, so a
		// transfer landing between this read and the write stands.
		if owner != "" && got.OwnerAccount == "" && got.Status != tenant.StatusDeleted {
			wrote, err := roster.SetOwnerIfEmpty(ctx, tenant.ID(id), owner)
			if err != nil {
				return fmt.Errorf("seed: setting the owner of %s: %w", id, err)
			}
			if wrote {
				fmt.Printf("seeded: tenant %s owner %s\n", id, owner)
			} else {
				fmt.Printf("skipped: tenant %s owner (set since the read)\n", id)
			}
			return nil
		}
		fmt.Printf("skipped: tenant %s (exists)\n", id)
		return nil
	}
	record := tenant.Tenant{ID: tenant.ID(id), Name: id, OwnerAccount: owner, Status: tenant.StatusActive}
	if err := roster.Create(ctx, record); err != nil {
		if errors.Is(err, tenant.ErrTenantExists) {
			fmt.Printf("skipped: tenant %s (exists)\n", id)
			return nil
		}
		return fmt.Errorf("seed: registering the tenant %s: %w", id, err)
	}
	fmt.Printf("seeded: tenant %s\n", id)
	return nil
}

// tenantHoldsTombstone reports whether the roster carries the identifier as a
// deleted tenant, which the seed must not write back into.
func tenantHoldsTombstone(ctx context.Context, roster tenant.Catalog, id string) (bool, error) {
	got, found, err := roster.Get(ctx, tenant.ID(id))
	if err != nil {
		return false, fmt.Errorf("seed: reading the tenant %s: %w", id, err)
	}
	return found && got.Status == tenant.StatusDeleted, nil
}

// seedMembers writes the memberships the configuration names through the
// membership seam, which answers ErrAccountNotFound for a missing account. A
// membership is written only for an account this run created; an existing
// account keeps whatever memberships the operator gave it.
func seedMembers(ctx context.Context, memberships authn.Memberships, members []seedMember,
	accountIDs map[string]authn.AccountID, created map[string]bool, deleted map[string]bool,
) error {
	for _, m := range members {
		_, name, err := seedLoginName(m.Account)
		if err != nil {
			return fmt.Errorf("seed: the account named by the member of the tenant %q: %w", m.Tenant, err)
		}
		if deleted[m.Tenant] {
			fmt.Printf("skipped: member %s in %s (deleted tenant)\n", name, m.Tenant)
			continue
		}
		id, ok := accountIDs[name]
		if !ok {
			// checkMembers refused this before anything was written.
			return fmt.Errorf("seed: the member of the tenant %q names the account %q, which seed.%s "+
				"does not declare", m.Tenant, m.Account, accountsItem)
		}
		_, found, err := memberships.Membership(ctx, m.Tenant, id)
		if err != nil {
			return fmt.Errorf("seed: reading the membership of %s in %s: %w", name, m.Tenant, err)
		}
		if found {
			fmt.Printf("skipped: member %s in %s (exists)\n", name, m.Tenant)
			continue
		}
		if !created[name] {
			// The account predates this run, so its missing membership is the
			// operator's removal and not a gap for the seed to fill.
			fmt.Printf("skipped: member %s in %s (account already present)\n", name, m.Tenant)
			continue
		}
		record := authn.Membership{
			Account: id,
			Tenant:  m.Tenant,
			Active:  !m.Suspended,
			Source:  seedMemberSource,
		}
		if err := memberships.Put(ctx, record); err != nil {
			return fmt.Errorf("seed: writing the membership of %s in %s: %w", name, m.Tenant, err)
		}
		if record.Active {
			fmt.Printf("seeded: member %s in %s\n", name, m.Tenant)
		} else {
			fmt.Printf("seeded: member %s in %s (suspended)\n", name, m.Tenant)
		}
	}
	return nil
}

// seedSettings writes the runtime settings into the global layer unless that
// layer already holds an explicit row for the key. The settings module holds each
// key to its declaration and each value to its domain.
func seedSettings(ctx context.Context, store settings.Store, items []seedSetting) error {
	if len(items) == 0 {
		return nil
	}
	rows, err := store.List(ctx, settings.Global())
	if err != nil {
		return fmt.Errorf("seed: listing the global settings: %w", err)
	}
	held := make(map[string]bool, len(rows))
	for _, row := range rows {
		held[row.Key] = true
	}
	for _, item := range items {
		if held[item.Key] {
			fmt.Printf("skipped: setting %s (exists)\n", item.Key)
			continue
		}
		if err := store.Set(ctx, settings.Write{
			Layer: settings.Global(),
			Key:   item.Key,
			Value: item.Value,
			Actor: seedCommand,
		}); err != nil {
			return fmt.Errorf("seed: writing the setting %s: %w", item.Key, err)
		}
		held[item.Key] = true
		fmt.Printf("seeded: setting %s=%s\n", item.Key, item.Value)
	}
	return nil
}

// seedTenantSettings is seedSettings for one tenant's layer.
func seedTenantSettings(ctx context.Context, store settings.Store, tenantName string, items []seedSetting) error {
	if len(items) == 0 {
		return nil
	}
	layer := settings.ForTenant(tenantName)
	rows, err := store.List(ctx, layer)
	if err != nil {
		return fmt.Errorf("seed: listing the settings of tenant %q: %w", tenantName, err)
	}
	held := make(map[string]bool, len(rows))
	for _, row := range rows {
		held[row.Key] = true
	}
	for _, item := range items {
		if held[item.Key] {
			fmt.Printf("skipped: setting %s in tenant %s (exists)\n", item.Key, tenantName)
			continue
		}
		if err := store.Set(ctx, settings.Write{
			Layer: layer,
			Key:   item.Key,
			Value: item.Value,
			Actor: seedCommand,
		}); err != nil {
			return fmt.Errorf("seed: writing the setting %s in tenant %q: %w", item.Key, tenantName, err)
		}
		held[item.Key] = true
		fmt.Printf("seeded: setting %s=%s in tenant %s\n", item.Key, item.Value, tenantName)
	}
	return nil
}

// seedOneAccount writes one configured account unless it is already there, and
// answers its identifier and whether this run created it. An entry naming both
// an email address and a username is found by either, and a create that loses
// the race adopts the winner's account by re-reading both names.
func seedOneAccount(ctx context.Context, accounts authn.Accounts, admin authn.AccountAdmin,
	a seedAccount,
) (authn.AccountID, bool, error) {
	kind, name, err := seedLoginName(a.Identifier)
	if err != nil {
		return "", false, err
	}
	otherKind, other, err := seedUsernameLogin(kind, a.Username)
	if err != nil {
		return "", false, err
	}
	id, err := accounts.Lookup(ctx, kind, name)
	switch {
	case err == nil:
		fmt.Printf("skipped: account %s (exists)\n", name)
		return id, false, nil
	case !errors.Is(err, authn.ErrAccountNotFound):
		return "", false, fmt.Errorf("seed: looking up the account %s: %w", name, err)
	}
	if otherKind != "" {
		id, err = accounts.Lookup(ctx, otherKind, other)
		switch {
		case err == nil:
			fmt.Printf("skipped: account %s (exists as %s)\n", name, other)
			return id, false, nil
		case !errors.Is(err, authn.ErrAccountNotFound):
			return "", false, fmt.Errorf("seed: looking up the account %s: %w", other, err)
		}
	}
	account, err := admin.Create(ctx, seedCreateAccount(kind, name, a))
	if errors.Is(err, authn.ErrAccountExists) {
		existsErr := err
		id, lookupErr := accounts.Lookup(ctx, kind, name)
		switch {
		case lookupErr == nil:
			fmt.Printf("skipped: account %s (exists)\n", name)
			return id, false, nil
		case !errors.Is(lookupErr, authn.ErrAccountNotFound):
			return "", false, fmt.Errorf("seed: looking up the account %s: %w", name, lookupErr)
		}
		if otherKind != "" {
			id, lookupErr = accounts.Lookup(ctx, otherKind, other)
			switch {
			case lookupErr == nil:
				fmt.Printf("skipped: account %s (exists as %s)\n", name, other)
				return id, false, nil
			case !errors.Is(lookupErr, authn.ErrAccountNotFound):
				return "", false, fmt.Errorf("seed: looking up the account %s: %w", other, lookupErr)
			}
		}
		// Neither configured login name belongs to the account the create
		// collided with, so the conflict cannot be resolved by adopting.
		conflict := name
		if otherKind != "" {
			conflict = other
		}
		return "", false, fmt.Errorf("seed: creating the account %s: the login name %s is already "+
			"taken by another account: %w", name, conflict, existsErr)
	}
	if err != nil {
		return "", false, fmt.Errorf("seed: creating the account %s: %w", name, err)
	}
	fmt.Printf("seeded: account %s (%s)\n", name, account.ID)
	return account.ID, true, nil
}

// seedUsernameLogin is the second login name beside an email identifier,
// normalized the way the account store looks it up. It is empty when the entry is
// itself a username or names none.
func seedUsernameLogin(kind authn.IdentifierKind, username string) (authn.IdentifierKind, string, error) {
	if kind == authn.IdentifierUsername || strings.TrimSpace(username) == "" {
		return "", "", nil
	}
	normalized, err := authnaccount.NormalizeIdentifier(authn.IdentifierUsername, username)
	if err != nil {
		return "", "", fmt.Errorf("seed: the configured username: %w", err)
	}
	return authn.IdentifierUsername, normalized, nil
}

// seedCreateAccount is the library's creation request for one configured
// account. An email address written by an administrator is unproved, so it is
// neither a login way nor a default contact until the holder proves it.
func seedCreateAccount(kind authn.IdentifierKind, name string, a seedAccount) authn.CreateAccount {
	in := authn.CreateAccount{
		Kind:         authn.KindUser,
		DisplayName:  a.DisplayName,
		Password:     a.Password,
		PasswordHash: a.PasswordHash,
		Via:          "admin",
	}
	if kind == authn.IdentifierEmail {
		in.Email = name
		in.EmailVerifiedVia = authn.Verification{Via: authn.VerifiedViaAdmin}
		in.Username = a.Username
		return in
	}
	in.Username = name
	return in
}

// seedLoginName reads the kind of identifier a configured login name is. It
// asks the account module rather than reading the rule here, so the seed and the
// login surface cannot drift.
func seedLoginName(identifier string) (authn.IdentifierKind, string, error) {
	kind, normalized, err := authnaccount.LoginName(identifier)
	if err != nil {
		return "", "", fmt.Errorf("seed: the configured account identifier: %w", err)
	}
	return kind, normalized, nil
}

// seedAdministration writes one domain's role, the grants the caller names, and
// the builtin mark, each only when absent. The order is load-bearing: the mark
// freezes the role before the grants would be added, so it comes last. A
// personal-prefixed domain stays unfrozen.
func seedAdministration(ctx context.Context, manager authz.Manager, seeder authz.Seeder,
	domain string, role authz.Role, descriptionKey string, grants []authz.Grant,
) error {
	now := time.Now().UTC()
	if err := seedRole(ctx, manager, domain, role, descriptionKey, now); err != nil {
		return err
	}
	if err := seedGrants(ctx, seeder, domain, role, grants); err != nil {
		return err
	}
	if tenantpersonal.IsPersonalTenantID(domain) {
		fmt.Printf("skipped: builtin mark on %s/%s (personal domain)\n", domain, role)
		return nil
	}
	return markRoleBuiltin(ctx, manager, domain, role)
}

// seedRosterAdministration writes the tenant-admin role and builtin mark for
// every non-personal, non-deleted tenant in the roster, then converges its grants
// to the governed set. It reads the roster rather than the configuration because
// it repairs a tenant it did not register.
func seedRosterAdministration(ctx context.Context, roster tenant.Catalog, manager authz.Manager,
	seeder authz.Seeder, reconciler authz.Reconciler, auditor audit.Audit, catalog authz.Catalog,
) error {
	tenants, err := roster.List(ctx)
	if err != nil {
		return fmt.Errorf("seed: listing the roster's tenants: %w", err)
	}
	target := governedGrants(catalog)
	retired := retiredTenantAdminGrants(catalog)
	for _, t := range tenants {
		id := string(t.ID)
		if tenantpersonal.IsPersonalTenantID(id) {
			fmt.Printf("skipped: builtin mark on %s/%s (personal domain)\n", id, tenantAdminRole)
			continue
		}
		// A deleted tenant is terminal: the deletion cleaned its rows, and the seed
		// must not bring them back.
		if t.Status == tenant.StatusDeleted {
			fmt.Printf("skipped: builtin mark on %s/%s (deleted tenant)\n", id, tenantAdminRole)
			continue
		}
		if err := seedAdministration(ctx, manager, seeder, id, tenantAdminRole,
			tenantAdminDescriptionKey, target); err != nil {
			return err
		}
		if err := reconcileTenantAdminGrants(ctx, auditor, reconciler, id, target, retired); err != nil {
			return err
		}
	}
	return nil
}

// reconcileTenantAdminGrants writes the mandatory audit row and then asks the
// host's reconciler to bring the tenant-admin role to target; a row that does not
// land stops the convergence.
func reconcileTenantAdminGrants(ctx context.Context, auditor audit.Audit, reconciler authz.Reconciler,
	tenantID string, target, retired []authz.Grant,
) error {
	entry := audit.Entry{
		Action:  seedReconcileAction,
		Object:  tenantID,
		Outcome: seedReconcileOutcome,
	}
	if err := auditor.Record(ctx, entry); err != nil {
		return fmt.Errorf("seed: writing the audit row of the tenant-admin convergence of %s: %w", tenantID, err)
	}
	added, removed, err := reconciler.ReconcileGrants(ctx, tenantID, tenantAdminRole, target, retired)
	if err != nil {
		return fmt.Errorf("seed: converging the tenant-admin grants of %s: %w", tenantID, err)
	}
	if added > 0 || removed > 0 {
		fmt.Printf("reconciled: %d grant(s) added and %d removed on %s/%s\n",
			added, removed, tenantID, tenantAdminRole)
	}
	return nil
}

// seedRole writes one administration role into one domain unless it is already
// there. The row is created without the builtin mark; markRoleBuiltin sets it
// after the grants.
func seedRole(ctx context.Context, manager authz.Manager, domain string,
	role authz.Role, descriptionKey string, now time.Time,
) error {
	roles, err := manager.Roles(ctx, domain)
	if err != nil {
		return fmt.Errorf("seed: reading the roles of %s: %w", domain, err)
	}
	if slices.ContainsFunc(roles, func(r authz.RoleRecord) bool { return r.Role == role }) {
		fmt.Printf("skipped: role %s/%s (exists)\n", domain, role)
		return nil
	}
	record := authz.RoleRecord{
		Tenant:         domain,
		Role:           role,
		DescriptionKey: descriptionKey,
		CreatedAt:      now,
	}
	if err := manager.PutRole(ctx, record); err != nil {
		return fmt.Errorf("seed: writing the role %s/%s: %w", domain, role, err)
	}
	fmt.Printf("seeded: role %s/%s\n", domain, role)
	return nil
}

// markRoleBuiltin freezes an administration role once its grants are written,
// correcting a role an earlier seed wrote without the mark. It uses the manager's
// own marking method, because a role write carrying the mark is refused.
func markRoleBuiltin(ctx context.Context, manager authz.Manager, domain string, role authz.Role) error {
	roles, err := manager.Roles(ctx, domain)
	if err != nil {
		return fmt.Errorf("seed: reading the roles of %s: %w", domain, err)
	}
	i := slices.IndexFunc(roles, func(r authz.RoleRecord) bool { return r.Role == role })
	if i < 0 {
		return fmt.Errorf("seed: the role %s/%s is not there to mark builtin", domain, role)
	}
	if roles[i].Builtin {
		fmt.Printf("skipped: role %s/%s (builtin)\n", domain, role)
		return nil
	}
	if err := manager.MarkRoleBuiltin(ctx, domain, role); err != nil {
		return fmt.Errorf("seed: marking the role %s/%s builtin: %w", domain, role, err)
	}
	fmt.Printf("seeded: role %s/%s (builtin mark)\n", domain, role)
	return nil
}

// seedGrants adds the grants the role does not already hold, through the seeding
// surface: once a role is builtin the manager refuses its grant write.
func seedGrants(ctx context.Context, seeder authz.Seeder,
	domain string, role authz.Role, grants []authz.Grant,
) error {
	added, err := seeder.SeedGrants(ctx, domain, role, grants)
	if err != nil {
		return fmt.Errorf("seed: granting the declared permissions to %s/%s: %w",
			domain, role, err)
	}
	switch {
	case added > 0:
		fmt.Printf("seeded: %d grant(s) on %s/%s\n", added, domain, role)
	case len(grants) == 0:
		// Nothing was grantable at all, which is the state of a role holding
		// nothing: saying "all present" here would report the opposite of what
		// the operator is about to get.
		fmt.Printf("seeded: no grant on %s/%s: the set translates to no pair\n", domain, role)
	default:
		fmt.Printf("skipped: grants on %s/%s (all present)\n", domain, role)
	}
	return nil
}

// seedBinding binds the account to one administration role in one domain unless
// it already holds it there. The role has to exist in that domain.
func seedBinding(ctx context.Context, manager authz.Manager, domain string,
	role authz.Role, id authn.AccountID,
) error {
	bindings, err := manager.BindingsOf(ctx, domain, id.String())
	if err != nil {
		return fmt.Errorf("seed: reading the bindings of %s in %s: %w", id, domain, err)
	}
	if slices.ContainsFunc(bindings, func(b authz.Binding) bool { return b.Role == role }) {
		fmt.Printf("skipped: binding %s -> %s in %s (exists)\n", id, role, domain)
		return nil
	}
	record := authz.Binding{
		Tenant:    domain,
		Account:   id.String(),
		Role:      role,
		GrantedAt: time.Now().UTC(),
	}
	if err := manager.PutBinding(ctx, record); err != nil {
		return fmt.Errorf("seed: binding %s to %s in %s: %w", id, role, domain, err)
	}
	fmt.Printf("seeded: binding %s -> %s in %s\n", id, role, domain)
	return nil
}
