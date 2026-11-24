package saml

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAConfigurationErrorNeverRepeatsTheServiceProviderKey checks that a configuration error never repeats the service provider key.
func TestAConfigurationErrorNeverRepeatsTheServiceProviderKey(t *testing.T) {
	const secret = "s3cr3t-sp-key-material-sentinel-9f8e7d6c"
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "sp-key.pem")
	if err := os.WriteFile(keyFile, []byte(secret), 0o600); err != nil {
		t.Fatalf("writing the key file: %v", err)
	}
	cfg := moduleConfig{SPKeyFile: keyFile, SPCertFile: filepath.Join(dir, "sp-cert.pem")}
	_, err := cfg.resolve()
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("resolve = %v, want one wrapping ErrInvalidConfig", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the failure %q repeats the private key material", err)
	}
}
