package resolve

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/slug"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
	"github.com/vislake/speed/pkg/tenant/personal"
)

type emailClaimRegister struct{ authn.SSOProviders }

func (emailClaimRegister) provider() authn.SSOProvider {
	return authn.SSOProvider{
		ID: idpAcme, Tenant: tenantAcme, Protocol: authn.SSOProtocolOIDC,
		Enabled: true, Provisioning: authn.ProvisioningJIT,
		UsernameStrategy: authn.UsernameStrategyClaim,
		UsernameClaim:    authn.UsernameClaimEmail,
	}
}

func (r emailClaimRegister) Get(_ context.Context, id string) (authn.SSOProvider, error) {
	if id != idpAcme {
		return authn.SSOProvider{}, fmt.Errorf("no provider %q", id)
	}
	return r.provider(), nil
}

func (r emailClaimRegister) Discover(_ context.Context, domain string) ([]authn.SSOProvider, error) {
	switch domain {
	case "acme.com", "example.com":
		return []authn.SSOProvider{r.provider()}, nil
	}
	return nil, nil
}

func withEmailClaimAssembly(t *testing.T, use func(context.Context, *core.Registry)) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "email-claim.db")
	configuration := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, dsn)

	modules := []core.Module{
		sqlite.Module(),
		tenantcatalog.Module(),
		authzpolicy.Module(),
		account.Module(),
		personal.Module(),
		module("stand-in.email-claim-audit", (*audit.Audit)(nil), func() (any, error) { return standInAudit{}, nil }),
	}
	reader := authntest.ConfigReaderAll(t, configuration, modules...)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{config.ModuleName}
	for _, module := range modules {
		reg.Register(module)
		names = append(names, module.Name)
	}
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			use(ctx, reg)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the real email-claim assembly: %v", err)
	}
}

func countRows(t *testing.T, handle *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := handle.Table(table).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

func usernameOf(t *testing.T, accounts authn.Accounts, id authn.AccountID) string {
	t.Helper()
	var username string
	for _, identifier := range identifiersOf(t, accounts, id) {
		if identifier.Kind == authn.IdentifierUsername {
			username = identifier.Value
		}
	}
	return username
}

// TestEmailClaimWithALegalLocalPartOpensTheAccountAndItsPersonalTenant holds that the account owns the tenant, with exactly the expected rows.
func TestEmailClaimWithALegalLocalPartOpensTheAccountAndItsPersonalTenant(t *testing.T) {
	withEmailClaimAssembly(t, func(ctx context.Context, reg *core.Registry) {
		accounts := mustResolve[authn.Accounts](t, reg)
		admin := mustResolve[authn.AccountAdmin](t, reg)
		memberships := mustResolve[authn.Memberships](t, reg)
		roster := mustResolve[tenant.Catalog](t, reg)
		database := mustResolve[db.Database](t, reg)

		r, err := New(Config{
			Accounts:          accounts,
			Admin:             admin,
			Memberships:       memberships,
			SSO:               emailClaimRegister{},
			NormalizeUsername: slug.Normalize,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		res, err := r.Resolve(ctx, Request{
			Proof: enterprise("u1", "annsmith@example.com"), Purpose: authn.PurposeLogin,
		})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if res.Outcome != OutcomeCreated || !res.MembershipCreated || res.Account == "" {
			t.Fatalf("Resolve = %+v, want a created account with its tenant membership", res)
		}
		if got := usernameOf(t, accounts, res.Account); got != "annsmith" {
			t.Errorf("the account's username is %q, want the address's local part", got)
		}

		personalID := personal.TenantID(res.Account)
		held, found, err := roster.Get(ctx, personalID)
		if err != nil || !found {
			t.Fatalf("reading the personal tenant: found=%t err=%v", found, err)
		}
		if held.Name != "annsmith" {
			t.Errorf("the personal tenant's name is %q, want the username", held.Name)
		}

		handle := database.DB()
		if n := countRows(t, handle, "authn_accounts", ""); n != 1 {
			t.Errorf("authn_accounts holds %d row(s), want the one account", n)
		}
		if n := countRows(t, handle, "tenants", ""); n != 1 {
			t.Errorf("tenants holds %d row(s), want the personal tenant alone", n)
		}
		if n := countRows(t, handle, "authn_memberships", "tenant_id = ?", string(personalID)); n != 1 {
			t.Errorf("the personal tenant holds %d membership(s), want the owner's one", n)
		}
		if n := countRows(t, handle, "authn_memberships", "tenant_id = ?", tenantAcme); n != 1 {
			t.Errorf("tenant %q holds %d membership(s), want the login's one", tenantAcme, n)
		}
		if n := countRows(t, handle, "authn_memberships", ""); n != 2 {
			t.Errorf("authn_memberships holds %d row(s), want the personal and the tenant membership", n)
		}
		if n := countRows(t, handle, "authn_identifiers", "account_id = ?", res.Account.String()); n != 1 {
			t.Errorf("the account holds %d identifier(s), want the username alone", n)
		}
		if n := countRows(t, handle, "authz_bindings", "tenant_id = ? AND account_id = ?",
			string(personalID), res.Account.String()); n != 1 {
			t.Errorf("the account holds %d personal binding(s), want one", n)
		}
	})
}

// TestEmailClaimWithADottedOrPlussedLocalPartWritesNothing holds that a dotted or plussed local part is an invalid JIT username and writes nothing.
func TestEmailClaimWithADottedOrPlussedLocalPartWritesNothing(t *testing.T) {
	withEmailClaimAssembly(t, func(ctx context.Context, reg *core.Registry) {
		accounts := mustResolve[authn.Accounts](t, reg)
		admin := mustResolve[authn.AccountAdmin](t, reg)
		memberships := mustResolve[authn.Memberships](t, reg)
		database := mustResolve[db.Database](t, reg)

		r, err := New(Config{
			Accounts:          accounts,
			Admin:             admin,
			Memberships:       memberships,
			SSO:               emailClaimRegister{},
			NormalizeUsername: slug.Normalize,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, err = r.Resolve(ctx, Request{
			Proof: enterprise("u1", "ann.smith+tag@example.com"), Purpose: authn.PurposeLogin,
		})
		if !errors.Is(err, authn.ErrJITUsernameInvalid) {
			t.Fatalf("err = %v, want ErrJITUsernameInvalid", err)
		}
		if errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("err = %v, want the username refusal, not an unavailable dependency", err)
		}

		handle := database.DB()
		if n := countRows(t, handle, "authn_accounts", ""); n != 0 {
			t.Errorf("the refusal left %d account row(s), want none", n)
		}
		if n := countRows(t, handle, "tenants", ""); n != 0 {
			t.Errorf("the refusal left %d tenant row(s), want none", n)
		}
		if n := countRows(t, handle, "authn_memberships", ""); n != 0 {
			t.Errorf("the refusal left %d membership row(s), want none", n)
		}
	})
}
