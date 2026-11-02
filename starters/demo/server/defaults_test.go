package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The platform defaults document is compiled into the program (defaults.go).
// These cases hold the two facts that move made: a run with no configuration
// file at all starts on the document, and the document's key values are the
// ones the YAML file and the sample configurations used to carry.

// clearHostEnvironment removes every variable under the host's prefix and
// every OpenTelemetry one for the duration of a case, so the values it decodes
// are the document's alone rather than whatever the process running the suite
// happens to export.
func clearHostEnvironment(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, envPrefix+"_") && !strings.HasPrefix(name, "OTEL_") {
			continue
		}
		previous, existed := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("removing %s from the case's environment: %v", name, err)
		}
		t.Cleanup(func() {
			if !existed {
				return
			}
			if err := os.Setenv(name, previous); err != nil {
				t.Errorf("restoring %s in the case's environment: %v", name, err)
			}
		})
	}
}

// compiledListener is one entry of http.listeners.
type compiledListener struct {
	Address string `config:"address"`
}

// compiledEndpoint is one entry of http.endpoints.
type compiledEndpoint struct {
	Listener     string   `config:"listener"`
	Prefixes     []string `config:"prefixes"`
	MaxBodyBytes int      `config:"max-body-bytes"`
}

// compiledCORSPolicy is one entry of http.cors.endpoints.
type compiledCORSPolicy struct {
	AllowOrigins []string `config:"allow-origins"`
}

// compiledSeedAccount is one entry of seed.accounts.
type compiledSeedAccount struct {
	Identifier    string `config:"identifier"`
	Password      string `config:"password"`
	DisplayName   string `config:"display-name"`
	PlatformAdmin bool   `config:"platform-admin"`
}

// compiledSeedTenant is one entry of seed.tenants.
type compiledSeedTenant struct {
	Name   string   `config:"name"`
	Admins []string `config:"admins"`
}

// compiledSeedSetting is one entry of seed.settings.
type compiledSeedSetting struct {
	Key   string `config:"key"`
	Value string `config:"value"`
}

