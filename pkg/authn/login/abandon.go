package login

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
)

// abandonOwner is the collaborator that holds a flow's own private state and
// offers the optional abandon callback, or nil when nothing does.
//
// An enrollment's state belongs to the multi-factor module, which wrote the
// pending row and kept its identifier in the flow's private bytes; the record
// names no method then. A flow at its first factor belongs to the method it
// names. A second-factor flow's private state belongs to a factor, and a flow
// that has settled has spent its state, so neither has an owner here: handing
// one collaborator another's bytes would let it misread them.
//
// No shipped first-factor method keeps state of its own, so the
// stateFirst/stateProcessing/stateReprove branch has no production implementer
// yet. It is kept as the seam for one that does: a redirect method whose
// upstream transaction is still open while its flow is at its first
// factor or swapped to processing. Such a method implements MethodAbandoner
// and gets the flow's private bytes handed over before the record is removed,
// instead of waiting for the upstream transaction to lapse.
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

// releaseFlow hands the flow's private state to the collaborator that owns it,
// before the record is removed. Abandoning is idempotent, so a callback that
// fails -- or panics -- is logged and the flow is removed all the same: a
// record left behind would keep a binding the client has already given up on.
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
