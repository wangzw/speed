package guard

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
)

// Decide implements authn.Decider.
//
// It translates the action to a query and puts it to the engine, reading the
// permission-to-(object, action) mapping the whole process decides by from
// pkg/authz/internal/translation, where the policy module resolves it. The
// subject is the actor's accountability key and the domain is the tenant the
// decision names, or the platform domain when it names none. An action with no
// row in the translation is refused without an error: the authentication side
// may add an action this mapping does not know, and the safe answer is a denial
// rather than an allow or a failure the caller reads as an outage.
//
// authn.tenant.access is the one action with a platform-domain fallback: a
// cross-tenant operator holds their role in the platform domain, so a query
// that does not match in the tenant's own domain is put again in the platform
// domain before it is denied. Every other action decides in one domain.
//
// An engine error is returned as it stands and is neither a denial nor a
// permission: the caller reads it as an outage.
func (p *product) Decide(ctx context.Context, d authn.Decision) (bool, error) {
	object, action, ok := translation.Lookup(authz.Permission(d.Action))
	if !ok {
		return false, nil
	}
	subject := string(d.Actor.Account())
	domain := authz.Domain(d.Tenant)
	if domain == "" {
		domain = authz.PlatformDomain
	}

	allowed, err := p.allowed(ctx, subject, domain, object, action)
	if err != nil {
		return false, err
	}
	if !allowed && d.Action == authn.ActionTenantAccess && domain != authz.PlatformDomain {
		allowed, err = p.allowed(ctx, subject, authz.PlatformDomain, object, action)
		if err != nil {
			return false, err
		}
	}
	if !allowed {
		return false, nil
	}
	if d.Action == authn.ActionImpersonateStart {
		holds, err := p.targetHoldsPlatformPermission(ctx, d.TargetAccount)
		if err != nil {
			return false, err
		}
		if holds {
			return false, nil
		}
	}
	return true, nil
}

// targetHoldsPlatformPermission reports whether an impersonation target holds
// any platform-domain permission. An account that holds one is an administrator
// and is never a target. A process with no permission reader has no policy, so
// no platform permission is in force and no target can hold one.
func (p *product) targetHoldsPlatformPermission(ctx context.Context, target string) (bool, error) {
	if p.permissions == nil {
		return false, nil
	}
	held, err := p.permissions.Held(ctx, authz.PlatformDomain, target)
	if err != nil {
		return false, err
	}
	return len(held) > 0, nil
}

// allowed puts one query to the engine for a subject in a domain.
func (p *product) allowed(ctx context.Context, subject string, domain authz.Domain, object, action string) (bool, error) {
	return p.enforcer.Allowed(ctx, authz.Query{
		Subject: subject,
		Domain:  domain,
		Object:  object,
		Action:  action,
	})
}
