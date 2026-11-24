package login

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// complete swaps the record to the done tombstone first, so the terminal action runs at most once.
func (r *routes) complete(c *call, token string, rec record, raw []byte) {
	if rec.State != stateReady {
		c.unauthorized(mount.CodeFlowInvalid)
		return
	}
	if rec.Purpose != authn.PurposeLogin && !r.review(c, rec) {
		return
	}
	done := rec
	done.State = stateDone
	if !r.swap(c, token, raw, done) {
		return
	}
	switch rec.Purpose {
	case authn.PurposeLogin:
		r.completeLogin(c, rec, false)
	case authn.PurposeLink:
		r.completeLink(c, rec)
	case authn.PurposeStepUp:
		r.completeStepUp(c, rec)
	case authn.PurposeEnroll:
		r.completeEnroll(c, rec)
	default:
		c.serverError()
	}
}

func (r *routes) review(c *call, rec record) bool {
	status, err := r.sessions.StatusFresh(c.ctx, authn.SessionID(rec.Session))
	if err != nil {
		if errors.Is(err, authn.ErrSessionNotFound) || errors.Is(err, authn.ErrSessionRevoked) {
			c.unauthorized(mount.CodeFlowInvalid)
			return false
		}
		r.fail(c, err)
		return false
	}
	if !status.Active || !flowOwnsSession(rec, status) {
		c.unauthorized(mount.CodeFlowInvalid)
		return false
	}
	if rec.Impersonation != "" && !r.recordHolds(c, rec) {
		return false
	}
	if rec.Purpose == authn.PurposeLink || rec.Purpose == authn.PurposeEnroll {
		req := r.requirementFor(rec.RequireMulti)
		if !statusIdentity(status).Satisfies(req, r.clock.Now()) {
			c.stepUp(statusIdentity(status), req)
			return false
		}
	}
	return true
}

// flowOwnsSession accepts the flow's own account, or during impersonation the operator's account and the same record.
func flowOwnsSession(rec record, st authn.SessionStatus) bool {
	if rec.Impersonation != "" {
		return st.Account == authn.AccountID(rec.Actor) && st.ImpersonationID == rec.Impersonation
	}
	return st.Account == authn.AccountID(rec.Account)
}

func (r *routes) recordHolds(c *call, rec record) bool {
	if r.impersonation == nil {
		c.unauthorized(mount.CodeFlowInvalid)
		return false
	}
	grant, active, err := r.impersonation.Active(c.ctx, rec.Impersonation)
	if err != nil {
		r.fail(c, err)
		return false
	}
	if !active || grant.Actor != authn.AccountID(rec.Actor) || grant.Target != authn.AccountID(rec.Account) {
		c.unauthorized(mount.CodeFlowInvalid)
		return false
	}
	return true
}

func (r *routes) requirementFor(requireMulti bool) authn.Requirement {
	if requireMulti {
		return authn.FreshLocalMulti(r.settings.recentWindow())
	}
	return authn.FreshFirstFactor()
}

func (r *routes) accountHasActiveFactor(ctx context.Context, account authn.AccountID) (bool, error) {
	factors, err := r.mfa.Factors(ctx, account)
	if err != nil {
		return false, fmt.Errorf("%w: %w", authn.ErrUnavailable, err)
	}
	for _, f := range factors {
		if f.Status == authn.FactorActive {
			return true, nil
		}
	}
	return false, nil
}

func (r *routes) completeLogin(c *call, rec record, redirect bool) {
	if rec.Proof == nil {
		c.serverError()
		return
	}
	account := authn.AccountID(rec.Account)
	first := *rec.Proof
	second := rec.MFA.Passed.toPass()
	result := r.mfa.Assurance(first, second)

	methods := append(append([]authn.Method{}, first.Methods...), passMethods(second)...)
	kind := authn.SessionBrowser
	if rec.Delivery == deliveryToken {
		kind = authn.SessionToken
	}
	now := r.clock.Now()
	issued, err := r.sessions.Create(c.ctx, authn.NewSession{
		Account:    account,
		Kind:       kind,
		Methods:    methods,
		Assurance:  result.Assurance,
		IDPAssured: result.IDPAssured,
		AuthTime:   now,
		Provider:   first.Provider,
		Attributes: idpAttributes(first.External),
		Client:     c.clientInfo(),
	})
	if err != nil {
		r.fail(c, err)
		return
	}

	if rec.Delivery == deliveryToken {
		if !r.deliverToken(c, account, issued, methods, result, now) {
			return
		}
	} else {
		r.sessions.SetCookie(c.w, issued)
		if redirect {
			c.redirect(http.StatusSeeOther, r.returnTarget(rec))
		} else {
			writeSession(c, issued)
		}
	}

	// The new session is created first, so it is the one left standing.
	if first.PasswordChanged {
		if _, err := r.sessions.RevokeAccount(c.ctx, account, issued.ID, authn.ReasonSelf); err != nil {
			r.logger.Warn(ModuleName+": the account's other sessions could not be revoked after a password change",
				"cause", err.Error())
		}
	}

	if err := r.admin.Touch(c.ctx, account, now); err != nil {
		r.logger.Warn(ModuleName+": the account's last-login time could not be recorded",
			"cause", err.Error())
	}
	if c.req != nil {
		if oldID, ok, err := r.sessions.Current(c.req); err == nil && ok {
			if rerr := r.sessions.Revoke(c.ctx, oldID, authn.ReasonSelf); rerr != nil {
				r.logger.Warn(ModuleName+": the session the login request came with could not be revoked",
					"cause", rerr.Error())
			}
		}
	}
	r.reportLogin(c.ctx, rec, outcomeSuccess)
	r.auditLogin(c, rec, account, issued.ID, methods, result)
	r.publishLogin(c.ctx, account, issued.ID, methods, result, rec.Delivery)
}