// compiledDefaults is the whole document as the loader hands it out. The
// fields mirror the sections the host writes; a field is here because the
// document states it and a case below holds it to its value.
type compiledDefaults struct {
	HTTP struct {
		Listeners map[string]compiledListener `config:"listeners"`
		Endpoints map[string]compiledEndpoint `config:"endpoints"`
		CORS      struct {
			Endpoints map[string]compiledCORSPolicy `config:"endpoints"`
		} `config:"cors"`
	} `config:"http"`
	Ops struct {
		Endpoint    string   `config:"endpoint"`
		Title       string   `config:"title"`
		Version     string   `config:"version"`
		Description string   `config:"description"`
		Servers     []string `config:"servers"`
	} `config:"ops"`
	Observability struct {
		OTel struct {
			ServiceName string `config:"service-name"`
		} `config:"otel"`
	} `config:"observability"`
	WebUI struct {
		Endpoint string `config:"endpoint"`
	} `config:"webui"`
	Authn struct {
		Gate struct {
			Endpoints []string `config:"endpoints"`
			Mount     struct {
				PublicURL string `config:"public-url"`
				Endpoints struct {
					Default string `config:"default"`
					Tenants string `config:"tenants"`
					System  string `config:"system"`
				} `config:"endpoints"`
			} `config:"mount"`
		} `config:"gate"`
		Token struct {
			SigningKeyFile string `config:"signing-key-file"`
		} `config:"token"`
		MFA struct {
			TOTP struct {
				Issuer string `config:"issuer"`
			} `config:"totp"`
			WebAuthn struct {
				RPID    string   `config:"rp-id"`
				RPName  string   `config:"rp-name"`
				Origins []string `config:"origins"`
			} `config:"webauthn"`
		} `config:"mfa"`
	} `config:"authn"`
	Tenant struct {
		Resolve struct {
			Endpoints []string `config:"endpoints"`
			Extractor string   `config:"extractor"`
		} `config:"resolve"`
		Admission struct {
			Endpoints []string `config:"endpoints"`
		} `config:"admission"`
		Admin struct {
			Endpoints []string `config:"endpoints"`
		} `config:"admin"`
		Self struct {
			Endpoints []string `config:"endpoints"`
		} `config:"self"`
	} `config:"tenant"`
	Authz struct {
		Guard struct {
			Endpoints []string `config:"endpoints"`
		} `config:"guard"`
		Admin struct {
			Endpoints []string `config:"endpoints"`
		} `config:"admin"`
		Self struct {
			Endpoints []string `config:"endpoints"`
		} `config:"self"`
	} `config:"authz"`
	Audit struct {
		Journal struct {
			Endpoints []string `config:"endpoints"`
			Requests  []string `config:"requests"`
		} `config:"journal"`
		Admin struct {
			Endpoints []string `config:"endpoints"`
		} `config:"admin"`
	} `config:"audit"`
	Ratelimit struct {
		Endpoints        []string `config:"endpoints"`
		AddressEndpoints []string `config:"address-endpoints"`
	} `config:"ratelimit"`
	Settings struct {
		Admin struct {
			Endpoints []string `config:"endpoints"`
		} `config:"admin"`
	} `config:"settings"`
	Feature struct {
		Board struct {
			Endpoint string `config:"endpoint"`
		} `config:"board"`
	} `config:"feature"`
	Notification struct {
		Inbox struct {
			Endpoints []string `config:"endpoints"`
		} `config:"inbox"`
		Stream struct {
			Endpoints []string `config:"endpoints"`
		} `config:"stream"`
		Preferences struct {
			Endpoints []string `config:"endpoints"`
		} `config:"preferences"`
	} `config:"notification"`
	Objectstore struct {
		FS struct {
			Root string `config:"root"`
		} `config:"fs"`
		Metadata struct {
			ContentEndpoints []string `config:"content-endpoints"`
		} `config:"metadata"`
	} `config:"objectstore"`
	Workspace struct {
		Personal struct {
			Enabled bool `config:"enabled"`
		} `config:"personal"`
	} `config:"workspace"`
	Seed struct {
		Accounts []compiledSeedAccount `config:"accounts"`
		Tenants  []compiledSeedTenant  `config:"tenants"`
		Settings []compiledSeedSetting `config:"settings"`
	} `config:"seed"`
	DB struct {
		SQLite struct {
			DSN           string `config:"dsn"`
			EncryptionKey string `config:"encryption-key"`
		} `config:"sqlite"`
	} `config:"db"`
}

