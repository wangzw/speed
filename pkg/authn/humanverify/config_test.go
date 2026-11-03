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

// body renders a whole configuration document holding this module's section. An
// empty section is left out: the loader refuses an empty object at a declared
// section path, and a deployment that states nothing omits the section as well,
// leaving the mounted defaults in force.
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

// readBody loads a document with the real configuration loader and decodes this
// module's section, returning the configuration the module runs on or the error
// the loader refused the startup with.
func readBody(t *testing.T, document string) (resolved, error) {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

// turnstileSection is a Turnstile section complete enough to resolve: the mode
// and the three items its provider requires. A case removes one to see it
// refused.
func turnstileSection() map[string]any {
	return map[string]any{
		"mode":       ModeTurnstile,
		"site-key":   "1x00000000000000000000AA",
		"secret-key": "turnstile-secret-2f7a91c4",
		"hostname":   "login.example.com",
	}
}

// The default deployment runs the proof of work on the other surface and the
// self-hosted captcha on the phone surface. Both need no credential, so a
// deployment that states nothing gates every send without reaching a third
// party.
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

// Every mode in the closed set resolves on either surface, independently of the
// other surface's default.
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

// The two surfaces are chosen independently: turning the phone surface off does
// not turn the other surface off.
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

// A Turnstile section carries the endpoint and timeout defaults without naming
// them, so a deployment supplies only what identifies it.
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

// The Turnstile mode cannot run without the site key, the secret and the
// expected hostname, and the failure names the item to supply.
func TestTurnstileNeedsItsSiteKeySecretAndHostname(t *testing.T) {
	for _, item := range []string{"site-key", "secret-key", "hostname"} {
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

// The Turnstile credential rule holds per surface: the phone surface selecting
// the mechanism without its site key, secret or hostname stops the startup
// naming the item, exactly as the other surface's selection does. The surfaces
// share the one credential set, so a phone-only selection is held to all three.
func TestTurnstileOnThePhoneSurfaceNeedsItsCredentials(t *testing.T) {
	for _, item := range []string{siteKeyItem, secretKeyItem, hostnameItem} {
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

// The siteverify endpoint is an https URL: the restricted egress client speaks
// https alone, and a plain-http endpoint would turn every verification into an
// outage discovered at the first send.
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

// The whole-verification timeout is bounded: a value outside the declared range
// stops the startup rather than bounding every send unreasonably.
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

// A boot that names the proof-of-work mode needs none of the Turnstile items:
// the mode decides which mechanism's configuration is required.
func TestAProofOfWorkModeNeedsNoneOfTheTurnstileItems(t *testing.T) {
	if _, err := readBody(t, body(t, map[string]any{"mode": ModePoW})); err != nil {
		t.Fatalf("resolving the proof-of-work mode: %v", err)
	}
}

// A mode outside the closed set is refused on either surface, and the failure
// names the key to correct rather than silently running the gate off.
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

// The declaration carries the module's items under its own namespace and mounts
// the defaults, so a section that omits an item runs on the declared value and
// the secret is kept out of logs and help output.
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