func (r *routes) deliverToken(c *call, account authn.AccountID, issued authn.Issued, methods []authn.Method, result authn.MFAResult, now time.Time) bool {
	if r.tokens == nil {
		c.serverError()
		return false
	}
	access, err := r.tokens.Issue(c.ctx, authn.IssueToken{
		Account:   account,
		Session:   issued.ID,
		Methods:   methods,
		Assurance: result.Assurance,
		AuthTime:  now,
	})
	if err != nil {
		r.fail(c, err)
		return false
	}
	refresh, err := r.tokens.NewRefresh(c.ctx, issued.ID)
	if err != nil {
		r.fail(c, err)
		return false
	}
	writeToken(c, access, refresh, now)
	return true
}

func (r *routes) completeLink(c *call, rec record) {
	account := authn.AccountID(rec.Account)
	if rec.Proof == nil || rec.Proof.External == nil {
		c.serverError()
		return
	}
	ext := rec.Proof.External
	err := r.admin.LinkExternal(c.ctx, account, authn.ExternalLink{
		Provider:      ext.Provider,
		Subject:       ext.Subject,
		Email:         ext.Email,
		EmailVerified: ext.EmailVerified,
	})
	if err != nil {
		r.fail(c, err)
		return
	}
	if _, err := r.sessions.RevokeAccount(c.ctx, account, authn.SessionID(rec.Session), authn.ReasonSelf); err != nil {
		r.logger.Warn(ModuleName+": the account's other sessions could not be revoked after a link",
			"cause", err.Error())
	}
	r.publishBinding(c.ctx, account, ext.Provider)
	r.auditBindingLink(c, rec, ext.Provider)
	writeAction(c, stateLinked)
}

func (r *routes) completeStepUp(c *call, rec record) {
	account := authn.AccountID(rec.Account)
	status, err := r.sessions.StatusFresh(c.ctx, authn.SessionID(rec.Session))
	if err != nil {
		r.fail(c, err)
		return
	}
	first := authn.Proof{Methods: status.Methods}
	second := rec.MFA.Passed.toPass()
	result := r.mfa.Assurance(first, second)
	methods := unionMethods(status.Methods, first.Methods, passMethods(second))
	now := r.clock.Now()
	issued, err := r.sessions.Elevate(c.ctx, authn.SessionID(rec.Session), authn.Elevation{
		Methods:    methods,
		Assurance:  result.Assurance,
		IDPAssured: result.IDPAssured,
		AuthTime:   now,
	})
	if err != nil {
		r.fail(c, err)
		return
	}
	if rec.Delivery == deliveryToken {
		if r.tokens == nil {
			c.serverError()
			return
		}
		issue := authn.IssueToken{
			Account:   account,
			Session:   issued.ID,
			Methods:   methods,
			Assurance: result.Assurance,
			AuthTime:  now,
		}
		// The token must stay an impersonation token: its actor, impersonation and scope come from the record, never the target's own credential.
		if rec.Impersonation != "" {
			issue.Actor = authn.AccountID(rec.Actor)
			issue.ImpersonationID = rec.Impersonation
			issue.Scope = rec.Scope
		}
		access, err := r.tokens.Issue(c.ctx, issue)
		if err != nil {
			r.fail(c, err)
			return
		}
		c.wire(tokenReply{
			State:       "elevated",
			TokenType:   "Bearer",
			AccessToken: access.Value,
			ExpiresIn:   max(0, int(access.ExpiresAt.Sub(now).Seconds())),
		})
		return
	}
	if issued.Token != "" {
		r.sessions.SetCookie(c.w, issued)
	}
	writeSession(c, issued)
}

func (r *routes) completeEnroll(c *call, rec record) {
	account := authn.AccountID(rec.Account)
	if _, err := r.sessions.RevokeAccount(c.ctx, account, authn.SessionID(rec.Session), authn.ReasonSelf); err != nil {
		r.logger.Warn(ModuleName+": the account's other sessions could not be revoked after an enrollment",
			"cause", err.Error())
	}
	writeAction(c, stateEnrolled)
}

func passMethods(p *authn.Pass) []authn.Method {
	if p == nil {
		return nil
	}
	return p.Methods
}

func unionMethods(lists ...[]authn.Method) []authn.Method {
	var out []authn.Method
	seen := map[authn.Method]bool{}
	for _, list := range lists {
		for _, m := range list {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}
