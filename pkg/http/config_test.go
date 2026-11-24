package http

import (
	"errors"
	nethttp "net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

func readThrough(t *testing.T, document string, declared config.Schema) (moduleConfig, error, error) {
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
		Resources: []any{config.HostIdentity{Prefix: "HTTPTEST", DefaultLocator: "file:" + path}},
	})
	reg.Register(core.Module{Name: "reader", Resources: []any{declared}})

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		return moduleConfig{}, nil, err
	}

	var got moduleConfig
	decodeErr := reader.Decode(configPath, &got)
	return got, decodeErr, nil
}

// TestTheSetsDeclareTheirOrigins pins the origin declaration.
func TestTheSetsDeclareTheirOrigins(t *testing.T) {
	want := map[string]config.Origin{
		listenersItem:       config.OriginPrimary | config.OriginEnv,
		endpointsItem:       config.OriginPrimary,
		trustedProxiesItem:  config.OriginPrimary,
		remoteIPHeadersItem: config.OriginPrimary,
	}
	for item, origins := range want {
		declared, ok := schema().Items[item]
		if !ok {
			t.Fatalf("the schema declares no %q item, so its origins are the default set", item)
		}
		if declared.Origins != origins {
			t.Errorf("the %s set declares the origins %v, want %v", item, declared.Origins, origins)
		}
	}

	for item, field := range map[string]string{listenersItem: "Listeners", endpointsItem: "Endpoints"} {
		got, ok := reflect.TypeFor[moduleConfig]().FieldByName(field)
		if !ok {
			t.Fatalf("moduleConfig has no %s field", field)
		}
		if got.Type.Kind() != reflect.Map {
			t.Errorf("the %s set is a %s; a map is what makes it a container leaf config "+
				"replaces as a whole", item, got.Type.Kind())
		}
	}
}

// TestAListenerSetFromTheEnvironmentIsAccepted is the half a declaration could get wrong in the
// other direction: the environment gives a container leaf as JSON.
func TestAListenerSetFromTheEnvironmentIsAccepted(t *testing.T) {
	t.Setenv("HTTPTEST_HTTP__LISTENERS", `{"from-env":{"address":"127.0.0.1:0"}}`)

	got, decodeErr, runErr := readThrough(t,
		`{"http":{"listeners":{"from-file":{"address":"127.0.0.1:1"}}}}`, schema())
	if runErr != nil {
		t.Fatalf("the run failed: %v", runErr)
	}
	if decodeErr != nil {
		t.Fatalf("decoding this module's section: %v", decodeErr)
	}
	if _, ok := got.Listeners["from-env"]; !ok {
		t.Errorf("the listener set decoded as %+v, and the variable's listener is missing",
			got.Listeners)
	}
	if _, ok := got.Listeners["from-file"]; ok {
		t.Errorf("the listener set decoded as %+v; the environment replaces it whole rather "+
			"than merging with the document", got.Listeners)
	}
}

// TestTheSetsDecodeFromThePrimarySource pins the whole path: the declaration this module registers
// is accepted.
func TestTheSetsDecodeFromThePrimarySource(t *testing.T) {
	got, decodeErr, runErr := readThrough(t, `{
		"http": {
			"listeners": {
				"backend": {"address": ":8080", "read-timeout": "45s"},
				"ops":     {"address": "127.0.0.1:9090"}
			},
			"endpoints": {
				"public": {"listener": "backend", "max-body-bytes": 10},
				"metrics": {"listener": "ops"}
			}
		}
	}`, schema())
	if runErr != nil {
		t.Fatalf("the run failed: %v", runErr)
	}
	if decodeErr != nil {
		t.Fatalf("decoding this module's section: %v", decodeErr)
	}

	if len(got.Listeners) != 2 {
		t.Fatalf("the document declares two listeners and %d arrived: %+v", len(got.Listeners), got.Listeners)
	}
	if got.Listeners["backend"].Address != ":8080" {
		t.Errorf("the backend listener's address arrived as %q", got.Listeners["backend"].Address)
	}
	if read := got.Listeners["backend"].ReadTimeout; read == nil || *read != 45*time.Second {
		t.Errorf("the backend listener's read-timeout arrived as %v", read)
	}
	if got.Listeners["ops"].ReadTimeout != nil {
		t.Error("the ops listener gives no read-timeout, and one arrived anyway")
	}
	if len(got.Endpoints) != 2 {
		t.Fatalf("the document declares two endpoints and %d arrived: %+v", len(got.Endpoints), got.Endpoints)
	}
	if got.Endpoints["public"].Listener != "backend" {
		t.Errorf("the public endpoint hangs on %q, want backend", got.Endpoints["public"].Listener)
	}
	if limit := got.Endpoints["public"].MaxBodyBytes; limit == nil || *limit != 10 {
		t.Errorf("the public endpoint's max-body-bytes arrived as %v", limit)
	}
}