// TestTheCompiledDefaultsHoldTheTopologyAndTheLocalRun loads the configuration
// of a run with no primary source and holds the key values to what the deleted
// defaults.go and the sqlite sample carried: the endpoint claim table, every
// capability's mounting, the development origin, the local seed and the sqlite
// locator. A value that moves out of the document without a replacement shows
// here as the zero it fell back to.
func TestTheCompiledDefaultsHoldTheTopologyAndTheLocalRun(t *testing.T) {
	clearHostEnvironment(t)
	reader, err := config.Load(t.Context(), core.ProcessRegistry, nil)
	if err != nil {
		t.Fatalf("loading the configuration of a run with no primary source: %v", err)
	}
	var doc compiledDefaults
	if err := reader.Decode("", &doc); err != nil {
		t.Fatalf("decoding the compiled defaults: %v", err)
	}

	// The one listener a bare run binds, and the namespace map the dispatch
	// selects an endpoint by.
	wantListeners := map[string]string{
		"backend": "127.0.0.1:8081",
	}
	if len(doc.HTTP.Listeners) != len(wantListeners) {
		t.Fatalf("http.listeners holds %d entries, want %d: %v",
			len(doc.HTTP.Listeners), len(wantListeners), doc.HTTP.Listeners)
	}
	for name, address := range wantListeners {
		if got := doc.HTTP.Listeners[name].Address; got != address {
			t.Errorf("http.listeners.%s.address = %q, want %q", name, got, address)
		}
	}
	wantEndpoints := map[string]compiledEndpoint{
		"api":     {Listener: "backend", Prefixes: []string{"/api"}},
		"account": {Listener: "backend", Prefixes: []string{"/api/v1/auth", "/api/v1/features", "/api/v1/workspace"}},
		"admin": {Listener: "backend", Prefixes: []string{
			"/api/v1/system/audit", "/api/v1/system/auth", "/api/v1/system/permissions",
			"/api/v1/system/settings", "/api/v1/system/sms", "/api/v1/system/tenants",
		}},
		"ops":     {Listener: "backend", Prefixes: []string{"/health", "/ready", "/openapi.json"}},
		"content": {Listener: "backend", Prefixes: []string{"/content"}, MaxBodyBytes: 0},
		"web":     {Listener: "backend", Prefixes: []string{"/"}},
	}
	if len(doc.HTTP.Endpoints) != len(wantEndpoints) {
		t.Fatalf("http.endpoints holds %d entries, want %d: %v",
			len(doc.HTTP.Endpoints), len(wantEndpoints), doc.HTTP.Endpoints)
	}
	for name, want := range wantEndpoints {
		got, declared := doc.HTTP.Endpoints[name]
		if !declared {
			t.Errorf("http.endpoints declares no %q", name)
			continue
		}
		if got.Listener != want.Listener {
			t.Errorf("http.endpoints.%s.listener = %q, want %q", name, got.Listener, want.Listener)
		}
		if !slices.Equal(got.Prefixes, want.Prefixes) {
			t.Errorf("http.endpoints.%s.prefixes = %v, want %v", name, got.Prefixes, want.Prefixes)
		}
		if got.MaxBodyBytes != want.MaxBodyBytes {
			t.Errorf("http.endpoints.%s.max-body-bytes = %d, want %d",
				name, got.MaxBodyBytes, want.MaxBodyBytes)
		}
	}
	if got := doc.HTTP.CORS.Endpoints["api"].AllowOrigins; !slices.Equal(got, []string{"https://app.demo.test"}) {
		t.Errorf("http.cors.endpoints.api.allow-origins = %v, want the development origin", got)
	}

	// The operations surface's document metadata.
	wantString(t, "ops.endpoint", doc.Ops.Endpoint, "ops")
	wantString(t, "ops.title", doc.Ops.Title, "demo")
	wantString(t, "ops.version", doc.Ops.Version, "0.1.0")
	wantString(t, "ops.description", doc.Ops.Description,
		"The demo assembly's API: every operation the modules named in this stack declare, "+
			"merged from their OpenAPI fragments.")
	wantStrings(t, "ops.servers", doc.Ops.Servers, []string{"http://localhost:8081"})

	wantString(t, "observability.otel.service-name", doc.Observability.OTel.ServiceName, "demo")
	wantString(t, "webui.endpoint", doc.WebUI.Endpoint, "web")

	// The identity system: what the gateway guards, the origin a bare run is
	// reached at, the signing key the program mints, and the relying party.
	wantStrings(t, "authn.gate.endpoints", doc.Authn.Gate.Endpoints, []string{"api", "account", "admin"})
	wantString(t, "authn.gate.mount.public-url", doc.Authn.Gate.Mount.PublicURL, "http://localhost:8081")
	wantString(t, "authn.gate.mount.endpoints.default", doc.Authn.Gate.Mount.Endpoints.Default, "account")
	wantString(t, "authn.gate.mount.endpoints.tenants", doc.Authn.Gate.Mount.Endpoints.Tenants, "api")
	wantString(t, "authn.gate.mount.endpoints.system", doc.Authn.Gate.Mount.Endpoints.System, "admin")
	wantString(t, "authn.token.signing-key-file", doc.Authn.Token.SigningKeyFile, signingKeyPath)
	wantString(t, "authn.mfa.totp.issuer", doc.Authn.MFA.TOTP.Issuer, "Speed Demo")
	wantString(t, "authn.mfa.webauthn.rp-id", doc.Authn.MFA.WebAuthn.RPID, "localhost")
	wantString(t, "authn.mfa.webauthn.rp-name", doc.Authn.MFA.WebAuthn.RPName, "demo")
	wantStrings(t, "authn.mfa.webauthn.origins", doc.Authn.MFA.WebAuthn.Origins, []string{"http://localhost:8081"})

	// Tenancy and authorization.
	wantStrings(t, "tenant.resolve.endpoints", doc.Tenant.Resolve.Endpoints, []string{"api"})
	wantString(t, "tenant.resolve.extractor", doc.Tenant.Resolve.Extractor, "header")
	wantStrings(t, "tenant.admission.endpoints", doc.Tenant.Admission.Endpoints, []string{"api"})
	wantStrings(t, "tenant.admin.endpoints", doc.Tenant.Admin.Endpoints, []string{"admin"})
	wantStrings(t, "tenant.self.endpoints", doc.Tenant.Self.Endpoints, []string{"account"})
	wantStrings(t, "authz.guard.endpoints", doc.Authz.Guard.Endpoints, []string{"api"})
	wantStrings(t, "authz.admin.endpoints", doc.Authz.Admin.Endpoints, []string{"admin"})
	wantStrings(t, "authz.self.endpoints", doc.Authz.Self.Endpoints, []string{"api"})

	// The journal, the two limiting layers, settings, the feature document and
	// the notification surfaces.
	wantStrings(t, "audit.journal.endpoints", doc.Audit.Journal.Endpoints, []string{"api", "account", "admin"})
	if len(doc.Audit.Journal.Requests) != 0 {
		t.Errorf("audit.journal.requests = %v, want the empty sample", doc.Audit.Journal.Requests)
	}
	wantStrings(t, "audit.admin.endpoints", doc.Audit.Admin.Endpoints, []string{"admin"})
	wantStrings(t, "ratelimit.endpoints", doc.Ratelimit.Endpoints, []string{"api"})
	wantStrings(t, "ratelimit.address-endpoints", doc.Ratelimit.AddressEndpoints, []string{"account"})
	wantStrings(t, "settings.admin.endpoints", doc.Settings.Admin.Endpoints, []string{"admin"})
	wantString(t, "feature.board.endpoint", doc.Feature.Board.Endpoint, "account")
	wantStrings(t, "notification.inbox.endpoints", doc.Notification.Inbox.Endpoints, []string{"api"})
	wantStrings(t, "notification.stream.endpoints", doc.Notification.Stream.Endpoints, []string{"api"})
	wantStrings(t, "notification.preferences.endpoints", doc.Notification.Preferences.Endpoints, []string{"api"})

	// The object store's local root and the content route it publishes on.
	wantString(t, "objectstore.fs.root", doc.Objectstore.FS.Root, "./var/objects")
	wantStrings(t, "objectstore.metadata.content-endpoints",
		doc.Objectstore.Metadata.ContentEndpoints, []string{"content"})
	if doc.Workspace.Personal.Enabled {
		t.Error("workspace.personal.enabled is true, want the bare run's switch off")
	}

	// The local seed a bare run starts from: the development operator, the
	// walkthrough tenant and the runtime setting.
	wantAccounts := []compiledSeedAccount{{
		Identifier:    "operator",
		Password:      demoOperatorLogin,
		DisplayName:   "Demo Operator",
		PlatformAdmin: true,
	}}
	if !slices.Equal(doc.Seed.Accounts, wantAccounts) {
		t.Errorf("seed.accounts = %+v, want the development operator %+v", doc.Seed.Accounts, wantAccounts)
	}
	wantTenants := []compiledSeedTenant{{Name: "acme", Admins: []string{"operator"}}}
	if len(doc.Seed.Tenants) != len(wantTenants) {
		t.Fatalf("seed.tenants = %+v, want the walkthrough tenant %+v", doc.Seed.Tenants, wantTenants)
	}
	for i, want := range wantTenants {
		if doc.Seed.Tenants[i].Name != want.Name || !slices.Equal(doc.Seed.Tenants[i].Admins, want.Admins) {
			t.Errorf("seed.tenants[%d] = %+v, want %+v", i, doc.Seed.Tenants[i], want)
		}
	}
	wantSettings := []compiledSeedSetting{{Key: "authn.mfa.sensitive-min-age", Value: "1h"}}
	if !slices.Equal(doc.Seed.Settings, wantSettings) {
		t.Errorf("seed.settings = %+v, want %+v", doc.Seed.Settings, wantSettings)
	}

	// The sqlite stack: a database under var/ and the development root key.
	wantString(t, "db.sqlite.dsn", doc.DB.SQLite.DSN, "./var/demo.db")
	wantString(t, "db.sqlite.encryption-key", doc.DB.SQLite.EncryptionKey, demoEncryptionKey)
}

