package token

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// envPrefix names the variables the loader in these cases reads under. It is one
// no machine carries variables under, so only what a case sets reaches the
// values.
const envPrefix = "SPEEDTOKENTEST"

// baseConfig is a configuration that resolves, so that a case can change one
// item and still reach the check it means to exercise.
func baseConfig() moduleConfig {
	return moduleConfig{ClientID: clientIDDefault, Algorithm: algES256}
}

func TestAnEmptyAudienceIsRefused(t *testing.T) {
	_, err := baseConfig().resolve(gateMount{})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("an audience with nothing to default to gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), audienceItem) {
		t.Errorf("the refusal %q does not name the %s item", err, audienceItem)
	}
}

func TestTheIssuerAndAudienceDefaultToThePublicAddress(t *testing.T) {
	const public = "https://auth.example.com"
	cfg, err := baseConfig().resolve(gateMount{PublicURL: public})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if cfg.issuer != public {
		t.Errorf("the issuer is %q, want the public address %q", cfg.issuer, public)
	}
	if len(cfg.audience) != 1 || cfg.audience[0] != public {
		t.Errorf("the audience is %v, want [%s]", cfg.audience, public)
	}
}

func TestAConfiguredAudienceAndIssuerWin(t *testing.T) {
	cfg := baseConfig()
	cfg.Issuer = "https://issuer.example.com"
	cfg.Audience = []string{"https://one.example.com", "https://two.example.com"}
	got, err := cfg.resolve(gateMount{PublicURL: "https://public.example.com"})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got.issuer != cfg.Issuer {
		t.Errorf("the issuer is %q, want the configured %q", got.issuer, cfg.Issuer)
	}
	if len(got.audience) != 2 || got.audience[0] != cfg.Audience[0] {
		t.Errorf("the audience is %v, want the configured %v", got.audience, cfg.Audience)
	}
}

func TestAnUnsupportedAlgorithmIsRefused(t *testing.T) {
	cfg := baseConfig()
	cfg.Algorithm = "HS256"
	_, err := cfg.resolve(gateMount{PublicURL: "https://auth.example.com"})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a symmetric algorithm gave %v, want ErrInvalidConfig", err)
	}
}

// The session check is a setting, not a configuration item: its declaration
// carries a domain and the strict value as its default.
func TestTheRelaxationSwitchIsASettingAndNotAConfigurationItem(t *testing.T) {
	if _, ok := schema().Items[checkSessionKey]; ok {
		t.Errorf("the configuration declares %s, want it declared as a setting alone", checkSessionKey)
	}
	item, ok := settingsSchema().Items[checkSessionKey]
	if !ok {
		t.Fatalf("the settings schema does not declare %s", checkSessionKey)
	}
	if item.Domain.Rule() == "" {
		t.Errorf("the %s setting carries no domain", checkSessionKey)
	}
	if !defaults.CheckSession {
		t.Errorf("the declared default of %s is false, want the strict value true", checkSessionKey)
	}
}

func TestTheSigningKeyFileIsSensitiveAndTakesNoCommandLine(t *testing.T) {
	item, ok := schema().Items[signingKeyFileItem]
	if !ok {
		t.Fatalf("the schema does not declare %s", signingKeyFileItem)
	}
	if !item.Sensitive {
		t.Errorf("the %s item is not marked sensitive", signingKeyFileItem)
	}
	if item.Description == "" {
		t.Errorf("the sensitive %s item carries no description", signingKeyFileItem)
	}
	if item.Origins&config.OriginFlag != 0 {
		t.Errorf("the %s item takes a command-line argument, which a process listing shows", signingKeyFileItem)
	}
}

