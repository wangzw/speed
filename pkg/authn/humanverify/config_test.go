package humanverify

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/humanverify/turnstile"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/internal/configtest"
)

func body(t *testing.T, s map[string]any) string {
	t.Helper()
	doc := map[string]any{}
	if len(s) > 0 {
		doc["authn"] = map[string]any{"humanverify": s}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func readBody(t *testing.T, document string) (resolved, error) {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

func turnstileSection() map[string]any {
	return map[string]any{
		"mode":       ModeTurnstile,
		"site-key":   "1x00000000000000000000AA",
		"secret-key": "turnstile-secret-2f7a91c4",
		"hostname":   "login.example.com",
		"action":     "login",
	}
}

// TestTheDefaultMechanismsArePoWOnTheOtherSurfaceAndCaptchaOnThePhoneSurface checks that the default mechanisms are proof of work on the other surface and captcha on the phone surface.
func TestTheDefaultMechanismsArePoWOnTheOtherSurfaceAndCaptchaOnThePhoneSurface(t *testing.T) {
	cfg, err := readBody(t, body(t, nil))
	if err != nil {
		t.Fatalf("resolving an empty section: %v", err)
	}
	if cfg.other.mode != ModePoW {
		t.Errorf("the other surface's default mode is %q, want %q", cfg.other.mode, ModePoW)
	}
	if cfg.phone.mode != ModeCaptcha {
		t.Errorf("the phone surface's default mode is %q, want %q", cfg.phone.mode, ModeCaptcha)
	}
}

// TestEveryModeInTheClosedSetResolves checks that every mode in the closed set resolves.
func TestEveryModeInTheClosedSetResolves(t *testing.T) {
	for _, item := range []string{modeItem, phoneModeItem} {
		t.Run(item, func(t *testing.T) {
			for _, mode := range []string{ModeOff, ModePoW, ModeCaptcha} {
				t.Run(mode, func(t *testing.T) {
					cfg, err := readBody(t, body(t, map[string]any{item: mode}))
					if err != nil {
						t.Fatalf("resolving %s = %q: %v", item, mode, err)
					}
					got := cfg.other.mode
					if item == phoneModeItem {
						got = cfg.phone.mode
					}
					if got != mode {
						t.Errorf("the resolved mode is %q, want %q", got, mode)
					}
				})
			}
			t.Run(ModeTurnstile, func(t *testing.T) {
				section := turnstileSection()
				if item == phoneModeItem {
					section = map[string]any{
						"phone-mode": ModeTurnstile,
						"site-key":   "1x00000000000000000000AA",
						"secret-key": "turnstile-secret-2f7a91c4",
						"hostname":   "login.example.com",
						"action":     "login",
					}
				}
				cfg, err := readBody(t, body(t, section))
				if err != nil {
					t.Fatalf("resolving %s = %q: %v", item, ModeTurnstile, err)
				}
				got := cfg.other.mode
				if item == phoneModeItem {
					got = cfg.phone.mode
				}
				if got != ModeTurnstile {
					t.Errorf("the resolved mode is %q, want %q", got, ModeTurnstile)
				}
			})
		})
	}
}

// TestTheSurfacesResolveIndependently checks that the surfaces resolve independently.
func TestTheSurfacesResolveIndependently(t *testing.T) {
	cfg, err := readBody(t, body(t, map[string]any{
		modeItem:      ModeOff,
		phoneModeItem: ModePoW,
	}))
	if err != nil {
		t.Fatalf("resolving two different modes: %v", err)
	}
	if cfg.other.mode != ModeOff || cfg.phone.mode != ModePoW {
		t.Errorf("modes are other %q and phone %q, want off and pow", cfg.other.mode, cfg.phone.mode)
	}
}

// TestTurningTheOtherSurfaceOffLeavesThePhoneSurfaceOnCaptcha checks that turning the other surface off leaves the phone surface on captcha.
func TestTurningTheOtherSurfaceOffLeavesThePhoneSurfaceOnCaptcha(t *testing.T) {
	cfg, err := readBody(t, body(t, map[string]any{modeItem: ModeOff}))
	if err != nil {
		t.Fatalf("resolving mode=off alone: %v", err)
	}
	if cfg.other.mode != ModeOff {
		t.Errorf("the other surface's mode is %q, want %q", cfg.other.mode, ModeOff)
	}
	if cfg.phone.mode != ModeCaptcha {
		t.Errorf("the phone surface's mode is %q, want its default %q", cfg.phone.mode, ModeCaptcha)
	}
	if !cfg.needsStore() {
		t.Error("the phone surface's captcha needs no store, so the defaulted mechanism was not resolved")
	}
}

// TestTurnstileOnTheOtherSurfaceLeavesThePhoneSurfaceOnCaptcha checks that Turnstile on the other surface leaves the phone surface on captcha.
func TestTurnstileOnTheOtherSurfaceLeavesThePhoneSurfaceOnCaptcha(t *testing.T) {
	cfg, err := readBody(t, body(t, turnstileSection()))
	if err != nil {
		t.Fatalf("resolving a Turnstile section: %v", err)
	}
	if cfg.other.mode != ModeTurnstile {
		t.Errorf("the other surface's mode is %q, want %q", cfg.other.mode, ModeTurnstile)
	}
	if cfg.phone.mode != ModeCaptcha {
		t.Errorf("the phone surface's mode is %q, want its default %q", cfg.phone.mode, ModeCaptcha)
	}
	if !cfg.needsStore() {
		t.Error("the phone surface's captcha needs no store, so the defaulted mechanism was not resolved")
	}
}

// TestTurnstileDefaultsTheVerifyEndpointAndTimeout checks that Turnstile defaults the verify endpoint and timeout.
func TestTurnstileDefaultsTheVerifyEndpointAndTimeout(t *testing.T) {
	cfg, err := readBody(t, body(t, turnstileSection()))
	if err != nil {
		t.Fatalf("resolving a Turnstile section: %v", err)
	}
	if cfg.other.verifyURL != turnstile.DefaultVerifyURL {
		t.Errorf("verify URL = %q, want %q", cfg.other.verifyURL, turnstile.DefaultVerifyURL)
	}
	if cfg.other.timeout != turnstile.DefaultTimeout {
		t.Errorf("timeout = %s, want %s", cfg.other.timeout, turnstile.DefaultTimeout)
	}
}

// TestTurnstileNeedsItsSiteKeySecretHostnameAndAction checks that Turnstile needs its site key, secret, hostname and action.
func TestTurnstileNeedsItsSiteKeySecretHostnameAndAction(t *testing.T) {
	for _, item := range []string{"site-key", "secret-key", "hostname", "action"} {
		t.Run(item, func(t *testing.T) {
			section := turnstileSection()
			delete(section, item)
			_, err := readBody(t, body(t, section))
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("a Turnstile section without %s resolved with %v, want ErrInvalidConfig", item, err)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+item) {
				t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, item)
			}
		})
	}
}

// TestTurnstileOnThePhoneSurfaceNeedsItsCredentials checks that Turnstile on the phone surface needs its credentials.
func TestTurnstileOnThePhoneSurfaceNeedsItsCredentials(t *testing.T) {
	for _, item := range []string{siteKeyItem, secretKeyItem, hostnameItem, actionItem} {
		t.Run(item, func(t *testing.T) {
			section := turnstileSection()
			delete(section, modeItem)
			section[phoneModeItem] = ModeTurnstile
			delete(section, item)
			_, err := readBody(t, body(t, section))
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("a phone-surface Turnstile section without %s resolved with %v, want ErrInvalidConfig", item, err)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+item) {
				t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, item)
			}
		})
	}
	t.Run("complete", func(t *testing.T) {
		section := turnstileSection()
		delete(section, modeItem)
		section[phoneModeItem] = ModeTurnstile
		cfg, err := readBody(t, body(t, section))
		if err != nil {
			t.Fatalf("resolving a complete phone-surface Turnstile section: %v", err)
		}
		if cfg.phone.mode != ModeTurnstile {
			t.Errorf("the phone surface's mode is %q, want %q", cfg.phone.mode, ModeTurnstile)
		}
		if cfg.other.mode != ModePoW {
			t.Errorf("the other surface's mode is %q, want its default %q", cfg.other.mode, ModePoW)
		}
	})
}

