package login

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
)

// the first-factor branch of abandonOwner is the reserved seam for a redirect method, not dead routing.
func (r *routes) abandonOwner(rec record) authn.MethodAbandoner {
	switch rec.State {
	case stateEnroll:
		if r.mfa == nil {
			return nil
		}
		owner, _ := r.mfa.(authn.MethodAbandoner)
		return owner
	case stateFirst, stateProcessing, stateReprove:
		method, ok := r.method(rec.Method)
		if !ok {
			return nil
		}
		owner, _ := method.(authn.MethodAbandoner)
		return owner
	default:
		return nil
	}
}

func (r *routes) releaseFlow(ctx context.Context, rec record, token string) {
	owner := r.abandonOwner(rec)
	if owner == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, r.settings.callBudget())
	defer cancel()
	f := r.flowState(rec, token)
	defer func() {
		if got := recover(); got != nil {
			r.logger.Error(ModuleName+": a flow's abandon callback panicked",
				slog.String("method", rec.Method), slog.String("purpose", rec.Purpose),
				slog.String("cause", fmt.Sprint(got)))
		}
	}()
	if err := owner.Abandon(cctx, f); err != nil {
		r.logger.Warn(ModuleName+": a flow's own state could not be released",
			slog.String("method", rec.Method), slog.String("purpose", rec.Purpose),
			slog.String("cause", err.Error()))
	}
}
