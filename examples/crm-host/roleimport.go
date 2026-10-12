package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// roleImportModuleName is the name the role-migration command's requirement
// module is registered under.
const roleImportModuleName = "role-import"

// roleImportGroup is the command group the member-role migration runs under. Its
// one leaf reads the host's own former member_roles table and writes each row
// into its tenant's policy as an account binding.
//
// The group takes the policy module in — every write is the policy manager's —
// and the account store, because a row whose membership is already there and
// whose binding is not is the one a re-run finishes. It also takes the import
// module in, which is what carries the command-line --dry-run switch this
// command and import run share: a command-line name is one argument across the
// whole binary, so the switch is declared once and reached here by taking its
// module in. The command-line module the tree adds to every group brings the
// roster, the database and the journal the action uses.
var roleImportGroup = commandGroup{
	word:     "role",
	platform: []string{accountModuleName, authzpolicy.ModuleName, dataImportModuleName},
	commands: []cliCommand{
		{use: "import", depth: core.StageStart, action: migrateMemberRolesCommand},
	},
}

// init registers the role-migration command's module and its group. The module
// runs on the role group alone, so no serving command is made to carry it, and
// the group registers itself here rather than in the shared command tree.
func init() {
	registerApplication(application{
		module: roleImportModule(),
		runsOn: []string{roleImportGroup.word},
		models: []any{MemberRole{}},
	})
	registerCommandGroup(roleImportGroup)
}

// roleImportModule carries what the migration action uses: the policy manager it
// writes bindings through, the membership store it reads to tell a resumed row
// from a created one, and the roster it reads to refuse a tenant nobody
// registered. Each capability is delivered exclusively, so a command that took
// this module in without one fails at startup rather than at the first read.
func roleImportModule() core.Module {
	return core.Module{
		Name: roleImportModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authz.Manager)(nil)},
		},
	}
}

// migrateMemberRolesCommand carries the host's own former role table into the
// policy.
//
// The previous migration set kept a member's role in this host's member_roles
// table; a role is a policy binding now, so a deployment carried over from that
// host holds roles the policy cannot answer for until they are written as
// bindings. Each row is written as a binding in its tenant's domain, the
// tenant's four roles provisioned with it first, through the same Manager the
// command line and the admin endpoint use.
//
// It is idempotent and resumable: a row whose binding is already in the tenant's
// policy is left as it is and counted skipped, so a run may be repeated without
// reverting a role an administrator raised since, and a run interrupted part-way
// is finished by the next one. The one half-written row a re-run finishes is a
// row whose account is already a member of the tenant — the membership written,
// the role not — and that row is counted resumed rather than created. A row it
// cannot import — an identifier that breaks the account shape, an identifier
// under the static prefix, a role the host does not know, a tenant the roster
// does not hold — is counted failed and the run goes on, and the underlying
// cause is logged at the failing row.
//
// It leaves the member_roles table in place: the host keeps it until it has
// confirmed the migration, and there is only one forward migration and no
// reverse. It writes no account, address or hash to its output, the journal or
// the log. It drives to Start because it writes: its write is recorded through
// the audit seam, which accepts records only from Start on. A --dry-run report
// writes nothing at all.
func migrateMemberRolesCommand(ctx context.Context, reg *core.Registry) error {
	const command = "role import"
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	in, err := readDataImportInput(reg)
	if err != nil {
		return err
	}
	logger, err := namedLogger(reg, roleImportModuleName)
	if err != nil {
		return err
	}
	ctx = log.WithLogger(ctx, logger)
	mgr, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return err
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return err
	}
	rows, err := readMemberRoleRows(ctx, tools.db)
	if err != nil {
		return err
	}

	report := dataImportReport{DryRun: in.DryRun, Reasons: map[string]int{}}
	provisioned := map[tenant.ID]bool{}
	for _, r := range rows {
		outcome, reason := carryMemberRole(ctx, tools, mgr, memberships, provisioned, r, in.DryRun)
		report.add(outcome, reason)
	}

	if in.DryRun {
		return printResult(report)
	}
	if err := tools.record(ctx, command, "role.import", "roles", map[string]string{
		"created": strconv.Itoa(report.Created),
		"resumed": strconv.Itoa(report.Resumed),
		"skipped": strconv.Itoa(report.Skipped),
		"failed":  strconv.Itoa(report.Failed),
	}); err != nil {
		return err
	}
	return printResult(report)
}