// TestTurnstileNeedsItsAction checks that Turnstile needs its action.
func TestTurnstileNeedsItsAction(t *testing.T) {
	section := turnstileSection()
	delete(section, actionItem)
	_, err := readBody(t, body(t, section))
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a Turnstile section without an action resolved with %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+actionItem) {
		t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, actionItem)
	}
}

// TestTurnstileCarriesItsActionToTheSurface checks that Turnstile carries its action to the surface.
func TestTurnstileCarriesItsActionToTheSurface(t *testing.T) {
	cfg, err := readBody(t, body(t, turnstileSection()))
	if err != nil {
		t.Fatalf("resolving a Turnstile section: %v", err)
	}
	if got, want := cfg.other.action, "login"; got != want {
		t.Errorf("the other surface's action is %q, want %q", got, want)
	}
}

// TestTurnstileVerifyURLMustBeHTTPS checks that the Turnstile verify URL must be https.
func TestTurnstileVerifyURLMustBeHTTPS(t *testing.T) {
	for _, value := range []string{"http://challenges.cloudflare.com/turnstile/v0/siteverify", "not a URL"} {
		t.Run(value, func(t *testing.T) {
			section := turnstileSection()
			section["verify-url"] = value
			_, err := readBody(t, body(t, section))
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("verify URL %q resolved with %v, want ErrInvalidConfig", value, err)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+verifyURLItem) {
				t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, verifyURLItem)
			}
		})
	}
}

