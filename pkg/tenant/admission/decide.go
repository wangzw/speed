package admission

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// decide is the judgement over one request. The rows are tried in this order and
// the first that applies ends it; the order is the design's, and several of the
// properties the layer guarantees are properties of it:
//
//   - every refusal of a request that is not a member is the same 404, because
//     the strength rows come after the membership row, so a caller that is not
//     a member never meets a prompt only a member would meet;
//   - an impersonation and a service account still go through the membership
//     row, which is what makes a removed member's impersonation stop working;
//   - an account let in by the permission model leaves the judgement there, and
//     the tenant's strength requirements, which bind the tenant's members, are
//     not asked of it.
func (l *layer) decide(ctx context.Context) decision {
	id, identified := authn.FromContext(ctx)
	owner, inTenant := tenant.FromContext(ctx)

	// No tenant: the request is not about one. Rows that follow need a tenant
	// to compare against, so an impersonation, which exists to be confined to
	// one, is the single thing refused here.
	if !inTenant {
		if _, scoped := id.Scope(); identified && scoped {
			return decision{outcome: scopeMismatch}
		}
		return decision{outcome: admitted}
	}

	if !identified {
		switch authn.AccessFromContext(ctx) {
		case authn.AccessOpen, authn.AccessOptional:
			return decision{outcome: admitted}
		}
		// Required, or a level the gateway never recorded: there is no
		// gateway in front of this endpoint, or it does not guard it. That is
		// a defect of the assembly and the request is refused for it.
		return decision{outcome: noIdentity}
	}

	scope, scoped := id.Scope()
	if scoped && scope != string(owner) {
		return decision{outcome: scopeMismatch}
	}

	j := &judging{layer: l, ctx: ctx, id: id, tenant: owner, ttl: l.checkTTL()}
	return j.judge(scoped)
}

// judging is one request past the rows that need no reads. It remembers what it
// read, so that the two rows that both need the provider's tenant ask once.
type judging struct {
	*layer
	ctx    context.Context
	id     authn.Identity
	tenant tenant.ID
	ttl    time.Duration

	providerRead bool
	providerOwns bool
	providerErr  error
}

func (j *judging) judge(scoped bool) decision {
	member, err := j.isMember()
	if err != nil {
		return unavailableFor(err)
	}
	if !member {
		return j.outsider(scoped)
	}

	// An impersonation was authenticated strongly by the account that began
	// it, and a service account can neither step up nor sign in through an
	// identity provider; asking either for what it cannot give would end every
	// integration of a tenant the moment it switched a requirement on.
	if j.id.Impersonating() || slices.Contains(j.id.Methods(), authn.MethodClientSecret) {
		return decision{outcome: admitted}
	}
	return j.requirements()
}

// outsider decides a request of an account that is not a member: the permission
// model is asked when there is one and the identity is not an impersonation,
// and the answer is a refusal unless it says otherwise. An impersonation is
// confined to its tenant and has no way in beside membership; only a caller
// acting as itself may be let in.
func (j *judging) outsider(scoped bool) decision {
	if j.decider == nil || scoped {
		return decision{outcome: notMember}
	}
	allowed, err := j.mayEnter()
	switch {
	case err != nil:
		return unavailableFor(err)
	case allowed:
		return decision{outcome: admittedByPolicy}
	}
	return decision{outcome: notMember}
}

