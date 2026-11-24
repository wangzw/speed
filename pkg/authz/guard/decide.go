package guard

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
)

func (p *product) Decide(ctx context.Context, d authn.Decision) (bool, error) {
	object, action, ok := translation.Lookup(authz.Permission(d.Action))
	if !ok {
		return false, nil
	}
	subject := string(d.Actor.Account())
	domain := authz.Domain(d.Tenant)
	if domain == "" || platformCapability(d.Action) {
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

func platformCapability(action authn.Action) bool {
	switch action {
	case authn.ActionImpersonateStart, authn.ActionImpersonationRead, authn.ActionImpersonationRevoke:
		return true
	}
	return false
}

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

func (p *product) allowed(ctx context.Context, subject string, domain authz.Domain, object, action string) (bool, error) {
	return p.enforcer.Allowed(ctx, authz.Query{
		Subject: subject,
		Domain:  domain,
		Object:  object,
		Action:  action,
	})
}
