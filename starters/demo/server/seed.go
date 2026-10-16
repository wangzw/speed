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
)

// seedModuleName is the name the seed command's configuration module is
// registered under.
const seedModuleName = "seed"

// seedNamespace is where the seed command's input items are mounted.
const seedNamespace = "seed"

// accountsItem is the one item the seed declares.
const accountsItem = "accounts"

// platformAdminRole is the role the seed gives the accounts its configuration
// marks. It holds every permission the process declares, and it lives in the
// platform domain, which is where a role that reaches across tenants belongs.
const platformAdminRole = authz.Role("platform-admin")

// platformAdminDescriptionKey is the description key written with the role. It
// travels in the API answer wherever a role is listed; nothing in this skeleton
// renders it, and a product that adds a role surface gives it a translation.
const platformAdminDescriptionKey = "authz.role.platformAdmin"

// seedInput carries the seed command's inputs.
type seedInput struct {
	// Accounts are the accounts a copy starts with.
	Accounts []seedAccount `config:"accounts"`
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
	// PasswordHash is the credential in the form the static package's Hash
	// writes: a PHC entry, read as it stands and written without a plaintext
	// passing through this configuration. It is what a deployment that does not
	// want its operators' passwords in a file gives instead of Password.
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
// no per-field metadata: the whole list is marked sensitive instead, the way the
// static credential table is, since every entry under it holds a password.
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

// seedDatabase writes the accounts the configuration names, and the
// platform-administrator role the ones marked with it hold.
//
// It is idempotent: an account, a role, a grant or a binding already there is
// left as it stands and reported as skipped, so the command is safe to run at
// every deployment. Nothing else is touched — an account the configuration no
// longer names is not disabled or deleted.
//
// The role and its bindings live in the platform domain, and this command is
// where they come from: no migration seeds a role or a binding, because who a
// deployment's administrators are is the host's answer and not the library's.
// The permissions the role holds are the ones the process declares, read from
// the catalog, so a module that adds a permission and its translation row is
// covered by the next run without an edit here.
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
	catalog, err := core.Resolve[authz.Catalog](reg)
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
	if len(administrators) == 0 {
		return nil
	}

	// Then the role and its grants, which belong to the platform domain and not
	// to an account: they are written once however many administrators the
	// configuration names, and before the bindings, which name a role that has
	// to exist.
	if err := seedAdministration(ctx, manager, catalog); err != nil {
		return err
	}
	for _, id := range administrators {
		if err := seedBinding(ctx, manager, string(authz.PlatformDomain), id); err != nil {
			return err
		}
	}
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

// seedAdministration writes what platform administration is made of: the role,
// and every declared permission on it. Each is written only when it is not
// already there.
//
// It names no account, because neither does: the role and its grants belong to
// the platform domain, so one write serves every administrator the
// configuration names.
func seedAdministration(ctx context.Context, manager authz.Manager, catalog authz.Catalog) error {
	// The platform domain is the reserved one, and it is a domain and not a
	// tenant: no tenant may carry its identifier.
	domain := string(authz.PlatformDomain)
	now := time.Now().UTC()
	if err := seedRole(ctx, manager, domain, now); err != nil {
		return err
	}
	return seedGrants(ctx, manager, catalog, domain, now)
}

// seedRole writes the administrator role into the platform domain unless it is
// already there.
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

// seedGrants gives the administrator role every permission the process declares
// and the translation has a row for.
//
// The translation is what turns a permission into the (resource, action) pair
// the grant table holds, and this command reads it off the catalog the policy
// module delivers — which is why the policy module is the one it takes in. A
// declared permission with no row is one no route can be held to, and there is
// nothing to grant for it.
func seedGrants(ctx context.Context, manager authz.Manager, catalog authz.Catalog,
	domain string, now time.Time,
) error {
	held, err := manager.Grants(ctx, domain, platformAdminRole)
	if err != nil {
		return fmt.Errorf("seed: reading the grants of %s/%s: %w", domain, platformAdminRole, err)
	}
	present := make(map[authz.GrantRecord]struct{}, len(held))
	for _, g := range held {
		present[authz.GrantRecord{Resource: g.Resource, Action: g.Action}] = struct{}{}
	}
	written, untranslatable := 0, 0
	for _, permission := range catalog.Declared() {
		object, action, ok := catalog.ObjectAction(permission)
		if !ok {
			// A declared permission no translation row covers: no route may name
			// it, so there is nothing to grant for it and nothing to report.
			untranslatable++
			continue
		}
		key := authz.GrantRecord{Resource: object, Action: action}
		if _, already := present[key]; already {
			continue
		}
		record := authz.GrantRecord{
			Tenant:    domain,
			Role:      platformAdminRole,
			Resource:  object,
			Action:    action,
			CreatedAt: now,
		}
		if err := manager.PutGrant(ctx, record); err != nil {
			return fmt.Errorf("seed: granting %s to %s/%s: %w", permission, domain, platformAdminRole, err)
		}
		written++
	}
	switch {
	case written > 0:
		fmt.Printf("seeded: %d grant(s) on %s/%s\n", written, domain, platformAdminRole)
	case untranslatable == 0:
		fmt.Printf("skipped: grants on %s/%s (all present)\n", domain, platformAdminRole)
	default:
		// Nothing was grantable at all, which is the state of a role holding
		// nothing: saying "all present" here would report the opposite of what
		// the operator is about to get.
		fmt.Printf("seeded: no grant on %s/%s: all %d declared permission(s) translate to no pair\n",
			domain, platformAdminRole, untranslatable)
	}
	return nil
}

// seedBinding binds the account to the administrator role unless it already
// holds it. The role has to be there already: a binding names a role, and one
// that names nothing is refused.
func seedBinding(ctx context.Context, manager authz.Manager, domain string,
	id authn.AccountID,
) error {
	bindings, err := manager.BindingsOf(ctx, domain, id.String())
	if err != nil {
		return fmt.Errorf("seed: reading the bindings of %s in %s: %w", id, domain, err)
	}
	if slices.ContainsFunc(bindings, func(b authz.Binding) bool { return b.Role == platformAdminRole }) {
		fmt.Printf("skipped: binding %s -> %s (exists)\n", id, platformAdminRole)
		return nil
	}
	record := authz.Binding{
		Tenant:    domain,
		Account:   id.String(),
		Role:      platformAdminRole,
		GrantedAt: time.Now().UTC(),
	}
	if err := manager.PutBinding(ctx, record); err != nil {
		return fmt.Errorf("seed: binding %s to %s: %w", id, platformAdminRole, err)
	}
	fmt.Printf("seeded: binding %s -> %s\n", id, platformAdminRole)
	return nil
}
