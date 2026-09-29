package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/tenant"
)

// cliModuleName is the name the command-line module is registered under. It
// constructs nothing: it carries the command-line input items, and its
// requirements are what hold every command that takes it in to the modules
// its actions use.
const cliModuleName = "cli"

// cliNamespace is where the command-line items are mounted. They take their
// value from the command line alone, so nothing under it may appear in a
// configuration file or the environment.
const cliNamespace = "cli"

// cliInput carries the command-line inputs the command groups share.
//
// A command-line name is one argument across the whole binary, so the commands
// share these items rather than each declaring its own: tenant create and user
// grant both read --tenant-id, and a second item under the same name would be
// a startup conflict for every command. No item is marked required for the
// same reason — required is judged per decoded struct, and the struct is
// shared — so each action checks the fields it needs and names the flag it is
// missing.
//
// A command group a feature slice adds reads these through readCLIInput, since
// the command-line module runs on every group. Inputs of its own go in a
// flag-only schema its module declares under a namespace of its own, with flag
// names no other item in the binary uses.
type cliInput struct {
	TenantID    string `config:"tenant-id"`
	TenantAlias string `config:"tenant-alias"`
	Email       string `config:"email"`
	DisplayName string `config:"display-name"`
	Role        string `config:"role"`
}

// cliSchema declares the command-line items.
func cliSchema() config.Schema {
	flag := func(name, placeholder, description string) config.Item {
		return config.Item{
			Origins:     config.OriginFlag,
			FlagName:    name,
			Placeholder: placeholder,
			Description: description,
		}
	}
	return config.Schema{
		Namespace: cliNamespace,
		Mounts:    []config.Mount{{Value: &cliInput{}}},
		Items: map[string]config.Item{
			"tenant-id":    flag("tenant-id", "ID", "the tenant's immutable identifier"),
			"tenant-alias": flag("tenant-alias", "ALIAS", "the name requests use to name the tenant"),
			"email":        flag("email", "EMAIL", "the email address of a CRM user"),
			"display-name": flag("display-name", "NAME", "the name a CRM user is shown under"),
			"role":         flag("role", "ROLE", "one of "+strings.Join(roles, ", ")),
		},
	}
}

// readCLIInput reads the command-line items.
func readCLIInput(reg *core.Registry) (cliInput, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return cliInput{}, err
	}
	var in cliInput
	if err := reader.Decode(cliNamespace, &in); err != nil {
		return cliInput{}, err
	}
	return in, nil
}

// errMissingFlag names a flag an action needs and was not given.
func errMissingFlag(command string, flags ...string) error {
	return fmt.Errorf("%s needs --%s", command, strings.Join(flags, " and --"))
}

// cliCommand is one leaf of a command group: the word, how far it drives the
// lifecycle, and what it does there.
type cliCommand struct {
	use    string
	depth  core.Stage
	action config.Action
}

// commandGroup is a word under every stack whose leaves are one-shot commands:
// tenant, user, and whatever a feature slice adds. The group node takes in the
// platform the command line runs on and the host modules that name the word in
// their registration, and each leaf inherits them.
type commandGroup struct {
	word     string
	commands []cliCommand
}

// commandGroups is every command group, in registration order. The command tree
// writes one subtree per group under every stack.
var commandGroups []commandGroup

// registerCommandGroup adds a command group. The file that defines the actions
// registers them from its own init.
func registerCommandGroup(g commandGroup) { commandGroups = append(commandGroups, g) }

// The depths the commands drive to.
//
// A command that writes goes to Start: its write is recorded through the audit
// seam, and the journal accepts records from its own Start on — every Init
// runs before any Start, so an action at Init would be refused whatever it
// declared. A command that only reads goes to Init, where the roster's snapshot
// has been loaded.
var (
	tenantGroup = commandGroup{word: "tenant", commands: []cliCommand{
		{use: "create", depth: core.StageStart, action: createTenantCommand},
		{use: "disable", depth: core.StageStart, action: statusCommand("tenant disable", tenant.StatusDisabled)},
		{use: "enable", depth: core.StageStart, action: statusCommand("tenant enable", tenant.StatusActive)},
		{use: "list", depth: core.StageInit, action: listTenantsCommand},
	}}
	userGroup = commandGroup{word: "user", commands: []cliCommand{
		{use: "create", depth: core.StageStart, action: createUserCommand},
		{use: "grant", depth: core.StageStart, action: grantCommand},
		{use: "list", depth: core.StageInit, action: listUsersCommand},
	}}
)

