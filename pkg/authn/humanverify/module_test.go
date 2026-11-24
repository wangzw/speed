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

const (
	kvStandIn    = "stand-in.kv"
	mountStandIn = "stand-in.mount"
)

func standIn(name string, token core.Token) core.Module {
	return core.Module{Name: name, Provides: []core.Provision{{Token: token}}}
}

type moduleRig struct {
	reg     *core.Registry
	mounter *mounttest.Mounter
	store   *stubKV
}

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

func registryWithReader(t *testing.T, document string) *core.Registry {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}

func driveVerifier(t *testing.T, document string) authn.HumanVerifier {
	t.Helper()
	return newRig(t, document).drive(t)
}

func sectionFor(mode string) map[string]any {
	if mode == ModeTurnstile {
		section := turnstileSection()
		section[phoneModeItem] = ModeTurnstile
		return section
	}
	return map[string]any{modeItem: mode, phoneModeItem: mode}
}

// TestTheOffProviderRequiresNothingAndAcceptsAnyProof checks that the off provider requires nothing and accepts any proof.
func TestTheOffProviderRequiresNothingAndAcceptsAnyProof(t *testing.T) {
	v := driveVerifier(t, body(t, sectionFor(ModeOff)))
	if got := v.Provider(authn.HumanScopeOther); got != ModeOff {
		t.Errorf("the verifier reports %q, want %q", got, ModeOff)
	}
	if v.Required(authn.HumanScopeOther) {
		t.Error("the off gate requires a proof")
	}
	if got := v.SiteKey(authn.HumanScopeOther); got != "" {
		t.Errorf("the off gate has the site key %q, want none", got)
	}
	for _, proof := range []authn.HumanProof{
		{},
		{Token: "any token at all"},
		{Token: "a token", RemoteIP: "203.0.113.7"},
	} {
		if err := v.Verify(t.Context(), proof, authn.HumanScopeOther); err != nil {
			t.Errorf("the off gate refused %+v with %v, want acceptance", proof, err)
		}
	}
}

// TestEveryModeBuildsAProviderThatReportsIt checks that every mode builds a provider that reports it.
func TestEveryModeBuildsAProviderThatReportsIt(t *testing.T) {
	for _, mode := range []string{ModeOff, ModePoW, ModeCaptcha, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			v := driveVerifier(t, body(t, sectionFor(mode)))
			if got := v.Provider(authn.HumanScopeOther); got != mode {
				t.Errorf("the verifier reports %q, want %q", got, mode)
			}
			if want := mode != ModeOff; v.Required(authn.HumanScopeOther) != want {
				t.Errorf("Required = %v for mode %q, want %v", v.Required(authn.HumanScopeOther), mode, want)
			}
		})
	}
}

// TestTurnstileReportsItsSiteKey checks that Turnstile reports its site key.
func TestTurnstileReportsItsSiteKey(t *testing.T) {
	v := driveVerifier(t, body(t, turnstileSection()))
	if got, want := v.SiteKey(authn.HumanScopeOther), "1x00000000000000000000AA"; got != want {
		t.Errorf("SiteKey = %q, want %q", got, want)
	}
}

// TestTurnstileReportsItsAction checks that Turnstile reports its action.
func TestTurnstileReportsItsAction(t *testing.T) {
	v := driveVerifier(t, body(t, turnstileSection()))
	if got, want := v.Action(authn.HumanScopeOther), "login"; got != want {
		t.Errorf("Action = %q, want %q", got, want)
	}
	if got := v.Action(authn.HumanScopePhone); got != "" {
		t.Errorf("the phone surface's action is %q, want none: it runs the captcha", got)
	}
}

