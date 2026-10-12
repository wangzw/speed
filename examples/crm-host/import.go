package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// dataImportModuleName is the name the import command's requirement module is
// registered under.
const dataImportModuleName = "data-import"

// dataImportNamespace is where the import command's own inputs are mounted.
// They take their value from the command line alone, so nothing under it may
// appear in a configuration file or the environment.
const dataImportNamespace = "import"

// The tables the previous migration set built, which this command reads and the
// current set does not create. They exist only in a database carried over from
// the previous deployment, and the host leaves them in place until it has
// confirmed the import.
const (
	legacyUsersTable       = "users"
	legacyMembershipsTable = "memberships"
)

// importVia is the Via the imported accounts and memberships are written with.
const importVia = "import"

// The outcomes a legacy row has. A row that is already there is skipped; one a
// run left half-written and this run finishes is resumed; one that cannot be
// imported is failed; the rest are created.
const (
	outcomeCreated = "created"
	outcomeResumed = "resumed"
	outcomeSkipped = "skipped"
	outcomeFailed  = "failed"
)

// legacyUser is one row of the users table the previous migration set built:
// the identifier the host's own tables name as a foreign-key value, the address
// the person signed in with, the name they showed, the password entry in the
// old host's envelope, and whether the account was switched off.
type legacyUser struct {
	ID           string `gorm:"column:id"`
	Email        string `gorm:"column:email"`
	DisplayName  string `gorm:"column:display_name"`
	PasswordHash string `gorm:"column:password_hash"`
	Disabled     bool   `gorm:"column:disabled"`
}

// legacyMembership is one row of the memberships table the previous migration
// set built: which user belonged to which tenant, and in what role.
type legacyMembership struct {
	TenantID string `gorm:"column:tenant_id"`
	UserID   string `gorm:"column:user_id"`
	Role     string `gorm:"column:role"`
}

// dataImportReport is what the import command answers with: how many rows it
// wrote, how many it finished a half-written one of, how many it left as they
// were, and how many it could not import, with the reasons counted. It carries
// no address, no password and no hash.
type dataImportReport struct {
	DryRun  bool           `json:"dry_run"`
	Created int            `json:"created"`
	Resumed int            `json:"resumed"`
	Skipped int            `json:"skipped"`
	Failed  int            `json:"failed"`
	Reasons map[string]int `json:"reasons"`
}

// add counts one row's outcome and, when it failed, the reason it did.
func (r *dataImportReport) add(outcome, reason string) {
	switch outcome {
	case outcomeCreated:
		r.Created++
	case outcomeResumed:
		r.Resumed++
	case outcomeSkipped:
		r.Skipped++
	default:
		r.Failed++
	}
	if reason != "" {
		r.Reasons[reason]++
	}
}

// dataImportInput carries the import command's own input.
type dataImportInput struct {
	DryRun bool `config:"dry-run"`
}

// dataImportSchema declares the import command's switch. It is flag-only: the
// command is run by hand against a database carried over from the previous
// deployment, and nothing about it belongs in a serving configuration.
func dataImportSchema() config.Schema {
	return config.Schema{
		Namespace: dataImportNamespace,
		Mounts:    []config.Mount{{Value: &dataImportInput{}}},
		Items: map[string]config.Item{
			"dry-run": {
				Origins:  config.OriginFlag,
				FlagName: "dry-run",
				Description: "report what the import would write without writing any account, membership, " +
					"role or audit row",
			},
		},
	}
}

// readDataImportInput reads the import command's own input.
func readDataImportInput(reg *core.Registry) (dataImportInput, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return dataImportInput{}, err
	}
	var in dataImportInput
	if err := reader.Decode(dataImportNamespace, &in); err != nil {
		return dataImportInput{}, err
	}
	return in, nil
}