// readMemberRoleRows reads every row of the host's own former member_roles
// table, ordered so a run is deterministic.
//
// The read crosses tenants: member_roles is a scoped model, and this command
// carries every tenant's roles in one pass. It says so with tenant.CrossTenant,
// as the other cross-tenant reads this host issues do, so the filter leaves the
// statement unconditioned rather than refusing it for naming no tenant.
func readMemberRoleRows(ctx context.Context, database db.Database) ([]MemberRole, error) {
	var out []MemberRole
	err := database.DB().WithContext(tenant.CrossTenant(ctx)).
		Order("tenant_id, account_id").Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("reading the %s table: %w", memberRoleTable, err)
	}
	return out, nil
}

// carryMemberRole writes one former member_roles row's role as a policy
// binding, or leaves the binding it already has.
//
// A binding the tenant's policy already holds for the account is left as it is
// and counted skipped: a role an administrator raised after an earlier run is
// not reverted. A binding that is missing is written — the tenant's roles
// provisioned first, idempotently, once per tenant — and the row is counted
// resumed when the account is already a member of the tenant (the half-write an
// earlier run left: the membership written, the role not) and created otherwise.
// A --dry-run reads the same state and answers the same outcome without writing.
func carryMemberRole(ctx context.Context, tools cliTools, mgr authz.Manager, memberships authn.Memberships, provisioned map[tenant.ID]bool, r MemberRole, dryRun bool) (outcome, reason string) {
	account, err := authn.ParseAccountID(r.AccountID)
	if err != nil {
		return outcomeFailed, "identifier-not-an-account-id"
	}
	if account.IsStatic() {
		return outcomeFailed, "identifier-static"
	}
	if !validRole(r.Role) {
		return outcomeFailed, "role-unknown"
	}
	tid := r.TenantID
	_, known, err := tools.roster.Get(ctx, tid)
	if err != nil {
		log.FromContext(ctx).Error("reading the roster for an imported role", "error", err, "tenant", r.TenantID)
		return outcomeFailed, "roster-unreadable"
	}
	if !known {
		return outcomeFailed, "tenant-not-in-roster"
	}
	bindings, err := mgr.BindingsOf(tenant.CrossTenant(ctx), string(tid), r.AccountID)
	if err != nil {
		log.FromContext(ctx).Error("reading the role of an imported member", "error", err, "tenant", r.TenantID)
		return outcomeFailed, "role-unreadable"
	}
	if len(bindings) > 0 {
		return outcomeSkipped, ""
	}
	_, member, err := memberships.Membership(ctx, string(r.TenantID), account)
	if err != nil {
		log.FromContext(ctx).Error("reading the membership of an imported role", "error", err, "tenant", r.TenantID)
		return outcomeFailed, "membership-unreadable"
	}
	if dryRun {
		if member {
			return outcomeResumed, ""
		}
		return outcomeCreated, ""
	}
	if !provisioned[tid] {
		if err := provisionTenantRoles(tenant.CrossTenant(ctx), mgr, string(tid)); err != nil {
			log.FromContext(ctx).Error("provisioning the roles of an imported tenant", "error", err, "tenant", r.TenantID)
			return outcomeFailed, "roles-not-provisioned"
		}
		provisioned[tid] = true
	}
	if err := mgr.PutBinding(tenant.CrossTenant(ctx), authz.Binding{
		Tenant: string(tid), Account: r.AccountID, Role: authz.Role(r.Role),
	}); err != nil {
		log.FromContext(ctx).Error("writing an imported role", "error", err, "tenant", r.TenantID)
		return outcomeFailed, "role-not-written"
	}
	if member {
		return outcomeResumed, ""
	}
	return outcomeCreated, ""
}
