package settings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/settings/admin"
	"github.com/vislake/speed/pkg/settings/tenantadmin"
)

const endpointName = "surface"

type platformDefaults struct {
	Token string
}

type tenantDefaults struct {
	Enabled bool
}

const tenantItemKey = "test.tenant.enabled"

func platformDeclarer() core.Module {
	defaults := platformDefaults{Token: "seed"}
	return core.Module{
		Name: "test.platform",
		Resources: []any{settings.Schema{
			Namespace: "test.platform",
			Mounts:    []settings.Mount{{Value: &defaults}},
		}},
	}
}

func tenantDeclarer() core.Module {
	defaults := tenantDefaults{}
	return core.Module{
		Name: "test.tenant",
		Resources: []any{settings.Schema{
			Namespace:    "test.tenant",
			TenantFacing: true,
			Mounts:       []settings.Mount{{Value: &defaults}},
			Items: map[string]settings.Item{
				"enabled": {
					Description:    "the control item",
					TitleKey:       tenantItemKey + ".title",
					DescriptionKey: tenantItemKey + ".description",
					Control:        settings.ControlSwitch,
					TenantAccess:   settings.TenantReadWrite,
				},
			},
		}},
	}
}

// TestASchemaWithoutTenantFacingStaysOffTheTenantSurface checks that a schema saying nothing about
// TenantFacing is visible and writable on the platform surface, not the tenant one.
func TestASchemaWithoutTenantFacingStaysOffTheTenantSurface(t *testing.T) {
	const body = `{
		"settings": {
			"admin": {"endpoints": ["surface"]},
			"tenantadmin": {"endpoints": ["surface"]}
		}
	}`
	reader := authntest.ConfigReaderAll(t, body, admin.Module(), tenantadmin.Module())

	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "settings.db"), nil)
	applySettingsMigration(t, database)
	router := newSurfaceRouter(endpointName)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	names := []string{
		settings.ModuleName,
		admin.ModuleName,
		tenantadmin.ModuleName,
		"test.platform",
		"test.tenant",
	}
	reg.Register(standIn("test.db", database, (*db.Database)(nil)))
	reg.Register(standIn("test.event", busStub{}, (*event.Bus)(nil)))
	reg.Register(standIn("test.guard", allowGuard{}, (*guard.Guard)(nil)))
	reg.Register(standIn("test.audit", discardAudit{}, (*audit.Audit)(nil)))
	reg.Register(standIn("test.verification", signupStub{}, (*authn.SignupVerification)(nil)))
	reg.Register(standIn("test.write-settings", writeSettingsStub{}, (*authz.WriteSettings)(nil)))
	reg.Register(standIn("test.route-guard", routeGuardStub{}, (*speedhttp.TenantAdminRouteGuard)(nil)))
	reg.Register(standIn("test.router", router, (*speedhttp.Router)(nil)))
	reg.Register(standIn("test.logger", discardLogger{}, (*log.Logger)(nil)))
	names = append(names,
		"test.db", "test.event", "test.guard", "test.audit", "test.verification",
		"test.write-settings", "test.route-guard", "test.router", "test.logger",
	)
	reg.Register(platformDeclarer())
	reg.Register(tenantDeclarer())
	reg.Register(settings.Module())
	reg.Register(admin.Module())
	reg.Register(tenantadmin.Module())

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			store, err := core.Resolve[settings.Store](reg)
			if err != nil {
				t.Fatalf("resolving the settings surface: %v", err)
			}
			engine := router.endpoint(endpointName).engine
			do := func(method, target string, body any) *httptest.ResponseRecorder {
				t.Helper()
				var payload []byte
				if body != nil {
					payload, _ = json.Marshal(body)
				}
				req := httptest.NewRequest(method, target, bytes.NewReader(payload))
				if body != nil {
					req.Header.Set("Content-Type", "application/json")
				}
				req = req.WithContext(authntest.Context(t, context.Background()))
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, req)
				return w
			}

			w := do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("the platform listing ended with %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "test.platform.token") {
				t.Fatalf("the platform listing does not carry the declared key: %s", w.Body.String())
			}
			w = do(http.MethodPut, "/api/v1/system/settings/test.platform/token?layer=global",
				map[string]any{"value": "written"})
			if w.Code != http.StatusNoContent {
				t.Fatalf("the platform write ended with %d: %s", w.Code, w.Body.String())
			}
			w = do(http.MethodGet, "/api/v1/system/settings/test.platform?layer=global", nil)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "written") {
				t.Fatalf("the platform namespace read ended with %d and did not show the write: %s",
					w.Code, w.Body.String())
			}

			w = do(http.MethodGet, "/api/v1/tenants/acme/settings", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("the tenant listing ended with %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "test.platform") {
				t.Fatalf("the tenant listing carries a platform-only key: %s", w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tenantItemKey) {
				t.Fatalf("the tenant listing does not carry the tenant-facing control: %s", w.Body.String())
			}

			for _, tc := range []struct {
				method string
				target string
				body   any
			}{
				{http.MethodGet, "/api/v1/tenants/acme/settings/test.platform", nil},
				{http.MethodPut, "/api/v1/tenants/acme/settings/test.platform/token", map[string]any{"value": "x"}},
				{http.MethodDelete, "/api/v1/tenants/acme/settings/test.platform/token", nil},
			} {
				if w := do(tc.method, tc.target, tc.body); w.Code != http.StatusNotFound {
					t.Errorf("%s %s ended with %d, want 404", tc.method, tc.target, w.Code)
				}
			}

			rows, err := store.List(ctx, settings.ForTenant("acme"))
			if err != nil {
				t.Fatalf("listing the tenant layer: %v", err)
			}
			for _, row := range rows {
				if row.Key == "test.platform.token" {
					t.Errorf("the tenant layer holds a row for the platform-only key: %+v", row)
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
}

func applySettingsMigration(t *testing.T, database db.Database) {
	t.Helper()
	for _, res := range settings.Module().Resources {
		migrations, ok := res.(db.Migrations)
		if !ok {
			continue
		}
		sql, err := fs.ReadFile(migrations.FS, "sqlite/0001_settings.sql")
		if err != nil {
			t.Fatalf("reading the settings migration: %v", err)
		}
		for statement := range strings.SplitSeq(string(sql), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if err := database.DB().Exec(statement).Error; err != nil {
				t.Fatalf("applying the settings migration: %v", err)
			}
		}
		return
	}
	t.Fatal("the settings module declares no migration")
}

func standIn(name string, instance any, token any) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type busStub struct{}

func (busStub) Publish(context.Context, event.Event) error { return nil }
func (busStub) Subscribe(string, string, event.Handler)    {}

type allowGuard struct{}

func (allowGuard) Allowed(*gin.Context, authz.Permission) (bool, error)  { return true, nil }
func (allowGuard) RequirePermission(*gin.Context, authz.Permission) bool { return true }
func (allowGuard) RequirePermissionIn(*gin.Context, authz.Permission, authz.Domain) bool {
	return true
}

type discardAudit struct{}

func (discardAudit) Record(context.Context, audit.Entry) error { return nil }

type signupStub struct{}

func (signupStub) SignupStateOf(context.Context, authn.AccountID) (authn.SignupStatus, error) {
	return authn.SignupStatus{State: authn.SignupNone}, nil
}

func (signupStub) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

type writeSettingsStub struct{}

func (writeSettingsStub) Denied(context.Context, string) (bool, error) { return false, nil }

type routeGuardStub struct{}

func (routeGuardStub) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc { return next }

type discardLogger struct{}

func (discardLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (discardLogger) Redaction() log.Redaction  { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

type surfaceEndpoint struct {
	engine *gin.Engine
}

func (e *surfaceEndpoint) Route(method, path string, handlers ...gin.HandlerFunc) {
	e.engine.Handle(method, path, handlers...)
}

func (e *surfaceEndpoint) Use(speedhttp.Middleware) {}
func (e *surfaceEndpoint) Accepting() bool          { return true }

type surfaceRouter struct {
	name string
	ep   *surfaceEndpoint
}

func newSurfaceRouter(name string) *surfaceRouter {
	gin.SetMode(gin.TestMode)
	return &surfaceRouter{name: name, ep: &surfaceEndpoint{engine: gin.New()}}
}

func (r *surfaceRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	if name != r.name {
		return nil, speedhttp.ErrUnknownEndpoint
	}
	return r.ep, nil
}

func (r *surfaceRouter) Endpoints() []string { return []string{r.name} }

func (r *surfaceRouter) endpoint(name string) *surfaceEndpoint { return r.ep }
