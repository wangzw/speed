package profile

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
)

const (
	actionProfileUpdate = "authn.profile.update"
	outcomeSuccess      = "success"

	detailChange  = "change"
	changeProfile = "profile"
)

func (p *product) auditProfile(ctx context.Context, account authn.AccountID) {
	if p.audit == nil {
		return
	}
	err := p.audit.Record(ctx, audit.Entry{
		Action:  actionProfileUpdate,
		Object:  audit.AccountObject(string(account)),
		Outcome: outcomeSuccess,
		Detail:  map[string]string{detailChange: changeProfile},
	})
	if err != nil {
		p.logger.Error(ModuleName+": the profile-write audit row could not be written",
			slog.String("cause", err.Error()))
	}
}
