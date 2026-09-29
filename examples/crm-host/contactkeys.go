package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/tenant"
)

// contactKeysModuleName is the name the module behind the contact command
// group is registered under.
const contactKeysModuleName = "contactkeys"

// contactGroupWord is the word of the contact command group.
const contactGroupWord = "contact"

// contactRekeyAction is the action the rekey's seam rows carry.
const contactRekeyAction = "contact.rekey"

// contactGroup holds the one contact command: the rekey an operator runs after
// rotating the database module's root key.
var contactGroup = commandGroup{word: contactGroupWord, commands: []cliCommand{
	{use: "rekey", depth: core.StageStart, action: rekeyContactsCommand},
}}

// rekeyedTenant is what the rekey answers for one tenant: how many contacts it
// wrote again, or why it could not.
type rekeyedTenant struct {
	TenantID string `json:"tenant_id"`
	Contacts int    `json:"contacts"`
	Error    string `json:"error,omitempty"`
}

// rekeyPurpose is the purpose the rekey's seam rows state.
const rekeyPurpose = "an operator ran `contact rekey` at the command line after rotating the database's root key: " +
	"each contact's sealed columns and email digest are written again under the current key"

// rekeyContactsCommand writes every contact of every tenant on the roster again
// under the database module's current root key.
//
// A key rotation leaves two things behind. The sealed columns still hold
// ciphertext under the retired key, which decrypts while the key is listed as
// retired and not after. And the email digest was taken under the retired key,
// so a lookup by email or a check of the per-tenant unique email, both taken
// under the current key, finds none of those contacts: the database module
// derives the digest under the current key alone and leaves recomputing it to
// the writer. Reading each contact and writing its sealed columns and its
// digest again settles both.
//
// Each tenant's contacts are one tenant's work, so each is done inside that
// tenant, entered through the roster, and the statements name no tenant. A
// disabled tenant is entered too, through EnterRegistered: its contacts are
// under the retired key as much as a served tenant's, and rewriting them is
// work that still belongs to it while it is not served. A deleted contact is
// written again as well: its row stays, and a retired key dropped later would
// leave it unreadable. A tenant the roster does not let this process enter is
// answered with the reason, the rest are done, and the command fails naming
// it, since its contacts are still under the retired key.
func rekeyContactsCommand(ctx context.Context, reg *core.Registry) error {
	const command = "contact rekey"
	tools, err := toolsOf(reg)
	if err != nil {
		return err
	}
	all, err := tools.roster.List(ctx)
	if err != nil {
		return err
	}
	out := make([]rekeyedTenant, 0, len(all))
	var failed []error
	for _, t := range all {
		done := rekeyedTenant{TenantID: string(t.ID)}
		n, err := rekeyTenant(ctx, tools, t.ID)
		done.Contacts = n
		if err != nil {
			done.Error = err.Error()
			failed = append(failed, fmt.Errorf("tenant %s: %w", t.ID, err))
		}
		out = append(out, done)
	}
	if err := printResult(out); err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s left contacts under a retired key: %w", command, errors.Join(failed...))
	}
	return nil
}

// rekeyTenant writes one tenant's contacts again in one transaction, inside the
// tenant whatever its status, and states it through the audit seam.
func rekeyTenant(ctx context.Context, tools cliTools, id tenant.ID) (int, error) {
	scoped, err := tools.roster.EnterRegistered(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("entering the tenant: %w", err)
	}
	var n int
	err = tools.db.DB().WithContext(scoped).Transaction(func(tx *gorm.DB) error {
		var contacts []Contact
		if readErr := tx.Unscoped().Order("id").Find(&contacts).Error; readErr != nil {
			return fmt.Errorf("reading the contacts: %w", readErr)
		}
		for i := range contacts {
			c := &contacts[i]
			c.EmailBidx = contactEmailIndex(tools.db, c.Email)
			if writeErr := tx.Unscoped().Model(c).Select("email", "phone", "email_bidx").Updates(c).Error; writeErr != nil {
				return fmt.Errorf("writing the contact %s: %w", c.ID, writeErr)
			}
		}
		n = len(contacts)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := tools.recorder.Record(scoped, audit.Entry{
		Action:  contactRekeyAction,
		Object:  contactTable,
		Outcome: "ok",
		Purpose: rekeyPurpose,
		Detail:  map[string]string{"contacts": strconv.Itoa(n)},
	}); err != nil {
		return n, fmt.Errorf("the contacts are written again and the audit record is not: %w", err)
	}
	return n, nil
}

// init registers the module the contact command group runs, and the group.
func init() {
	registerApplication(application{
		module: contactKeysModule(),
		runsOn: []string{contactGroupWord},
		models: []any{Contact{}},
	})
	registerCommandGroup(contactGroup)
}

// contactKeysModule is what the contact command group runs beside the
// command-line module. It constructs nothing: it is here so that the group's
// registration lists the model its action issues statements on and requires
// what the action takes up — the database, the journal, and the roster, whose
// filter scopes the contacts the action writes and without which the command
// would write them unscoped.
func contactKeysModule() core.Module {
	return core.Module{
		Name: contactKeysModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*audit.Audit)(nil)},
		},
	}
}