// TestEachSurfaceAnswersItsOwnMechanism checks that each surface answers its own mechanism.
func TestEachSurfaceAnswersItsOwnMechanism(t *testing.T) {
	v := driveVerifier(t, body(t, map[string]any{
		modeItem:      ModePoW,
		phoneModeItem: ModeCaptcha,
	}))
	if got := v.Provider(authn.HumanScopeOther); got != ModePoW {
		t.Errorf("the other surface reports %q, want %q", got, ModePoW)
	}
	if got := v.Provider(authn.HumanScopePhone); got != ModeCaptcha {
		t.Errorf("the phone surface reports %q, want %q", got, ModeCaptcha)
	}
	for _, scope := range []authn.HumanScope{authn.HumanScopeOther, authn.HumanScopePhone} {
		if !v.Required(scope) {
			t.Errorf("the %q surface requires no proof, want one", scope)
		}
		if got := v.SiteKey(scope); got != "" {
			t.Errorf("the %q surface has the site key %q, want none", scope, got)
		}
	}
}

// TestSiteKeyIsPerSurface checks that the site key is per surface.
func TestSiteKeyIsPerSurface(t *testing.T) {
	const siteKey = "1x00000000000000000000AA"
	v := driveVerifier(t, body(t, map[string]any{
		modeItem:      ModeTurnstile,
		phoneModeItem: ModeCaptcha,
		siteKeyItem:   siteKey,
		secretKeyItem: "turnstile-secret-2f7a91c4",
		hostnameItem:  "login.example.com",
		actionItem:    "login",
	}))
	if got := v.SiteKey(authn.HumanScopeOther); got != siteKey {
		t.Errorf("the other surface's site key is %q, want %q", got, siteKey)
	}
	if got := v.SiteKey(authn.HumanScopePhone); got != "" {
		t.Errorf("the phone surface's site key is %q, want none: it runs the captcha", got)
	}
}

// TestOffDisablesOnlyItsSurface checks that off disables only its surface.
func TestOffDisablesOnlyItsSurface(t *testing.T) {
	cases := []struct {
		name       string
		section    map[string]any
		offScope   authn.HumanScope
		gatedScope authn.HumanScope
		gatedMode  string
	}{
		{
			name:       "the other surface off",
			section:    map[string]any{modeItem: ModeOff, phoneModeItem: ModeCaptcha},
			offScope:   authn.HumanScopeOther,
			gatedScope: authn.HumanScopePhone,
			gatedMode:  ModeCaptcha,
		},
		{
			name:       "the phone surface off",
			section:    map[string]any{modeItem: ModePoW, phoneModeItem: ModeOff},
			offScope:   authn.HumanScopePhone,
			gatedScope: authn.HumanScopeOther,
			gatedMode:  ModePoW,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := driveVerifier(t, body(t, tc.section))
			if v.Required(tc.offScope) {
				t.Errorf("the %q surface requires a proof, want the gate off", tc.offScope)
			}
			if got := v.Provider(tc.offScope); got != ModeOff {
				t.Errorf("the %q surface reports %q, want %q", tc.offScope, got, ModeOff)
			}
			if !v.Required(tc.gatedScope) {
				t.Errorf("the %q surface requires no proof, want one", tc.gatedScope)
			}
			if got := v.Provider(tc.gatedScope); got != tc.gatedMode {
				t.Errorf("the %q surface reports %q, want %q", tc.gatedScope, got, tc.gatedMode)
			}
			if err := v.Verify(t.Context(), authn.HumanProof{}, tc.offScope); err != nil {
				t.Errorf("the off surface refused an empty proof with %v, want acceptance", err)
			}
			if err := v.Verify(t.Context(), authn.HumanProof{}, tc.gatedScope); !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Errorf("the gated surface answered an empty proof with %v, want ErrHumanVerificationRequired", err)
			}
		})
	}
}