// TestAMisspelledKeyInAListenerIsAStartupFailure pins the keys inside one listener's entry.
func TestAMisspelledKeyInAListenerIsAStartupFailure(t *testing.T) {
	for _, key := range []string{"drain-timout", "read_timeout", "addr"} {
		got, decodeErr, runErr := readThrough(t,
			`{"http":{"listeners":{"backend":{"address":":8080","`+key+`":"1s"}}}}`, schema())
		if runErr != nil || decodeErr != nil {
			t.Fatalf("%s: the section did not decode: %v", key, errors.Join(runErr, decodeErr))
		}
		_, _, err := got.resolve()
		if err == nil {
			t.Errorf("a listener giving the key %q started, and the key was dropped", key)
			continue
		}
		mustContain(t, err.Error(), `"`+key+`"`, "the key that is not one of the listener's")
		mustContain(t, err.Error(), `"backend"`, "the listener the key was written under")
		mustContain(t, err.Error(), "drain-timeout", "the keys a listener takes")
	}

	got, decodeErr, runErr := readThrough(t, `{"http":{"listeners":{"backend":{
		"address":":8080","read-header-timeout":"1s","read-timeout":"1s","write-timeout":"1s",
		"idle-timeout":"1s","drain-timeout":"1s"}}}}`, schema())
	if runErr != nil || decodeErr != nil {
		t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
	}
	if _, _, err := got.resolve(); err != nil {
		t.Errorf("a listener giving only its own keys was refused: %v", err)
	}
}

// TestAMisspelledKeyInAnEndpointIsAStartupFailure is the same check for an endpoint's entry: the
// endpoint keys are checked here.
func TestAMisspelledKeyInAnEndpointIsAStartupFailure(t *testing.T) {
	for _, key := range []string{"listner", "max-body-bytez", "trusted_proxies"} {
		got, decodeErr, runErr := readThrough(t,
			`{"http":{"listeners":{"backend":{"address":":8080"}},`+
				`"endpoints":{"public":{"listener":"backend","`+key+`":"x"}}}}`, schema())
		if runErr != nil || decodeErr != nil {
			t.Fatalf("%s: the section did not decode: %v", key, errors.Join(runErr, decodeErr))
		}
		_, _, err := got.resolve()
		if err == nil {
			t.Errorf("an endpoint giving the key %q started, and the key was dropped", key)
			continue
		}
		mustContain(t, err.Error(), `"`+key+`"`, "the key that is not one of the endpoint's")
		mustContain(t, err.Error(), `"public"`, "the endpoint the key was written under")
		mustContain(t, err.Error(), "max-body-bytes", "the keys an endpoint takes")
	}
}