// cliPurpose is the purpose every command-line write records. The command line
// carries no identity and no tenant, so the seam row is the one place the
// write's origin is stated.
func cliPurpose(command string) string {
	return "an operator ran `" + command + "` at the command line, outside any request: " +
		"no identity or tenant scope carries this write"
}

// cliTools is what every command action takes up.
type cliTools struct {
	in       cliInput
	db       db.Database
	roster   tenant.Catalog
	recorder audit.Audit
}

// toolsOf takes the command-line module's capabilities up.
func toolsOf(reg *core.Registry) (cliTools, error) {
	in, err := readCLIInput(reg)
	if err != nil {
		return cliTools{}, err
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return cliTools{}, err
	}
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return cliTools{}, err
	}
	recorder, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return cliTools{}, err
	}
	return cliTools{in: in, db: database, roster: roster, recorder: recorder}, nil
}

// record writes a command-line write's seam row.
func (c cliTools) record(ctx context.Context, command, action, object string, detail map[string]string) error {
	if err := c.recorder.Record(ctx, audit.Entry{
		Action:  action,
		Object:  object,
		Outcome: "ok",
		Purpose: cliPurpose(command),
		Detail:  detail,
	}); err != nil {
		return fmt.Errorf("the write is done and its audit record is not: %w", err)
	}
	return nil
}

// printResult writes what the command answers with, as one JSON line on
// standard output. It is what the caller asked for rather than a record of what
// the program is doing, so it does not go through the logger.
func printResult(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(line))
	return err
}

// createTenantCommand registers an active tenant.
func createTenantCommand(ctx context.Context, reg *core.Registry) error {
	const command = "tenant create"
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	if tools.in.TenantID == "" || tools.in.TenantAlias == "" {
		return errMissingFlag(command, "tenant-id", "tenant-alias")
	}
	t, err := registerTenant(ctx, tools.roster, tools.in.TenantID, tools.in.TenantAlias)
	if err != nil {
		return err
	}
	if err := tools.record(ctx, command, "tenant.create", "tenants/"+string(t.ID),
		map[string]string{"alias": t.Alias}); err != nil {
		return err
	}
	return printResult(viewOf(t))
}

// statusCommand disables or enables a tenant.
func statusCommand(command string, status tenant.Status) config.Action {
	return func(ctx context.Context, reg *core.Registry) error {
		tools, err := toolsOf(reg)
		if err != nil {
			return err
		}
		if tools.in.TenantID == "" {
			return errMissingFlag(command, "tenant-id")
		}
		t, err := setTenantStatus(ctx, tools.roster, tools.in.TenantID, status)
		if err != nil {
			return err
		}
		if err := tools.record(ctx, command, "tenant.status", "tenants/"+string(t.ID),
			map[string]string{"status": t.Status.String()}); err != nil {
			return err
		}
		return printResult(viewOf(t))
	}
}

// listTenantsCommand prints the roster.
func listTenantsCommand(ctx context.Context, reg *core.Registry) error {
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	all, err := tools.roster.List(ctx)
	if err != nil {
		return err
	}
	out := make([]tenantView, 0, len(all))
	for _, t := range all {
		out = append(out, viewOf(t))
	}
	return printResult(out)
}

// createUserCommand adds a CRM user. The password is read from standard input
// rather than taken as a flag, so it does not land in the shell history or in
// the process list of every other user on the machine.
func createUserCommand(ctx context.Context, reg *core.Registry) error {
	const command = "user create"
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	if tools.in.Email == "" || tools.in.DisplayName == "" {
		return errMissingFlag(command, "email", "display-name")
	}
	password, err := readSecret(os.Stdin)
	if err != nil {
		return err
	}
	// The users module runs on this group for its derivation alone, and
	// delivers it exclusively, so it is taken up here rather than required
	// by the command-line module every group runs.
	derivation, err := core.Resolve[passwords](reg)
	if err != nil {
		return err
	}
	user, err := createUser(ctx, tools.db, derivation, tools.in.Email, tools.in.DisplayName, password)
	if err != nil {
		return err
	}
	if err := tools.record(ctx, command, "user.create", userTable+"/"+user.ID, nil); err != nil {
		return err
	}
	return printResult(userViewOf(user))
}

