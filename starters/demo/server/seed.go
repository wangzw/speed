package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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

// seedMemberSource is the source written with a membership the seed creates.
// The membership module records one of a closed set of sources — admin,
// jit:<provider>, invite or import — and a seeded row is an administrator's
// decision about the deployment's start state. It is the module's own constant
// for the administrator source, so the value cannot drift from the one the
// membership management surface writes.
const seedMemberSource = authn.ReasonAdmin

// platformAdminRole is the role the seed gives the accounts its configuration
// marks. It holds every permission the process declares, and it is written into
// the platform domain and into each tenant the deployment serves. A role is a
// per-domain row, so the platform domain's copy does not reach a tenant's, and
// the seed writes one in each domain the administrator acts in.
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

// seedInput carries the seed command's inputs.
type seedInput struct {
	// Accounts are the accounts a copy starts with.
	Accounts []seedAccount `config:"accounts"`
	// Tenants are the tenants this deployment serves. Each is registered in the
	// roster, and the accounts marked as platform administrators hold the
	// platform-administrator role within each one, so the console's
	// tenant-addressed administration pages answer for a tenant the seed wrote.
	// The reserved platform domain is not among them: it names the platform
	// operations' policy domain, not a tenant, and a name equal to it is refused
	// rather than written.
	Tenants []string `config:"tenants"`
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
	// Identifier is the account's login name: one holding an @ is an email
	// address and any other a username, as a password login reads it. It is the
	// name the operator signs in with.
	Identifier string `config:"identifier"`
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
		tenants[t] = true
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

// seedSchema declares the seed command's input items.
//
// The account list is one item and not a tree of them, because a list carries
// no per-field metadata: the whole list is marked sensitive instead, since
// every entry under it holds a password.
func seedSchema() config.Schema {
	return config.Schema{
		Namespace: seedNamespace,
		Mounts:    []config.Mount{{Value: &seedInput{}}},
		Items: map[string]config.Item{
			accountsItem: {
				Origins:   config.OriginPrimary,
				Required:  true,
				Sensitive: true,
				Description: "the accounts a copy starts with, each with its login name, its " +
					"credential as a plaintext password or as a hash, and whether it holds " +
					"the platform-administrator role",
			},
			tenantsItem: {
				Origins: config.OriginPrimary,
				Description: "the tenants this deployment serves, each registered in the roster " +
					"and each holding the platform-administrator role for the accounts marked " +
					"with it; the reserved platform domain is refused",
			},
			settingsItem: {
				Origins: config.OriginPrimary,
				Description: "runtime settings the deployment starts with, each written into " +
					"the global layer while that layer holds no explicit row for the key; the " +
					"settings module holds every value to its declaration and domain",
			},
			membersItem: {
				Origins: config.OriginPrimary,
				Description: "the memberships the deployment starts with, each naming one of the " +
					"tenants under seed.tenants and one of the accounts under seed.accounts; a " +
					"member is active unless it is marked suspended",
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
		Name:      seedModuleName,
		Resources: []any{seedSchema()},
	}
}

// seedDatabase writes the accounts the configuration names, the tenants this
// deployment serves, the memberships the configuration declares, the
// platform-administrator role the accounts marked with it hold, and the
// runtime settings the configuration starts the deployment with.
//
// It is idempotent: an account, a tenant, a membership, a role, a grant or a
// binding already there is left as it stands and reported as skipped, so the
// command is safe to run at every deployment. On an existing database it
// corrects two things: it marks the administrator role builtin when an earlier
// seed wrote the row without the mark, and it adds the declared grants the role
// does not hold. A membership is written only while the run is creating its
// account. An account that already exists keeps the memberships the operator
// gave it through the membership management surface — an addition, a suspension,
// a resume or a removal all survive a later run — because after the account
// exists its memberships are runtime state and not a gap for the seed to fill.
// Nothing else is touched — an account, a tenant or a membership the
// configuration no longer names is not disabled or deleted.
//
// The role is written into two kinds of domain. The platform domain carries it
// so the platform's own management surface answers; each tenant the
// configuration names carries it too, so the console's tenant-addressed pages —
// members, invitations and SSO providers — are complete out of the box. A
// platform-domain binding does not reach a tenant domain, so a deployment whose
// administrator held the role in the platform domain alone would be refused
// those pages. The role is a per-domain row: writing the binding in a tenant
// takes the role and its grants in that tenant, not only the binding.
//
// This command is where the role, its grants and its bindings come from: no
// migration seeds a role or a binding, because who a deployment's
// administrators are is the host's answer and not the library's. The
// permissions the role holds are the ones the process declares, read from the
// catalog, so a first run covers every permission the process declares without
// an edit here. The role is then marked builtin, which keeps the management
// surface from rewriting it, deleting it or changing its grants. The grants are
// written through the seeding surface, which adds the ones the role lacks and
// never removes one: a permission a module declares later is added on the next
// run, so the operator is never short of it and the command never fails over
// the mark.
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
	catalog, err := core.Resolve[authz.Catalog](reg)
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
	// The account and member lists are judged before anything is written: a
	// list entry is a value the manifest cannot hold to, so a credential key
	// spelled wrong, or a member naming a tenant or an account the
	// configuration does not carry, is caught here rather than by the store,
	// and a run that would refuse one must not have written the settings below
	// first.
	for _, a := range in.Accounts {
		if err := a.check(); err != nil {
			return err
		}
	}
	if err := checkMembers(in); err != nil {
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

	// Then the accounts, the identifiers of the ones the configuration marks as
	// administrators, and the identifier of every account a member entry can
	// name. created records the ones this run wrote, because a membership is
	// provisioned with its account and never for one that was already there.
	var administrators []authn.AccountID
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
			administrators = append(administrators, id)
		}
	}

	// Then the tenants, which the console's tenant-addressed pages read from the
	// roster: a page selects a tenant from it, and a tenant that is not there is
	// one no page can name.
	for _, t := range in.Tenants {
		if err := seedTenant(ctx, roster, t); err != nil {
			return err
		}
	}

	// Then the memberships, which name both an account above and a tenant
	// above, so both are written.
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

	if len(administrators) == 0 {
		return nil
	}

	// Then the role and its grants, one set per domain the administrator's role
	// is held in: the platform domain, and each tenant the configuration names.
	// They are written before the bindings, which name a role that has to exist
	// in that domain.
	domains := append([]string{string(authz.PlatformDomain)}, in.Tenants...)
	for _, domain := range domains {
		if err := seedAdministration(ctx, manager, seeder, catalog, domain); err != nil {
			return err
		}
	}
	for _, id := range administrators {
		for _, domain := range domains {
			if err := seedBinding(ctx, manager, domain, id); err != nil {
				return err
			}
		}
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

// seedTenant registers one tenant the configuration names unless the roster
// already holds it. The alias is the identifier, so a request carrying the
// tenant's name on its surface resolves to it.
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
		if errors.Is(err, tenant.ErrTenantExists) || errors.Is(err, tenant.ErrAliasConflict) {
			// Another run took the identifier or the alias between the read and
			// the write. The tenant is there, which is what this command is for,
			// so the race is the answer and not a failure.
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

// seedOneAccount writes one configured account unless its login name is already
// taken, and answers with the account's identifier. The second result says
// whether this run created the account: a membership is provisioned with the
// account and never for one that was already there.
func seedOneAccount(ctx context.Context, accounts authn.Accounts, admin authn.AccountAdmin,
	a seedAccount,
) (authn.AccountID, bool, error) {
	kind, name, err := seedLoginName(a.Identifier)
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
	account, err := admin.Create(ctx, seedCreateAccount(kind, name, a))
	if errors.Is(err, authn.ErrAccountExists) {
		// Another run took the login name between the lookup and the write. The
		// account is there, which is what this command is for, so the race is
		// the answer and not a failure.
		id, err = accounts.Lookup(ctx, kind, name)
		if err != nil {
			return "", false, fmt.Errorf("seed: looking up the account %s: %w", name, err)
		}
		fmt.Printf("skipped: account %s (exists)\n", name)
		return id, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("seed: creating the account %s: %w", name, err)
	}
	fmt.Printf("seeded: account %s (%s)\n", name, account.ID)
	return account.ID, true, nil
}

// seedCreateAccount is the library's creation request for one configured
// account. A username is a login name and needs no proof. An email address
// written here is an administrator's assertion, not a proof of ownership, so the
// account reads pending until its holder proves the address; the baseline ships
// no mail channel and therefore seeds usernames, while a stack that can deliver
// a code may seed the address.
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

// seedAdministration writes what platform administration is made of in one
// domain: the role, every declared permission on it, and the builtin mark that
// keeps the management surface off both. Each is written only when it is not
// already there. The caller runs it once per domain the role is held in — the
// platform domain, and each tenant the deployment serves — because a role and
// its grants are per-domain rows and one domain's do not answer for another's.
//
// The order is load-bearing: the role is created not builtin, the grants are
// written, and only then is the mark set. A role created with the mark would be
// frozen before the grants that give it meaning are in place; the grants are
// written through the seeding surface, which reaches the role whether or not it
// carries the mark, so a re-run over a builtin role adds what the process
// declares since the last one.
//
// It names no account, because neither does: the role and its grants belong to
// the domain, so one write serves every administrator the configuration names.
func seedAdministration(ctx context.Context, manager authz.Manager, seeder authz.Seeder,
	catalog authz.Catalog, domain string,
) error {
	now := time.Now().UTC()
	if err := seedRole(ctx, manager, domain, now); err != nil {
		return err
	}
	if err := seedGrants(ctx, seeder, catalog, domain); err != nil {
		return err
	}
	return markRoleBuiltin(ctx, manager, domain)
}

// seedRole writes the administrator role into one domain unless it is already
// there. The row is created without the builtin mark: the grants are written
// next, and markRoleBuiltin sets the mark once they are in place.
func seedRole(ctx context.Context, manager authz.Manager, domain string, now time.Time) error {
	roles, err := manager.Roles(ctx, domain)
	if err != nil {
		return fmt.Errorf("seed: reading the roles of %s: %w", domain, err)
	}
	if slices.ContainsFunc(roles, func(r authz.RoleRecord) bool { return r.Role == platformAdminRole }) {
		fmt.Printf("skipped: role %s/%s (exists)\n", domain, platformAdminRole)
		return nil
	}
	record := authz.RoleRecord{
		Tenant:         domain,
		Role:           platformAdminRole,
		DescriptionKey: platformAdminDescriptionKey,
		CreatedAt:      now,
	}
	if err := manager.PutRole(ctx, record); err != nil {
		return fmt.Errorf("seed: writing the role %s/%s: %w", domain, platformAdminRole, err)
	}
	fmt.Printf("seeded: role %s/%s\n", domain, platformAdminRole)
	return nil
}

// markRoleBuiltin freezes the administrator role once its grants are written.
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
func markRoleBuiltin(ctx context.Context, manager authz.Manager, domain string) error {
	roles, err := manager.Roles(ctx, domain)
	if err != nil {
		return fmt.Errorf("seed: reading the roles of %s: %w", domain, err)
	}
	i := slices.IndexFunc(roles, func(r authz.RoleRecord) bool { return r.Role == platformAdminRole })
	if i < 0 {
		return fmt.Errorf("seed: the role %s/%s is not there to mark builtin", domain, platformAdminRole)
	}
	if roles[i].Builtin {
		fmt.Printf("skipped: role %s/%s (builtin)\n", domain, platformAdminRole)
		return nil
	}
	if err := manager.MarkRoleBuiltin(ctx, domain, platformAdminRole); err != nil {
		return fmt.Errorf("seed: marking the role %s/%s builtin: %w", domain, platformAdminRole, err)
	}
	fmt.Printf("seeded: role %s/%s (builtin mark)\n", domain, platformAdminRole)
	return nil
}

// seedGrants gives the administrator role every permission the process declares
// and the translation has a row for, adding the ones the role does not already
// hold.
//
// The translation is what turns a permission into the (resource, action) pair
// the grant table holds, and this command reads it off the catalog the policy
// module delivers — which is why the policy module is the one it takes in. A
// declared permission with no row is one no route can be held to, and there is
// nothing to grant for it.
//
// The write goes through the seeding surface, not the manager's grant write:
// once the role carries the builtin mark the manager refuses the latter, and a
// re-run over a role whose declared set has grown has to add the new grants.
// The seeding write adds only and never removes, and reports how many it added,
// which is what makes a second run write nothing.
func seedGrants(ctx context.Context, seeder authz.Seeder, catalog authz.Catalog,
	domain string,
) error {
	grants := make([]authz.Grant, 0, len(catalog.Declared()))
	untranslatable := 0
	for _, permission := range catalog.Declared() {
		object, action, ok := catalog.ObjectAction(permission)
		if !ok {
			// A declared permission no translation row covers: no route may name
			// it, so there is nothing to grant for it and nothing to report.
			untranslatable++
			continue
		}
		grants = append(grants, authz.Grant{Resource: object, Action: action})
	}
	added, err := seeder.SeedGrants(ctx, domain, platformAdminRole, grants)
	if err != nil {
		return fmt.Errorf("seed: granting the declared permissions to %s/%s: %w",
			domain, platformAdminRole, err)
	}
	switch {
	case added > 0:
		fmt.Printf("seeded: %d grant(s) on %s/%s\n", added, domain, platformAdminRole)
	case len(grants) == 0:
		// Nothing was grantable at all, which is the state of a role holding
		// nothing: saying "all present" here would report the opposite of what
		// the operator is about to get.
		fmt.Printf("seeded: no grant on %s/%s: all %d declared permission(s) translate to no pair\n",
			domain, platformAdminRole, untranslatable)
	default:
		fmt.Printf("skipped: grants on %s/%s (all present)\n", domain, platformAdminRole)
	}
	return nil
}

// seedBinding binds the account to the administrator role in one domain unless
// it already holds it there. The role has to be there already: a binding names
// a role that exists in its domain, and one that names nothing is refused.
func seedBinding(ctx context.Context, manager authz.Manager, domain string,
	id authn.AccountID,
) error {
	bindings, err := manager.BindingsOf(ctx, domain, id.String())
	if err != nil {
		return fmt.Errorf("seed: reading the bindings of %s in %s: %w", id, domain, err)
	}
	if slices.ContainsFunc(bindings, func(b authz.Binding) bool { return b.Role == platformAdminRole }) {
		fmt.Printf("skipped: binding %s -> %s in %s (exists)\n", id, platformAdminRole, domain)
		return nil
	}
	record := authz.Binding{
		Tenant:    domain,
		Account:   id.String(),
		Role:      platformAdminRole,
		GrantedAt: time.Now().UTC(),
	}
	if err := manager.PutBinding(ctx, record); err != nil {
		return fmt.Errorf("seed: binding %s to %s in %s: %w", id, platformAdminRole, domain, err)
	}
	fmt.Printf("seeded: binding %s -> %s in %s\n", id, platformAdminRole, domain)
	return nil
}