// requirements holds a member to what its tenant asks of how its members
// authenticated: multi-factor first, then a sign-in through one of the tenant's
// own identity providers.
func (j *judging) requirements() decision {
	mfa, err := j.flag(requireMFAKey)
	if err != nil {
		return unavailableFor(err)
	}
	if mfa {
		var short bool
		if short, err = j.belowMultiFactor(); err != nil {
			return unavailableFor(err)
		}
		if short {
			return j.stepUp(stepUpMFA)
		}
	}

	sso, err := j.flag(requireSSOKey)
	if err != nil {
		return unavailableFor(err)
	}
	if !sso {
		return decision{outcome: admitted}
	}
	// The tenants the platform exempts are not held to this row. Its question
	// is whether the session signed in through one of THIS tenant's own
	// identity providers, and a tenant that has none can never answer yes: the
	// platform's own tenant hosts the administration surface and its
	// administrators, so holding it to the row would refuse every request
	// there and close the console. The membership rows above still bind.
	if slices.Contains(j.ssoExemptTenants, string(j.tenant)) {
		return decision{outcome: admitted}
	}
	owned, err := j.providerOwnsSession()
	if err != nil {
		return unavailableFor(err)
	}
	if owned {
		return decision{outcome: admitted}
	}
	exempt, err := j.ssoExempt()
	if err != nil {
		return unavailableFor(err)
	}
	if !exempt {
		return j.stepUp(stepUpSSO)
	}
	// The emergency pass lifts the sign-in requirement and keeps the factor
	// one: a tenant locked out of its provider is not a tenant whose members
	// may then come in with a password alone.
	short, err := j.belowMultiFactor()
	if err != nil {
		return unavailableFor(err)
	}
	if short {
		return j.stepUp(stepUpMFA)
	}
	return decision{outcome: admitted}
}

func (j *judging) stepUp(o outcome) decision {
	return decision{outcome: o, challenge: j.id.Challenge()}
}

func unavailableFor(err error) decision {
	return decision{outcome: unavailable, cause: err}
}

// isMember reads whether the effective account belongs to the tenant, from the
// memory when it holds an answer. For an impersonation the account is the
// impersonated one, whose membership was checked when the impersonation began
// and is checked again here, so that removing the member ends the impersonation.
func (j *judging) isMember() (bool, error) {
	key := cacheKey{account: j.id.Account(), tenant: j.tenant}
	if v, found := j.cache.get(kindMember, key, j.ttl); found {
		return v, nil
	}
	epoch := j.cache.begin()
	v, err := j.memberships.IsMember(j.ctx, key.account, string(j.tenant))
	if err != nil {
		return false, fmt.Errorf("reading the membership: %w", err)
	}
	j.cache.put(kindMember, key, v, j.ttl, epoch)
	return v, nil
}

// mayEnter asks the permission model whether a non-member may enter the tenant.
func (j *judging) mayEnter() (bool, error) {
	key := cacheKey{account: j.id.Account(), tenant: j.tenant}
	if v, found := j.cache.get(kindAdmit, key, j.ttl); found {
		return v, nil
	}
	epoch := j.cache.begin()
	allowed, err := askDecider(j.ctx, j.decider, authn.Decision{
		Action: authn.ActionTenantAccess,
		Actor:  j.id,
		Tenant: string(j.tenant),
	})
	if err != nil {
		return false, fmt.Errorf("asking the permission model: %w", err)
	}
	j.cache.put(kindAdmit, key, allowed, j.ttl, epoch)
	return allowed, nil
}

// askDecider asks a Decider, taking a panic in it for an error, which is the
// contract the question is put under: an action is not carried out on a
// Decider that failed, whichever way it failed.
func askDecider(ctx context.Context, d authn.Decider, q authn.Decision) (allowed bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			allowed, err = false, fmt.Errorf("the decider panicked: %v", r)
		}
	}()
	return d.Decide(ctx, q)
}

// ssoExempt reads whether the member holds the emergency pass. It is read only
// for a request that failed the sign-in requirement.
func (j *judging) ssoExempt() (bool, error) {
	key := cacheKey{account: j.id.Account(), tenant: j.tenant}
	if v, found := j.cache.get(kindExempt, key, j.ttl); found {
		return v, nil
	}
	epoch := j.cache.begin()
	m, present, err := j.memberships.Membership(j.ctx, string(j.tenant), key.account)
	if err != nil {
		return false, fmt.Errorf("reading the membership row: %w", err)
	}
	// A membership that is not there, or is suspended, reads as no pass.
	exempt := present && m.Active && m.SSOExempt
	j.cache.put(kindExempt, key, exempt, j.ttl, epoch)
	return exempt, nil
}

