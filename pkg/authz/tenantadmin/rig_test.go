package tenantadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/i18n"
)

// roleBody is the request a role write's cases send: the description key alone.
type roleBody struct {
	DescriptionKey string `json:"description_key"`
}

// readRoute binds a read operation's typed handler behind the route's
// identity-and-permission guard, the way the module mounts it.
func readRoute(p *product, handler gin.HandlerFunc) gin.HandlerFunc {
	return p.guardRoute(http.MethodGet, authz.PermissionTenantRoleRead, handler)
}

// writeRoute binds a write operation's typed handler behind the route's
// identity-and-registration-and-permission guard, the way the module mounts it.
func writeRoute(p *product, handler gin.HandlerFunc) gin.HandlerFunc {
	return p.guardRoute(http.MethodPut, authz.PermissionTenantRoleManage, handler)
}

// rolePutRoute binds the role write's operation, the one route whose body the
// module prescreens before the entry point binds it.
func rolePutRoute(p *product) gin.HandlerFunc {
	return writeRoute(p, prescreenRoleWrite(rolePutOperation.Handle(p.handleRolePut)))
}

// rig is the module with every dependency a case drives, built directly so a
// case can call one handler with the exact identity and path parameters it wants
// without bringing the entry point up.
type rig struct {
	t          *testing.T
	p          *product
	manager    *stubManager
	catalog    *stubCatalog
	subject    *stubSubjectPermissions
	membership *stubMembership
	guard      *stubGuard
	writeGuard *stubWriteGuard
	audit      *recordingAudit
	router     *stubRouter
	routeGuard *stubRouteGuard
	logs       *logBuffer
	signup     *fakeSignupVerification
	locale     i18n.Locale
}

// newRig builds the module over the stubs. The catalog declares the permissions
// a case names and governs the tenant role codes among them; the caller holds
// the same set. The membership checker admits every account and the path-tenant
// guard passes every request.
func newRig(t *testing.T, perms ...authz.Permission) *rig {
	t.Helper()
	logs := &logBuffer{}
	catalog := newStubCatalog(perms...)
	for _, perm := range perms {
		if perm == authz.PermissionTenantRoleRead || perm == authz.PermissionTenantRoleManage {
			catalog.govern(perm)
		}
	}
	r := &rig{
		t:          t,
		manager:    &stubManager{},
		catalog:    catalog,
		subject:    &stubSubjectPermissions{},
		membership: &stubMembership{member: true},
		guard:      &stubGuard{allow: true},
		writeGuard: &stubWriteGuard{},
		audit:      &recordingAudit{},
		router:     newStubRouter("api"),
		routeGuard: &stubRouteGuard{},
		logs:       logs,
		signup:     &fakeSignupVerification{},
	}
	r.subject.answer(perms...)
	r.p = &product{
		manager:      r.manager,
		catalog:      r.catalog,
		subject:      r.subject,
		membership:   r.membership,
		guard:        r.guard,
		writeGuard:   r.writeGuard,
		audit:        r.audit,
		verification: r.signup,
		routeGuard:   r.routeGuard,
		logger:       logs.logger(),
		cfg:          resolved{endpoints: []string{"api"}},
	}
	return r
}

// localizeWith assembles the message localizer and the request's language.
func (r *rig) localizeWith(localizer i18n.Localizer, locale i18n.Locale) *rig {
	r.p.localizer = localizer
	r.locale = locale
	return r
}

// serve calls one handler with the identity the options build and the path
// parameters given, over a real *gin.Context and a JSON body when one is given.
func (r *rig) serve(handler gin.HandlerFunc, method, target string, params gin.Params, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.dispatch(handler, method, target, params, body, func(ctx context.Context) context.Context {
		return authntest.Context(r.t, ctx, opts...)
	})
}

// serveAnonymous calls one handler with no identity on the request.
func (r *rig) serveAnonymous(handler gin.HandlerFunc, method, target string, params gin.Params) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.dispatch(handler, method, target, params, nil, func(ctx context.Context) context.Context {
		return authntest.Anonymous(r.t, ctx, authn.AccessOptional)
	})
}

// serveRaw sends a body that is a string as written.
func (r *rig) serveRaw(handler gin.HandlerFunc, method, target string, params gin.Params, raw string) *httptest.ResponseRecorder {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, target, strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authntest.Context(r.t, req.Context()))
	c.Request = req
	c.Params = params
	c.Set(realmKey, "api")
	handler(c)
	return w
}

// dispatch builds the request, installs the context withCtx derives and calls
// the handler.
func (r *rig) dispatch(handler gin.HandlerFunc, method, target string, params gin.Params, body any, withCtx func(context.Context) context.Context) *httptest.ResponseRecorder {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			r.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(i18n.WithLocale(withCtx(req.Context()), r.locale))
	c.Request = req
	c.Params = params
	c.Set(realmKey, "api")
	handler(c)
	return w
}

// params builds gin path parameters from a key-value map.
func params(pairs ...string) gin.Params {
	out := make(gin.Params, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, gin.Param{Key: pairs[i], Value: pairs[i+1]})
	}
	return out
}

// writes returns the manager calls that change a table: a Put or a Delete. The
// manager's reads — the role grants a subset check reads, most of all — are
// legitimate before a refusal, so a case that holds "no write reached the
// manager" asks for those and not for every call.
func writes(calls []managerCall) []managerCall {
	out := make([]managerCall, 0, len(calls))
	for _, call := range calls {
		switch call.op {
		case "PutRole", "DeleteRole", "PutGrant", "DeleteGrant", "PutBinding", "DeleteBinding",
			"MarkRoleBuiltin", "ClearAccountBindings":
			out = append(out, call)
		}
	}
	return out
}

// TestTheStubsStartEmpty holds the fixtures' own invariant.
func TestTheStubsStartEmpty(t *testing.T) {
	if got := (&stubManager{}).recorded(); len(got) != 0 {
		t.Errorf("a fresh stub manager recorded %d calls, want none", len(got))
	}
	if got := (&stubCatalog{}).asked(); len(got) != 0 {
		t.Errorf("a fresh stub catalog recorded %d Has queries, want none", len(got))
	}
	if got := (&stubGuard{}).askedFor(); len(got) != 0 {
		t.Errorf("a fresh stub guard recorded %d decisions, want none", len(got))
	}
	if got := (&recordingAudit{}).entries(); len(got) != 0 {
		t.Errorf("a fresh recording audit recorded %d rows, want none", len(got))
	}
	if got := (&stubSubjectPermissions{}).readCount(); got != 0 {
		t.Errorf("a fresh permission reader answered %d reads, want none", got)
	}
	if tenants, accounts := (&stubMembership{}).asked(); len(tenants) != 0 || len(accounts) != 0 {
		t.Errorf("a fresh membership checker answered %v/%v, want no question", tenants, accounts)
	}
}