// TestABareRunStartsOnTheCompiledDefaults runs seed and then serve with no
// --config and no DEMO_CONFIG: the program has to satisfy its own required
// items, bind the listener the environment names, reach the sqlite database,
// mint the token signing key, seed the development operator and issue a token
// signed with the origin the document names.
//
// The listener address is the one value the case moves off the document, as
// JSON through DEMO_HTTP__LISTENERS: a suite that bound 127.0.0.1:8081 could not
// run beside another process. The move also exercises the environment origin the
// document's runtime-different values take, and the run log then names the
// address the variable gave.
//
// The single listener is also what the operations probes share with the API:
// GET /ready and GET /api/v1/auth/methods both answer 200 on the same address.
// A build that kept the operations endpoint on a listener of its own would
// answer the probe on one port and the API on another, so one of the two
// requests below fails.
func TestABareRunStartsOnTheCompiledDefaults(t *testing.T) {
	dir := t.TempDir()
	ports := freePorts(t, listenerCount)
	backendPort := ports[backendListener]
	listeners := fmt.Sprintf(
		`{"backend":{"address":"127.0.0.1:%d"}}`, backendPort)
	env := []string{"DEMO_HTTP__LISTENERS=" + listeners}

	seeded := runCommandArgs(t, hostBinary, dir, []string{"sqlite", seedCommand}, env)
	if seeded.err != nil {
		t.Fatalf("seed on the compiled defaults failed: %v\n%s", seeded.err, seeded.combined())
	}
	if !strings.Contains(seeded.stdout, "seeded: account operator") {
		t.Errorf("seed did not write the document's operator account:\n%s", seeded.combined())
	}
	// The document names both paths relative to the working directory the
	// program runs in, which is the case's own here.
	for _, path := range []string{
		filepath.Join("var", "token-signing-key.pem"),
		filepath.Join("var", "demo.db"),
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("the bare run did not create %s: %v", path, err)
		}
	}

	serve := startArgs(t, dir, []string{"sqlite", serveCommand}, env)
	serve.waitReady(t)

	// The environment's address is the one bound, over the document's.
	if want := fmt.Sprintf("address=127.0.0.1:%d", backendPort); !strings.Contains(serve.output(), want) {
		t.Errorf("the run log names no listener at %s; the environment's value did not "+
			"reach the listener set:\n%s", want, serve.output())
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", backendPort)
	if ready := get(t, base+"/ready"); ready.status != 200 {
		t.Errorf("GET /ready answered %d, want 200:\n%s", ready.status, ready.body)
	}
	if methods := get(t, base+"/api/v1/auth/methods"); methods.status != 200 {
		t.Errorf("GET /api/v1/auth/methods answered %d, want 200 on the same address as /ready:\n%s",
			methods.status, methods.body)
	}

	// The seeded account signs in, which is the database, the seed, the minted
	// key and the token issuer all working together.
	token := signIn(t, base, "operator", demoOperatorLogin)
	if issuer := tokenIssuer(t, token); issuer != "http://localhost:8081" {
		t.Errorf("the issued token's iss = %q, want the document's public URL", issuer)
	}
}