// dataImportGroup is the command group the one-shot import runs under, on every
// stack. It carries the account store, so its action resolves the account,
// membership and write capabilities, and the policy module, because a legacy
// membership's role is written as a policy binding; the shared flags and the
// roster come with the command-line platform every group carries.
var dataImportGroup = commandGroup{
	word:     "import",
	platform: []string{accountModuleName, authzpolicy.ModuleName},
	commands: []cliCommand{
		{use: "run", depth: core.StageStart, action: importCommand},
	},
}

// init registers the import command's module and its group. The module runs on
// the import group alone, so no serving command is made to carry it.
func init() {
	registerApplication(application{
		module: dataImportModule(),
		runsOn: []string{dataImportGroup.word},
	})
	registerCommandGroup(dataImportGroup)
}

// dataImportModule carries the account capabilities the import action uses, the
// policy it writes the roles into, and the logger its per-row failures are
// reported through.
//
// It requires the roster because importMembership reads it to refuse a tenant
// nobody registered, and because toolsOf resolves it: a command that ran this
// module without the roster fails at startup rather than at the first read. It
// requires the policy manager because a legacy membership's role is written as a
// policy binding, not as a row of any table of this host's. Each account
// capability is delivered exclusively, so a command that took this module in
// without one fails here rather than at the first write.
func dataImportModule() core.Module {
	return core.Module{
		Name: dataImportModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.AccountAdmin)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authz.Manager)(nil)},
		},
		Resources: []any{dataImportSchema()},
	}
}

// importCommand writes the previous deployment's users and memberships into the
// account store as accounts, memberships and roles. It is a one-shot command the
// host runs once, by hand; no route reaches it, and it exposes no account
// parameter a caller could name.
//
// It is idempotent and resumable: an account or membership already there is left
// as it is and counted skipped, so a run may be repeated without reverting a
// change made since — a role an administrator raised, a status one set — and a
// run interrupted part-way is finished by the next one. The one half-written row
// a later run does finish is a membership whose role row the earlier run never
// wrote: a role row is only ever filled in when it is missing, and the row is
// counted resumed rather than skipped. A row it cannot import — an identifier
// that breaks the account shape, an entry it cannot read, a tenant the roster
// does not hold, an address another account already owns, an identifier whose
// account was deleted — is counted failed and the run goes on, and the
// underlying cause is logged at the failing row. It writes no password, hash or
// address to its output, the journal or the log.
//
// It drives to Start because it writes: its write is recorded through the audit
// seam, which accepts records only from Start on. A --dry-run report writes
// nothing at all.
func importCommand(ctx context.Context, reg *core.Registry) error {
	const command = "import run"
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	in, err := readDataImportInput(reg)
	if err != nil {
		return err
	}
	logger, err := namedLogger(reg, dataImportModuleName)
	if err != nil {
		return err
	}
	ctx = log.WithLogger(ctx, logger)
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return err
	}
	admin, err := core.Resolve[authn.AccountAdmin](reg)
	if err != nil {
		return err
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return err
	}
	mgr, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return err
	}

	users, err := readLegacyUsers(ctx, tools.db)
	if err != nil {
		return err
	}
	grants, err := readLegacyMemberships(ctx, tools.db)
	if err != nil {
		return err
	}

	report := dataImportReport{DryRun: in.DryRun, Reasons: map[string]int{}}
	for _, u := range users {
		outcome, reason := importUser(ctx, accounts, admin, u, in.DryRun)
		report.add(outcome, reason)
	}
	for _, g := range grants {
		outcome, reason := importMembership(ctx, tools, memberships, mgr, g, in.DryRun)
		report.add(outcome, reason)
	}

	if in.DryRun {
		return printResult(report)
	}
	if err := tools.record(ctx, command, "account.import", "accounts", map[string]string{
		"created": strconv.Itoa(report.Created),
		"resumed": strconv.Itoa(report.Resumed),
		"skipped": strconv.Itoa(report.Skipped),
		"failed":  strconv.Itoa(report.Failed),
	}); err != nil {
		return err
	}
	return printResult(report)
}

