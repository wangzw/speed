package oidc

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/authn/sso"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// configReader builds the real configuration loader over the gateway's mount
// items and the SSO namespace, which this method reads. The SSO module is
// registered for its declaration alone; the loader never drives it.
func configReader(t *testing.T, caFile string) config.Reader {
	t.Helper()
	extra := ""
	if caFile != "" {
		extra = fmt.Sprintf(`,"oidc-ca-file":%q`, caFile)
	}
	body := fmt.Sprintf(
		`{"authn":{"gate":{"mount":{"public-url":"https://app.example.com"}},`+
			`"sso":{"allow-private-egress":true%s}}}`, extra)
	return authntest.ConfigReaderAll(t, body, gate.Module(), sso.Module(), Module())
}

// driveRegistered drives this module through a real registry to Init and hands
// the delivered product to action.
func driveRegistered(t *testing.T, reader config.Reader, providers *fakeProviders, action func(*product)) error {
	t.Helper()
	reg := core.New()
	reg.Register(Module())
	reg.Register(standIn(providersModule, (*authn.SSOProviders)(nil)))
	reg.Register(standIn(accountsModule, (*authn.Accounts)(nil)))
	reg.Register(standIn(readerModule, (*config.Reader)(nil)))
	reg.Supply(providers, providersModule)
	reg.Supply(fakeAccounts{}, accountsModule)
	reg.Supply(reader, readerModule)
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, providersModule, accountsModule, readerModule},
		Action: func(_ context.Context, reg *core.Registry) error {
			value, err := core.Resolve[authn.LoginMethod](reg)
			if err != nil {
				return err
			}
			p, ok := value.(*product)
			if !ok {
				return fmt.Errorf("the delivered method is %T, not this module's product", value)
			}
			action(p)
			return nil
		},
	})
}

// writeCert writes one certificate as a PEM file and returns its path.
func writeCert(t *testing.T, cert *x509.Certificate) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roots.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing the root certificate: %v", err)
	}
	return path
}

// beginProvider starts a login on the named provider against m.
func beginProvider(t *testing.T, p *product, id string) authn.Step {
	t.Helper()
	got, err := p.Begin(context.Background(), authn.Start{
		Purpose: authn.PurposeLogin,
		Fields:  map[string]string{fieldProvider: id},
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return got
}

// TestAnUnsetCAFileLeavesTheSystemRoots holds that a deployment which configures
// no CA file trusts the system roots alone: a provider serving a certificate no
// system root signs is an outage, not a discovery that succeeds.
func TestAnUnsetCAFileLeavesTheSystemRoots(t *testing.T) {
	m := newMockIDP(t)
	providers := newFakeProviders()
	reader := configReader(t, "")

	err := driveRegistered(t, reader, providers, func(p *product) {
		r := &rig{t: t, p: p, providers: providers}
		r.putProvider("idp-1", "tenant-a", m, nil)

		got := beginProvider(t, p, "idp-1")
		if kind := step.KindOf(got); kind != step.KindUnavailable {
			t.Errorf("Begin over an untrusted certificate answered %s, want unavailable", kind)
		}
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
}

// TestAConfiguredCAFileIsTrustedForTheOIDCCall holds that the configured file's
// certificate is trusted by the egress client: a provider serving a certificate
// from that file is discovered and the whole login completes.
func TestAConfiguredCAFileIsTrustedForTheOIDCCall(t *testing.T) {
	m := newMockIDP(t)
	providers := newFakeProviders()
	reader := configReader(t, writeCert(t, m.cert()))

	err := driveRegistered(t, reader, providers, func(p *product) {
		r := &rig{t: t, p: p, providers: providers}
		r.putProvider("idp-1", "tenant-a", m, nil)

		got := r.run("idp-1", m)
		if kind := step.KindOf(got); kind != step.KindProven {
			t.Errorf("the login answered %s, want proven", kind)
		}
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
}

// TestACAFileWithNoCertificateIsRefused holds that a file the deployment named
// but that holds no certificate fails the start rather than quietly leaving the
// system roots in place.
func TestACAFileWithNoCertificateIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("writing the CA file: %v", err)
	}
	reader := configReader(t, path)

	err := driveRegistered(t, reader, newFakeProviders(), func(*product) {
		t.Error("the drive reached its action with a CA file that holds no certificate")
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("drive = %v, want authn.ErrInvalidConfig", err)
	}
}