// TestTheWildcardIsNotAnEndpointName pins the one name an endpoint cannot carry.
func TestTheWildcardIsNotAnEndpointName(t *testing.T) {
	for name, document := range map[string]string{
		"alone": `{"http":{"listeners":{"backend":{"address":":8080"}},` +
			`"endpoints":{"*":{"listener":"backend"}}}}`,
		"beside more": `{"http":{"listeners":{"backend":{"address":":8080"}},` +
			`"endpoints":{"public":{"listener":"backend"},"*":{"listener":"backend"}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, decodeErr, runErr := readThrough(t, document, schema())
			if runErr != nil || decodeErr != nil {
				t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
			}
			if _, ok := got.Endpoints["*"]; !ok {
				t.Fatalf("the entry named \"*\" did not reach the carrier: %v", got.Endpoints)
			}

			_, _, err := got.resolve()
			if err == nil {
				t.Fatal("an endpoint named \"*\" resolved, and the name is the one every list of " +
					"endpoint names writes for all of them")
			}
			mustContain(t, err.Error(), `"*"`, "the endpoint whose name is refused")
			mustContain(t, err.Error(), "reserves", "that the name is refused as a reserved one")
			mustContain(t, err.Error(), "http.endpoints", "the key the entry is written under")
		})
	}
}

// TestAnEndpointNamingAnUnknownListenerIsStartupFailure pins the reference check.
func TestAnEndpointNamingAnUnknownListenerIsStartupFailure(t *testing.T) {
	_, _, err := moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: ":8080"}},
		Endpoints: map[string]endpointConfig{"public": {Listener: "frontend"}},
	}.resolve()
	if !errors.Is(err, ErrUnknownListener) {
		t.Fatalf("an endpoint on an undeclared listener resolved with %v, want ErrUnknownListener", err)
	}
	mustContain(t, err.Error(), `"public"`, "the endpoint that hung on it")
	mustContain(t, err.Error(), `"frontend"`, "the listener it named")
	mustContain(t, err.Error(), `"backend"`, "the listeners that do exist")
	mustContain(t, err.Error(), "http.listeners", "the key to write a listener under")
}

// TestDurationItemRejectsBareNumber pins config's rule for a duration, which this module's field
// types opt into.
func TestDurationItemRejectsBareNumber(t *testing.T) {
	_, decodeErr, runErr := readThrough(t,
		`{"http":{"listeners":{"backend":{"address":":8080","read-timeout":300}}}}`, schema())
	refusal := errors.Join(runErr, decodeErr)
	if refusal == nil {
		t.Fatal("a bare number was accepted as a duration")
	}
	mustContain(t, refusal.Error(), "duration", "what the value was refused as")
	mustContain(t, refusal.Error(), "read-timeout", "the item it was given for")
}

// TestListenerDefaultsAppliedAfterDecode pins the three states of an optional item.
func TestListenerDefaultsAppliedAfterDecode(t *testing.T) {
	off := time.Duration(0)
	explicit := 5 * time.Second
	cfg := moduleConfig{Listeners: map[string]listenerConfig{
		"absent":   {Address: ":1"},
		"switched": {Address: ":2", IdleTimeout: &off},
		"given":    {Address: ":3", IdleTimeout: &explicit},
	}}

	listeners, _, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	byName := make(map[string]listenerSettings, len(listeners))
	for _, s := range listeners {
		byName[s.name] = s
	}

	if byName["absent"].idleTimeout != defaultIdleTimeout {
		t.Errorf("an absent idle-timeout resolved to %v, and the default is %v",
			byName["absent"].idleTimeout, defaultIdleTimeout)
	}
	if byName["switched"].idleTimeout != 0 {
		t.Errorf("an explicit 0 resolved to %v; 0 is how the bound is switched off",
			byName["switched"].idleTimeout)
	}
	if byName["given"].idleTimeout != explicit {
		t.Errorf("an explicit %v resolved to %v", explicit, byName["given"].idleTimeout)
	}
}

// TestTheSetsResolveInNameOrder pins the order everything downstream inherits: the order listeners
// are bound and stopped in, the order endpoints are assembled in.
func TestTheSetsResolveInNameOrder(t *testing.T) {
	cfg := moduleConfig{
		Listeners: map[string]listenerConfig{
			"public": {Address: ":1"}, "admin": {Address: ":2"}, "metrics": {Address: ":3"},
		},
		Endpoints: map[string]endpointConfig{
			"public": {Listener: "public"}, "admin": {Listener: "admin"}, "metrics": {Listener: "metrics"},
		},
	}
	listeners, endpoints, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	gotListeners := make([]string, 0, len(listeners))
	for _, s := range listeners {
		gotListeners = append(gotListeners, s.name)
	}
	if want := []string{"admin", "metrics", "public"}; !slices.Equal(gotListeners, want) {
		t.Errorf("the listeners resolved in the order %v, and name order is %v", gotListeners, want)
	}
	gotEndpoints := make([]string, 0, len(endpoints))
	for _, s := range endpoints {
		gotEndpoints = append(gotEndpoints, s.name)
	}
	if want := []string{"admin", "metrics", "public"}; !slices.Equal(gotEndpoints, want) {
		t.Errorf("the endpoints resolved in the order %v, and name order is %v", gotEndpoints, want)
	}
}

// TestMissingAddressIsStartupFailure pins the one item with no default.
func TestMissingAddressIsStartupFailure(t *testing.T) {
	cfg := moduleConfig{Listeners: map[string]listenerConfig{"admin": {}}}
	_, _, err := cfg.resolve()
	if err == nil {
		t.Fatal("a listener with no address was accepted")
	}
	mustContain(t, err.Error(), `"admin"`, "the listener that has no address")
	mustContain(t, err.Error(), "http.listeners.admin.address", "the key to write")
}

// TestReadHeaderTimeoutAlwaysSet pins the bound on a server this module builds.
func TestReadHeaderTimeoutAlwaysSet(t *testing.T) {
	listeners, _, err := moduleConfig{Listeners: map[string]listenerConfig{
		"public": {Address: "127.0.0.1:0"},
	}}.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	logger, _ := newRecordingLogger()
	l, _ := startTestEndpoint(t, listeners[0], nethttp.NotFoundHandler(), logger)

	if l.server.ReadHeaderTimeout == 0 {
		t.Error("the server was built without a header timeout")
	}
	if l.server.ReadHeaderTimeout != defaultReadHeaderTimeout {
		t.Errorf("the header timeout is %v, and the default is %v",
			l.server.ReadHeaderTimeout, defaultReadHeaderTimeout)
	}
}

// TestReadHeaderTimeoutCannotBeSwitchedOff pins one of the two bounds that have no "off" state.
func TestReadHeaderTimeoutCannotBeSwitchedOff(t *testing.T) {
	off := time.Duration(0)
	_, _, err := moduleConfig{Listeners: map[string]listenerConfig{
		"public": {Address: ":8080", ReadHeaderTimeout: &off},
	}}.resolve()
	if err == nil {
		t.Fatal("read-header-timeout was switched off with 0")
	}
	mustContain(t, err.Error(), "read-header-timeout", "the item that cannot be switched off")
	mustContain(t, err.Error(), "one byte at a time", "why it cannot")
}

// TestDrainTimeoutRefusesExplicitZero pins the second of the two bounds that cannot be switched
// off, and pins it through the real load.
func TestDrainTimeoutRefusesExplicitZero(t *testing.T) {
	got, decodeErr, runErr := readThrough(t,
		`{"http":{"listeners":{"backend":{"address":":8080","drain-timeout":"0s"}}}}`, schema())
	if runErr != nil {
		t.Fatalf("the run failed: %v", runErr)
	}
	if decodeErr != nil {
		t.Fatalf("decoding this module's section: %v", decodeErr)
	}
	given := got.Listeners["backend"].DrainTimeout
	if given == nil {
		t.Fatal("config delivered an explicit \"0s\" as an absent key, so the listener would " +
			"silently take the default instead of being refused")
	}
	if *given != 0 {
		t.Fatalf("an explicit \"0s\" arrived as %v", *given)
	}

	// resolve is what Prepare runs the decoded section through, so its
	// refusal is the startup failure.
	_, _, err := got.resolve()
	if err == nil {
		t.Fatal("drain-timeout was switched off with 0")
	}
	mustContain(t, err.Error(), "drain-timeout", "the item that cannot be switched off")
	mustContain(t, err.Error(), `"backend"`, "the listener it was given on")
	mustContain(t, err.Error(), "for ever", "why it cannot be switched off")
}

// TestDrainTimeoutAbsentTakesTheDefault and its positive-value twin below are what keep the refusal
// aimed at the explicit 0 alone.
func TestDrainTimeoutAbsentTakesTheDefault(t *testing.T) {
	listeners, _, err := moduleConfig{Listeners: map[string]listenerConfig{
		"public": {Address: ":8080"},
	}}.resolve()
	if err != nil {
		t.Fatalf("a listener that gives no drain-timeout was refused: %v", err)
	}
	if listeners[0].drainTimeout != defaultDrainTimeout {
		t.Errorf("an absent drain-timeout resolved to %v, and the default is %v",
			listeners[0].drainTimeout, defaultDrainTimeout)
	}
}

// TestDrainTimeoutAcceptsAPositiveValue checks that a positive drain timeout is accepted.
func TestDrainTimeoutAcceptsAPositiveValue(t *testing.T) {
	given := 90 * time.Second
	listeners, _, err := moduleConfig{Listeners: map[string]listenerConfig{
		"public": {Address: ":8080", DrainTimeout: &given},
	}}.resolve()
	if err != nil {
		t.Fatalf("a positive drain-timeout was refused: %v", err)
	}
	if listeners[0].drainTimeout != given {
		t.Errorf("a drain-timeout of %v resolved to %v", given, listeners[0].drainTimeout)
	}
}

// TestOtherDurationsStillAcceptExplicitZero pins that the exceptions are exactly two.
func TestOtherDurationsStillAcceptExplicitZero(t *testing.T) {
	off := time.Duration(0)
	cases := []struct {
		key   string
		give  func(*listenerConfig)
		taken func(listenerSettings) time.Duration
	}{
		{
			"read-timeout",
			func(l *listenerConfig) { l.ReadTimeout = &off },
			func(s listenerSettings) time.Duration { return s.readTimeout },
		},
		{
			"write-timeout",
			func(l *listenerConfig) { l.WriteTimeout = &off },
			func(s listenerSettings) time.Duration { return s.writeTimeout },
		},
		{
			"idle-timeout",
			func(l *listenerConfig) { l.IdleTimeout = &off },
			func(s listenerSettings) time.Duration { return s.idleTimeout },
		},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			given := listenerConfig{Address: ":8080"}
			c.give(&given)
			listeners, _, err := moduleConfig{Listeners: map[string]listenerConfig{
				"public": given,
			}}.resolve()
			if err != nil {
				t.Fatalf("an explicit 0 for %s was refused: %v", c.key, err)
			}
			if taken := c.taken(listeners[0]); taken != 0 {
				t.Errorf("an explicit 0 for %s resolved to %v; 0 is how the bound is "+
					"switched off", c.key, taken)
			}
		})
	}
}

// TestNegativeDurationIsRejected pins the difference between switching a bound off and mistyping
// it.
func TestNegativeDurationIsRejected(t *testing.T) {
	negative := -time.Second
	_, _, err := moduleConfig{Listeners: map[string]listenerConfig{
		"public": {Address: ":8080", WriteTimeout: &negative},
	}}.resolve()
	if err == nil {
		t.Fatal("a negative write-timeout was accepted")
	}
	mustContain(t, err.Error(), "write-timeout", "the item that was mistyped")
	mustContain(t, err.Error(), `"public"`, "the listener it was given on")
}

// TestHalfConfiguredTLSIsRejected pins the pair.
func TestHalfConfiguredTLSIsRejected(t *testing.T) {
	for _, given := range []listenerConfig{
		{Address: ":8443", TLSCertFile: "/etc/cert.pem"},
		{Address: ":8443", TLSKeyFile: "/etc/key.pem"},
	} {
		_, _, err := moduleConfig{Listeners: map[string]listenerConfig{"public": given}}.resolve()
		if err == nil {
			t.Fatalf("half a TLS configuration was accepted: %+v", given)
		}
		mustContain(t, err.Error(), "tls-cert-file", "the pair that has to be given together")
	}
}

// TestEmptySetsResolve pins that empty sets resolve cleanly.
func TestEmptySetsResolve(t *testing.T) {
	listeners, endpoints, err := moduleConfig{}.resolve()
	if err != nil {
		t.Fatalf("an empty configuration failed to resolve: %v", err)
	}
	if len(listeners) != 0 || len(endpoints) != 0 {
		t.Errorf("an empty configuration resolved to %d listeners and %d endpoints",
			len(listeners), len(endpoints))
	}
}

// TestMaxBodyBytesHasThreeStatesAndRefusesANegativeOne pins the body limit as an optional item in
// the states the surface documents.
func TestMaxBodyBytesHasThreeStatesAndRefusesANegativeOne(t *testing.T) {
	zero, given, negative := int64(0), int64(7), int64(-1)
	cfg := moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: "127.0.0.1:0"}},
		Endpoints: map[string]endpointConfig{
			"absent": {Listener: "backend", Prefixes: []string{"/absent"}},
			"off":    {Listener: "backend", Prefixes: []string{"/off"}, MaxBodyBytes: &zero},
			"given":  {Listener: "backend", Prefixes: []string{"/given"}, MaxBodyBytes: &given},
		},
	}
	_, endpoints, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolving the three states: %v", err)
	}
	limits := map[string]int64{}
	for _, s := range endpoints {
		limits[s.name] = s.maxBodyBytes
	}
	for name, want := range map[string]int64{"absent": defaultMaxBodyBytes, "off": 0, "given": 7} {
		if limits[name] != want {
			t.Errorf("the endpoint with max-body-bytes %s resolved to %d, want %d", name, limits[name], want)
		}
	}

	_, _, err = moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: "127.0.0.1:0"}},
		Endpoints: map[string]endpointConfig{
			"public": {Listener: "backend", MaxBodyBytes: &negative},
		},
	}.resolve()
	if err == nil {
		t.Fatal("a negative max-body-bytes was accepted")
	}
	mustContain(t, err.Error(), "max-body-bytes", "the item that was refused")
	mustContain(t, err.Error(), `endpoint "public"`, "the endpoint that gave it")
}

// TestTrustedProxiesAndRemoteIPHeadersDecodeAndResolve pins the two input items an endpoint behind
// a proxy needs.
func TestTrustedProxiesAndRemoteIPHeadersDecodeAndResolve(t *testing.T) {
	got, decodeErr, runErr := readThrough(t, `{
		"http": {
			"listeners": {"backend": {"address": ":8080"}},
			"endpoints": {"public": {
				"listener": "backend",
				"trusted-proxies": ["10.0.0.0/8", "192.0.2.7"],
				"remote-ip-headers": ["X-Forwarded-For", "X-Real-IP"]
			}}
		}
	}`, schema())
	if runErr != nil || decodeErr != nil {
		t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
	}

	_, endpoints, err := got.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if want := []string{"10.0.0.0/8", "192.0.2.7"}; !slices.Equal(endpoints[0].trustedProxies, want) {
		t.Errorf("trusted-proxies resolved to %v, want %v", endpoints[0].trustedProxies, want)
	}
	if want := []string{"X-Forwarded-For", "X-Real-IP"}; !slices.Equal(endpoints[0].remoteIPHeaders, want) {
		t.Errorf("remote-ip-headers resolved to %v, want %v", endpoints[0].remoteIPHeaders, want)
	}
}

// TestNoTrustedProxyIsTheDefault pins the safe default: with neither the module-level nor the
// endpoint-level items written, no proxy is trusted.
func TestNoTrustedProxyIsTheDefault(t *testing.T) {
	_, endpoints, err := moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: ":8080"}},
		Endpoints: map[string]endpointConfig{"public": {Listener: "backend"}},
	}.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(endpoints[0].trustedProxies) != 0 || len(endpoints[0].remoteIPHeaders) != 0 {
		t.Errorf("an endpoint naming no proxy resolved to trusted-proxies %v remote-ip-headers %v, want none",
			endpoints[0].trustedProxies, endpoints[0].remoteIPHeaders)
	}
}

// TestAnInvalidTrustedProxyIsStartupFailure pins the check that turns a mistyped network into a
// message naming the endpoint and the key.
func TestAnInvalidTrustedProxyIsStartupFailure(t *testing.T) {
	_, _, err := moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: ":8080"}},
		Endpoints: map[string]endpointConfig{
			"public": {Listener: "backend", TrustedProxies: []string{"10.0.0.0/8", "not-a-network"}},
		},
	}.resolve()
	if err == nil {
		t.Fatal("an endpoint naming a value that is not an address or a block was accepted")
	}
	mustContain(t, err.Error(), "trusted-proxies", "the key that was written")
	mustContain(t, err.Error(), `"public"`, "the endpoint it was written on")
	mustContain(t, err.Error(), "not-a-network", "the value that is not a network")
}

// TestModuleLevelProxySettingsAreInheritedByEveryEndpoint pins where a deployment names the
// networks in front of it: once, at the module level, for every endpoint to take as it stands.
func TestModuleLevelProxySettingsAreInheritedByEveryEndpoint(t *testing.T) {
	got, decodeErr, runErr := readThrough(t, `{
		"http": {
			"listeners": {"backend": {"address": ":8080"}},
			"trusted-proxies": ["10.0.0.0/8", "192.0.2.7"],
			"remote-ip-headers": ["X-Forwarded-For", "X-Real-IP"],
			"endpoints": {
				"public": {"listener": "backend"},
				"admin": {"listener": "backend", "prefixes": ["/admin"]}
			}
		}
	}`, schema())
	if runErr != nil || decodeErr != nil {
		t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
	}

	_, endpoints, err := got.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("the document declares two endpoints and %d resolved: %+v", len(endpoints), endpoints)
	}
	proxies := []string{"10.0.0.0/8", "192.0.2.7"}
	headers := []string{"X-Forwarded-For", "X-Real-IP"}
	for _, e := range endpoints {
		if !slices.Equal(e.trustedProxies, proxies) {
			t.Errorf("the endpoint %q resolved to trusted-proxies %v, want the module-level %v",
				e.name, e.trustedProxies, proxies)
		}
		if !slices.Equal(e.remoteIPHeaders, headers) {
			t.Errorf("the endpoint %q resolved to remote-ip-headers %v, want the module-level %v",
				e.name, e.remoteIPHeaders, headers)
		}
	}
}

// TestEndpointProxySettingsOverrideTheModuleLevelOnes pins the other half of the inheritance: an
// endpoint that writes either key takes its own value and leaves the module-level one alone.
func TestEndpointProxySettingsOverrideTheModuleLevelOnes(t *testing.T) {
	got, decodeErr, runErr := readThrough(t, `{
		"http": {
			"listeners": {"backend": {"address": ":8080"}},
			"trusted-proxies": ["10.0.0.0/8"],
			"remote-ip-headers": ["X-Forwarded-For"],
			"endpoints": {
				"custom": {
					"listener": "backend",
					"prefixes": ["/custom"],
					"trusted-proxies": ["192.0.2.0/24"],
					"remote-ip-headers": ["Fly-Client-IP"]
				},
				"inheriting": {"listener": "backend", "prefixes": ["/inheriting"]}
			}
		}
	}`, schema())
	if runErr != nil || decodeErr != nil {
		t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
	}

	_, endpoints, err := got.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	byName := make(map[string]endpointSettings, len(endpoints))
	for _, e := range endpoints {
		byName[e.name] = e
	}
	if want := []string{"192.0.2.0/24"}; !slices.Equal(byName["custom"].trustedProxies, want) {
		t.Errorf("the endpoint naming its own trusted-proxies resolved to %v, want %v",
			byName["custom"].trustedProxies, want)
	}
	if want := []string{"Fly-Client-IP"}; !slices.Equal(byName["custom"].remoteIPHeaders, want) {
		t.Errorf("the endpoint naming its own remote-ip-headers resolved to %v, want %v",
			byName["custom"].remoteIPHeaders, want)
	}
	if want := []string{"10.0.0.0/8"}; !slices.Equal(byName["inheriting"].trustedProxies, want) {
		t.Errorf("the endpoint omitting trusted-proxies resolved to %v, want the module-level %v",
			byName["inheriting"].trustedProxies, want)
	}
	if want := []string{"X-Forwarded-For"}; !slices.Equal(byName["inheriting"].remoteIPHeaders, want) {
		t.Errorf("the endpoint omitting remote-ip-headers resolved to %v, want the module-level %v",
			byName["inheriting"].remoteIPHeaders, want)
	}
}

// TestAnExplicitEmptyEndpointTrustedProxiesOverridesTheModuleLevel pins the difference between
// omitting the key and writing an empty list through the real configuration path.
func TestAnExplicitEmptyEndpointTrustedProxiesOverridesTheModuleLevel(t *testing.T) {
	got, decodeErr, runErr := readThrough(t, `{
		"http": {
			"listeners": {"backend": {"address": ":8080"}},
			"trusted-proxies": ["10.0.0.0/8"],
			"endpoints": {
				"untrusting": {
					"listener": "backend",
					"prefixes": ["/untrusting"],
					"trusted-proxies": []
				},
				"inheriting": {"listener": "backend", "prefixes": ["/inheriting"]}
			}
		}
	}`, schema())
	if runErr != nil || decodeErr != nil {
		t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
	}

	_, endpoints, err := got.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	byName := make(map[string]endpointSettings, len(endpoints))
	for _, e := range endpoints {
		byName[e.name] = e
	}
	untrusting := byName["untrusting"].trustedProxies
	if untrusting == nil {
		t.Error("an endpoint writing trusted-proxies [] resolved to a nil list, which is how an omitted key reads")
	}
	if len(untrusting) != 0 {
		t.Errorf("an endpoint writing trusted-proxies [] resolved to %v, want none", untrusting)
	}
	if want := []string{"10.0.0.0/8"}; !slices.Equal(byName["inheriting"].trustedProxies, want) {
		t.Errorf("the sibling omitting trusted-proxies resolved to %v, want the module-level %v",
			byName["inheriting"].trustedProxies, want)
	}
}

// TestAnInvalidModuleLevelTrustedProxyIsStartupFailure pins the validation of the module-level
// networks: a mistyped one is refused while the configuration is resolved.
func TestAnInvalidModuleLevelTrustedProxyIsStartupFailure(t *testing.T) {
	got, decodeErr, runErr := readThrough(t, `{
		"http": {
			"listeners": {"backend": {"address": ":8080"}},
			"trusted-proxies": ["10.0.0.0/8", "not-a-network"],
			"endpoints": {"public": {"listener": "backend"}}
		}
	}`, schema())
	if runErr != nil || decodeErr != nil {
		t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
	}
	_, _, err := got.resolve()
	if err == nil {
		t.Fatal("a module-level trusted-proxies entry that is not an address or a block was accepted")
	}
	mustContain(t, err.Error(), "http.trusted-proxies", "the module-level key that was written")
	mustContain(t, err.Error(), "not-a-network", "the value that is not a network")
}

// TestAnUnusableClaimPrefixIsAValueError pins each rule a single claimed prefix has to meet, and
// the shape of the failure: a value error carrying the complete key path and the refused value.
func TestAnUnusableClaimPrefixIsAValueError(t *testing.T) {
	cases := []struct {
		name     string
		prefixes []string
		want     []string
	}{
		{"empty string", []string{""}, []string{"prefixes[0]", `""`}},
		{"no leading slash", []string{"api"}, []string{"prefixes[0]", `"api"`}},
		{"trailing slash", []string{"/api/"}, []string{"prefixes[0]", `"/api/"`}},
		{"empty segment", []string{"/api//v1"}, []string{"prefixes[0]", `"/api//v1"`}},
		{"dot segment", []string{"/api/./v1"}, []string{"prefixes[0]", `"/api/./v1"`}},
		{"dot-dot segment", []string{"/api/../v1"}, []string{"prefixes[0]", `"/api/../v1"`}},
		{"parameter segment", []string{"/api/:id"}, []string{"prefixes[0]", `"/api/:id"`}},
		{"wildcard segment", []string{"/api/*rest"}, []string{"prefixes[0]", `"/api/*rest"`}},
		{"percent", []string{"/api%2Fv1"}, []string{"prefixes[0]", "contains"}},
		{"query", []string{"/api?x"}, []string{"prefixes[0]", "contains"}},
		{"fragment", []string{"/api#x"}, []string{"prefixes[0]", "contains"}},
		{"space", []string{"/api v1"}, []string{"prefixes[0]", "whitespace"}},
		{"control", []string{"/api\x01"}, []string{"prefixes[0]", "control character"}},
		{"empty list", []string{}, []string{"http.endpoints.public.prefixes", "[]"}},
		{"duplicate", []string{"/api", "/api"}, []string{"prefixes[1]", `"/api"`}},
		{"ancestor", []string{"/api", "/api/v1"}, []string{"prefixes[1]", `"/api/v1"`, `"/api"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := moduleConfig{
				Listeners: map[string]listenerConfig{"backend": {Address: ":8080"}},
				Endpoints: map[string]endpointConfig{
					"public": {Listener: "backend", Prefixes: tc.prefixes},
				},
			}.resolve()
			if err == nil {
				t.Fatalf("the claim list %q was accepted", tc.prefixes)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("resolving the claim list %q returned %v, want ErrInvalidConfig", tc.prefixes, err)
			}
			mustContain(t, err.Error(), "http.endpoints.public.prefixes", "the key path of the claim list")
			for _, want := range tc.want {
				mustContain(t, err.Error(), want, "the refused value locating the mistake")
			}
		})
	}
}

