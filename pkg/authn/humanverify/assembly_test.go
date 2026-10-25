package humanverify

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The descriptor claims the human-verification seam exclusively, so a command
// names this module or a verifier of its own, never both.
func TestTheDescriptorDeliversTheHumanVerifierExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	token := core.Token((*authn.HumanVerifier)(nil))
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the one verifier", m.Provides)
	}
	if p := m.Provides[0]; p.Token != token || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want the human-verification seam claimed exclusively", p)
	}
	if len(m.Requires) != 0 {
		t.Errorf("Requires = %+v, want no construction-time dependency", m.Requires)
	}
	if len(m.Resources) != 1 {
		t.Errorf("Resources = %v, want the one input-item schema", m.Resources)
	}
}

// A run that also holds another verifier claiming the seam exclusively fails
// the resolution rather than letting two gates stand.
func TestASecondExclusiveVerifierFailsTheResolution(t *testing.T) {
	reg := registryWithReader(t, body(t, nil))
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "verifier.other",
		Provides: []core.Provision{{Token: (*authn.HumanVerifier)(nil), Exclusive: true}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return otherVerifier{}, nil },
	})
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: []string{ModuleName, "verifier.other", config.ModuleName},
	})
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Fatalf("two exclusive verifiers resolved with %v, want ErrExclusiveViolated", err)
	}
}

// otherVerifier is a stand-in carrying the other exclusive claim.
type otherVerifier struct{}

func (otherVerifier) Required() bool                                 { return true }
func (otherVerifier) Provider() string                               { return "other" }
func (otherVerifier) SiteKey() string                                { return "" }
func (otherVerifier) Verify(context.Context, authn.HumanProof) error { return nil }
