package invite

import (
	"context"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

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