// TestOmittedClaimPrefixesClaimTheRoot pins the default: an endpoint that does not write prefixes
// claims "/", which makes it its listener's fallback.
func TestOmittedClaimPrefixesClaimTheRoot(t *testing.T) {
	_, endpoints, err := moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: ":8080"}},
		Endpoints: map[string]endpointConfig{"public": {Listener: "backend"}},
	}.resolve()
	if err != nil {
		t.Fatalf("resolving an endpoint without prefixes: %v", err)
	}
	if want := []string{"/"}; !slices.Equal(endpoints[0].prefixes, want) {
		t.Errorf("an endpoint that omitted prefixes resolved to %v, want %v", endpoints[0].prefixes, want)
	}
}

// TestRootAndADeeperClaimOnOneEndpointAreLegal pins that "/" may stand beside a non- root claim in
// one endpoint's list: the endpoint answers the paths no one else claims.
func TestRootAndADeeperClaimOnOneEndpointAreLegal(t *testing.T) {
	_, endpoints, err := moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: ":8080"}},
		Endpoints: map[string]endpointConfig{
			"web":   {Listener: "backend", Prefixes: []string{"/", "/api"}},
			"admin": {Listener: "backend", Prefixes: []string{"/admin"}},
		},
	}.resolve()
	if err != nil {
		t.Fatalf("resolving an endpoint claiming \"/\" and \"/api\": %v", err)
	}
	claims := make(map[string][]string, len(endpoints))
	for _, e := range endpoints {
		claims[e.name] = e.prefixes
	}
	if want := []string{"/", "/api"}; !slices.Equal(claims["web"], want) {
		t.Errorf("the endpoint claiming both resolved to %v, want %v", claims["web"], want)
	}
}

