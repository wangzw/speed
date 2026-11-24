package selfservice

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
)

const moduleName = "authn.login"

const (
	actionPasswordChange = "authn.password.change"
	actionBindingUnlink  = "authn.binding.unlink"

	outcomeSuccess = "success"
	outcomeDenied  = "denied"
)

type caller struct {
	account authn.AccountID
	session authn.SessionID
}

type callerKey struct{}

func withCaller(ctx context.Context, who caller) context.Context {
	return context.WithValue(ctx, callerKey{}, who)
}

func callerFrom(ctx context.Context) (caller, bool) {
	who, ok := ctx.Value(callerKey{}).(caller)
	return who, ok
}

func (r *routes) authorize(c *call, action string) (caller, bool) {
	if who, carried := callerFrom(c.ctx); carried {
		return who, true
	}
	id, ok := r.identify(c)
	if !ok {
		return caller{}, false
	}
	if !r.fresh(c, id, action) {
		return caller{}, false
	}
	return caller{account: id.Account(), session: id.Session()}, true
}

func (r *routes) identity(c *call) (caller, bool) {
	if who, carried := callerFrom(c.ctx); carried {
		return who, true
	}
	id, ok := r.identify(c)
	if !ok {
		return caller{}, false
	}
	return caller{account: id.Account(), session: id.Session()}, true
}

func (r *routes) freshIdentity(c *call, action string) bool {
	id, ok := authn.FromContext(c.ctx)
	if !ok {
		c.unauthorized("")
		return false
	}
	return r.fresh(c, id, action)
}

func (r *routes) contactProofNeedsFreshness(ctx context.Context, account authn.AccountID,
	kind authn.IdentifierKind, value string,
) (bool, error) {
	held, err := r.deps.Accounts.Identifiers(ctx, account)
	if err != nil {
		return false, err
	}
	proved := func(row authn.Identifier) bool {
		return (authn.Verification{Via: row.VerifiedVia}).Proves()
	}
	switch kind {
	case authn.IdentifierEmail:
		hasDefault := false
		hasOther := false
		for _, row := range held {
			if row.Kind == authn.IdentifierEmail && row.Value == value && proved(row) {
				return false, nil
			}
			if row.Kind == authn.IdentifierEmail && row.Value != value {
				hasOther = true
			}
			if row.DefaultContact {
				hasDefault = true
			}
		}
		return !hasDefault || !hasOther, nil
	case authn.IdentifierPhone:
		factors, err := r.deps.MFA.Factors(ctx, account)
		if err != nil {
			return false, err
		}
		active := false
		for _, f := range factors {
			if f.Kind == authn.FactorSMS && f.Status == authn.FactorActive {
				active = true
				break
			}
		}
		if !active {
			return false, nil
		}
		for _, row := range held {
			if row.Kind == authn.IdentifierPhone && row.Value == value && proved(row) {
				return false, nil
			}
		}
		return true, nil
	default:
		return false, nil
	}
}

func (r *routes) identify(c *call) (authn.Identity, bool) {
	id, ok := authn.FromContext(c.ctx)
	if !ok || id.Account() == "" {
		c.unauthorized("")
		return authn.Identity{}, false
	}
	if id.Session() == "" {
		c.unauthorized("")
		return authn.Identity{}, false
	}
	return id, true
}

func (r *routes) fresh(c *call, id authn.Identity, action string) bool {
	req, err := r.requirement(c.ctx, id.Account())
	if err != nil {
		r.fail(c, err)
		return false
	}
	if !id.Satisfies(req, r.deps.Clock.Now()) {
		if action != "" {
			r.record(c.ctx, action, outcomeDenied, id.Account(), nil)
		}
		c.stepUp(id, req)
		return false
	}
	return true
}

func (r *routes) requirement(ctx context.Context, account authn.AccountID) (authn.Requirement, error) {
	factors, err := r.deps.MFA.Factors(ctx, account)
	if err != nil {
		return authn.Requirement{}, err
	}
	for _, f := range factors {
		if f.Status == authn.FactorActive {
			return authn.FreshLocalMulti(r.durations.get(ctx, keyRecentWindow, defaultRecentWindow, minRecentWindow, maxRecentWindow)), nil
		}
	}
	return authn.FreshFirstFactor(), nil
}

func (r *routes) settle(c *call, who caller, reason string) bool {
	ctx := c.ctx
	issued, rotateErr := r.deps.Sessions.Rotate(ctx, who.session)
	_, revokeErr := r.deps.Sessions.RevokeAccount(ctx, who.account, who.session, reason)
	if rotateErr == nil && issued.Token != "" {
		r.deps.Sessions.SetCookie(c.w, issued)
	}
	if err := errors.Join(rotateErr, revokeErr); err != nil {
		r.fail(c, err)
		return false
	}
	return true
}

func (r *routes) publish(ctx context.Context, eventType string, payload any) {
	if r.deps.Bus == nil {
		return
	}
	if err := r.deps.Bus.Publish(ctx, event.Event{Type: eventType, Payload: payload}); err != nil {
		log.FromContext(ctx).Warn(moduleName+": an event could not be published",
			slog.String("event_type", eventType), slog.String("cause", err.Error()))
	}
}

func (r *routes) record(ctx context.Context, action, outcome string, account authn.AccountID, detail map[string]string) {
	if r.deps.Audit == nil {
		return
	}
	err := r.deps.Audit.Record(ctx, audit.Entry{
		Action:  action,
		Object:  audit.AccountObject(string(account)),
		Outcome: outcome,
		Detail:  detail,
	})
	if err != nil {
		logError(ctx, "an audit row of an account self-service change could not be written", err)
	}
}

func (r *routes) engaged(ctx context.Context, scope authn.ThrottleScope) {
	r.publish(ctx, authn.EventThrottleEngaged, authn.ThrottleEngaged{Scope: string(scope)})
	if r.deps.ThrottleEngaged != nil {
		r.deps.ThrottleEngaged(ctx, scope)
	}
}

func logError(ctx context.Context, msg string, err error) {
	log.FromContext(ctx).Error(moduleName+": "+msg, slog.String("cause", err.Error()))
}

func seconds(d time.Duration) int64 {
	return int64((d + time.Second - 1) / time.Second)
}
