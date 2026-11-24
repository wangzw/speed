package sso

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestPutRefusesAConfigItCannotRunOn checks that Put refuses a configuration it cannot run on.
func TestPutRefusesAConfigItCannotRunOn(t *testing.T) {
	r := newRigSQLite(t)
	existing := r.putOIDC("tenant-a", "kept")
	existingSAMLSecret := "only-oidc-has-a-secret"

	cases := []struct {
		name    string
		in      authn.SSOProvider
		secret  *string
		wantErr error
	}{
		{
			name: "an unknown key in an OIDC config",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"issuer":"https://idp.example.com","client_id":"c","saml_only":true}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "a SAML-only key in an OIDC config",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"issuer":"https://idp.example.com","client_id":"c","idp_sso_url":"https://x"}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "provisioning left out",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "provisioning outside the closed set",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "auto",
				Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "an OIDC issuer with an aggregator placeholder",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"issuer":"https://login.example.com/common/v2.0","client_id":"c"}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "an OIDC issuer that is not https",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"issuer":"http://idp.example.com","client_id":"c"}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "OIDC algs containing none",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"issuer":"https://idp.example.com","client_id":"c","algs":["RS256","none"]}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "OIDC algs containing a symmetric algorithm",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"issuer":"https://idp.example.com","client_id":"c","algs":["HS256"]}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "SAML missing idp_sso_url",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"idp_entity_id":"e","idp_certs":["x"]}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "SAML missing idp_entity_id",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"]}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "SAML missing idp_certs",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso"}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "SAML idp_sso_url with user information",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"idp_entity_id":"e","idp_sso_url":"https://user:pass@idp.example.com/sso","idp_certs":["x"]}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "SAML too many certificates",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["a","b","c","d","e"]}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "SAML with a transient NameID",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"name_id_format":"transient"}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "SAML with an emailAddress NameID and no subject attribute",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"name_id_format":"emailAddress"}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "SAML asking signed requests with no service provider key",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"sign_requests":true}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "an empty name",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "a name longer than the bound",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: strings.Repeat("x", maxNameChars+1), Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "a protocol outside the closed set",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "ldap", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "a secret on a SAML provider",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: samlConfig(),
			},
			secret:  &existingSAMLSecret,
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "a claims map naming an unknown target",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"name":"role"}}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "a claims map with too many attributes",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit", UsernameStrategy: "user-confirm",
				Config: json.RawMessage(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"a1":"attr.1","a2":"attr.2","a3":"attr.3","a4":"attr.4","a5":"attr.5","a6":"attr.6","a7":"attr.7","a8":"attr.8","a9":"attr.9"}}`),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "an update that moves the provider to another tenant",
			in: authn.SSOProvider{
				ID: existing.ID, Tenant: "tenant-b", Protocol: "oidc", Name: "kept",
				Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "an update of a provider that is not there",
			in: authn.SSOProvider{
				ID: "0192f5a0-0000-7000-8000-000000000000", Tenant: "tenant-a", Protocol: "oidc",
				Name: "ghost", Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
			},
			wantErr: ErrProviderNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.p.Put(context.Background(), tc.in, tc.secret)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Put returned %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestPutCreateAndUpdateVersionAndKeepTheSecret checks that Put raises the version on create and update and keeps a nil secret.
func TestPutCreateAndUpdateVersionAndKeepTheSecret(t *testing.T) {
	r := newRigSQLite(t)
	created := r.putOIDC("tenant-a", "idp")

	if created.Version != 1 {
		t.Errorf("a created provider has version %d, want 1", created.Version)
	}
	if created.Config == nil || !json.Valid(created.Config) {
		t.Fatalf("the read provider's config %q is not JSON", created.Config)
	}
	got, err := r.p.Secret(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if got != "shh-idp" {
		t.Errorf("Secret = %q, want the value written", got)
	}

	updated, err := r.p.Put(context.Background(), authn.SSOProvider{
		ID: created.ID, Tenant: "tenant-a", Protocol: "oidc", Name: "renamed", Enabled: true,
		Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
	}, nil)
	if err != nil {
		t.Fatalf("Put (update, nil secret): %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("an updated provider has version %d, want 2", updated.Version)
	}
	if s, _ := r.p.Secret(context.Background(), created.ID); s != "shh-idp" {
		t.Errorf("the secret did not survive an update with a nil secret: %q", s)
	}

	replacement := "new-secret"
	if _, err := r.p.Put(context.Background(), authn.SSOProvider{
		ID: created.ID, Tenant: "tenant-a", Protocol: "oidc", Name: "renamed", Enabled: true,
		Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
	}, &replacement); err != nil {
		t.Fatalf("Put (update, new secret): %v", err)
	}
	if s, _ := r.p.Secret(context.Background(), created.ID); s != replacement {
		t.Errorf("Secret = %q, want %q after a non-nil secret", s, replacement)
	}
}

// TestTheReadsNeverHoldTheSecret checks that no read returns the provider secret.
func TestTheReadsNeverHoldTheSecret(t *testing.T) {
	r := newRigSQLite(t)
	created := r.putOIDC("tenant-a", "idp")

	got, err := r.p.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if bytesContain(got.Config, "shh-idp") {
		t.Errorf("Get's config %q holds the secret", got.Config)
	}
	list, err := r.p.List(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range list {
		if bytesContain(p.Config, "shh-idp") {
			t.Errorf("List's config %q holds the secret", p.Config)
		}
	}
}

// TestTenantAnswersTheOwningTenant checks that Tenant answers the owning tenant.
func TestTenantAnswersTheOwningTenant(t *testing.T) {
	r := newRigSQLite(t)
	created := r.putOIDC("tenant-a", "idp")

	got, err := r.p.Tenant(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Tenant: %v", err)
	}
	if got != "tenant-a" {
		t.Errorf("Tenant = %q, want tenant-a", got)
	}
	if _, err := r.p.Tenant(context.Background(), "0192f5a0-0000-7000-8000-000000000000"); !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Tenant of a missing provider returned %v, want ErrProviderNotFound", err)
	}
}

// TestPutAndDeletePublishTheChange checks that Put and Delete publish authn.idp.changed.
func TestPutAndDeletePublishTheChange(t *testing.T) {
	r := newRigSQLite(t)
	created := r.putOIDC("tenant-a", "idp")
	if got := r.bus.changes(created.ID); len(got) != 1 || got[0] != changeCreated {
		t.Errorf("after create the changes are %v, want [created]", got)
	}
	if _, err := r.p.Put(context.Background(), authn.SSOProvider{
		ID: created.ID, Tenant: "tenant-a", Protocol: "oidc", Name: "renamed", Enabled: true,
		Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
	}, nil); err != nil {
		t.Fatalf("Put (update): %v", err)
	}
	if got := r.bus.changes(created.ID); len(got) != 2 || got[1] != changeUpdated {
		t.Errorf("after update the changes are %v, want the second to be updated", got)
	}
	if err := r.p.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := r.bus.changes(created.ID); len(got) != 3 || got[2] != changeDeleted {
		t.Errorf("after delete the changes are %v, want the third to be deleted", got)
	}
	if _, err := r.p.Get(context.Background(), created.ID); !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Get after Delete returned %v, want ErrProviderNotFound", err)
	}
}

// TestPutNamesAStatusChange holds the change values a status transition carries.
func TestPutNamesAStatusChange(t *testing.T) {
	r := newRigSQLite(t)
	created, err := r.p.Put(context.Background(), authn.SSOProvider{
		Tenant: "tenant-a", Protocol: "oidc", Name: "off", Enabled: false, Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
	}, nil)
	if err != nil {
		t.Fatalf("Put (create disabled): %v", err)
	}
	if got := r.bus.changes(created.ID); len(got) != 1 || got[0] != changeCreated {
		t.Fatalf("after create the changes are %v, want [created]", got)
	}
	enable := func(enabled bool) {
		t.Helper()
		if _, err := r.p.Put(context.Background(), authn.SSOProvider{
			ID: created.ID, Tenant: "tenant-a", Protocol: "oidc", Name: "off", Enabled: enabled,
			Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
		}, nil); err != nil {
			t.Fatalf("Put (set enabled=%v): %v", enabled, err)
		}
	}
	enable(true)
	enable(false)
	got := r.bus.changes(created.ID)
	if len(got) != 3 || got[1] != changeEnabled || got[2] != changeDisabled {
		t.Errorf("the changes are %v, want created, enabled, disabled", got)
	}
}

// TestListTakesTheDisabledOnesToo holds that a disabled provider is listed.
func TestListTakesTheDisabledOnesToo(t *testing.T) {
	r := newRigSQLite(t)
	disabled, err := r.p.Put(context.Background(), authn.SSOProvider{
		Tenant: "tenant-a", Protocol: "oidc", Name: "off", Enabled: false, Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
	}, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	list, err := r.p.List(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != disabled.ID || list[0].Enabled {
		t.Errorf("List = %+v, want the one disabled provider", list)
	}
}

// TestPutHoldsTheUsernameStrategyToItsCombinations checks that Put holds the username strategy to its valid combinations.
func TestPutHoldsTheUsernameStrategyToItsCombinations(t *testing.T) {
	r := newRigSQLite(t)
	cases := []struct {
		name    string
		in      authn.SSOProvider
		wantErr error
	}{
		{
			name: "jit with no strategy",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningJIT, Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "jit with an unknown strategy",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningJIT, UsernameStrategy: "surprise", Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "claim with no claim",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningJIT, UsernameStrategy: authn.UsernameStrategyClaim, Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "claim with an unknown claim",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningJIT, UsernameStrategy: authn.UsernameStrategyClaim,
				UsernameClaim: "surprise", Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "user-confirm with a claim",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningJIT, UsernameStrategy: authn.UsernameStrategyUserConfirm,
				UsernameClaim: authn.UsernameClaimEmail, Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "preprovisioned with a claim and no strategy",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningPreprovisioned, UsernameClaim: authn.UsernameClaimEmail, Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "preprovisioned with a claim strategy",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningPreprovisioned, UsernameStrategy: authn.UsernameStrategyClaim,
				UsernameClaim: authn.UsernameClaimEmail, Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "preprovisioned with the user-confirm strategy",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningPreprovisioned, UsernameStrategy: authn.UsernameStrategyUserConfirm,
				Config: oidcConfig(),
			},
			wantErr: authn.ErrInvalidConfig,
		},
		{
			name: "preprovisioned with no strategy",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningPreprovisioned, Config: oidcConfig(),
			},
		},
		{
			name: "jit claim preferred_username",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningJIT, UsernameStrategy: authn.UsernameStrategyClaim,
				UsernameClaim: authn.UsernameClaimPreferredUsername, Config: oidcConfig(),
			},
		},
		{
			name: "jit claim email",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningJIT, UsernameStrategy: authn.UsernameStrategyClaim,
				UsernameClaim: authn.UsernameClaimEmail, Config: oidcConfig(),
			},
		},
		{
			name: "jit user-confirm",
			in: authn.SSOProvider{
				Tenant: "tenant-a", Protocol: "oidc", Name: "p",
				Provisioning: authn.ProvisioningJIT, UsernameStrategy: authn.UsernameStrategyUserConfirm,
				Config: oidcConfig(),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			written, err := r.p.Put(context.Background(), tc.in, nil)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Put returned %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			if written.UsernameStrategy != tc.in.UsernameStrategy || written.UsernameClaim != tc.in.UsernameClaim {
				t.Errorf("Put returned %+v, want the strategy %q and claim %q",
					written, tc.in.UsernameStrategy, tc.in.UsernameClaim)
			}
			got, err := r.p.Get(context.Background(), written.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.UsernameStrategy != tc.in.UsernameStrategy || got.UsernameClaim != tc.in.UsernameClaim {
				t.Errorf("Get returned %+v, want the strategy %q and claim %q",
					got, tc.in.UsernameStrategy, tc.in.UsernameClaim)
			}
		})
	}
}

func bytesContain(b []byte, sub string) bool { return strings.Contains(string(b), sub) }
