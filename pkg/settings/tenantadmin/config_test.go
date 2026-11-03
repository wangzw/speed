package tenantadmin

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/internal/configtest"
)

// TestTheEndpointListDecodesToTheAPIEndpointByDefault holds the default through
// the real loader, not only the literal: a configuration that names nothing gets
// the API endpoint the tenant layers guard.
func TestTheEndpointListDecodesToTheAPIEndpointByDefault(t *testing.T) {
	reader := configtest.Reader(t, "{}", Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		t.Fatalf("decoding the module's section: %v", err)
	}
	got, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.endpoints) != 1 || got.endpoints[0] != "api" {
		t.Fatalf("the decoded endpoints = %v, want [api]", got.endpoints)
	}
}

// TestTheEndpointListDefaultsToTheAPIEndpoint holds the module's worth without a
// configuration file and refuses the shapes it cannot run on.
func TestTheEndpointListDefaultsToTheAPIEndpoint(t *testing.T) {
	if got := configDefaults.Endpoints; len(got) != 1 || got[0] != "api" {
		t.Fatalf("the defaults name %v, want [api]", got)
	}

	refused := []struct {
		name      string
		endpoints []string
	}{
		{"empty", nil},
		{"an empty name", []string{""}},
		{"the every-endpoint name", []string{allEndpoints}},
		{"a name written twice", []string{"api", "api"}},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := moduleConfig{Endpoints: tc.endpoints}.resolve()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("resolve(%v) = %v, want ErrInvalidConfig", tc.endpoints, err)
			}
		})
	}

	got, err := moduleConfig{Endpoints: []string{"api", "admin"}}.resolve()
	if err != nil {
		t.Fatalf("resolve = %v, want the list as written", err)
	}
	if len(got.endpoints) != 2 || got.endpoints[0] != "api" || got.endpoints[1] != "admin" {
		t.Fatalf("resolved endpoints = %v, want [api admin]", got.endpoints)
	}
}
