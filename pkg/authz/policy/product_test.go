package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/tenant"
)

// TestAllowedOnAnUnloadedProductIsUnavailable checks that allowed on an unloaded product is unavailable.
func TestAllowedOnAnUnloadedProductIsUnavailable(t *testing.T) {
	p := newProductOver(t)
	allowed, err := p.Allowed(t.Context(), authz.Query{
		Subject: "acc1", Domain: "acme", Object: "account", Action: "read",
	})
	if allowed {
		t.Errorf("an unloaded product allowed a query")
	}
	if !errors.Is(err, authz.ErrPolicyUnavailable) {
		t.Errorf("Allowed on an unloaded product gave %v, want ErrPolicyUnavailable", err)
	}
}

// TestAllowedHonoursACancelledContext checks that allowed honours a cancelled context.
func TestAllowedHonoursACancelledContext(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := f.Allowed(ctx, authz.Query{
		Subject: "acc1", Domain: "acme", Object: "account", Action: "read",
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Allowed with a cancelled context gave %v, want context.Canceled", err)
	}
}

const brokenModelText = `[request_definition]
r = sub, dom, obj, act
[policy_definition]
p = sub, dom, obj, act
[role_definition]
g = _, _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub, r.dom) && r.obj == p.obj && r.act == p.act
`

// TestBeginSelfChecksTheModelText checks that begin self checks the model text.
func TestBeginSelfChecksTheModelText(t *testing.T) {
	p := newProductOver(t)
	p.text = brokenModelText
	err := p.begin(t.Context())
	if !errors.Is(err, authz.ErrPolicyUnavailable) {
		t.Fatalf("begin with a model that behaves other than the design states gave %v, "+
			"want ErrPolicyUnavailable", err)
	}
}

// TestOptionalPropagatesANonMissingError checks that optional propagates a non-missing error.
func TestOptionalPropagatesANonMissingError(t *testing.T) {
	reg := core.New()
	for _, name := range []string{"stand-in.a", "stand-in.b"} {
		reg.Register(core.Module{Name: name, Provides: []core.Provision{{Token: (*event.Bus)(nil)}}})
		reg.Supply(newRecordingBus(), name)
	}
	_, _, err := optional[event.Bus](reg, "the event bus")
	if err == nil {
		t.Fatalf("optional swallowed an ambiguous-provider error")
	}
	if errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("optional reported a resolution failure as a missing provider: %v", err)
	}
}

type errorRosterStub struct {
	rosterStub
	err error
}

func (r errorRosterStub) Get(context.Context, tenant.ID) (tenant.Tenant, bool, error) {
	return tenant.Tenant{}, false, r.err
}

// TestCheckPlatformDomainPropagatesARosterError checks that checkPlatformDomain propagates a roster error.
func TestCheckPlatformDomainPropagatesARosterError(t *testing.T) {
	sentinel := errors.New("the roster is unavailable")
	if err := checkPlatformDomain(t.Context(), errorRosterStub{err: sentinel}); !errors.Is(err, sentinel) {
		t.Errorf("checkPlatformDomain with a failing roster gave %v, want the roster's error", err)
	}
}
