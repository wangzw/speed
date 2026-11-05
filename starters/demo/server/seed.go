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
)

// seedModuleName is the name the seed command's configuration module is
// registered under.
const seedModuleName = "seed"

// seedNamespace is where the seed command's input items are mounted.
const seedNamespace = "seed"

// accountsItem is the one item the seed declares.
const accountsItem = "accounts"

// tenantsItem is the item the seed reads the deployment's tenants from.
const tenantsItem = "tenants"

// settingsItem is the item the seed reads the deployment's initial runtime
// settings from.
const settingsItem = "settings"

// membersItem is the item the seed reads the deployment's initial memberships
// from.
const membersItem = "members"

// systemAdminsItem is the item the seed reads the accounts that administer the
// system tenant without the platform-administrator role from.
const systemAdminsItem = "system-admins"

// seedMemberSource is the source written with a membership the seed creates.
// The membership module records one of a closed set of sources — admin,
// jit:<provider>, invite or import — and a seeded row is an administrator's
// decision about the deployment's start state. It is the module's own constant
// for the administrator source, so the value cannot drift from the one the
// membership management surface writes.
const seedMemberSource = authn.ReasonAdmin

// platformAdminRole is the role the seed gives the accounts its configuration
// marks. The platform domain is the system tenant's domain, and this role
// exists there alone: it holds every permission the process declares, and an
// account may hold it only while it is an active member of that tenant. A
// binding in the platform domain does not reach a business tenant's domain, so
// acting inside a business tenant still needs that tenant's tenant-admin.
//
// It is builtin: the management surface may neither rewrite it nor delete it,
// and may not change the grants it holds. Deleting it would revoke the
// operator's every permission and lock them out of the console, and no command
// would put the role back while the administrator still exists — the seed would
// only do so on a database with no role row at all. The mark is set after the
// grants are written; the host's seeding path adds the grants a later run
// declares, so the role keeps up with the process's declared permissions while
// the management surface stays unable to touch it.
const platformAdminRole = authz.Role("platform-admin")

// platformAdminDescriptionKey is the description key written with the role. It
// travels in the API answer wherever a role is listed; nothing in this skeleton
// renders it, and a product that adds a role surface gives it a translation.
const platformAdminDescriptionKey = "authz.role.platformAdmin"

// tenantAdminRole is every tenant's own highest role. Each tenant domain has
// one, the platform domain included: the system tenant's copy is its own tenant
// administration beside the platform administrator role. The seed writes it
// with the governed permissions and marks it builtin, because it is the role
// the creator of a tenant holds and a tenant without it has no administrator.
// Its grant set is converged on every run, so a role seeded before the governed
// split loses the platform-only grants it used to hold.
const tenantAdminRole = authz.TenantAdminRole

// tenantAdminDescriptionKey is the description key written with every tenant's
// tenant-admin role. It travels in the API answer wherever a role is listed.
const tenantAdminDescriptionKey = "authz.role.tenantAdmin"

// The audit vocabulary of a tenant-admin grant convergence. The action names
// the seed's own step rather than a permission, and the outcome is a closed
// success value: a convergence a caller cannot write the row for does not run.
const (
	seedReconcileAction  = "seed.authz.tenant-admin.reconcile"
	seedReconcileOutcome = "success"
)

