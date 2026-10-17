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
// deployment serves, and the platform-administrator role the accounts marked
// with it hold.
//
// It is idempotent: an account, a tenant, a role, a grant or a binding already
// there is left as it stands and reported as skipped, so the command is safe to
// run at every deployment. On an existing database it corrects two things: it
// marks the administrator role builtin when an earlier seed wrote the row
// without the mark, and it adds the declared grants the role does not hold.
// Nothing else is touched — an account or a tenant the configuration no longer
// names is not disabled or deleted.
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
	// The reserved platform domain is not a tenant, and the configuration is
	// checked before anything is written.
	//
	// The policy module refuses to start when the roster holds a tenant carrying
	// that identifier, so a configuration naming it here would seed cleanly and
	// leave the next serve refused: the failure would surface a run later, in a
	// different command, with nothing pointing back at the entry that caused it.
	// The read that writes no row is this command's own, so refusing the name now
	// is what keeps the mistake caught in the run that reads the configuration.
	// The value is the same reserved one the startup check tests against, so the
	// two refusals speak one vocabulary and a fix is the same in both.
	for _, t := range in.Tenants {
		if t == string(authz.PlatformDomain) {
			return fmt.Errorf("%w: seed.%s names the tenant %q. Rename it: the platform "+
				"domain is a reserved value and its policy rows are the platform operations'",
				authz.ErrPlatformDomainTaken, tenantsItem, t)
		}
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
	// The accounts first, and the identifiers of the ones the configuration
	// marks as administrators.
	var administrators []authn.AccountID
	for _, a := range in.Accounts {
		if err := a.check(); err != nil {
			return err
		}
		id, err := seedOneAccount(ctx, accounts, admin, a)
		if err != nil {
			return err
		}
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

// seedOneAccount writes one configured account unless its login name is already
// taken, and answers with the account's identifier either way.
func seedOneAccount(ctx context.Context, accounts authn.Accounts, admin authn.AccountAdmin,
	a seedAccount,
) (authn.AccountID, error) {
	kind, name, err := seedLoginName(a.Identifier)
	if err != nil {
		return "", err
	}
	id, err := accounts.Lookup(ctx, kind, name)
	switch {
	case err == nil:
		fmt.Printf("skipped: account %s (exists)\n", name)
		return id, nil
	case !errors.Is(err, authn.ErrAccountNotFound):
		return "", fmt.Errorf("seed: looking up the account %s: %w", name, err)
	}
	created, err := admin.Create(ctx, seedCreateAccount(kind, name, a))
	if errors.Is(err, authn.ErrAccountExists) {
		// Another run took the login name between the lookup and the write. The
		// account is there, which is what this command is for, so the race is
		// the answer and not a failure.
		id, err = accounts.Lookup(ctx, kind, name)
		if err != nil {
			return "", fmt.Errorf("seed: looking up the account %s: %w", name, err)
		}
		fmt.Printf("skipped: account %s (exists)\n", name)
		return id, nil
	}
	if err != nil {
		return "", fmt.Errorf("seed: creating the account %s: %w", name, err)
	}
	fmt.Printf("seeded: account %s (%s)\n", name, created.ID)
	return created.ID, nil
}

// seedCreateAccount is the library's creation request for one configured
// account. The identifier is written verified: the host wrote it, so the account
// does not have to prove a mailbox this skeleton ships it with.
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
