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

// section renders this module's section out of the two required items and the
// given override.
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

// body renders a whole configuration document holding this module's section.
func body(t *testing.T, s map[string]any) string {
	t.Helper()
	doc := map[string]any{"mail": map[string]any{"smtp": s}}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// readBody loads a document with the real configuration loader and decodes this
// module's section, then resolves it: the error the module would refuse the
// startup with.
func readBody(t *testing.T, document string) (resolved, error) {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

// TestACompleteSectionResolves is the control: the two required items and the
// defaults resolve into the connection settings the module runs on.
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

// TestEachRequiredItemIsRequired holds that a section leaving out host or from
// fails the load.
func TestEachRequiredItemIsRequired(t *testing.T) {
	for _, item := range []string{"host", "from"} {
		t.Run(item, func(t *testing.T) {
			_, err := readBody(t, body(t, section(map[string]any{item: nil})))
			if !errors.Is(err, config.ErrMissingRequired) {
				t.Fatalf("a section with no %s was accepted: %v", item, err)
			}
		})
	}
}

// TestAnUnknownTLSValueIsRefused holds that the posture is one of the three named
// and a value outside the set is a startup failure naming the item.
func TestAnUnknownTLSValueIsRefused(t *testing.T) {
	_, err := readBody(t, body(t, section(map[string]any{"tls": "plaintext"})))
	if !errors.Is(err, mail.ErrInvalidConfig) {
		t.Fatalf("an unknown tls value gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "tls") {
		t.Errorf("the failure %q does not name the tls item", err.Error())
	}
}

// TestEachTLSValueIsAccepted holds the control: every value a host may write
// resolves. The values are spelled out rather than taken from this package's own
// constants, because they are the configuration contract a host writes down: a
// renamed constant that every other case followed silently fails here.
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

// TestAUsernameWithNoPasswordIsRefused holds that AUTH is all-or-nothing: an
// identity with no secret is a startup failure rather than a silent no-AUTH.
func TestAUsernameWithNoPasswordIsRefused(t *testing.T) {
	_, err := readBody(t, body(t, section(map[string]any{"username": "the-user"})))
	if !errors.Is(err, mail.ErrInvalidConfig) {
		t.Fatalf("a username with no password gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), passwordItem) {
		t.Errorf("the failure %q does not name the password item", err.Error())
	}
}

// TestASenderThatDoesNotParseIsRefused holds that the default sender is checked
// at startup, naming the item and not repeating the address.
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

// TestAnAllowedSenderThatDoesNotParseIsRefused holds that every additional
// allowed sender is checked at startup too.
func TestAnAllowedSenderThatDoesNotParseIsRefused(t *testing.T) {
	_, err := readBody(t, body(t, section(map[string]any{"from-allowed": []string{"fine@example.com", "bad"}})))
	if !errors.Is(err, mail.ErrInvalidConfig) {
		t.Fatalf("an allowed sender that does not parse gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), fromAllowedItem) {
		t.Errorf("the failure %q does not name the from-allowed item", err.Error())
	}
}

// TestTheSchemaDeclaresTheSensitiveAndContainerItems holds the declaration: the
// password is sensitive, the allowed set and the two required items carry their
// flags, and the default port and posture come from the prototype.
func TestTheSchemaDeclaresTheSensitiveAndContainerItems(t *testing.T) {
	resources := Module().Resources
	if len(resources) != 1 {
		t.Fatalf("the module carries %d resources, want one schema", len(resources))
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
	if !s.Items[hostItem].Required || !s.Items[fromItem].Required {
		t.Error("host or from is not declared required")
	}
	if got := s.Items[fromAllowedItem].Origins; got != config.OriginPrimary {
		t.Errorf("from-allowed takes the origins %v, want the primary source alone", got)
	}
	if configDefaults.Port != defaultPort || configDefaults.TLS != tlsStartTLS {
		t.Errorf("the defaults are port %d, tls %q, want %d, %q",
			configDefaults.Port, configDefaults.TLS, defaultPort, tlsStartTLS)
	}
}

// TestTheAllowedSetComparesOnTheAddrSpec holds that a display name does not
// change whether a sender is accepted: the string's addr-spec is what is
// checked.
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
