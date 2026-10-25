package humanverify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
)

// registryWithReader builds a registry with a real configuration reader over a
// body, supplied under the config module's name, which is what this module reads
// before its Prepare runs.
func registryWithReader(t *testing.T, document string) *core.Registry {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}

// driveVerifier assembles the module the way a host does and returns the verifier
// it delivers.
func driveVerifier(t *testing.T, document string) authn.HumanVerifier {
	t.Helper()
	reg := registryWithReader(t, document)
	reg.Register(Module())
	var found authn.HumanVerifier
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			v, err := core.Resolve[authn.HumanVerifier](reg)
			found = v
			return err
		},
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}
	if found == nil {
		t.Fatal("the module delivered no verifier")
	}
	return found
}

// The off gate requires nothing and accepts any proof, an empty one included,
// so a deployment that leaves it off behaves as though the gate were absent.
func TestTheOffProviderRequiresNothingAndAcceptsAnyProof(t *testing.T) {
	v := driveVerifier(t, body(t, nil))
	if got := v.Provider(); got != ModeOff {
		t.Errorf("the verifier reports %q, want %q", got, ModeOff)
	}
	if v.Required() {
		t.Error("the off gate requires a proof")
	}
	if got := v.SiteKey(); got != "" {
		t.Errorf("the off gate has the site key %q, want none", got)
	}
	for _, proof := range []authn.HumanProof{
		{},
		{Token: "any token at all"},
		{Token: "a token", RemoteIP: "203.0.113.7"},
	} {
		if err := v.Verify(t.Context(), proof); err != nil {
			t.Errorf("the off gate refused %+v with %v, want acceptance", proof, err)
		}
	}
}

// Every mode is a provider that reports its own name, and every mode but off
// makes a proof a condition of sending.
func TestEveryModeBuildsAProviderThatReportsIt(t *testing.T) {
	for _, mode := range []string{ModeOff, ModePoW, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			v := driveVerifier(t, body(t, map[string]any{"mode": mode}))
			if got := v.Provider(); got != mode {
				t.Errorf("the verifier reports %q, want %q", got, mode)
			}
			if want := mode != ModeOff; v.Required() != want {
				t.Errorf("Required = %v for mode %q, want %v", v.Required(), mode, want)
			}
		})
	}
}

// A gate that requires a proof refuses a request that presents none, whatever
// the mechanism, and that refusal is the 403 answer rather than an outage.
func TestAVerifyingModeRefusesAnEmptyProof(t *testing.T) {
	for _, mode := range []string{ModePoW, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			v := driveVerifier(t, body(t, map[string]any{"mode": mode}))
			err := v.Verify(t.Context(), authn.HumanProof{})
			if !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Fatalf("an empty proof resolved with %v, want ErrHumanVerificationRequired", err)
			}
			if errors.Is(err, authn.ErrUnavailable) {
				t.Errorf("an empty proof reads as an outage: %v", err)
			}
		})
	}
}

// A mechanism whose proof check is not in this build fails closed: a presented
// proof is an outage, never an acceptance, so an enabled deployment sends
// nothing rather than trusting a token nobody examined.
func TestAVerifyingModeWithoutItsCheckFailsClosed(t *testing.T) {
	for _, mode := range []string{ModePoW, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			v := driveVerifier(t, body(t, map[string]any{"mode": mode}))
			err := v.Verify(t.Context(), authn.HumanProof{Token: "a token"})
			if !errors.Is(err, authn.ErrUnavailable) {
				t.Fatalf("a presented proof resolved with %v, want ErrUnavailable", err)
			}
			if errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Errorf("a presented proof reads as an invalid one: %v", err)
			}
		})
	}
}

// Prepare states enabled for every mode in the closed set: the mode decides what
// the verifier requires, not whether the module runs.
func TestPrepareStatesEnabledForEveryMode(t *testing.T) {
	for _, mode := range []string{ModeOff, ModePoW, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			reg := registryWithReader(t, body(t, map[string]any{"mode": mode}))
			got, err := Module().Prepare(t.Context(), reg)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got != (core.Enablement{State: core.StateEnabled}) {
				t.Errorf("Prepare = %+v, want the enabled stance alone", got)
			}
		})
	}
}

// A mode outside the closed set stops the startup in Prepare, where a
// configuration defect belongs, and names the key to correct.
func TestPrepareRefusesAnUnknownModeNamingTheKey(t *testing.T) {
	reg := registryWithReader(t, body(t, map[string]any{"mode": "captcha"}))
	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("Prepare accepted an unknown mode: %v", err)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+modeItem) {
		t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, modeItem)
	}
}

// The module is named and namespaced as the command tree writes it.
func TestTheModuleCarriesItsNameAndNamespace(t *testing.T) {
	if ModuleName != "authn.humanverify" || ConfigNamespace != "authn.humanverify" {
		t.Errorf("the module name %q and namespace %q, want authn.humanverify", ModuleName, ConfigNamespace)
	}
}