// TestTwoEndpointsClaimingOnePrefixIsAClaimConflict pins the cross-entry check and its sentinel.
func TestTwoEndpointsClaimingOnePrefixIsAClaimConflict(t *testing.T) {
	for name, cfg := range map[string]moduleConfig{
		"the same prefix": {
			Listeners: map[string]listenerConfig{"backend": {Address: ":8080"}},
			Endpoints: map[string]endpointConfig{
				"a": {Listener: "backend", Prefixes: []string{"/api"}},
				"b": {Listener: "backend", Prefixes: []string{"/api"}},
			},
		},
		"both omitted": {
			Listeners: map[string]listenerConfig{"backend": {Address: ":8080"}},
			Endpoints: map[string]endpointConfig{
				"a": {Listener: "backend"},
				"b": {Listener: "backend"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := cfg.resolve()
			if !errors.Is(err, ErrClaimConflict) {
				t.Fatalf("the configuration resolved with %v, want ErrClaimConflict", err)
			}
			mustContain(t, err.Error(), `"backend"`, "the listener carrying both endpoints")
			mustContain(t, err.Error(), `"a"`, "the first endpoint claiming the prefix")
			mustContain(t, err.Error(), `"b"`, "the second endpoint claiming the prefix")
			mustContain(t, err.Error(), "prefixes", "the key the failure tells the host to write")
		})
	}
}

// TestTheClaimConflictAdviceResolvesTheConflict pins that the example in the failure message is
// itself a fix.
func TestTheClaimConflictAdviceResolvesTheConflict(t *testing.T) {
	cases := map[string]struct {
		endpoints map[string]endpointConfig
		// rootConflict marks the conflict on "/", the only one an omitted key
		// can cause; its message must say so, and the other must not.
		rootConflict bool
	}{
		"both omitted": {
			endpoints: map[string]endpointConfig{
				"a": {Listener: "backend"},
				"b": {Listener: "backend"},
			},
			rootConflict: true,
		},
		"the same prefix": {
			endpoints: map[string]endpointConfig{
				"a": {Listener: "backend", Prefixes: []string{"/api"}},
				"b": {Listener: "backend", Prefixes: []string{"/api"}},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			listeners := map[string]listenerConfig{"backend": {Address: ":8080"}}
			_, _, err := moduleConfig{Listeners: listeners, Endpoints: tc.endpoints}.resolve()
			if !errors.Is(err, ErrClaimConflict) {
				t.Fatalf("the configuration resolved with %v, want ErrClaimConflict", err)
			}
			if hasHint := strings.Contains(err.Error(), "omits prefixes"); hasHint != tc.rootConflict {
				t.Errorf("the message mentions omitted prefixes = %t, want %t: %v",
					hasHint, tc.rootConflict, err)
			}

			endpoint, prefixes := suggestedClaim(t, err.Error())
			fixed := make(map[string]endpointConfig, len(tc.endpoints))
			for endpointName, e := range tc.endpoints {
				fixed[endpointName] = e
			}
			rewritten := fixed[endpoint]
			rewritten.Prefixes = prefixes
			fixed[endpoint] = rewritten
			if _, _, err := (moduleConfig{Listeners: listeners, Endpoints: fixed}).resolve(); err != nil {
				t.Fatalf("writing the suggested %s.%s.%s.prefixes: %q still fails to resolve: %v",
					configNamespace, endpointsItem, endpoint, prefixes, err)
			}
		})
	}
}

// suggestedClaim reads the example the claim-conflict message writes for one
// endpoint: the endpoint's name and the prefix list the message tells the host
// to put there. It fails when the message carries no example, so rewording the
// message cannot silently stop the example from being checked.
func suggestedClaim(t *testing.T, message string) (string, []string) {
	t.Helper()
	pattern := regexp.MustCompile(regexp.QuoteMeta(configNamespace+"."+endpointsItem+".") +
		`([A-Za-z0-9_-]+)\.prefixes: \[("(?:[^"\\]|\\.)*")\]`)
	match := pattern.FindStringSubmatch(message)
	if match == nil {
		t.Fatalf("the conflict message names no endpoint prefixes example: %s", message)
	}
	prefix, err := strconv.Unquote(match[2])
	if err != nil {
		t.Fatalf("unquoting the suggested prefix %s: %v", match[2], err)
	}
	return match[1], []string{prefix}
}

// TestTwoListenersMayClaimTheSamePrefix pins that the conflict check is per listener.
func TestTwoListenersMayClaimTheSamePrefix(t *testing.T) {
	_, _, err := moduleConfig{
		Listeners: map[string]listenerConfig{
			"a": {Address: ":8080"},
			"b": {Address: ":8081"},
		},
		Endpoints: map[string]endpointConfig{
			"one": {Listener: "a", Prefixes: []string{"/api"}},
			"two": {Listener: "b", Prefixes: []string{"/api"}},
		},
	}.resolve()
	if err != nil {
		t.Fatalf("two listeners claiming the same prefix on their own endpoints: %v", err)
	}
}

// TestAnExplicitlyEmptyClaimListIsRefusedAfterDecode pins the difference between omitting the key
// and writing an empty list through the real configuration path.
func TestAnExplicitlyEmptyClaimListIsRefusedAfterDecode(t *testing.T) {
	got, decodeErr, runErr := readThrough(t, `{
		"http": {
			"listeners": {"backend": {"address": ":8080"}},
			"endpoints": {"public": {"listener": "backend", "prefixes": []}}
		}
	}`, schema())
	if decodeErr != nil || runErr != nil {
		t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
	}
	_, _, err := got.resolve()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("an explicitly empty claim list resolved with %v, want ErrInvalidConfig", err)
	}
	mustContain(t, err.Error(), "http.endpoints.public.prefixes", "the key path of the empty list")
	mustContain(t, err.Error(), "[]", "the value that was refused")
}