// TestTurnstileTimeoutIsBounded checks that the Turnstile timeout is bounded.
func TestTurnstileTimeoutIsBounded(t *testing.T) {
	for _, value := range []string{"0s", "31s"} {
		t.Run(value, func(t *testing.T) {
			section := turnstileSection()
			section["timeout"] = value
			_, err := readBody(t, body(t, section))
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("timeout %q resolved with %v, want ErrInvalidConfig", value, err)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+timeoutItem) {
				t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, timeoutItem)
			}
		})
	}
}

// TestAProofOfWorkModeNeedsNoneOfTheTurnstileItems checks that a proof-of-work mode needs none of the Turnstile items.
func TestAProofOfWorkModeNeedsNoneOfTheTurnstileItems(t *testing.T) {
	if _, err := readBody(t, body(t, map[string]any{"mode": ModePoW})); err != nil {
		t.Fatalf("resolving the proof-of-work mode: %v", err)
	}
}

// TestAnUnknownModeIsRefusedNamingTheKey checks that an unknown mode is refused naming the key.
func TestAnUnknownModeIsRefusedNamingTheKey(t *testing.T) {
	for _, item := range []string{modeItem, phoneModeItem} {
		t.Run(item, func(t *testing.T) {
			for _, mode := range []string{"none", "PoW", "captcha ", "bogus"} {
				t.Run(mode, func(t *testing.T) {
					_, err := readBody(t, body(t, map[string]any{item: mode}))
					if !errors.Is(err, authn.ErrInvalidConfig) {
						t.Fatalf("mode %q resolved with %v, want ErrInvalidConfig", mode, err)
					}
					if !strings.Contains(err.Error(), ConfigNamespace+"."+item) {
						t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, item)
					}
					if !strings.Contains(err.Error(), mode) {
						t.Errorf("the failure %q does not name the value %q", err, mode)
					}
				})
			}
		})
	}
}

// TestTheSchemaDeclaresTheItemsUnderTheNamespace checks that the schema declares the items under the namespace.
func TestTheSchemaDeclaresTheItemsUnderTheNamespace(t *testing.T) {
	s, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module carries %T, want a config.Schema", Module().Resources[0])
	}
	if s.Namespace != ConfigNamespace {
		t.Errorf("the schema namespace is %q, want %q", s.Namespace, ConfigNamespace)
	}
	for _, item := range []string{
		modeItem, phoneModeItem, siteKeyItem, secretKeyItem, verifyURLItem, hostnameItem,
		actionItem, timeoutItem,
	} {
		if _, ok := s.Items[item]; !ok {
			t.Errorf("the schema declares no %q item", item)
		}
	}
	if !s.Items[secretKeyItem].Sensitive {
		t.Errorf("%s is not marked sensitive, so its value could reach a log", secretKeyItem)
	}
	if len(s.Mounts) != 1 || s.Mounts[0].Value == nil {
		t.Fatalf("the schema mounts %+v, want the default configuration", s.Mounts)
	}
}
