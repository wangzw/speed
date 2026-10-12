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

// The cases here hold the product's own seams: the unavailable-or-denial split
// on the decision path, the model self-check at startup, the refusal to swallow
// a resolution failure, and the platform-domain check's error.

// An unloaded snapshot is unavailable, not a denial: the process cannot yet
// answer from a policy it read, and telling the caller "denied" would report a
// failure as a permission conclusion.
func TestAllowedOnAnUnloadedProductIsUnavailable(t *testing.T) {
	p := newProductOver(t) // begin is never called, so no snapshot is loaded
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

// A decision with a context that is already done returns the context's error
// rather than evaluating the policy: a withdrawn request is not answered.
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

// brokenModelText parses but behaves other than the design states: its matcher
// drops the policy domain's equality with the request's, so a grant in one
// domain would reach a query in another. The self-check runs on the text the
// process will enforce with, so a text like this must stop the startup.
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

// The startup runs the model self-check on the text it loaded: a text that
// behaves other than the design states fails the startup with the same
// disposition as an unavailable engine, rather than beginning to serve from it.
func TestBeginSelfChecksTheModelText(t *testing.T) {
	p := newProductOver(t)
	p.text = brokenModelText
	err := p.begin(t.Context())
	if !errors.Is(err, authz.ErrPolicyUnavailable) {
		t.Fatalf("begin with a model that behaves other than the design states gave %v, "+
			"want ErrPolicyUnavailable", err)
	}
}

// optional carries out any failure that is not the absence of a provider: an
// ambiguous provider is a real failure and must not be read as "the capability
// is simply not here" and silently dropped.
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

// errorRosterStub is a tenant catalog whose read fails.
type errorRosterStub struct {
	rosterStub
	err error
}

func (r errorRosterStub) Get(context.Context, tenant.ID) (tenant.Tenant, bool, error) {
	return tenant.Tenant{}, false, r.err
}

// The platform-domain check carries a roster read failure out: a roster that
// cannot be read is not "no conflict" and must not let the startup through.
func TestCheckPlatformDomainPropagatesARosterError(t *testing.T) {
	sentinel := errors.New("the roster is unavailable")
	if err := checkPlatformDomain(t.Context(), errorRosterStub{err: sentinel}); !errors.Is(err, sentinel) {
		t.Errorf("checkPlatformDomain with a failing roster gave %v, want the roster's error", err)
	}
}