// TestTheCompiledDefaultsNameOneListenerAndTheOpsPrefixes holds the two facts
// the single-listener topology rests on: a bare run binds one socket, and the
// operations endpoint claims its three probe paths explicitly on it instead of
// relying on the root fallback. The web endpoint owns "/", so an operations
// endpoint that omitted prefixes would either lose the claim conflict check or
// take the whole path space with it.
func TestTheCompiledDefaultsNameOneListenerAndTheOpsPrefixes(t *testing.T) {
	clearHostEnvironment(t)
	reader, err := config.Load(t.Context(), core.ProcessRegistry, nil)
	if err != nil {
		t.Fatalf("loading the configuration of a run with no primary source: %v", err)
	}
	var doc compiledDefaults
	if err := reader.Decode("", &doc); err != nil {
		t.Fatalf("decoding the compiled defaults: %v", err)
	}

	if len(doc.HTTP.Listeners) != 1 {
		t.Fatalf("http.listeners holds %d entries, want one: %v", len(doc.HTTP.Listeners), doc.HTTP.Listeners)
	}
	backend, ok := doc.HTTP.Listeners["backend"]
	if !ok {
		t.Fatalf("http.listeners names no backend listener: %v", doc.HTTP.Listeners)
	}
	wantString(t, "http.listeners.backend.address", backend.Address, "127.0.0.1:8081")

	ops, declared := doc.HTTP.Endpoints["ops"]
	if !declared {
		t.Fatalf("http.endpoints declares no ops endpoint: %v", doc.HTTP.Endpoints)
	}
	wantString(t, "http.endpoints.ops.listener", ops.Listener, "backend")
	wantStrings(t, "http.endpoints.ops.prefixes", ops.Prefixes,
		[]string{"/health", "/ready", "/openapi.json"})
}