// belowMultiFactor reports whether the effective assurance level falls short of
// multi-factor.
//
// An identity whose multi-factor level is an identity provider's declaration
// keeps it only in the tenant that provider belongs to: the provider's
// administrator is trusted for the tenant's own members, and a tenant that
// requires multi-factor has not agreed to take another tenant's administrator's
// word. Where the registry of providers is absent nobody's declaration can be
// placed, so none counts.
func (j *judging) belowMultiFactor() (bool, error) {
	level := j.id.Assurance()
	if level >= authn.AssuranceMulti && j.id.AssuranceFromIDP() {
		owned, err := j.providerOwnsSession()
		if err != nil {
			return false, err
		}
		if !owned {
			level = authn.AssuranceSingle
		}
	}
	return level < authn.AssuranceMulti, nil
}

// providerOwnsSession reports whether the identity provider the session was
// signed in through belongs to this tenant. No provider, no registry to ask and
// a provider the registry does not know all read as no.
//
// The registry answers an unknown provider with an empty tenant and no error; an
// error is a failure to read, which refuses the request rather than reading as
// an answer.
func (j *judging) providerOwnsSession() (bool, error) {
	if j.providerRead {
		return j.providerOwns, j.providerErr
	}
	j.providerRead = true
	provider := j.id.Provider()
	if provider == "" || j.providers == nil {
		return false, nil
	}
	owner, err := j.providers.Tenant(j.ctx, provider)
	if err != nil {
		j.providerErr = fmt.Errorf("reading the tenant of the identity provider: %w", err)
		return false, j.providerErr
	}
	j.providerOwns = owner == string(j.tenant)
	return j.providerOwns, nil
}

// errSettingLayer is the failure of a tenant the settings cannot name a layer
// for.
var errSettingLayer = errors.New("the tenant identifier is outside the settings' identifier domain")

// flag reads one of the two switches for this tenant. The settings are an
// optional dependency: without them each switch reads as the default declared,
// which is off, and the requirement cannot be turned on in that assembly. A
// switch that cannot be read is an error, never the default, because the default
// is the permissive value.
func (j *judging) flag(key string) (bool, error) {
	if j.store == nil {
		return false, nil
	}
	if !nameableLayer(string(j.tenant)) {
		return false, fmt.Errorf("reading %s: %w", key, errSettingLayer)
	}
	v, err := settings.Get[bool](j.store, settings.ForTenant(string(j.tenant)), key)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", key, err)
	}
	return v, nil
}

// nameableLayer reports whether the settings can name a layer for the tenant: the
// settings refuse an empty identifier and one with a blank or a control character
// by panicking, and a request must not take that panic.
func nameableLayer(id string) bool {
	return id != "" && strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

// checkTTL reads how long an answer may be remembered. It is an adjustment and
// not a requirement: where it cannot be read, or holds a value outside its
// domain, the declared default is used and the reading is reported.
func (l *layer) checkTTL() time.Duration {
	if l.store == nil {
		return defaults.CheckTTL
	}
	ttl, err := settings.Get[time.Duration](l.store, settings.Global(), checkTTLKey)
	switch {
	case err != nil:
		l.reportTTL("the setting cannot be read, so its declared default is used", err.Error())
		return defaults.CheckTTL
	case !checkTTLInDomain(ttl):
		l.reportTTL("the setting holds a value outside its domain, so its declared default is used",
			ttl.String())
		return defaults.CheckTTL
	}
	l.reportTTL("", "")
	return ttl
}

// reportTTL writes the Error line for a lifetime read as its default. The
// setting is read on every request, so the same fault is written once and
// again only when it changes or has been cleared in between; an empty problem
// clears it.
func (l *layer) reportTTL(problem, detail string) {
	l.mu.Lock()
	changed := detail != l.reportedTTL
	l.reportedTTL = detail
	l.mu.Unlock()
	if problem != "" && changed {
		l.logger.Error(diagnosticPrefix+ModuleName+": "+problem, "setting", checkTTLKey)
	}
}
