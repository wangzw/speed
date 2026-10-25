package humanverify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/kv"
)

// The stand-in modules that deliver the capabilities this module takes up when
// the proof-of-work mode asks for them. A case supplies the instances so the
// real registry can drive the real descriptor.
const (
	kvStandIn    = "stand-in.kv"
	mountStandIn = "stand-in.mount"
)

// standIn is a module that declares the capability token and delivers it
// without a New, so a case supplies the instance.
func standIn(name string, token core.Token) core.Module {
	return core.Module{Name: name, Provides: []core.Provision{{Token: token}}}
}

// moduleRig is what a case drives: the registry, the mounter the module mounts
// its route on, and the store the provider keeps its challenges in.
type moduleRig struct {
	reg     *core.Registry
	mounter *mounttest.Mounter
	store   *stubKV
}

// newRig builds a registry over a configuration body, with the optional
// capabilities the proof-of-work mode takes up supplied as stand-ins.
func newRig(t *testing.T, document string) *moduleRig {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(Module())
	reg.Register(standIn(kvStandIn, (*kv.KV)(nil)))
	reg.Register(standIn(mountStandIn, (*mount.Mounter)(nil)))
	mounter := mounttest.New(t)
	store := newStubKV()
	reg.Supply(store, kvStandIn)
	reg.Supply(mounter, mountStandIn)
	reg.Supply(reader, config.ModuleName)
	return &moduleRig{reg: reg, mounter: mounter, store: store}
}

// drive advances the rig to Init and returns the verifier the module delivers.
func (r *moduleRig) drive(t *testing.T) authn.HumanVerifier {
	t.Helper()
	var found authn.HumanVerifier
	err := r.reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName, kvStandIn, mountStandIn},
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
	return newRig(t, document).drive(t)
}

// sectionFor is the configuration a case runs a mode on: the credentials the
// Turnstile provider needs, and the mode alone for the mechanisms that need
// none.
func sectionFor(mode string) map[string]any {
	if mode == ModeTurnstile {
		return turnstileSection()
	}
	return map[string]any{"mode": mode}
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
			v := driveVerifier(t, body(t, sectionFor(mode)))
			if got := v.Provider(); got != mode {
				t.Errorf("the verifier reports %q, want %q", got, mode)
			}
			if want := mode != ModeOff; v.Required() != want {
				t.Errorf("Required = %v for mode %q, want %v", v.Required(), mode, want)
			}
		})
	}
}

// The Turnstile provider reports the public site key a client renders its
// widget with, so the surface can answer with it.
func TestTurnstileReportsItsSiteKey(t *testing.T) {
	v := driveVerifier(t, body(t, turnstileSection()))
	if got, want := v.SiteKey(), "1x00000000000000000000AA"; got != want {
		t.Errorf("SiteKey = %q, want %q", got, want)
	}
}

// A gate that requires a proof refuses a request that presents none, whatever
// the mechanism, and that refusal is the 403 answer rather than an outage.
func TestAVerifyingModeRefusesAnEmptyProof(t *testing.T) {
	for _, mode := range []string{ModePoW, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			v := driveVerifier(t, body(t, sectionFor(mode)))
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

// A deployment that names the Turnstile mode without the items its provider
// needs fails the startup where the configuration is read. It does not come up
// with a gate that cannot check a proof and send anyway.
func TestTurnstileWithoutItsCredentialsFailsTheStartup(t *testing.T) {
	reg := registryWithReader(t, body(t, map[string]any{"mode": ModeTurnstile}))
	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a Turnstile mode without credentials resolved with %v, want ErrInvalidConfig", err)
	}
}

// The proof-of-work mode checks a presented proof now: an invalid one is the
// 403 refusal, not the outage the unbuilt mechanism answers.
func TestProofOfWorkRefusesAnInvalidProofAsAProofFailure(t *testing.T) {
	v := driveVerifier(t, body(t, map[string]any{"mode": ModePoW}))
	err := v.Verify(t.Context(), authn.HumanProof{Token: "not a challenge and a nonce"})
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("an invalid proof resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("an invalid proof reads as an outage: %v", err)
	}
}

// Prepare states enabled for every mode in the closed set: the mode decides what
// the verifier requires, not whether the module runs.
func TestPrepareStatesEnabledForEveryMode(t *testing.T) {
	for _, mode := range []string{ModeOff, ModePoW, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			reg := registryWithReader(t, body(t, sectionFor(mode)))
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

// The proof-of-work mode needs the store its challenges live in: a command that
// asks for the mode and delivers no key-value capability fails at construction,
// naming the mode, rather than starting a gate that cannot issue a challenge.
func TestProofOfWorkWithoutAKeyValueStoreFailsTheStartup(t *testing.T) {
	reader := configtest.Reader(t, body(t, map[string]any{"mode": ModePoW}), Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(Module())
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName, config.ModuleName},
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a proof-of-work mode without a store resolved with %v, want ErrInvalidConfig", err)
	}
}

// The proof-of-work mode needs the route mounter: whatever delivers the store,
// a command with no mounter fails at Init rather than serving a mode whose
// clients cannot obtain a challenge.
func TestProofOfWorkWithoutARouteMounterFailsTheStartup(t *testing.T) {
	reader := configtest.Reader(t, body(t, map[string]any{"mode": ModePoW}), Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(Module())
	reg.Register(standIn(kvStandIn, (*kv.KV)(nil)))
	reg.Supply(newStubKV(), kvStandIn)
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName, kvStandIn},
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a proof-of-work mode without a mounter resolved with %v, want ErrInvalidConfig", err)
	}
}

// The module is named and namespaced as the command tree writes it.
func TestTheModuleCarriesItsNameAndNamespace(t *testing.T) {
	if ModuleName != "authn.humanverify" || ConfigNamespace != "authn.humanverify" {
		t.Errorf("the module name %q and namespace %q, want authn.humanverify", ModuleName, ConfigNamespace)
	}
}