// readLegacyUsers reads every row of the previous set's users table, ordered by
// identifier so a run is deterministic. A database without the table is an
// error naming it: there is nothing to import.
func readLegacyUsers(ctx context.Context, database db.Database) ([]legacyUser, error) {
	var out []legacyUser
	err := database.DB().WithContext(ctx).
		Raw("SELECT id, email, display_name, password_hash, disabled FROM " + legacyUsersTable + " ORDER BY id").
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("reading the %s table: %w", legacyUsersTable, err)
	}
	return out, nil
}

// readLegacyMemberships reads every row of the previous set's memberships table,
// ordered so a run is deterministic.
func readLegacyMemberships(ctx context.Context, database db.Database) ([]legacyMembership, error) {
	var out []legacyMembership
	err := database.DB().WithContext(ctx).
		Raw("SELECT tenant_id, user_id, role FROM " + legacyMembershipsTable + " ORDER BY tenant_id, user_id").
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("reading the %s table: %w", legacyMembershipsTable, err)
	}
	return out, nil
}

// importUser writes one legacy user into the account store, or leaves the
// account it already has.
//
// The identifier is carried over unchanged, so the host's own tables that name
// it keep pointing at the same person; the address is written as an
// unverified import assertion, never as a proof of ownership, so no automatic
// social link and no enterprise merge will act on it; and the password entry is
// handed over in the old envelope for the account module to convert without
// loss, the same lossless conversion ImportHash performs. A user switched off in
// the old system becomes a disabled account, switched off the moment it is made.
//
// An account already there is left as it is: its status is the store's and an
// administrator's to hold, and a run that re-applied the legacy row's status
// would silently undo a change made since.
func importUser(ctx context.Context, accounts authn.Accounts, admin authn.AccountAdmin, u legacyUser, dryRun bool) (outcome, reason string) {
	id, err := authn.ParseAccountID(u.ID)
	if err != nil {
		return outcomeFailed, "identifier-not-an-account-id"
	}
	if id.IsStatic() {
		return outcomeFailed, "identifier-static"
	}
	existing, err := accounts.Get(ctx, id)
	switch {
	case err == nil:
		if existing.Kind != authn.KindUser {
			return outcomeFailed, "existing-account-not-a-user"
		}
		return outcomeSkipped, ""
	case !errors.Is(err, authn.ErrAccountNotFound):
		log.FromContext(ctx).Error("reading an account to import", "error", err, "identifier", u.ID)
		return outcomeFailed, "account-unreadable"
	}
	if dryRun {
		return outcomeCreated, ""
	}
	account, err := admin.Create(ctx, authn.CreateAccount{
		ID:               id,
		Kind:             authn.KindUser,
		DisplayName:      u.DisplayName,
		Email:            u.Email,
		EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaImportUnverified},
		PasswordHash:     u.PasswordHash,
		Via:              importVia,
	})
	switch {
	case errors.Is(err, authn.ErrAccountExists):
		return outcomeFailed, collisionReason(ctx, accounts, id, u.Email)
	case err != nil:
		log.FromContext(ctx).Error("creating an imported account", "error", err, "identifier", u.ID)
		return outcomeFailed, "account-not-created"
	}
	if u.Disabled {
		if err := admin.SetStatus(ctx, account.ID, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			log.FromContext(ctx).Error("disabling an imported account", "error", err, "identifier", u.ID)
			return outcomeFailed, "status-not-set"
		}
	}
	return outcomeCreated, ""
}

// collisionReason names which of the two things Create can collide on a row hit:
// the address, which another account already owns, or the identifier, held by an
// account that was deleted after an earlier run imported it. A deleted account
// reads as gone but keeps its row as a tombstone, so the identifier still
// collides with the create; the address a tombstone may still be found by is
// bound to that same identifier, which is why a look-up that finds this
// identifier or nothing at all both mean the identifier is the collision.
func collisionReason(ctx context.Context, accounts authn.Accounts, id authn.AccountID, email string) string {
	held, err := accounts.Lookup(ctx, authn.IdentifierEmail, email)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return "identifier-tombstoned"
	case err != nil:
		log.FromContext(ctx).Error("reading the address of a colliding import", "error", err)
		return "account-not-created"
	case held != id:
		return "address-held-by-another-account"
	default:
		return "identifier-tombstoned"
	}
}