// seedInput carries the seed command's inputs.
type seedInput struct {
	// Accounts are the accounts a copy starts with.
	Accounts []seedAccount `config:"accounts"`
	// Tenants are the business tenants this deployment serves. Each is
	// registered in the roster, names the accounts that administer it, and may
	// list the runtime settings it starts with: the first administrator is the
	// tenant's first tenant-admin, and every one of them holds that role in the
	// tenant's domain. The system tenant must not be named here: the seed
	// registers it itself, and a list entry naming it is refused. Every
	// administrator's membership and tenant-admin binding are reconciled on every
	// run — a missing one is written — which is where this differs from
	// seed.members.
	Tenants []seedTenantEntry `config:"tenants"`
	// Settings are the runtime settings a deployment starts with, each
	// written into the global layer while that layer holds no explicit row
	// for its key. A key already written — by an earlier seed run or by the
	// operator through the settings surface — is left as it stands, so a
	// later run never overrides a runtime change.
	Settings []seedSetting `config:"settings"`
	// Impersonation asks the review build to seed the impersonation journey's
	// second factors: two judged methods on the platform administrator, one of
	// them aged past authn.mfa.sensitive-min-age. It is opt-in per deployment
	// because those factors change how that administrator signs in — every later
	// sign-in has to complete a second factor. A default build accepts the key
	// and writes nothing, because it seeds no factor at all.
	Impersonation bool `config:"impersonation"`
	// Members are the memberships an account starts with. Each names a tenant
	// under Tenants and an account under Accounts; the row is written through
	// the membership seam, so the account has to exist and the tenant is
	// validated against this list. A membership is written only while the run
	// is creating the account: an account that is already there keeps whatever
	// memberships the operator gave it, so a later run neither suspends,
	// resumes nor restores one. A run that names neither, names a tenant or an
	// account the configuration does not carry, or declares one pair twice is
	// refused before anything is written.
	Members []seedMember `config:"members"`
	// SystemAdmins are the accounts that administer the system tenant without
	// holding the platform-administrator role. Each gets a membership in the
	// system tenant and that tenant's tenant-admin binding, exactly as a
	// business tenant's administrators do; the platform-administrator role is
	// not written for them. Every name must be an account under Accounts. The
	// system tenant itself is registered by the command and is not listed under
	// Tenants, so this is the one way the configuration names who administers
	// it besides the platform administrators the command derives from the
	// account list.
	SystemAdmins []string `config:"system-admins"`
}

// seedMember is one membership the seed writes.
type seedMember struct {
	// Tenant is the tenant the account belongs to, as one of the names under
	// Tenants.
	Tenant string `config:"tenant"`
	// Account is the account's login name, as one of the identifiers under
	// Accounts.
	Account string `config:"account"`
	// Suspended writes the membership suspended instead of active. It is
	// negative on purpose: the zero value is an active member, so an entry
	// that leaves it out does not silently seed a suspended one.
	Suspended bool `config:"suspended"`
}

// seedTenantEntry is one business tenant the seed writes: its name, the
// accounts that administer it, and the settings that tenant starts with.
type seedTenantEntry struct {
	// Name is the tenant's identifier, which is also its alias: a request
	// carrying the tenant's name on its surface resolves to it.
	Name string `config:"name"`
	// Admins are the accounts that administer the tenant, each named by a login
	// name under Accounts. Every one gets a membership and a tenant-admin binding
	// in the tenant's domain. The first entry is written first, so it is the
	// tenant's first administrator.
	Admins []string `config:"admins"`
	// Settings are runtime settings written into this tenant's layer, each while
	// that layer holds no explicit row for its key. They go through the settings
	// store exactly as the global list does, so a misspelled key and a value
	// outside its declared domain both fail the command. An entry that names none
	// writes none, and the global layer is untouched either way.
	Settings []seedSetting `config:"settings"`
}

// seedAdministrator is one configured platform administrator: the account's
// identifier and its login name, which the membership check keys on.
type seedAdministrator struct {
	id   authn.AccountID
	name string
}

// seedSetting is one runtime setting the seed writes.
type seedSetting struct {
	// Key is the full setting path, as the module declaring it names it,
	// such as authn.mfa.sensitive-min-age.
	Key string `config:"key"`
	// Value is the setting in the declared type's own text form: "1h" for a
	// duration, "12" for a whole number. The settings module holds the value
	// to the declaration and its domain, so a misspelled key and an
	// out-of-domain value both fail this command instead of being written.
	Value string `config:"value"`
}

