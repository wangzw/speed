package account

import (
	"context"
	"log/slog"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
)

type noopProvisioning struct{}

var _ provisioning.MembershipWriter = (*product)(nil)

func (noopProvisioning) ProvisionAccount(context.Context, *gorm.DB, provisioning.AccountFacts) error {
	return nil
}

func (noopProvisioning) ProvisionAccountDeletion(context.Context, *gorm.DB, provisioning.AccountFacts) error {
	return nil
}

func accountFactsFor(row accountRow, username string) provisioning.AccountFacts {
	return provisioning.AccountFacts{
		ID:          row.ID,
		Kind:        row.Kind,
		Status:      statusOf(row.Status).String(),
		Username:    username,
		DisplayName: row.DisplayName,
	}
}

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
// identifier and the display name away.
func deletionFacts(tx *gorm.DB, row accountRow) (provisioning.AccountFacts, error) {
	username, err := usernameOf(tx, row.ID)
	if err != nil {
		return provisioning.AccountFacts{}, err
	}
	return accountFactsFor(row, username), nil
}

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