// TestTheSampleStacksBindTheCompiledListener holds the six sample
// configurations to the compiled defaults for the values a copy inherits unless
// it changes them: the backend listener, the operations document's server
// origin, the public URL the gateway advertises and the WebAuthn origin. A
// sample is an override document and may choose another address, but what it
// ships is the compiled default, so a copy starts on the socket a bare run
// binds and the dev server's proxy target reaches it without a flag. A sample
// left on an older port runs a socket nothing names.
func TestTheSampleStacksBindTheCompiledListener(t *testing.T) {
	clearHostEnvironment(t)
	reader, err := config.Load(t.Context(), core.ProcessRegistry, nil)
	if err != nil {
		t.Fatalf("loading the configuration of a run with no primary source: %v", err)
	}
	var compiled compiledDefaults
	if err := reader.Decode("", &compiled); err != nil {
		t.Fatalf("decoding the compiled defaults: %v", err)
	}
	wantAddress := compiled.HTTP.Listeners["backend"].Address
	wantServers := compiled.Ops.Servers
	wantPublicURL := compiled.Authn.Gate.Mount.PublicURL
	wantOrigins := compiled.Authn.MFA.WebAuthn.Origins

	for _, sample := range testdataFiles {
		t.Run(sample, func(t *testing.T) {
			name := "testdata/" + sample
			var doc compiledDefaults
			readEffective(t, name, "file:"+filepath.Join("testdata", sample), "", &doc)
			wantString(t, "http.listeners.backend.address", doc.HTTP.Listeners["backend"].Address, wantAddress)
			wantStrings(t, "ops.servers", doc.Ops.Servers, wantServers)
			wantString(t, "authn.gate.mount.public-url", doc.Authn.Gate.Mount.PublicURL, wantPublicURL)
			wantStrings(t, "authn.mfa.webauthn.origins", doc.Authn.MFA.WebAuthn.Origins, wantOrigins)
		})
	}
}

// tokenIssuer reads the iss claim out of a JWT without verifying it. The case
// is about which value the program signed, not about the signature's validity.
func tokenIssuer(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("the access token has %d parts, want 3", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decoding the access token's payload: %v", err)
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("reading the access token's claims: %v", err)
	}
	return claims.Issuer
}

// wantString fails the case when one loaded value is not the expected one.
func wantString(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

// wantStrings fails the case when one loaded list is not the expected one.
func wantStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}
