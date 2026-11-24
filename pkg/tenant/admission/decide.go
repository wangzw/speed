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

// decide judges one request: the rows are tried in a fixed order and the first that
// applies ends it. The strength rows must stay after the membership row, so that a
// non-member never meets a prompt only a member would meet.
func (l *layer) decide(ctx context.Context) decision {
	id, identified := authn.FromContext(ctx)
	owner, inTenant := tenant.FromContext(ctx)

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
		return decision{outcome: noIdentity}
	}

	scope, scoped := id.Scope()
	if scoped && scope != string(owner) {
		return decision{outcome: scopeMismatch}
	}

	j := &judging{layer: l, ctx: ctx, id: id, tenant: owner, ttl: l.checkTTL()}
	return j.judge(scoped)
}

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

	if j.id.Impersonating() || slices.Contains(j.id.Methods(), authn.MethodClientSecret) {
		return decision{outcome: admitted}
	}
	return j.requirements()
}

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

func (j *judging) requirements() decision {
	mfa, err := j.mfaBinds()
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

	sso, err := j.ssoBinds()
	if err != nil {
		return unavailableFor(err)
	}
	if !sso {
		return decision{outcome: admitted}
	}
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

// isMember reads the membership of the effective account, which for an impersonation is the impersonated one.
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

// askDecider takes a panic in the Decider for an error, so an action is never carried out on a Decider that failed.
func askDecider(ctx context.Context, d authn.Decider, q authn.Decision) (allowed bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			allowed, err = false, fmt.Errorf("the decider panicked: %v", r)
		}
	}()
	return d.Decide(ctx, q)
}

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
	exempt := present && m.Active && m.SSOExempt
	j.cache.put(kindExempt, key, exempt, j.ttl, epoch)
	return exempt, nil
}

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

var errSettingLayer = errors.New("the tenant identifier is outside the settings' identifier domain")

func (j *judging) mfaBinds() (bool, error) {
	return j.binds(requireMFAKey)
}

func (j *judging) ssoBinds() (bool, error) {
	return j.binds(requireSSOKey)
}

// binds reports whether the requirement named by key applies to this session. The
// comparison is at whole seconds: a session's authentication time has no sub-second
// part, so comparing instants would exempt a session signed in within the write's second.
func (j *judging) binds(key string) (bool, error) {
	if j.store == nil {
		return false, nil
	}
	if !nameableLayer(string(j.tenant)) {
		return false, fmt.Errorf("reading %s: %w", key, errSettingLayer)
	}
	tenantLayer := settings.ForTenant(string(j.tenant))

	// One read gives both layers from one snapshot, so the value and its revision cannot come from two states.
	layers, err := settings.ReadLayers[bool](j.store, key, settings.Global(), tenantLayer)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", key, err)
	}
	platform, tenantRow := layers[0], layers[1]

	var (
		enabled  bool
		boundary time.Time
	)

	if platform.Value {
		enabled = true
		if !platform.Present || platform.WrittenAt.IsZero() {
			return true, nil
		}
		boundary = platform.WrittenAt
	}

	if tenantRow.Present && tenantRow.Value {
		enabled = true
		if tenantRow.WrittenAt.IsZero() {
			return true, nil
		}
		if boundary.IsZero() || tenantRow.WrittenAt.Before(boundary) {
			boundary = tenantRow.WrittenAt
		}
	}

	if !enabled {
		return false, nil
	}
	authenticated := j.id.AuthTime()
	if authenticated.IsZero() {
		return true, nil
	}
	return authenticated.Unix() >= boundary.Unix(), nil
}

// nameableLayer refuses what the settings would panic on, so that a request never takes that panic.
func nameableLayer(id string) bool {
	return id != "" && strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

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

func (l *layer) reportTTL(problem, detail string) {
	l.mu.Lock()
	changed := detail != l.reportedTTL
	l.reportedTTL = detail
	l.mu.Unlock()
	if problem != "" && changed {
		l.logger.Error(diagnosticPrefix+ModuleName+": "+problem, "setting", checkTTLKey)
	}
}
