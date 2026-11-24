package smtp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/mail"
)

func section(override map[string]any) map[string]any {
	out := map[string]any{
		"host": "relay.example.com",
		"from": defaultSender,
	}
	for key, value := range override {
		if value == nil {
			delete(out, key)
			continue
		}
		out[key] = value
	}
	return out
}

func body(t *testing.T, s map[string]any) string {
	t.Helper()
	doc := map[string]any{"mail": map[string]any{"smtp": s}}
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
	if _, err := stance(&cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

// TestACompleteSectionResolves checks that the required items and the defaults
// resolve into the connection settings and allowed sender set.
func TestACompleteSectionResolves(t *testing.T) {
	got, err := readBody(t, body(t, section(nil)))
	if err != nil {
		t.Fatalf("resolving a complete section: %v", err)
	}
	if got.host != "relay.example.com" || got.from != defaultSender {
		t.Errorf("the connection settings did not survive: %+v", got)
	}
	if got.port != 587 || got.tls != "starttls" {
		t.Errorf("the defaults did not apply: port %d, tls %q", got.port, got.tls)
	}
	if !got.allowed[defaultSender] {
		t.Errorf("the default sender is not in the allowed set: %v", got.allowed)
	}
}

// TestEachIncompleteSectionIsRefused checks that a section missing host or from is
// refused with the missing item named.
func TestEachIncompleteSectionIsRefused(t *testing.T) {
	for _, item := range []string{"host", "from"} {
		t.Run(item, func(t *testing.T) {
			_, err := readBody(t, body(t, section(map[string]any{item: nil})))
			if !errors.Is(err, mail.ErrInvalidConfig) {
				t.Fatalf("a section with no %s was accepted: %v", item, err)
			}
			if !strings.Contains(err.Error(), item) {
				t.Errorf("the failure %q does not name %s", err.Error(), item)
			}
		})
	}
}

// TestAnUnknownTLSValueIsRefused checks that a posture outside the three named
// values is refused naming the item.
func TestAnUnknownTLSValueIsRefused(t *testing.T) {
	_, err := readBody(t, body(t, section(map[string]any{"tls": "plaintext"})))
	if !errors.Is(err, mail.ErrInvalidConfig) {
		t.Fatalf("an unknown tls value gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "tls") {
		t.Errorf("the failure %q does not name the tls item", err.Error())
	}
}

// TestEachTLSValueIsAccepted checks that every spelled-out posture resolves.
func TestEachTLSValueIsAccepted(t *testing.T) {
	for _, value := range []string{"starttls", "implicit", "none"} {
		t.Run(value, func(t *testing.T) {
			got, err := readBody(t, body(t, section(map[string]any{"tls": value})))
			if err != nil {
				t.Fatalf("tls %q was refused: %v", value, err)
			}
			if got.tls != value {
				t.Errorf("resolved tls is %q, want %q", got.tls, value)
			}
		})
	}
}

// TestAUsernameWithNoPasswordIsRefused checks that an AUTH identity without a
// secret is a startup failure.
func TestAUsernameWithNoPasswordIsRefused(t *testing.T) {
	_, err := readBody(t, body(t, section(map[string]any{"username": "the-user"})))
	if !errors.Is(err, mail.ErrInvalidConfig) {
		t.Fatalf("a username with no password gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), passwordItem) {
		t.Errorf("the failure %q does not name the password item", err.Error())
	}
}

// TestASenderThatDoesNotParseIsRefused checks that an unparseable default sender
// is refused without the address repeating.
func TestASenderThatDoesNotParseIsRefused(t *testing.T) {
	const bad = "not-an-address"
	_, err := readBody(t, body(t, section(map[string]any{"from": bad})))
	if !errors.Is(err, mail.ErrInvalidConfig) {
		t.Fatalf("a sender that does not parse gave %v, want ErrInvalidConfig", err)
	}
	if strings.Contains(err.Error(), bad) {
		t.Errorf("the failure repeats the address %q", err.Error())
	}
}

// TestAnAllowedSenderThatDoesNotParseIsRefused checks that every additional
// allowed sender is checked at startup.
func TestAnAllowedSenderThatDoesNotParseIsRefused(t *testing.T) {
	_, err := readBody(t, body(t, section(map[string]any{"from-allowed": []string{"fine@example.com", "bad"}})))
	if !errors.Is(err, mail.ErrInvalidConfig) {
		t.Fatalf("an allowed sender that does not parse gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), fromAllowedItem) {
		t.Errorf("the failure %q does not name the from-allowed item", err.Error())
	}
}

// TestTheSchemaDeclaresTheSensitiveAndContainerItems checks the password's
// sensitivity, the item flags and the prototype defaults.
func TestTheSchemaDeclaresTheSensitiveAndContainerItems(t *testing.T) {
	resources := Module().Resources
	if len(resources) != 2 {
		t.Fatalf("the module carries %d resources, want the schema and the carrier name", len(resources))
	}
	s, ok := resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module carries %T, want a config.Schema", resources[0])
	}
	if s.Namespace != ModuleName {
		t.Errorf("Namespace = %q, want %q", s.Namespace, ModuleName)
	}
	if password := s.Items[passwordItem]; !password.Sensitive || password.Description == "" {
		t.Errorf("password = %+v, want sensitive and described", password)
	}
	if s.Items[hostItem].Required || s.Items[fromItem].Required {
		t.Error("host or from is declared required; the stance judges an unconfigured carrier instead")
	}
	if got := s.Items[fromAllowedItem].Origins; got != config.OriginPrimary {
		t.Errorf("from-allowed takes the origins %v, want the primary source alone", got)
	}
	if configDefaults.Port != defaultPort || configDefaults.TLS != tlsStartTLS {
		t.Errorf("the defaults are port %d, tls %q, want %d, %q",
			configDefaults.Port, configDefaults.TLS, defaultPort, tlsStartTLS)
	}
}

// TestTheAllowedSetComparesOnTheAddrSpec checks that a display name does not
// change whether a sender is accepted.
func TestTheAllowedSetComparesOnTheAddrSpec(t *testing.T) {
	got, err := readBody(t, body(t, section(map[string]any{
		"from-allowed": []string{"Alias <" + otherSender + ">"},
	})))
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	for _, addr := range []string{defaultSender, otherSender} {
		if !got.allowed[addr] {
			t.Errorf("the allowed set does not hold %q: %v", addr, got.allowed)
		}
	}
}