// seedAccount is one account the seed writes.
type seedAccount struct {
	// Identifier is the account's identifier: one holding an @ is an email
	// address and any other a username. An email address written here is an
	// administrator's assertion, not a login way, so it does not sign in.
	Identifier string `config:"identifier"`
	// Username is the account's login name when Identifier is an email address.
	// Every account is created with a username, so an entry whose only
	// identifier is an address must name one; when Identifier is itself a
	// username this is ignored.
	Username string `config:"username"`
	// Password is the plaintext the credential is derived from. It and
	// PasswordHash are alternatives, and one of them has to be given.
	Password string `config:"password"`
	// PasswordHash is the credential in the form the `pkg/authn/static` `Hash`
	// helper writes: a PHC entry, read as it stands and written without a
	// plaintext passing through this configuration. It is what a deployment that
	// does not want its operators' passwords in a file gives instead of Password.
	PasswordHash string `config:"password-hash"`
	// DisplayName is the account's own name.
	DisplayName string `config:"display-name"`
	// PlatformAdmin grants the account the platform-administrator role, with
	// every permission the process declares. Without it the account signs in and
	// reaches nothing that asks for a permission.
	PlatformAdmin bool `config:"platform-admin"`
	// ProvedEmail asks the review build to prove the account's email address
	// after it is created, through the account module's own AddIdentifier with
	// the same email-code way a completed verification flow writes. The address
	// itself is stated by Identifier, which must then be an email address. A
	// baseline build accepts the field and writes the address as the
	// administrator's assertion it already writes every seeded address as: the
	// proving path is a review fixture, not a deployment capability.
	ProvedEmail bool `config:"proved-email"`
}

// check refuses an account the seed cannot write as the credential it is meant
// to be.
//
// It is this command's own check and not the configuration's, because a list
// carries no item metadata: the manifest holds the list as one key, so a field
// inside an entry — a misspelled password key above all — is not a key any
// source can be held to, and one that names nothing arrives here as a zero
// value. Without this the account store is asked for an account with no
// credential, which it writes, and the command reports it seeded: a login that
// can never succeed, announced as the one thing the command exists to produce.
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

// validateSeedInput runs every check on the configuration, in the order the
// command has to know them: an account, then the platform administrator, then a
// tenant list, then a member list. Both the module's Init callback and the seed
// action call it, so a run that refuses the configuration has written nothing
// in either place.
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
// admits. The platform-administrator role is the only way to reach the
// management surface, and no migration or later command writes a binding for an
// account the configuration does not mark, so a copy seeded without one has a
// console nobody can enter and no second run that repairs it.
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