// grantCommand gives a user a role in a tenant.
//
// Like the admin endpoint's grant it crosses tenants — nothing on the command
// line is scoped to the tenant it writes into — so it says so with
// tenant.CrossTenant and names the tenant in the row it saves, and its seam row
// carries the purpose.
func grantCommand(ctx context.Context, reg *core.Registry) error {
	const command = "user grant"
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	if tools.in.TenantID == "" || tools.in.Email == "" || tools.in.Role == "" {
		return errMissingFlag(command, "tenant-id", "email", "role")
	}
	id := tenant.ID(tools.in.TenantID)
	_, registered, err := tools.roster.Get(ctx, id)
	if err != nil {
		return err
	}
	if !registered {
		return fmt.Errorf("%s: %w", tools.in.TenantID, errNoSuchTenant)
	}
	user, found, err := userByEmail(ctx, tools.db, tools.in.Email)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s: no user has this email", tools.in.Email)
	}
	granted, err := grantIn(tenant.CrossTenant(ctx), tools.db, id, user.ID, tools.in.Role)
	if err != nil {
		return err
	}
	if err := tools.record(ctx, command, "membership.grant",
		membershipTable+"/"+string(id)+"/"+user.ID, map[string]string{"role": granted.Role}); err != nil {
		return err
	}
	return printResult(grantView{TenantID: string(granted.TenantID), UserID: granted.UserID, Role: granted.Role})
}

// userListing is one user as user list prints it, with the tenants they belong
// to.
type userListing struct {
	userView
	Memberships []grantView `json:"memberships"`
}

// listUsersCommand prints every user and their memberships. The membership
// read crosses tenants, as the question does.
func listUsersCommand(ctx context.Context, reg *core.Registry) error {
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	people, err := listUsers(ctx, tools.db)
	if err != nil {
		return err
	}
	out := make([]userListing, 0, len(people))
	for _, u := range people {
		rows, err := membershipsOfUser(tenant.CrossTenant(ctx), tools.db, u.ID)
		if err != nil {
			return err
		}
		entry := userListing{userView: userViewOf(u), Memberships: []grantView{}}
		for _, m := range rows {
			entry.Memberships = append(entry.Memberships,
				grantView{TenantID: string(m.TenantID), UserID: m.UserID, Role: m.Role})
		}
		out = append(out, entry)
	}
	return printResult(out)
}

// readSecret reads one line from a reader and drops its line ending. A secret
// may contain spaces, so the whole line is the secret.
func readSecret(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the password from standard input: %w", err)
	}
	secret := strings.TrimRight(line, "\r\n")
	if secret == "" {
		return "", errors.New("reading the password from standard input: the input is empty")
	}
	return secret, nil
}

// init registers the command-line module and the two command groups whose
// actions are here. The module names no command of its own: the command tree
// takes it into every command group, because every group's actions read the
// shared flags.
func init() {
	registerApplication(application{module: cliModule(), models: []any{Membership{}, User{}}})
	registerCommandGroup(tenantGroup)
	registerCommandGroup(userGroup)
}

// cliModule carries the command-line items and states what the command actions
// take up.
//
// It has no New: an action is not a module and has no product, and the module
// exists so that its input items reach the command line of the commands that
// take it in, and so that those commands answer for what the actions use. The
// roster requirement is the guard every writer of the scoped memberships table
// states — without the roster module in the command its filter is not
// installed and user grant would write the table unscoped — and the audit
// requirement fails a command that forgot the journal rather than letting it
// write with no seam row.
func cliModule() core.Module {
	return core.Module{
		Name: cliModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*audit.Audit)(nil)},
		},
		Resources: []any{cliSchema()},
	}
}
