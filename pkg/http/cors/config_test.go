package cors

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// policy assembles a one-endpoint configuration around one policy.
func policy(p Policy) Config {
	return Config{Endpoints: map[string]Policy{"public": p}}
}

// loadConfig loads a JSON primary source through the real loader and hands back
// this module's section decoded from it. CORSTEST is a prefix no machine carries
// variables under, so nothing outside a case steers the values.
func loadConfig(t *testing.T, document string) Config {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}

	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "CORSTEST", DefaultLocator: "file:" + path}},
	})
	reg.Register(Module())

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	cfg, err := readConfig(reader)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	return cfg
}

// TestTheEndpointsItemTakesThePrimarySourceAlone pins the origin declaration:
// the endpoint table carries allow-origins, which decides who may make
// cross-origin calls, so only the primary config source and the host defaults
// document beneath it may give it. A container item declares the environment
// explicitly when it wants it, so dropping OriginEnv is what closes the door.
func TestTheEndpointsItemTakesThePrimarySourceAlone(t *testing.T) {
	item, ok := schema().Items[endpointsKey]
	if !ok {
		t.Fatalf("the schema declares no %q item, so its origins are the default set", endpointsKey)
	}
	if item.Origins != config.OriginPrimary {
		t.Errorf("the %q item takes the origins %v, want the primary source alone (%v)",
			endpointsKey, item.Origins, config.OriginPrimary)
	}
}

// TestAnEnvironmentVariableCannotAddAGuardedEndpoint is the other half: a
// variable naming http.cors.endpoints does not add an endpoint, so a run whose
// primary source guards none still fails with the required-item error rather
// than accepting a policy written in the process environment. The loader reports
// the variable as unclaimed on stderr; it does not read it.
func TestAnEnvironmentVariableCannotAddAGuardedEndpoint(t *testing.T) {
	t.Setenv("CORSTEST_HTTP__CORS__ENDPOINTS", `{"from-env":{"allow-origins":["https://env.example"]}}`)

	cfg := loadConfig(t, `{}`)
	if len(cfg.Endpoints) != 0 {
		t.Errorf("the endpoint table reads %v, which a variable put there", cfg.Endpoints)
	}
	if err := cfg.validate(); !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("validating the decoded configuration gave %v, want ErrMissingRequired", err)
	}
}

func TestAnEmptyEndpointSetIsRefused(t *testing.T) {
	err := Config{}.validate()
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("validating an empty endpoint set gave %v, want config.ErrMissingRequired", err)
	}
	if !strings.Contains(err.Error(), endpointsPath) {
		t.Fatalf("the failure does not name %s: %v", endpointsPath, err)
	}
}

func TestAPolicyNeedsExactlyOneOriginSource(t *testing.T) {
	cases := []struct {
		name string
		give Policy
	}{
		{"neither allow-all-origins nor allow-origins", Policy{}},
		{"both allow-all-origins and allow-origins", Policy{
			AllowAllOrigins: true,
			AllowOrigins:    []string{"https://app.example"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := policy(tc.give).validate(); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("validating a policy with %s gave %v, want ErrInvalidPolicy", tc.name, err)
			}
		})
	}
}

func TestTheStarOriginIsAccepted(t *testing.T) {
	if err := policy(Policy{AllowOrigins: []string{"*"}}).validate(); err != nil {
		t.Fatalf("validating allow-origins [\"*\"] gave %v, want no error", err)
	}
}

func TestAnOriginWithoutASchemeIsRefused(t *testing.T) {
	err := policy(Policy{AllowOrigins: []string{"app.example"}}).validate()
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("validating an origin without a scheme gave %v, want ErrInvalidPolicy", err)
	}
}

func TestAnUnknownPolicyKeyIsRefused(t *testing.T) {
	err := policy(Policy{
		AllowOrigins: []string{"https://app.example"},
		Unknown:      map[string]any{"allow-origin": "https://app.example"},
	}).validate()
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("validating an unknown key gave %v, want ErrInvalidPolicy", err)
	}
	if !strings.Contains(err.Error(), "allow-origin") {
		t.Fatalf("the failure does not name the offending key: %v", err)
	}
}

func TestANegativeMaxAgeIsRefused(t *testing.T) {
	negative := -time.Second
	err := policy(Policy{AllowAllOrigins: true, MaxAge: &negative}).validate()
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("validating a negative max-age gave %v, want ErrInvalidPolicy", err)
	}
}

func TestAnOutOfRangeStatusCodeIsRefused(t *testing.T) {
	err := policy(Policy{AllowAllOrigins: true, OptionsResponseStatusCode: 700}).validate()
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("validating a status of 700 gave %v, want ErrInvalidPolicy", err)
	}
}

func TestAMultiWildcardOriginIsRefused(t *testing.T) {
	// The library's wildcard parsing panics on this, so the policy has to be
	// refused before the layer is ever built.
	err := policy(Policy{AllowOrigins: []string{"https://*.*.example"}, AllowWildcard: true}).validate()
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("validating an origin with two wildcards gave %v, want ErrInvalidPolicy", err)
	}
	if !strings.Contains(err.Error(), "https://*.*.example") {
		t.Fatalf("the failure does not name the offending origin: %v", err)
	}
}

func TestTheLibraryDefaultsSurviveAnEmptyPolicy(t *testing.T) {
	got := Policy{AllowAllOrigins: true}.libraryConfig()
	if len(got.AllowMethods) == 0 {
		t.Fatalf("an empty policy produced no default allow-methods")
	}
	if len(got.AllowHeaders) == 0 {
		t.Fatalf("an empty policy produced no default allow-headers")
	}
	if got.MaxAge <= 0 {
		t.Fatalf("an empty policy produced max-age %s, want the library's positive default", got.MaxAge)
	}
}

func TestThePolicyOverridesTheLibraryDefaults(t *testing.T) {
	zero := time.Duration(0)
	got := Policy{
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET"},
		AllowHeaders:    []string{"X-Custom"},
		MaxAge:          &zero,
	}.libraryConfig()
	if len(got.AllowMethods) != 1 || got.AllowMethods[0] != "GET" {
		t.Fatalf("allow-methods is %v, want [GET]", got.AllowMethods)
	}
	if len(got.AllowHeaders) != 1 || got.AllowHeaders[0] != "X-Custom" {
		t.Fatalf("allow-headers is %v, want [X-Custom]", got.AllowHeaders)
	}
	if got.MaxAge != 0 {
		t.Fatalf("max-age is %s, want 0", got.MaxAge)
	}
}
