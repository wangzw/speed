package mount

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// gateBody is the configuration body that writes items under authn.gate.
func gateBody(items string) string { return `{"authn":{"gate":{` + items + `}}}` }

// TestTheKeysAreReadFromTheGatewaysSectionWithItsDefaults holds what the module
// reads and what it reads when nothing is written: the base path defaults to
// /api/v1/auth, and the other keys are empty or false.
func TestTheKeysAreReadFromTheGatewaysSectionWithItsDefaults(t *testing.T) {
	got, err := readSettings(newReader(t, `{}`))
	if err != nil {
		t.Fatalf("reading the defaults: %v", err)
	}
	if got.Mount.BasePath != "/api/v1/auth" || got.Mount.Endpoint != "" || got.Mount.PublicURL != "" ||
		got.Mount.AllowInsecure || len(got.TrustedOrigins) != 0 {
		t.Errorf("the defaults read %+v, want /api/v1/auth and nothing else", got)
	}

	got, err = readSettings(newReader(t, gateBody(`"mount":{"endpoint":"admin","base-path":"/identity",`+
		`"public-url":"https://app.example.com","allow-insecure":true},"trusted-origins":["https://other.example.com"]`)))
	if err != nil {
		t.Fatalf("reading written values: %v", err)
	}
	if got.Mount.Endpoint != "admin" || got.Mount.BasePath != "/identity" || got.Mount.PublicURL != "https://app.example.com" ||
		!got.Mount.AllowInsecure || len(got.TrustedOrigins) != 1 || got.TrustedOrigins[0] != "https://other.example.com" {
		t.Errorf("the written values read %+v, which is not what the configuration says", got)
	}
}