// checkTenants refuses a tenant list the seed cannot write, before anything is
// written.
//
// A tenant without an administrator would be a tenant nobody can enter, because
// its tenant-admin binding is the only way to hold that role; the model has no
// such state, so the list must name at least one. Every administrator must be
// one of the declared accounts, and one tenant cannot name the same account
// twice.
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
		if !tenant.IsAlias(name) {
			return fmt.Errorf("seed: the tenant %q is not a lower-case slug of 3 to 63 letters, "+
				"digits and dashes; the name is the tenant's alias", name)
		}
		if name == string(authz.PlatformDomain) {
			return fmt.Errorf("seed: the tenant %q is the system tenant, which the command registers "+
				"itself and must not be listed under seed.%s", name, tenantsItem)
		}
		if isPersonalTenantID(name) {
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

// checkMembers refuses a member list the seed cannot write, before anything is
// written.
//
// Like the account check, it is this command's own and not the
// configuration's: a list carries no item metadata, so a member entry naming no
// tenant or no account arrives here as a zero value. A tenant or an account the
// configuration does not declare would otherwise surface as a row written
// against a name no roster or account table holds — the membership seam checks
// that the account exists, but a tenant is only a string to it — and one pair
// declared twice would make the command report one write for a list that names
// two.
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

// checkSystemAdmins refuses a system-administrator list the seed cannot write.
//
// The system tenant is not listed under seed.tenants, so this list is held to
// the account list instead: every name must be an account the run writes, and
// one account cannot be named twice. Nothing else is checked here — the
// membership and the binding are written through the same seams a business
// tenant's administrators use, and the system tenant is always registered.
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

// seedSchema declares the seed command's input items.
//
// The account list is one item and not a tree of them, because a list carries
// no per-field metadata: the whole list is marked sensitive instead, since
// every entry under it holds a password.
//
// Every list takes the environment as well as the configuration file, and its
// value there is JSON that replaces the whole list. The seed is a value that
// differs between runs of one binary — a deployment seeds its own operators and
// tenants — so a run pointed at no configuration file can give it, and the
// precedence stays the command line above the environment above the
// configuration file above the defaults document. The document itself names
// the local seed a bare run starts from; see defaults.go.
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

// init registers the seed command's configuration module. It constructs nothing:
// it carries the input items and its only purpose is to put them on the seed
// command's configuration.
func init() { core.ProcessRegistry.Register(seedModule()) }

func seedModule() core.Module {
	return core.Module{
		Name: seedModuleName,
		// The system tenant is registered in Init, not in the seed action the
		// command runs once every module has started: the policy module's Start
		// requires the roster to carry the platform domain, and a fresh database
		// has no row until this command writes one. Init runs after every module
		// is constructed and before any Start, so the row is there when the policy
		// module checks for it. Requiring the roster orders this callback after the
		// roster's own Init, which is where the snapshot is read.
		Requires:  []core.Requirement{{Token: (*tenant.Catalog)(nil)}},
		Resources: []any{seedSchema()},
		Init: func(ctx context.Context, reg *core.Registry, _ any) error {
			// The configuration is judged before the row is written: a run that
			// would refuse an account, a tenant or a member list must have written
			// nothing at all, and the system tenant is the first write this command
			// makes.
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
			return seedTenant(ctx, roster, string(authz.PlatformDomain))
		},
	}
}

// seedDatabase writes the global runtime settings the configuration names, the
// system tenant, the accounts, the business tenants with their administrators
// and each tenant's own runtime settings, the memberships the configuration
// declares, and the two administration roles with their grants and bindings.
//
// It is idempotent: an account, a tenant, a membership, a role, a grant or a
// binding already there is left as it stands and reported as skipped, so the
// command is safe to run at every deployment. An account entry naming both an
// email address and a username is found by either: an address the holder removed
// from the account page leaves the account under its username, and the run
// adopts that account instead of writing the address back. On an existing
// database it
// corrects two things per administration role: it marks the role builtin when an
// earlier seed wrote the row without the mark, and it adds the declared grants
// the role does not hold. A membership under seed.members is written only while
// the run is creating its account: an account that already exists keeps whatever
// membership the operator gave it, and a missing row is the operator's removal
// rather than a gap for the seed to fill. A tenant administrator named under
// seed.tenants (and a platform administrator's system membership) is reconciled
// on every run instead: a missing membership is written and a missing
// tenant-admin binding is added, because the configuration names that account an
// administrator and the binding has no effect without the membership. Nothing
// else is touched — an account, a tenant, an administrator or a membership the
// configuration no longer names is not disabled, deleted or demoted.
//
// The system tenant is always registered: the platform policy domain is that
// tenant's domain, and the policy module refuses to start without it. Its
// tenant-admin role is the system tenant's own administration; the
// platform-administrator role exists only in that domain and is bound only to
// accounts that are active members of it.
//
// A business tenant gets its tenant-admin role, the declared grants and the
// builtin mark in its own domain — every non-personal tenant the roster holds,
// not only the ones the configuration lists, so a tenant another replica opened
// or an earlier deployment left unfrozen is repaired on this run. A personal-
// prefixed domain is the host's own and is left alone. Each account the
// configuration names under admins gets a membership and a tenant-admin binding
// there. A business tenant never gets the platform-administrator role: that
// role's domain is the platform domain alone.
//
// This command is where the roles, their grants and their bindings come from: no
// migration seeds a role or a binding, because who a deployment's administrators
// are is the host's answer and not the library's. The permissions each role
// holds are the ones the process declares, read from the catalog, so a first run
// covers every permission the process declares without an edit here. Each role is
// then marked builtin, which keeps the management surface from rewriting it,
// deleting it or changing its grants. The grants are written through the seeding
// surface, which adds the ones the role lacks and never removes one: a permission
// a module declares later is added on the next run, so the operator is never
// short of it and the command never fails over the mark.
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
	// Every list is judged before anything is written: a list entry is a value
	// the manifest cannot hold to, so a credential key spelled wrong, a tenant
	// with no administrator, or a member naming a tenant or an account the
	// configuration does not carry is caught here rather than by a store, and a
	// run that would refuse one must not have written the settings below first.
	if err := validateSeedInput(in); err != nil {
		return err
	}

	// The settings first: a value the configuration names may govern how the
	// accounts below are written — the account store's password policy among
	// them — and a policy written after the account it should have judged has
	// judged nothing. The settings belong to no account or tenant: each is
	// written once, into the global layer, and a key that layer already holds is
	// left as it stands.
	if err := seedSettings(ctx, store, in.Settings); err != nil {
		return err
	}

	// The system tenant first, always: the platform policy domain is that
	// tenant's domain, and the policy module refuses to start without it.
	if err := seedTenant(ctx, roster, string(authz.PlatformDomain)); err != nil {
		return err
	}

	// Then the accounts, the identifiers of the ones the configuration marks as
	// administrators, and the identifier of every account a member or admin entry
	// can name. created records the ones this run wrote, because a membership
	// under seed.members is provisioned with its account and never for one that
	// was already there.
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

	// Then the business tenants, which the console's tenant-addressed pages read
	// from the roster: a page selects a tenant from it, and a tenant that is not
	// there is one no page can name. Each tenant's settings follow its row into
	// that tenant's layer.
	for _, t := range in.Tenants {
		if err := seedTenant(ctx, roster, t.Name); err != nil {
			return err
		}
		if err := seedTenantSettings(ctx, store, t.Name, t.Settings); err != nil {
			return err
		}
	}

	// Then the memberships under seed.members, which name both an account above
	// and a tenant above, so both are written.
	if err := seedMembers(ctx, memberships, in.Members, accountIDs, created); err != nil {
		return err
	}

	// The review build seeds the impersonation fixture's second factors here,
	// through the product's own register and encryption seams. A default build
	// compiles the no-op beside this call and writes no factor; see
	// review_seed.go and review_off.go.
	if err := reviewSeedExtras(ctx, reg, in); err != nil {
		return err
	}

	// Then the roles and their grants, one role row per domain it exists in. The
	// platform domain carries the platform-administrator role, and the
	// tenant-admin role of every non-personal tenant in the roster follows — not
	// only the ones this configuration lists, so a tenant another replica opened
	// or an earlier deployment left with an unfrozen role is repaired on this run.
	// Membership and bindings are reconciled later, only for the administrators
	// seed.tenants declares. The roles are written before the bindings, which name
	// a role that has to exist in that domain.
	if err := seedAdministration(ctx, manager, seeder,
		string(authz.PlatformDomain), platformAdminRole, platformAdminDescriptionKey,
		declaredGrants(catalog)); err != nil {
		return err
	}
	if err := seedRosterAdministration(ctx, roster, manager, seeder, reconciler, auditor, catalog); err != nil {
		return err
	}

	// The platform administrators are members of the system tenant and hold both
	// administration roles there: platform-admin is what reaches the platform
	// management surface, and tenant-admin is the system tenant's own
	// administration. The platform-administrator binding is refused by the policy
	// module for an account that is not an active member, so the membership is
	// written first.
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

	// Then each business tenant's administrators, in the order the configuration
	// names them: the first entry is written first, so it is the tenant's first
	// administrator. A missing membership or tenant-admin binding is added on
	// every run, so an account the configuration names keeps administering its
	// tenant after an operator removes one of the two.
	for _, t := range in.Tenants {
		for _, adminName := range t.Admins {
			_, name, err := seedLoginName(adminName)
			if err != nil {
				return fmt.Errorf("seed: the administrator %q of the tenant %q: %w", adminName, t.Name, err)
			}
			id, ok := accountIDs[name]
			if !ok {
				// checkTenants refused this before anything was written; the map
				// only lacks the entry if the two disagree.
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

	// Then the accounts that administer the system tenant without the
	// platform-administrator role. The system tenant's tenant-admin role is
	// already written above, and a platform administrator's binding to it is
	// written in the loop before this one; this list is what lets a deployment
	// name a system administrator who is not platform staff. The membership is
	// reconciled the same way a business tenant administrator's is.
	if err := seedSystemTenantAdmins(ctx, memberships, manager, in.SystemAdmins, accountIDs); err != nil {
		return err
	}

	// No second factor is seeded in a default build. The public authn surface
	// hands back a registration step it does not let a host read, so a seed
	// cannot finish an enrollment; write-path testing needs a factor added in
	// account settings first, and the console's step-up gate refuses every write
	// until then. The review build is the exception, and says so.
	if !reviewSeedsFactors(in) {
		fmt.Print("note: no second factor is seeded; add one in account settings " +
			"before testing a write path (see AGENTS.md, \"Exercising a write path\")\n")
	}
	return nil
}

// seedAdminMembership ensures an administrator is an active member of the
// tenant they administer. A missing row is written on every run: the
// configuration names the account an administrator of that tenant, and a
// membership is what lets the binding take effect, so its absence is a gap the
// seed fills rather than an operator's decision it preserves. This is the one
// place the rule differs from seed.members, where a missing row is the
// operator's removal and is left as it stands.
//
// An existing row is left as it stands: a suspension or a source the operator
// set is runtime state, not a gap for the seed to fill. It answers whether the
// account is an active member, because the binding that follows has no effect
// without one; a suspended administrator's binding is reported and not written.
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

// seedSystemTenantAdmins gives each account the configuration names under
// seed.system-admins a membership in the system tenant and that tenant's
// tenant-admin binding. It is the system tenant's counterpart of the business
// tenant administrator loop: the membership is reconciled on every run, and an
// account that is not an active member is reported and skipped. The
// platform-administrator role is never written here, so an account named only
// by this list administers the system tenant without platform staff power.
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
			// checkSystemAdmins refused this before anything was written; the
			// map only lacks the entry if the two disagree.
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

// seedTenant registers one tenant the configuration names unless the roster
// already holds it. The alias is the identifier, so a request carrying the
// tenant's name on its surface resolves to it. The name is held to the slug
// form by checkTenants, so both the identifier and the alias it stands in for
// are names the roster accepts.
//
// A repeated run reads the row and skips. A create that loses the identifier to
// another run between the read and the write is the same answer: the tenant is
// there, which is what this command is for. An alias conflict is not: the
// identifier is free and another tenant holds the alias, so the tenant is not
// registered, and skipping it would let the startup fail later without naming
// the cause.
func seedTenant(ctx context.Context, roster tenant.Catalog, id string) error {
	_, found, err := roster.Get(ctx, tenant.ID(id))
	if err != nil {
		return fmt.Errorf("seed: reading the tenant %s: %w", id, err)
	}
	if found {
		fmt.Printf("skipped: tenant %s (exists)\n", id)
		return nil
	}
	record := tenant.Tenant{ID: tenant.ID(id), Alias: id, Status: tenant.StatusActive}
	if err := roster.Create(ctx, record); err != nil {
		if errors.Is(err, tenant.ErrTenantExists) {
			// Another run took the identifier between the read and the write.
			// The tenant is there, which is what this command is for, so the
			// race is the answer and not a failure.
			fmt.Printf("skipped: tenant %s (exists)\n", id)
			return nil
		}
		return fmt.Errorf("seed: registering the tenant %s: %w", id, err)
	}
	fmt.Printf("seeded: tenant %s\n", id)
	return nil
}

// seedMembers writes the memberships the configuration names, through the
// membership seam rather than the table.
//
// The seam is what holds the row to the account: Put answers
// authn.ErrAccountNotFound for an account that is missing or deleted, so a
// tombstoned account does not silently keep a membership. The tenant is only a
// string to the seam, and checkMembers has already held each one to the tenants
// the configuration declares.
//
// A membership is provisioned with its account and never written for one that
// was already there. The account this run creates has no memberships yet, so
// each declared pair is written; an account the run found already in place
// keeps the memberships the operator gave it. That is what leaves the operator's
// decisions through the membership management surface alone: a membership
// added, suspended or resumed is reported as skipped, and one the operator
// removed stays removed rather than being put back, because a row that is
// absent and a row the operator deleted are the same thing to this seam. An
// existing row is never rewritten, so its source and SSO exemption stand too.
// Deleting a member from the configuration does not remove the row, just as it
// does not disable an account or delete a tenant.
func seedMembers(ctx context.Context, memberships authn.Memberships, members []seedMember,
	accountIDs map[string]authn.AccountID, created map[string]bool,
) error {
	for _, m := range members {
		_, name, err := seedLoginName(m.Account)
		if err != nil {
			return fmt.Errorf("seed: the account named by the member of the tenant %q: %w", m.Tenant, err)
		}
		id, ok := accountIDs[name]
		if !ok {
			// checkMembers refused this before anything was written; the map
			// only lacks the entry if the two disagree, and the offender is
			// named either way.
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

// seedSettings writes the runtime settings the configuration names into the
// global layer, unless that layer already holds an explicit row for the key.
//
// The write goes through the settings module rather than the table, so a
// misspelled key is refused as unknown and a value outside its declared domain
// as invalid, in this command and with the module's own vocabulary. The
// declared type decides the text: a duration is written as "1h", a whole
// number as "12", and the module stores the declared type's own form.
//
// A key the global layer already holds is skipped, not overwritten: the row may
// be an operator's runtime change, and a second seed run must leave it as it
// stands. That is what makes the command idempotent for settings the way it is
// for accounts and tenants.
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

// seedTenantSettings writes the settings one tenant's configuration names into
// that tenant's layer, unless the layer already holds an explicit row for the
// key.
//
// It is the global seed's write path with the tenant as the layer: the key is
// held to its declaration and the value to the declared type and domain by the
// settings module, so a misspelled key and an out-of-domain value both fail
// this command with the module's own vocabulary. A key the tenant's layer
// already holds is skipped, not overwritten, for the reason seedSettings gives:
// the row may be an operator's runtime change, and a later seed run must leave
// it as it stands.
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
// answers with the account's identifier. The second result says whether this run
// created the account: a membership is provisioned with the account and never for
// one that was already there.
//
// An entry that names both an email address and a username names one account by
// either. The address is the first login name the run looks up, and the username
// beside it is the second: a holder may remove the address from the account page,
// which releases it, and the username is what the account keeps. That is a
// decision a later run preserves — it adopts the account the username names and
// does not write the address back — rather than a collision that has to fail the
// deployment. When neither login name is there the account is created, and the
// same two names are re-read if the create loses the race, so a concurrent run's
// account is adopted instead of reported as a conflict.
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
		// Another run took a login name between the lookups and the write. The
		// account is there, which is what this command is for, so the race is
		// the answer and not a failure. Both login names the entry configures are
		// re-read: the configured one first, then the username beside an address.
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
		// collided with, so the conflict is one this run cannot resolve by
		// adopting. The username is the name named: the address was read as free
		// just above, and a create that also writes a username loses to the
		// username's unique constraint.
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

// seedUsernameLogin is the second login name an account entry carries besides
// the one it is found by: the username written with an email-identifier entry.
// The kind is empty when the entry is itself a username, where the field is
// ignored, and when no username is configured. The answer is normalized the way
// the account store looks it up, so a lookup cannot miss a row the create wrote
// under the same name.
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
// account. A username is a login name and needs no proof. An email address
// written here is an administrator's assertion, not a proof of ownership, so the
// account reads pending until its holder proves the address; it is neither a
// login way nor a default contact until the holder chooses. Every account is
// created with a username, so an email entry's own username is written beside
// the address; the baseline ships no mail channel and therefore seeds usernames,
// while a stack that can deliver a code may seed the address too.
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

// seedLoginName reads the kind of identifier a configured login name is.
//
// It asks the account module rather than reading the rule here: the rule is the
// password surface's — one identifier holding an @ is an email address and any
// other a username — and a second copy would be one that drifts, leaving this
// command writing accounts the login surface cannot find.
func seedLoginName(identifier string) (authn.IdentifierKind, string, error) {
	kind, normalized, err := authnaccount.LoginName(identifier)
	if err != nil {
		return "", "", fmt.Errorf("seed: the configured account identifier: %w", err)
	}
	return kind, normalized, nil
}

// seedAdministration writes what administration is made of in one domain for
// one role: the role, the grants the caller names for it, and the builtin mark
// that keeps the management surface off both. Each is written only when it is
// not already there. The caller runs it once per (domain, role) pair — the
// platform domain carries both administration roles, and each business tenant
// carries its tenant-admin role — because a role and its grants are per-domain
// rows and one domain's do not answer for another's.
//
// The grants are the caller's choice because the two administration roles hold
// different sets: platform-admin takes every declared permission, while
// tenant-admin takes the governed set. That routing is the caller's and is not
// inferred here.
//
// The order is load-bearing: the role is created not builtin, the grants are
// written, and only then is the mark set. A role created with the mark would be
// frozen before the grants that give it meaning are in place; the grants are
// written through the seeding surface, which reaches the role whether or not it
// carries the mark, so a re-run over a builtin role adds what the process
// declares since the last one.
//
// A personal-prefixed domain is the host's personal space, and its tenant-admin
// is not builtin: the host revokes grants outside the allowlist through the
// manager, and the mark refuses that write. That role is left unfrozen, as
// tenant.Provisioner leaves it.
//
// It names no account, because neither does: the role and its grants belong to
// the domain, so one write serves every administrator the configuration names.
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
	if isPersonalTenantID(domain) {
		fmt.Printf("skipped: builtin mark on %s/%s (personal domain)\n", domain, role)
		return nil
	}
	return markRoleBuiltin(ctx, manager, domain, role)
}

// seedRosterAdministration writes the tenant-admin role, the governed grants,
// and the builtin mark for every non-personal tenant the roster holds, then
// converges each role's grant set to the governed set. It reads the roster
// rather than the configuration because the seed repairs a tenant it did not
// register: one another replica opened between runs, or one an earlier
// deployment left with a role neither granted nor frozen. A tenant dropped from
// seed.tenants stays registered, so its administration is still repaired.
//
// The convergence is what takes a role seeded before the governed split from
// the full declared set down to the governed one. It removes only the grants
// this version lists as retired — the declared permissions that are no longer
// governed — and does nothing on a fresh role the seeding step already filled
// with the governed set.
//
// A personal-prefixed domain is the host's personal space and is skipped
// entirely: its tenant-admin holds the host's allowlist rather than the governed
// set, and the builtin mark would refuse the host's own grant revocation. The
// role and grants are per domain, so this names no account; membership and
// bindings stay limited to the administrators seed.tenants declares, because
// the configuration says nothing about who administers a tenant it does not
// list.
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
		if isPersonalTenantID(id) {
			fmt.Printf("skipped: builtin mark on %s/%s (personal domain)\n", id, tenantAdminRole)
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

// reconcileTenantAdminGrants writes the mandatory audit row of a convergence and
// then asks the host's reconciler to bring the tenant-admin role to target,
// removing the grants listed in retired. A row that does not land stops the
// convergence: the write that changes a builtin role's grant set is never
// carried out without its record.
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
// there. The row is created without the builtin mark: the grants are written
// next, and markRoleBuiltin sets the mark once they are in place.
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

// markRoleBuiltin freezes an administration role once its grants are written.
// The management surface may then neither rewrite it nor delete it, and the
// manager refuses both here as well; it also refuses to change the role's
// grants, which leaves the seeding surface the one path that adds one. Deleting
// the role would revoke the operator's every permission with nothing to put it
// back: the seed rebuilds a role row only when it is absent, and the account
// that lost the binding path could no longer reach the surface that would
// restore it.
//
// A role an earlier seed wrote without the mark is corrected here, which is the
// upgrade path for a database seeded before the mark existed. Re-running the
// seed would otherwise skip the row and leave the console offering a delete
// that locks the operator out. The mark is set through the manager's own
// marking method and not by rewriting the record: the mark is the one thing
// this step changes, and a role write carrying it is refused.
//
// The mark does not freeze the grant set against the seed: a permission a
// module declares after this run is added to the role by the seeding surface on
// the next run, and the management surface still cannot touch it.
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

// seedGrants gives one administration role the grants the caller names, adding
// the ones the role does not already hold.
//
// The grants come from the caller because the two administration roles hold
// different sets: platform-admin takes every declared permission and tenant-admin
// the governed set. This step only writes; the convergence that removes grants
// the governed split retired runs separately, after the mark.
//
// The write goes through the seeding surface, not the manager's grant write:
// once the role carries the builtin mark the manager refuses the latter, and a
// re-run over a role whose declared set has grown has to add the new grants.
// The seeding write adds only and never removes, and reports how many it added,
// which is what makes a second run write nothing.
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
// it already holds it there. The role has to be there already: a binding names a
// role that exists in its domain, and one that names nothing is refused.
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
