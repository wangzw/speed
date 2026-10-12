package main

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// operatorCommandsModuleName is the name the operator-provisioning command's
// requirement module is registered under.
const operatorCommandsModuleName = "operator-commands"

// operatorProvisionGroup is the command group the platform operator's
// provisioner runs under. Its one leaf writes the operator role, its grant rows
// and the operator's binding in the reserved platform domain, through the policy
// manager. The group takes the policy module in, since the write is the
// manager's; the command-line module the tree adds to every group brings the
// roster, the database and the journal the action uses.
var operatorProvisionGroup = commandGroup{
	word:     "operator",
	platform: []string{authzpolicy.ModuleName},
	commands: []cliCommand{
		{use: "provision", depth: core.StageStart, action: provisionOperatorCommand},
	},
}

// init registers the operator-provisioning command's module and its group. The
// module runs on the operator group alone, so no serving command is made to
// carry it.
func init() {
	registerApplication(application{module: operatorCommandsModule(), runsOn: []string{operatorProvisionGroup.word}})
	registerCommandGroup(operatorProvisionGroup)
}

// operatorCommandsModule holds the policy manager the provision action writes
// through. Its instance is never built — an action is not a module and has no
// product — so it exists to answer for what the action uses, which makes a
// command that took it in without the manager fail at startup rather than at the
// first write.
func operatorCommandsModule() core.Module {
	return core.Module{
		Name: operatorCommandsModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*authz.Manager)(nil)},
		},
	}
}

// operatorProvisionReport is what the provision action answers with: the account
// it binds, the role it binds it to, and the domain both belong to. It carries
// no policy secret.
type operatorProvisionReport struct {
	Domain  string `json:"domain"`
	Account string `json:"account"`
	Role    string `json:"role"`
}

// provisionOperatorCommand writes the platform operator's role, its grant rows
// and the operator's binding in the reserved platform domain, through the policy
// manager. It is the host's own command-line face of the same provisioning the
// authz-roles module runs at serve startup: policy rows are data the host writes
// and the migration seeds none, so a deployment that has not served yet — or one
// re-asserting the binding over rows an administrator removed — writes it here.
//
// The operator is the account the static credential table answers for the
// operator subject, the full `static.<subject>` identifier; the reserved
// platform domain carries that account's role and no tenant carries a binding of
// it, so the operator stays a stranger to every tenant.
//
// It is idempotent: every row it writes is keyed by what it names, so a second
// run rewrites the same rows to the same values and adds none. Its write is
// recorded through the audit seam, which is why it drives to Start. It exposes
// nothing over HTTP — no route reaches it and it names no handler — so the only
// way to run it is the command line.
func provisionOperatorCommand(ctx context.Context, reg *core.Registry) error {
	const command = "operator provision"
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	logger, err := namedLogger(reg, operatorCommandsModuleName)
	if err != nil {
		return err
	}
	ctx = log.WithLogger(ctx, logger)
	mgr, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return err
	}
	if err := provisionPlatformRoles(ctx, mgr); err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	domain := string(authz.PlatformDomain)
	if err := tools.record(ctx, command, "operator.provision",
		"bindings/"+domain+"/"+string(operatorAccount),
		map[string]string{"role": string(authzRoleOperator)}); err != nil {
		return err
	}
	return printResult(operatorProvisionReport{
		Domain: domain, Account: string(operatorAccount), Role: string(authzRoleOperator),
	})
}
