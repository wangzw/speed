package account

import (
	"context"
	"log/slog"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
)

// noopProvisioning is the seam a product built directly holds: a case that
// constructs the product to drive its own tables does not need an assembly, and
// the module's own drive replaces this with the provider it required.
type noopProvisioning struct{}

// The product is the transaction-aware membership writer the descriptor
// delivers.
var _ provisioning.MembershipWriter = (*product)(nil)

func (noopProvisioning) ProvisionAccount(context.Context, *gorm.DB, provisioning.AccountFacts) error {
	return nil
}

func (noopProvisioning) ProvisionAccountDeletion(context.Context, *gorm.DB, provisioning.AccountFacts) error {
	return nil
}

// accountFactsFor is what the seam is told of one account row.
func accountFactsFor(row accountRow, username string) provisioning.AccountFacts {
	return provisioning.AccountFacts{
		ID:          row.ID,
		Kind:        row.Kind,
		Status:      statusOf(row.Status).String(),
		Username:    username,
		DisplayName: row.DisplayName,
	}
}

// usernameOf reads the account's username identifier through tx. A service
// account has none, and an account written around this module's surface may
// have none either.
func usernameOf(tx *gorm.DB, id string) (string, error) {
	var rows []identifierRow
	err := tx.Where("account_id = ? AND kind = ?", id, string(authn.IdentifierUsername)).
		Limit(1).Find(&rows).Error
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].Value, nil
}

// deletionFacts reads what the deletion seam needs before eraseAccount takes the
// identifier and the display name away. It runs inside the transaction that is
// about to erase them.
func deletionFacts(tx *gorm.DB, row accountRow) (provisioning.AccountFacts, error) {
	username, err := usernameOf(tx, row.ID)
	if err != nil {
		return provisioning.AccountFacts{}, err
	}
	return accountFactsFor(row, username), nil
}

// runAfterCommit runs the work a provisioning implementation queued through the
// context's AfterCommit collector, once the account's transaction has committed.
// The work is this module's bookkeeping for a write already in the table, so a
// failure is a warning and never a failure of the call: reporting the write as
// failed would tell the caller the opposite of what happened.
func (p *product) runAfterCommit(ctx context.Context, after *provisioning.AfterCommit) {
	if after == nil {
		return
	}
	if err := after.Run(ctx); err != nil {
		p.logger.Warn(ModuleName+": the work a provisioning implementation left for after the commit failed; "+
			"this process may serve a snapshot older than the write",
			slog.String("cause", describe(err)))
	}
}
