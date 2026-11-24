package journal

import (
	"context"
	"maps"
	nethttp "net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

func gateStandIn(t *testing.T, opts ...authntest.Option) speedhttp.Middleware {
	t.Helper()
	// Built once here, in the test's own goroutine, so that a combination the
	// gateway would refuse fails the test where it can be reported.
	authntest.Context(t, context.Background(), opts...)
	return speedhttp.Middleware{
		Name:     "gate-stand-in",
		Provides: []core.Token{(*authn.Gate)(nil)},
		Stage:    speedhttp.StageIdentify,
		Handler: func(c *gin.Context) {
			c.Request = c.Request.WithContext(authntest.Context(t, c.Request.Context(), opts...))
			c.Next()
		},
	}
}

var impersonatedOptions = []authntest.Option{
	authntest.WithAccount("customer-1"),
	authntest.WithActor("admin-9"),
	authntest.WithSession("0198f0aa-0000-7000-8000-000000000002"),
	authntest.WithScope("acme"),
}

// The request path reads the identity out of the request the layer
// received, in a real chain, on both ways out of it.
func TestAnImpersonationReachesTheRowOfARequestThroughTheChain(t *testing.T) {
	for name, c := range map[string]struct {
		request, method, path string
		handler               gin.HandlerFunc
		status                int
		outcome               string
	}{
		"a request that returns": {
			"GET /ok", "GET", "/ok", func(c *gin.Context) { c.Status(nethttp.StatusNoContent) },
			nethttp.StatusNoContent, "204",
		},
		"a write that returns": {
			"POST /orders", "POST", "/orders", func(c *gin.Context) { c.Status(nethttp.StatusNoContent) },
			nethttp.StatusNoContent, "204",
		},
		"a request that panics": {
			"GET /boom", "GET", "/boom", func(*gin.Context) { panic("the handler fell over") },
			nethttp.StatusInternalServerError, "panic",
		},
	} {
		t.Run(name, func(t *testing.T) {
			servedRequests(t, []string{c.request}, func(endpoint speedhttp.Endpoint) {
				endpoint.Use(gateStandIn(t, impersonatedOptions...))
				endpoint.Route(c.method, c.path, c.handler)
			}, func(ctx context.Context, s *served) error {
				s.expect(ctx, c.method, c.path, c.status)
				rows := s.awaitRows(1)
				if len(rows) != 1 {
					t.Errorf("the rows are %v, want the one request", describe(rows))
					return nil
				}
				if rows[0].Outcome != c.outcome {
					t.Errorf("the outcome is %q, want %q", rows[0].Outcome, c.outcome)
				}
				wantImpersonation(t, name, rows[0])
				return nil
			})
		})
	}
}

// The other side of the same chain: an ordinary identity records its key as the
// actor and none of the impersonation keys.
func TestAnOrdinaryIdentityThroughTheChainRecordsItsKeyAndNoImpersonationKey(t *testing.T) {
	servedRequests(t, []string{"GET /ok"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Use(gateStandIn(t, authntest.WithAccount("alice-1"),
			authntest.WithSession("0198f0aa-0000-7000-8000-000000000003")))
		endpoint.Route("GET", "/ok", func(c *gin.Context) { c.Status(nethttp.StatusNoContent) })
	}, func(ctx context.Context, s *served) error {
		s.expect(ctx, "GET", "/ok", nethttp.StatusNoContent)
		rows := s.awaitRows(1)
		if len(rows) != 1 {
			t.Errorf("the rows are %v, want the one request", describe(rows))
			return nil
		}
		if rows[0].Actor != "alice-1" {
			t.Errorf("the actor is %q, want alice-1", rows[0].Actor)
		}
		detail := decodedDetail(t, rows[0])
		for _, key := range []string{"on_behalf_of", "impersonation_scope", "session"} {
			if _, found := detail[key]; found {
				t.Errorf("the detail carries %q for an ordinary identity: %v", key, detail)
			}
		}
		return nil
	})
}

// The three keys are the whole of what an impersonation adds.
func TestAnImpersonationAddsExactlyTheThreeKeysAndNoOther(t *testing.T) {
	ctx := authntest.Context(t, context.Background(), append([]authntest.Option{
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithMethods(authn.MethodPassword),
		authntest.WithAttributes(map[string]string{"dept": "sales", "region": "eu"}),
	}, impersonatedOptions...)...)
	want := map[string]string{
		"role":                "admin",
		"on_behalf_of":        "customer-1",
		"impersonation_scope": "acme",
		"session":             "0198f0aa-0000-7000-8000-000000000002",
	}
	got := detailOfRow(t, assemble(ctx, audit.SourceSeam,
		audit.Entry{Action: "x", Detail: map[string]string{"role": "admin"}}, ""))
	if !maps.Equal(got, want) {
		t.Errorf("the detail reads %v, want exactly %v", got, want)
	}
	delete(want, "role")
	got = detailOfRow(t, assemble(ctx, audit.SourceSeam, audit.Entry{Action: "x"}, ""))
	if !maps.Equal(got, want) {
		t.Errorf("with no detail of the caller's the detail reads %v, want exactly %v", got, want)
	}
}

// With a tenant in the context, the tenant column is that tenant whatever
// the impersonation scope says.
func TestTheTenantColumnOfAnImpersonationInATenantIsTheContextsTenant(t *testing.T) {
	assembled(t, []core.Module{catalog.Module()}, func(ctx context.Context, reg *core.Registry) error {
		roster, err := core.Resolve[tenant.Catalog](reg)
		if err != nil {
			t.Errorf("resolving the tenant roster: %v", err)
			return errCaseFailed
		}
		recorder, err := core.Resolve[audit.Audit](reg)
		if err != nil {
			t.Errorf("resolving the audit capability: %v", err)
			return errCaseFailed
		}
		for _, registered := range []tenant.Tenant{
			{ID: "t_acme", Name: "acme", Status: tenant.StatusActive},
			{ID: "t_globex", Name: "globex", Status: tenant.StatusActive},
		} {
			if putErr := roster.Put(ctx, registered); putErr != nil {
				t.Errorf("registering %s: %v", registered.ID, putErr)
				return errCaseFailed
			}
		}
		// The identity is confined to t_globex and the work is done in t_acme: the
		// two differ, so the column cannot be read from the wrong one by accident.
		entered, err := roster.Enter(authntest.Context(t, ctx,
			authntest.WithAccount("customer-1"), authntest.WithActor("admin-9"),
			authntest.WithSession("0198f0aa-0000-7000-8000-000000000002"),
			authntest.WithScope("t_globex")), "t_acme")
		if err != nil {
			t.Errorf("entering the tenant: %v", err)
			return errCaseFailed
		}
		if recordErr := recorder.Record(entered, audit.Entry{Action: "scoped-export"}); recordErr != nil {
			t.Errorf("recording: %v", recordErr)
			return errCaseFailed
		}
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		var rows []row
		if findErr := database.DB().Where("action = ?", "scoped-export").Find(&rows).Error; findErr != nil || len(rows) != 1 {
			t.Errorf("reading the row: %v, %d rows, want one", findErr, len(rows))
			return nil
		}
		column := "<null>"
		if rows[0].TenantID != nil {
			column = *rows[0].TenantID
		}
		if column != "t_acme" {
			t.Errorf("the tenant column reads %s, want t_acme, the tenant the work was done under", column)
		}
		if got := detailOfRow(t, rows[0])["impersonation_scope"]; got != "t_globex" {
			t.Errorf("the scope key reads %q, want t_globex", got)
		}
		return nil
	})
}