// TestCaptchaWithoutItsDependenciesFailsTheStartup checks that captcha without its dependencies fails the startup.
func TestCaptchaWithoutItsDependenciesFailsTheStartup(t *testing.T) {
	t.Run("store", func(t *testing.T) {
		reader := configtest.Reader(t, body(t, sectionFor(ModeCaptcha)), Module())
		reg := core.New()
		reg.Register(config.Module())
		reg.Register(Module())
		reg.Supply(reader, config.ModuleName)

		err := reg.Drive(t.Context(), core.Drive{
			Depth:        core.StageNew,
			Participants: []string{ModuleName, config.ModuleName},
		})
		if !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("a captcha mode without a store resolved with %v, want ErrInvalidConfig", err)
		}
	})
	t.Run("mounter", func(t *testing.T) {
		reader := configtest.Reader(t, body(t, sectionFor(ModeCaptcha)), Module())
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
			t.Fatalf("a captcha mode without a mounter resolved with %v, want ErrInvalidConfig", err)
		}
	})
}

// TestThePhoneSurfaceAloneStillNeedsItsDependencies checks that the phone surface alone still needs its dependencies.
func TestThePhoneSurfaceAloneStillNeedsItsDependencies(t *testing.T) {
	section := map[string]any{modeItem: ModeOff, phoneModeItem: ModeCaptcha}
	t.Run("store", func(t *testing.T) {
		reader := configtest.Reader(t, body(t, section), Module())
		reg := core.New()
		reg.Register(config.Module())
		reg.Register(Module())
		reg.Supply(reader, config.ModuleName)

		err := reg.Drive(t.Context(), core.Drive{
			Depth:        core.StageNew,
			Participants: []string{ModuleName, config.ModuleName},
		})
		if !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("a phone-surface captcha without a store resolved with %v, want ErrInvalidConfig", err)
		}
	})
	t.Run("mounter", func(t *testing.T) {
		reader := configtest.Reader(t, body(t, section), Module())
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
			t.Fatalf("a phone-surface captcha without a mounter resolved with %v, want ErrInvalidConfig", err)
		}
	})
}

// TestTheOtherSurfaceOffAloneStillNeedsThePhoneCaptchaStore checks that the other surface off alone still needs the phone captcha store.
func TestTheOtherSurfaceOffAloneStillNeedsThePhoneCaptchaStore(t *testing.T) {
	reader := configtest.Reader(t, body(t, map[string]any{modeItem: ModeOff}), Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(Module())
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName, config.ModuleName},
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("mode=off alone without a store resolved with %v, want ErrInvalidConfig", err)
	}
}

// TestAVerifyingModeRefusesAnEmptyProof checks that a verifying mode refuses an empty proof.
func TestAVerifyingModeRefusesAnEmptyProof(t *testing.T) {
	for _, mode := range []string{ModePoW, ModeCaptcha, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			v := driveVerifier(t, body(t, sectionFor(mode)))
			err := v.Verify(t.Context(), authn.HumanProof{}, authn.HumanScopeOther)
			if !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Fatalf("an empty proof resolved with %v, want ErrHumanVerificationRequired", err)
			}
			if errors.Is(err, authn.ErrUnavailable) {
				t.Errorf("an empty proof reads as an outage: %v", err)
			}
		})
	}
}

// TestTurnstileWithoutItsCredentialsFailsTheStartup checks that Turnstile without its credentials fails the startup.
func TestTurnstileWithoutItsCredentialsFailsTheStartup(t *testing.T) {
	reg := registryWithReader(t, body(t, map[string]any{"mode": ModeTurnstile}))
	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a Turnstile mode without credentials resolved with %v, want ErrInvalidConfig", err)
	}
}

// TestTurnstileWithoutTheSecretKeyFailsNamingIt checks that Turnstile without the secret key fails naming it.
func TestTurnstileWithoutTheSecretKeyFailsNamingIt(t *testing.T) {
	section := turnstileSection()
	delete(section, secretKeyItem)
	reg := registryWithReader(t, body(t, section))
	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a Turnstile mode without a secret key resolved with %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+secretKeyItem) {
		t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, secretKeyItem)
	}
}

