package login

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// complete runs the terminal action of a ready flow: the record is swapped to
// the done tombstone first, so that the winner alone executes the action and a
// login completes at most once.
func (r *routes) complete(c *gin.Context, token string, rec record, raw []byte) {
	if rec.State != stateReady {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
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
		serverError(c)
	}
}

// review re-checks, for a link, a step-up or an enrollment, that the session the
// flow was started on is still valid and still the flow's own, and that a link
// or an enrollment still meets the freshness the account's factors call for. It
// answers the request and returns false when it does not.
func (r *routes) review(c *gin.Context, rec record) bool {
	ctx := c.Request.Context()
	status, err := r.sessions.StatusFresh(ctx, authn.SessionID(rec.Session))
	if err != nil {
		if errors.Is(err, authn.ErrSessionNotFound) || errors.Is(err, authn.ErrSessionRevoked) {
			mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
			return false
		}
		r.fail(c, err)
		return false
	}
	if !status.Active || string(status.Account) != rec.Account || status.ImpersonationID != "" {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return false
	}
	if rec.Purpose == authn.PurposeLink || rec.Purpose == authn.PurposeEnroll {
		req, err := r.accountRequirement(ctx, authn.AccountID(rec.Account))
		if err != nil {
			r.fail(c, err)
			return false
		}
		if !statusIdentity(status).Satisfies(req, r.clock.Now()) {
			mount.StepUpRequired(c, statusIdentity(status), req)
			return false
		}
	}
	return true
}

// accountRequirement is the freshness a change of the account's credentials
// asks for: a fresh local multi-factor authentication when the account has an
// active factor, a fresh first factor otherwise.
func (r *routes) accountRequirement(ctx context.Context, account authn.AccountID) (authn.Requirement, error) {
	factors, err := r.mfa.Factors(ctx, account)
	if err != nil {
		return authn.Requirement{}, fmt.Errorf("%w: %w", authn.ErrUnavailable, err)
	}
	for _, f := range factors {
		if f.Status == authn.FactorActive {
			return authn.FreshLocalMulti(r.settings.recentWindow()), nil
		}
	}
	return authn.FreshFirstFactor(), nil
}

