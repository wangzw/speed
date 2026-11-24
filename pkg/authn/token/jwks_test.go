package token

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

func loadedSet(t *testing.T, alg string) *keySet {
	t.Helper()
	signing := generatedKey(t, alg)
	extra := generatedKey(t, alg)
	set, _, err := loadKeySet(resolved{algorithm: alg, signingFile: signing, verifyFiles: []string{extra}})
	if err != nil {
		t.Fatalf("loading the key set: %v", err)
	}
	return set
}

type jwksBody struct {
	Keys []map[string]any `json:"keys"`
}

// TestJWKSPublishesEveryPublicHalfAndNoPrivateField checks that jwks
// publishes every public half and no private field.
func TestJWKSPublishesEveryPublicHalfAndNoPrivateField(t *testing.T) {
	set := loadedSet(t, algES256)
	p := &product{keys: set}

	body, err := p.JWKS(context.Background())
	if err != nil {
		t.Fatalf("JWKS: %v", err)
	}
	var decoded jwksBody
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the JWKS body is not a JSON object: %v\n%s", err, body)
	}
	if len(decoded.Keys) != len(set.entries) {
		t.Fatalf("the set holds %d keys, want %d", len(decoded.Keys), len(set.entries))
	}
	private := []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"}
	for i, key := range decoded.Keys {
		if key["kid"] == "" {
			t.Errorf("key %d has no kid", i)
		}
		if key["alg"] != algES256 {
			t.Errorf("key %d carries alg %v, want %s", i, key["alg"], algES256)
		}
		if key["use"] != "sig" {
			t.Errorf("key %d carries use %v, want sig", i, key["use"])
		}
		for _, field := range private {
			if _, ok := key[field]; ok {
				t.Errorf("key %d carries the private field %q", i, field)
			}
		}
	}
}

// TestTheJWKSRouteCarriesItsOwnHeadersAndContentType checks that the JWKS
// route carries its own headers and content type.
func TestTheJWKSRouteCarriesItsOwnHeadersAndContentType(t *testing.T) {
	set := loadedSet(t, algES256)
	m := mounttest.New(t)
	p := &product{keys: set, mounter: m, logger: slog.Default()}
	p.mountJWKS()

	if _, ok := m.Registered(http.MethodGet, m.BaseIn("")+jwksPath); !ok {
		t.Fatalf("no GET %s%s is registered: %v", m.BaseIn(""), jwksPath, m.Registrations())
	}

	rec := m.Do(httptest.NewRequest(http.MethodGet, m.BaseIn("")+jwksPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the key set answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != jwksContentType {
		t.Errorf("Content-Type is %q, want %q", got, jwksContentType)
	}
	if got := rec.Header().Get("Cache-Control"); got != jwksCacheControl {
		t.Errorf("Cache-Control is %q, want %q, which overrides the mounter's no-store", got, jwksCacheControl)
	}
	var decoded jwksBody
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("the route body is not a key set: %v", err)
	}
	if len(decoded.Keys) == 0 {
		t.Error("the route published no key")
	}
}

// TestTheJWKSHeadersAreTheStableLiterals pins the two header values to their
// literal text rather than to the constants that produce them.
func TestTheJWKSHeadersAreTheStableLiterals(t *testing.T) {
	set := loadedSet(t, algES256)
	m := mounttest.New(t)
	p := &product{keys: set, mounter: m, logger: slog.Default()}
	p.mountJWKS()
	rec := m.Do(httptest.NewRequest(http.MethodGet, m.BaseIn("")+jwksPath, nil))
	if got := rec.Header().Get("Content-Type"); got != "application/jwk-set+json" {
		t.Errorf("Content-Type is %q, want application/jwk-set+json", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control is %q, want public, max-age=300", got)
	}
}
