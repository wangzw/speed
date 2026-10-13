package token

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAConfigurationErrorNeverRepeatsTheSigningKeyMaterial holds that the text of
// a failure to load the signing key carries the file and the reason and never the
// key material itself.
//
// The sensitive input is signing-key-file, which names the file the private key
// is read from: the value of that item is a path, and the secret is that file's
// content. The module holds no secret configuration value, so it registers no
// literal redaction rule; the failure a startup prints is the place a leak would
// surface, and a file whose content is not a key is exactly the failure a
// deployment hits. The path itself is meant to appear, since an operator
// correcting a key has to be told which file; the material inside it must not.
func TestAConfigurationErrorNeverRepeatsTheSigningKeyMaterial(t *testing.T) {
	const secret = "s3cr3t-signing-key-material-sentinel-9f8e7d6c"
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "signing-key.pem")
	if err := os.WriteFile(keyFile, []byte(secret), 0o600); err != nil {
		t.Fatalf("writing the key file: %v", err)
	}
	cfg, err := baseConfig().resolve(gateMount{PublicURL: "https://auth.example.com"})
	if err != nil {
		t.Fatalf("resolving the base configuration: %v", err)
	}
	cfg.signingFile = keyFile
	_, _, err = loadKeySet(cfg)
	if !errors.Is(err, authn.ErrInvalidKey) {
		t.Fatalf("loadKeySet = %v, want one wrapping ErrInvalidKey", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the failure %q repeats the private key material", err)
	}
}