// completeLogin makes the login session or issues the token pair, touches the
// account, revokes the session the request came with, and writes the audit row
// and the event. redirect is true on the callback route of a cookie login, whose
// answer is a 303 to the flow's return target.
func (r *routes) completeLogin(c *gin.Context, rec record, redirect bool) {
	ctx := c.Request.Context()
	if rec.Proof == nil {
		serverError(c)
		return
	}
	account := authn.AccountID(rec.Account)
	first := *rec.Proof
	second := rec.MFA.Passed.toPass()
	result := r.mfa.Assurance(first, second)

	if rec.Merged {
		if !r.mergeCleanup(c, account) {
			return
		}
	}

	methods := append(append([]authn.Method{}, first.Methods...), passMethods(second)...)
	kind := authn.SessionBrowser
	if rec.Delivery == deliveryToken {
		kind = authn.SessionToken
	}
	now := r.clock.Now()
	issued, err := r.sessions.Create(ctx, authn.NewSession{
		Account:    account,
		Kind:       kind,
		Methods:    methods,
		Assurance:  result.Assurance,
		IDPAssured: result.IDPAssured,
		AuthTime:   now,
		Provider:   first.Provider,
		Attributes: idpAttributes(first.External),
		Client:     requestClient(c),
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
		r.sessions.SetCookie(c.Writer, issued)
		if redirect {
			c.Redirect(http.StatusSeeOther, rec.ReturnTo)
		} else {
			writeSession(c, issued)
		}
	}

	// A login that replaced the account's password while proving it leaves the
	// account's other sessions alone no longer: the password changed. The new
	// session is created already, so it is the one left standing.
	if first.PasswordChanged {
		if _, err := r.sessions.RevokeAccount(ctx, account, issued.ID, authn.ReasonSelf); err != nil {
			r.logger.Warn(ModuleName+": the account's other sessions could not be revoked after a password change",
				"cause", err.Error())
		}
	}

	if err := r.admin.Touch(ctx, account, now); err != nil {
		r.logger.Warn(ModuleName+": the account's last-login time could not be recorded",
			"cause", err.Error())
	}
	if oldID, ok, err := r.sessions.Current(c.Request); err == nil && ok {
		if rerr := r.sessions.Revoke(ctx, oldID, authn.ReasonSelf); rerr != nil {
			r.logger.Warn(ModuleName+": the session the login request came with could not be revoked",
				"cause", rerr.Error())
		}
	}
	r.reportLogin(ctx, rec, outcomeSuccess)
	r.auditLogin(c, rec, account, issued.ID, methods, result)
	r.publishLogin(ctx, account, issued.ID, methods, result, rec.Delivery)
}

// deliverToken issues the access token and the refresh token of a token login
// and writes the answer.
func (r *routes) deliverToken(c *gin.Context, account authn.AccountID, issued authn.Issued, methods []authn.Method, result authn.MFAResult, now time.Time) bool {
	ctx := c.Request.Context()
	if r.tokens == nil {
		serverError(c)
		return false
	}
	access, err := r.tokens.Issue(ctx, authn.IssueToken{
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
	refresh, err := r.tokens.NewRefresh(ctx, issued.ID)
	if err != nil {
		r.fail(c, err)
		return false
	}
	writeToken(c, access, refresh, now)
	return true
}

// mergeCleanup is the A10 ordering of an automatic merge: every session the
// account had is revoked, then its password credential is deleted, and only
// then is the session of this login created. The new session is not yet in the
// store when the account's sessions are revoked, so a revoke cannot reach it.
func (r *routes) mergeCleanup(c *gin.Context, account authn.AccountID) bool {
	ctx := c.Request.Context()
	if _, err := r.sessions.RevokeAccount(ctx, account, "", authn.ReasonSelf); err != nil {
		r.fail(c, err)
		return false
	}
	if err := r.passwords.Clear(ctx, account); err != nil {
		r.fail(c, err)
		return false
	}
	return true
}

// completeLink binds the external identity the flow proved to the account.
func (r *routes) completeLink(c *gin.Context, rec record) {
	ctx := c.Request.Context()
	account := authn.AccountID(rec.Account)
	if rec.Proof == nil || rec.Proof.External == nil {
		serverError(c)
		return
	}
	ext := rec.Proof.External
	err := r.admin.LinkExternal(ctx, account, authn.ExternalLink{
		Provider:      ext.Provider,
		Subject:       ext.Subject,
		Email:         ext.Email,
		EmailVerified: ext.EmailVerified,
	})
	if err != nil {
		r.fail(c, err)
		return
	}
	if _, err := r.sessions.RevokeAccount(ctx, account, authn.SessionID(rec.Session), authn.ReasonSelf); err != nil {
		r.logger.Warn(ModuleName+": the account's other sessions could not be revoked after a link",
			"cause", err.Error())
	}
	r.publishBinding(ctx, account, ext.Provider)
	r.auditBindingLink(c, account, ext.Provider)
	c.JSON(http.StatusOK, gin.H{"state": "linked"})
}

// completeStepUp elevates the flow's session to the level the second factor (or
// the first factor, when the account had none) reached.
func (r *routes) completeStepUp(c *gin.Context, rec record) {
	ctx := c.Request.Context()
	account := authn.AccountID(rec.Account)
	status, err := r.sessions.StatusFresh(ctx, authn.SessionID(rec.Session))
	if err != nil {
		r.fail(c, err)
		return
	}
	// The elevation's level and IDPAssured come from MFA.Assurance over a Proof
	// built from the session's method set, which is what the step-up policy was
	// asked with too; the flow's own first-factor proof does not drive it.
	first := authn.Proof{Methods: status.Methods}
	second := rec.MFA.Passed.toPass()
	result := r.mfa.Assurance(first, second)
	methods := unionMethods(status.Methods, first.Methods, passMethods(second))
	now := r.clock.Now()
	issued, err := r.sessions.Elevate(ctx, authn.SessionID(rec.Session), authn.Elevation{
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
			serverError(c)
			return
		}
		access, err := r.tokens.Issue(ctx, authn.IssueToken{
			Account:   account,
			Session:   issued.ID,
			Methods:   methods,
			Assurance: result.Assurance,
			AuthTime:  now,
		})
		if err != nil {
			r.fail(c, err)
			return
		}
		c.JSON(http.StatusOK, tokenReply{
			State:       "elevated",
			TokenType:   "Bearer",
			AccessToken: access.Value,
			ExpiresIn:   max(0, int(access.ExpiresAt.Sub(now).Seconds())),
		})
		return
	}
	if issued.Token != "" {
		r.sessions.SetCookie(c.Writer, issued)
	}
	writeSession(c, issued)
}

// completeEnroll ends a registration: the factor is already active, because the
// multi-factor module makes a pending row active on the factor's last step. The
// account's other sessions are revoked, and the session's level is not changed,
// since a factor registered during this authentication does not strengthen it.
func (r *routes) completeEnroll(c *gin.Context, rec record) {
	ctx := c.Request.Context()
	account := authn.AccountID(rec.Account)
	if _, err := r.sessions.RevokeAccount(ctx, account, authn.SessionID(rec.Session), authn.ReasonSelf); err != nil {
		r.logger.Warn(ModuleName+": the account's other sessions could not be revoked after an enrollment",
			"cause", err.Error())
	}
	c.JSON(http.StatusOK, gin.H{"state": "enrolled"})
}

// passMethods is the second factor's methods, or nil when there was none.
func passMethods(p *authn.Pass) []authn.Method {
	if p == nil {
		return nil
	}
	return p.Methods
}

// unionMethods is the methods of a set of lists, in first-seen order and without
// repeats.
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