// loadConfig runs the real loader over a JSON primary source holding body, with
// args as the command line, and resolves this module's configuration from the
// result. The environment is the process's own, which a case steers with
// t.Setenv. The gateway's module is registered so the public address the issuer
// defaults to is declared.
func loadConfig(t *testing.T, body string, args ...string) (resolved, error) {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(gate.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: envPrefix, DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	reader, err := config.Load(t.Context(), reg, args)
	if err != nil {
		return resolved{}, err
	}
	return readConfig(reader)
}

// TestAnEnvironmentVariableCannotTurnTheSessionCheckOff holds that the session
// check takes no value from the environment. A variable on an item that does
// read the environment is the control: without it a variable name that was
// never going to be read would pass the case.
func TestAnEnvironmentVariableCannotTurnTheSessionCheckOff(t *testing.T) {
	const issuerBody = `{"authn":{"token":{"issuer":"https://auth.example.com"}}}`

	t.Run("the control: an item that takes the environment", func(t *testing.T) {
		t.Setenv(envPrefix+"_AUTHN__TOKEN__ISSUER", "https://env.example.com")
		cfg, err := loadConfig(t, `{}`)
		if err != nil {
			t.Fatalf("loading: %v", err)
		}
		if cfg.issuer != "https://env.example.com" {
			t.Fatalf("the issuer reads %q, so the variable name used here is not one the loader reads "+
				"and the case below proves nothing", cfg.issuer)
		}
	})

	t.Run("the session check", func(t *testing.T) {
		t.Setenv(envPrefix+"_AUTHN__TOKEN__CHECK_SESSION", "false")
		if _, err := loadConfig(t, issuerBody); err != nil {
			t.Fatalf("loading: %v", err)
		}
		if !defaults.CheckSession {
			t.Error("a variable turned the session check off")
		}
	})
}

// TestACommandLineArgumentCannotTurnTheSessionCheckOff holds the same setting
// against the command line. It is not a configuration item, so the loader
// refuses the argument as a key no module declares.
func TestACommandLineArgumentCannotTurnTheSessionCheckOff(t *testing.T) {
	_, err := loadConfig(t, `{"authn":{"token":{"issuer":"https://auth.example.com"}}}`,
		"--authn.token.check-session=false")
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Fatalf("the argument loaded with %v, want config.ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), checkSessionPath) {
		t.Errorf("loading failed with %q, which does not name %s", err, checkSessionPath)
	}
}

// TestAnRS256ConfigurationResolves holds that RS256 is a supported value of the
// algorithm item, not only ES256.
func TestAnRS256ConfigurationResolves(t *testing.T) {
	cfg := baseConfig()
	cfg.Algorithm = algRS256
	got, err := cfg.resolve(gateMount{PublicURL: "https://auth.example.com"})
	if err != nil {
		t.Fatalf("an RS256 configuration gave %v, want it to resolve", err)
	}
	if got.algorithm != algRS256 {
		t.Errorf("the algorithm is %q, want RS256", got.algorithm)
	}
}

// TestAnEmptyIssuerWithAnAudienceIsRefused holds the issuer item's own refusal:
// an audience that is configured keeps the audience branch from firing, so an
// empty issuer with no public address to default it to is refused, naming the
// issuer.
func TestAnEmptyIssuerWithAnAudienceIsRefused(t *testing.T) {
	cfg := baseConfig()
	cfg.Audience = []string{"https://api.example.com"}
	cfg.Issuer = ""
	_, err := cfg.resolve(gateMount{})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("an empty issuer gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), issuerItem) {
		t.Errorf("the refusal %q does not name the %s item", err, issuerItem)
	}
}

// TestAnEmptyClientIDIsRefused holds the client-id item's refusal: an explicit
// empty client-id is refused rather than signing a token with an empty
// client_id.
func TestAnEmptyClientIDIsRefused(t *testing.T) {
	cfg := baseConfig()
	cfg.Issuer = "https://iss.example.com"
	cfg.Audience = []string{"https://api.example.com"}
	cfg.ClientID = ""
	_, err := cfg.resolve(gateMount{})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("an empty client-id gave %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), clientIDItem) {
		t.Errorf("the refusal %q does not name the %s item", err, clientIDItem)
	}
}

// TestBlankAudienceAndVerifyFileEntriesAreDropped holds that blank and
// whitespace-only entries are dropped from both lists.
func TestBlankAudienceAndVerifyFileEntriesAreDropped(t *testing.T) {
	cfg := baseConfig()
	cfg.Issuer = "https://iss.example.com"
	cfg.Audience = []string{"", "  ", "https://api.example.com"}
	cfg.VerifyKeyFiles = []string{"", "  ", "/tmp/k.pem"}
	got, err := cfg.resolve(gateMount{})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(got.audience) != 1 || got.audience[0] != "https://api.example.com" {
		t.Errorf("the audience is %v, want the one non-blank entry", got.audience)
	}
	if len(got.verifyFiles) != 1 || got.verifyFiles[0] != "/tmp/k.pem" {
		t.Errorf("the verify files are %v, want the one non-blank entry", got.verifyFiles)
	}
}

// TestTheDeclaredAlgorithmIsES256 holds the default the schema declares: a
// configuration that names no algorithm resolves to ES256.
func TestTheDeclaredAlgorithmIsES256(t *testing.T) {
	cfg, err := loadConfig(t, `{"authn":{"token":{"issuer":"https://auth.example.com"}}}`)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if cfg.algorithm != algES256 {
		t.Errorf("the default algorithm is %q, want ES256", cfg.algorithm)
	}
}
