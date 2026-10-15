package static_test

// This file holds the reverse of the redaction obligation, pinned where a case
// can reach the modules it is about. The rule is that a value which is a path
// and not a secret is not registered as a literal redaction rule: the saml
// backend's sp-key-file and the token backend's signing-key-file name a file,
// and the secret is that file's content. Those two modules are driven here,
// through a real registry, with a path configured, against a logging capability
// that records every rule registered with it; the case holds that the capability
// took none. A regression that registered the path — as a literal rule, through
// the logging capability rather than the literal package — is caught here.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/sso/saml"
	"github.com/vislake/speed/pkg/authn/token"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
)

// The names of the stand-in modules a driven registry is built from.
const (
	providersName = "stand-in.providers"
	accountsName  = "stand-in.accounts"
	sessionsName  = "stand-in.sessions"
	passwordsName = "stand-in.passwords"
	throttleName  = "stand-in.throttle"
	databaseName  = "stand-in.database"
	mounterName   = "stand-in.mounter"
	configName    = "stand-in.config"
	logName       = "stand-in.log"
)

// registeredRules is a logging capability's registration face that keeps the
// name of every rule registered with it.
type registeredRules struct{ names []string }

func (r *registeredRules) AddKeys(...string) {}

func (r *registeredRules) AddPattern(name string, _ log.Matcher) { r.names = append(r.names, name) }

// captureLogger is the logging capability a case supplies, so a rule registered
// with it is seen.
type captureLogger struct{ rules *registeredRules }

func (l captureLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (l captureLogger) Redaction() log.Redaction  { return l.rules }

// The stand-ins a driven registry supplies, each delivering one required
// capability and called by nothing here. They embed the interface so no method
// has to be written.
type (
	noProviders struct{ authn.SSOProviders }
	noAccounts  struct{ authn.Accounts }
	noSessions  struct{ authn.Sessions }
	noPasswords struct{ authn.Passwords }
	noThrottle  struct{ authn.Throttle }
)

// RememberAssertion makes noProviders the register the saml method requires: the
// method refuses a register that cannot record a consumed assertion.
func (noProviders) RememberAssertion(context.Context, string, string, time.Time) error { return nil }

// declaring builds the stand-in module that declares one capability.
func declaring(name string, tok core.Token) core.Module {
	return core.Module{Name: name, Provides: []core.Provision{{Token: tok}}}
}

// reverseReader builds the real configuration loader over body, with the modules
// whose sections the case configures registered so their declared items are
// known.
func reverseReader(t *testing.T, body string) config.Reader {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDREVERSETEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(saml.Module())
	reg.Register(token.Module())
	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}
	return reader
}

// registry is the registry a case drives, with the config reader and the logging
// capability supplied and the stand-in modules declared.
func registry(t *testing.T, body string) (*core.Registry, *registeredRules) {
	t.Helper()
	rules := &registeredRules{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(declaring(providersName, (*authn.SSOProviders)(nil)))
	reg.Register(declaring(accountsName, (*authn.Accounts)(nil)))
	reg.Register(declaring(sessionsName, (*authn.Sessions)(nil)))
	reg.Register(declaring(passwordsName, (*authn.Passwords)(nil)))
	reg.Register(declaring(throttleName, (*authn.Throttle)(nil)))
	reg.Register(declaring(databaseName, (*db.Database)(nil)))
	reg.Register(declaring(mounterName, (*mount.Mounter)(nil)))
	reg.Register(declaring(logName, (*log.Logger)(nil)))
	reg.Supply(reverseReader(t, body), config.ModuleName)
	reg.Supply(noProviders{}, providersName)
	reg.Supply(noAccounts{}, accountsName)
	reg.Supply(noSessions{}, sessionsName)
	reg.Supply(noPasswords{}, passwordsName)
	reg.Supply(noThrottle{}, throttleName)
	reg.Supply(dbtest.SQLite(t, filepath.Join(t.TempDir(), "reverse.db")+"?_pragma=busy_timeout(5000)", nil), databaseName)
	reg.Supply(mounttest.New(t, mounttest.WithPublicURL("https://auth.example.com"), mounttest.WithBase("/auth")), mounterName)
	reg.Supply(captureLogger{rules: rules}, logName)
	return reg, rules
}

// TestTheSAMLModuleRegistersNoLiteralRedactionRule holds the saml backend to
// registering nothing, with sp-key-file configured to a real key file: the item
// is marked sensitive — config keeps it out of its own output — but its value is
// a path, and the module holds no secret configuration value to register.
func TestTheSAMLModuleRegistersNoLiteralRedactionRule(t *testing.T) {
	keyPath, certPath := spKeyPair(t)
	body := fmt.Sprintf(`{"authn":{"sso":{"saml":{"sp-key-file":%q,"sp-cert-file":%q}}}}`, keyPath, certPath)
	reg, rules := registry(t, body)
	reg.Register(saml.Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth: core.StageNew,
		Participants: []string{
			saml.ModuleName, providersName, accountsName, mounterName,
			config.ModuleName, logName,
		},
	})
	if err != nil {
		t.Fatalf("driving the saml module: %v", err)
	}
	requireNoRule(t, rules, saml.ModuleName)
}

// TestTheTokenModuleRegistersNoLiteralRedactionRule holds the token backend to
// registering nothing, with signing-key-file configured to a real key file: the
// item is marked sensitive, but its value is a path, and the module holds no
// secret configuration value to register.
func TestTheTokenModuleRegistersNoLiteralRedactionRule(t *testing.T) {
	keyPath := ecKey(t)
	body := fmt.Sprintf(`{"authn":{"token":{"issuer":"https://auth.example.com",`+
		`"algorithm":"ES256","signing-key-file":%q}}}`, keyPath)
	reg, rules := registry(t, body)
	reg.Register(token.Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth: core.StageNew,
		Participants: []string{
			token.ModuleName, accountsName, sessionsName, passwordsName,
			throttleName, databaseName, mounterName, config.ModuleName, logName,
		},
	})
	if err != nil {
		t.Fatalf("driving the token module: %v", err)
	}
	requireNoRule(t, rules, token.ModuleName)
}

// requireNoRule fails the case for each rule the logging capability was handed.
func requireNoRule(t *testing.T, rules *registeredRules, module string) {
	t.Helper()
	if len(rules.names) != 0 {
		t.Errorf("%s registered the literal rules %q, and it holds no secret configuration value: the configured "+
			"value is a path, whose content is the secret", module, rules.names)
	}
}

// spKeyPair writes an RSA private key and a self-signed certificate for it, and
// returns their paths: the pair the saml backend loads when sp-key-file and
// sp-cert-file are set.
func spKeyPair(t *testing.T) (keyPath, certPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the service provider key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the service provider certificate: %v", err)
	}
	keyPath = writePEM(t, "sp-key.pem", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	certPath = writePEM(t, "sp-cert.pem", "CERTIFICATE", der)
	return keyPath, certPath
}

// ecKey writes a P-256 private key in PKCS#8, the kind the token backend loads
// for its ES256 signing key, and returns its path.
func ecKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the signing key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encoding the signing key: %v", err)
	}
	return writePEM(t, "signing-key.pem", "PRIVATE KEY", der)
}

// writePEM writes one PEM block to a file under a case's temporary directory and
// returns its path.
func writePEM(t *testing.T, name, blockType string, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	block := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}
