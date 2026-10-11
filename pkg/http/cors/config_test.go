package cors

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
)

// policy assembles a one-endpoint configuration around one policy.
func policy(p Policy) Config {
	return Config{Endpoints: map[string]Policy{"public": p}}
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
