package profile

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
)

// The audit row a profile write leaves. The action names the surface that
// changed the caller's own profile, so that a reader tells a self-service change
// apart from an administrator's write of the same fields; the tier is
// best-effort, and an assembly with no audit capability simply writes none.
const (
	actionProfileUpdate = "authn.profile.update"
	outcomeSuccess      = "success"

	// The detail keys are the capability's closed set. change names what kind of
	// profile change the row records; no field value a client sent enters a row.
	detailChange  = "change"
	changeProfile = "profile"
)

// auditProfile writes the best-effort audit row of a profile write. It is nil
// when the assembly has no audit capability, and a write that fails is an Error
// line that changes nothing about the answer.
func (p *product) auditProfile(ctx context.Context, account authn.AccountID) {
	if p.audit == nil {
		return
	}
	err := p.audit.Record(ctx, audit.Entry{
		Action:  actionProfileUpdate,
		Object:  string(account),
		Outcome: outcomeSuccess,
		Detail:  map[string]string{detailChange: changeProfile},
	})
	if err != nil {
		p.logger.Error(ModuleName+": the profile-write audit row could not be written",
			slog.String("cause", err.Error()))
	}
}