// importMembership writes one legacy membership into the account store and its
// role into the tenant's policy, or leaves the membership it already has.
//
// A membership whose tenant the roster does not hold is failed rather than
// written: the role is a policy binding in that tenant's domain, and a
// membership for a tenant nobody registered serves nobody. The membership is
// written through authn.Memberships and the role through grantIn, the same pair
// the command line and the admin endpoint use.
//
// A membership already there keeps the role the tenant's policy holds: a role an
// administrator raised after an earlier run is left as it is, and the row is
// counted skipped. Only a membership whose role binding is missing — one an
// earlier run wrote the membership of and then failed before writing the role —
// has the role written, and that row is counted resumed.
//
// It is written active: the previous set's memberships table carries no state
// column to carry over, and a Membership whose Active is false reads as a
// suspended membership, which a carried-over row is not.
func importMembership(ctx context.Context, tools cliTools, memberships authn.Memberships, mgr authz.Manager, g legacyMembership, dryRun bool) (outcome, reason string) {
	account, err := authn.ParseAccountID(g.UserID)
	if err != nil {
		return outcomeFailed, "identifier-not-an-account-id"
	}
	if account.IsStatic() {
		return outcomeFailed, "identifier-static"
	}
	if !validRole(g.Role) {
		return outcomeFailed, "role-unknown"
	}
	tid := tenant.ID(g.TenantID)
	_, known, err := tools.roster.Get(ctx, tid)
	if err != nil {
		log.FromContext(ctx).Error("reading the roster for an imported membership", "error", err, "tenant", g.TenantID)
		return outcomeFailed, "roster-unreadable"
	}
	if !known {
		return outcomeFailed, "tenant-not-in-roster"
	}
	_, exists, err := memberships.Membership(ctx, g.TenantID, account)
	if err != nil {
		log.FromContext(ctx).Error("reading a membership to import", "error", err, "tenant", g.TenantID)
		return outcomeFailed, "membership-unreadable"
	}
	if exists {
		bindings, bindErr := mgr.BindingsOf(tenant.CrossTenant(ctx), string(tid), string(account))
		if bindErr != nil {
			log.FromContext(ctx).Error("reading the role of an imported membership", "error", bindErr, "tenant", g.TenantID)
			return outcomeFailed, "role-unreadable"
		}
		if len(bindings) > 0 {
			return outcomeSkipped, ""
		}
		if dryRun {
			return outcomeResumed, ""
		}
		if _, writeErr := grantIn(tenant.CrossTenant(ctx), mgr, tid, account, g.Role); writeErr != nil {
			log.FromContext(ctx).Error("writing the role of an imported membership", "error", writeErr, "tenant", g.TenantID)
			return outcomeFailed, "role-not-written"
		}
		return outcomeResumed, ""
	}
	if dryRun {
		return outcomeCreated, ""
	}
	err = memberships.Put(tenant.CrossTenant(ctx), authn.Membership{
		Account: account, Tenant: g.TenantID, Active: true, Source: importVia,
	})
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return outcomeFailed, "account-missing"
	case err != nil:
		log.FromContext(ctx).Error("writing an imported membership", "error", err, "tenant", g.TenantID)
		return outcomeFailed, "membership-not-written"
	}
	if _, err := grantIn(tenant.CrossTenant(ctx), mgr, tid, account, g.Role); err != nil {
		log.FromContext(ctx).Error("writing the role of an imported membership", "error", err, "tenant", g.TenantID)
		return outcomeFailed, "role-not-written"
	}
	return outcomeCreated, ""
}
