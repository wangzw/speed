package invite

import (
	"context"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// handleEligibility answers GET /api/v1/auth/invitations/:token/eligibility: it
// tells the signed-in caller which state a token points to and whether the
// caller holds the invited address as a proven identifier, so a client can show
// the proof step as soon as the page opens instead of on the first attempt to
// accept. The route is guarded: the answer is about an account, so a request
// with no usable credential is 401 before the handler runs. The answer never
// carries the invited address, the inviter or the organization.
//
// The probe budget is charged exactly as the public read charges it, by token
// and by client and before the token is looked up, so this authenticated read
// cannot be used to walk tokens faster than the public one allows.
func (p *product) handleEligibility(ctx context.Context, in eligibilityInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	caller, err := p.caller(ctx)
	if err != nil {
		return nil, err
	}
	if probeErr := p.chargeProbe(ctx, in.Token, meta.ClientIP, p.at()); probeErr != nil {
		return p.acceptAnswerReply("probing an invitation", probeErr)
	}
	inv, state, err := p.viewToken(ctx, in.Token)
	if err != nil {
		return p.acceptAnswerReply("reading an invitation", err)
	}
	proved, err := p.provesInvitedEmail(ctx, caller.Account(), inv.Email)
	if err != nil {
		return p.acceptAnswerReply("reading the account's identifiers", err)
	}
	return speedhttp.JSONReply(http.StatusOK,
		AuthnInviteEligibility{State: state, EmailProved: proved}), nil
}