// TestAConfigurationTheModuleCannotMountOnFailsTheStartWithTheKeyNamed holds each
// refusal the module makes, through the whole start: the sentinel, the key in the
// text, and no route registered.
func TestAConfigurationTheModuleCannotMountOnFailsTheStartWithTheKeyNamed(t *testing.T) {
	for _, c := range []struct {
		name  string
		items string
		want  error
		named string
	}{
		{"a base path with no leading slash", `"mount":{"base-path":"auth"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"a base path with a trailing slash", `"mount":{"base-path":"/api/v1/auth/"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"the root as the base path", `"mount":{"base-path":"/"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"a base path with a parameter", `"mount":{"base-path":"/a/:b"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"a base path with a wildcard", `"mount":{"base-path":"/a/*b"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"a base path with a brace", `"mount":{"base-path":"/a/{b}"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"an uncleaned base path", `"mount":{"base-path":"/a//b"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"a base path with a dot segment", `"mount":{"base-path":"/a/../b"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"an http public URL", `"mount":{"public-url":"http://app.example.com"}`, authn.ErrInsecureConfig, "authn.gate.mount.allow-insecure"},
		{"a public URL with a path", `"mount":{"public-url":"https://app.example.com/app"}`, authn.ErrInvalidConfig, "authn.gate.mount.base-path"},
		{"a public URL with a trailing slash", `"mount":{"public-url":"https://app.example.com/"}`, authn.ErrInvalidConfig, "authn.gate.mount.public-url"},
		{"a public URL with a query", `"mount":{"public-url":"https://app.example.com?x=1"}`, authn.ErrInvalidConfig, "authn.gate.mount.public-url"},
		{"a public URL with no scheme", `"mount":{"public-url":"app.example.com"}`, authn.ErrInvalidConfig, "authn.gate.mount.public-url"},
		{"a public URL with another scheme", `"mount":{"public-url":"ftp://app.example.com"}`, authn.ErrInvalidConfig, "authn.gate.mount.public-url"},
		{"a wildcard host", `"mount":{"public-url":"https://*.example.com"}`, authn.ErrInvalidConfig, "authn.gate.mount.public-url"},
		{"a trusted origin that is not an origin", `"trusted-origins":["https://ok.example.com","not an origin"]`, authn.ErrInvalidConfig, "authn.gate.trusted-origins"},
		{"a trusted origin with a path", `"trusted-origins":["https://ok.example.com/x"]`, authn.ErrInvalidConfig, "authn.gate.trusted-origins"},
		{"a wildcard trusted origin", `"trusted-origins":["https://*.example.com"]`, authn.ErrInvalidConfig, "authn.gate.trusted-origins"},
		{"an empty trusted origin", `"trusted-origins":[""]`, authn.ErrInvalidConfig, "authn.gate.trusted-origins"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &ledger{}
			err := stack{
				body:           gateBody(c.items),
				router:         &standInRouter{names: []string{"main"}, ledger: l},
				gate:           standInGate{ledger: l},
				realGateModule: true,
				register:       func(m mount.Mounter) { m.Public("GET", "/ping", handler) },
			}.drive(t, nothing)
			if !errors.Is(err, c.want) {
				t.Fatalf("the drive returned %v, want %v", err, c.want)
			}
			if !strings.Contains(err.Error(), c.named) {
				t.Errorf("the failure %q does not name %s", err, c.named)
			}
			if routes, access := l.snapshot(); len(routes) != 0 || len(access) != 0 {
				t.Errorf("registered %v and %v although the configuration was refused", routes, access)
			}
		})
	}
}

// TestAConfigurationTheModuleMountsOnIsAccepted is the control for the refusals:
// each value written the allowed way starts, so a refusal above is the value's
// and not the surrounding configuration's.
func TestAConfigurationTheModuleMountsOnIsAccepted(t *testing.T) {
	for _, c := range []struct{ name, items string }{
		{"an https public URL", `"mount":{"public-url":"https://app.example.com"}`},
		{"an https public URL with a port", `"mount":{"public-url":"https://app.example.com:8443"}`},
		{"an http public URL on localhost", `"mount":{"public-url":"http://localhost:8080"}`},
		{"an http public URL on a loopback address", `"mount":{"public-url":"http://127.0.0.1:8080"}`},
		{"an http public URL on a loopback IPv6 address", `"mount":{"public-url":"http://[::1]:8080"}`},
		{"an http public URL with insecure use allowed", `"mount":{"public-url":"http://app.example.com","allow-insecure":true}`},
		{"no public URL", `"mount":{"base-path":"/identity"}`},
		{"a nested base path", `"mount":{"base-path":"/api/v1/auth"}`},
		{"trusted origins in several spellings", `"trusted-origins":["https://a.example.com","HTTP://B.example.com:80","https://c.example.com."]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &ledger{}
			err := stack{
				body:           gateBody(c.items),
				router:         &standInRouter{names: []string{"main"}, ledger: l},
				gate:           standInGate{ledger: l},
				realGateModule: true,
			}.drive(t, nothing)
			if err != nil {
				t.Fatalf("the drive failed: %v", err)
			}
		})
	}
}

// TestARefusalNeverCopiesAValueThatMayCarryASecret holds that the text of a
// refusal does not repeat user information, a query or a fragment written into a
// URL where none belongs, and names a trusted origin by its position.
func TestARefusalNeverCopiesAValueThatMayCarryASecret(t *testing.T) {
	const secret = "hunter2"
	for _, c := range []struct{ name, items string }{
		{"user information in the public URL", `"mount":{"public-url":"https://alice:` + secret + `@app.example.com"}`},
		{"a query in the public URL", `"mount":{"public-url":"https://app.example.com?token=` + secret + `"}`},
		{"a fragment in the public URL", `"mount":{"public-url":"https://app.example.com#` + secret + `"}`},
		{"a path in the public URL beside a query", `"mount":{"public-url":"https://app.example.com/x?token=` + secret + `"}`},
		{"user information in an http public URL", `"mount":{"public-url":"http://alice:` + secret + `@app.example.com"}`},
		{"user information in a trusted origin", `"trusted-origins":["https://ok.example.com","https://alice:` + secret + `@x.example.com"]`},
		{"a secret as a trusted origin", `"trusted-origins":["` + secret + `"]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := readSettings(newReader(t, gateBody(c.items)))
			if err == nil {
				t.Fatal("the value was accepted")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the refusal repeats the value: %v", err)
			}
		})
	}
}