// TestProofOfWorkRefusesAnInvalidProofAsAProofFailure checks that proof of work refuses an invalid proof as a proof failure.
func TestProofOfWorkRefusesAnInvalidProofAsAProofFailure(t *testing.T) {
	v := driveVerifier(t, body(t, map[string]any{"mode": ModePoW}))
	err := v.Verify(t.Context(), authn.HumanProof{Token: "not a challenge and a nonce"}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("an invalid proof resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("an invalid proof reads as an outage: %v", err)
	}
}

// TestPrepareStatesEnabledForEveryMode checks that Prepare states enabled for every mode.
func TestPrepareStatesEnabledForEveryMode(t *testing.T) {
	for _, mode := range []string{ModeOff, ModePoW, ModeCaptcha, ModeTurnstile} {
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

// TestPrepareRefusesAnUnknownModeNamingTheKey checks that Prepare refuses an unknown mode naming the key.
func TestPrepareRefusesAnUnknownModeNamingTheKey(t *testing.T) {
	reg := registryWithReader(t, body(t, map[string]any{"mode": "bogus"}))
	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("Prepare accepted an unknown mode: %v", err)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+modeItem) {
		t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, modeItem)
	}
}

// TestTurnstileWithoutAKeyValueStoreFailsTheStartup checks that Turnstile without a key-value store fails the startup.
func TestTurnstileWithoutAKeyValueStoreFailsTheStartup(t *testing.T) {
	section := turnstileSection()
	section[phoneModeItem] = ModeOff
	reader := configtest.Reader(t, body(t, section), Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(Module())
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName, config.ModuleName},
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a Turnstile mode without a store resolved with %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "key-value store") {
		t.Errorf("the failure %q does not name the missing key-value store", err)
	}
}

// TestProofOfWorkWithoutAKeyValueStoreFailsTheStartup checks that proof of work without a key-value store fails the startup.
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

// TestProofOfWorkWithoutARouteMounterFailsTheStartup checks that proof of work without a route mounter fails the startup.
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

// TestAnUnknownScopeAnswersAsTheGatedSurface checks that an unknown scope answers as the gated surface.
func TestAnUnknownScopeAnswersAsTheGatedSurface(t *testing.T) {
	const unknown authn.HumanScope = "no-such-surface"
	cases := []struct {
		name      string
		document  map[string]any
		required  bool
		provider  string
		verifyErr bool
	}{
		{
			name:      "phone gated, other off",
			document:  map[string]any{modeItem: ModeOff, phoneModeItem: ModeCaptcha},
			required:  true,
			provider:  ModeCaptcha,
			verifyErr: true,
		},
		{
			name:      "phone off, other gated",
			document:  map[string]any{modeItem: ModePoW, phoneModeItem: ModeOff},
			required:  true,
			provider:  ModePoW,
			verifyErr: true,
		},
		{
			name:      "both off",
			document:  map[string]any{modeItem: ModeOff, phoneModeItem: ModeOff},
			required:  false,
			provider:  ModeOff,
			verifyErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := driveVerifier(t, body(t, tc.document))
			if got := v.Required(unknown); got != tc.required {
				t.Errorf("Required(unknown) = %v, want %v", got, tc.required)
			}
			if got := v.Provider(unknown); got != tc.provider {
				t.Errorf("Provider(unknown) = %q, want %q", got, tc.provider)
			}
			err := v.Verify(t.Context(), authn.HumanProof{}, unknown)
			if tc.verifyErr {
				if !errors.Is(err, authn.ErrHumanVerificationRequired) {
					t.Errorf("Verify(unknown) with no proof = %v, want ErrHumanVerificationRequired", err)
				}
				return
			}
			if err != nil {
				t.Errorf("Verify(unknown) with no proof = %v, want nil", err)
			}
		})
	}
}

// TestTheModuleCarriesItsNameAndNamespace checks that the module is named and namespaced as the command tree writes it.
func TestTheModuleCarriesItsNameAndNamespace(t *testing.T) {
	if ModuleName != "authn.humanverify" || ConfigNamespace != "authn.humanverify" {
		t.Errorf("the module name %q and namespace %q, want authn.humanverify", ModuleName, ConfigNamespace)
	}
}
