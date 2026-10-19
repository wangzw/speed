package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
)

// roleBody is the request a role write's cases send: the description key alone.
// The route's declared input pairs it with the path's tenant and role.
type roleBody struct {
	DescriptionKey string `json:"description_key"`
}

// readRoute binds a read operation's typed handler behind the route's
// identity-and-permission guard, the way the module mounts it.
func readRoute(p *product, handler gin.HandlerFunc) gin.HandlerFunc {
	return p.guardRoute(authz.PermissionRoleRead, handler)
}

// writeRoute binds a write operation's typed handler behind the route's
// identity-and-permission guard, the way the module mounts it.
func writeRoute(p *product, handler gin.HandlerFunc) gin.HandlerFunc {
	return p.guardRoute(authz.PermissionRoleManage, handler)
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
	t       *testing.T
	p       *product
	manager *stubManager
	catalog *stubCatalog
	guard   *stubGuard
	audit   *recordingAudit
	router  *stubRouter
	logs    *logBuffer
}

// newRig builds the module over the stubs, with the permissions a case names
// declared in the catalog and one endpoint declared on the router.
func newRig(t *testing.T, perms ...authz.Permission) *rig {
	t.Helper()
	logs := &logBuffer{}
	r := &rig{
		t:       t,
		manager: &stubManager{},
		catalog: newStubCatalog(perms...),
		guard:   &stubGuard{allow: true},
		audit:   &recordingAudit{},
		router:  newStubRouter("public"),
		logs:    logs,
	}
	r.p = &product{
		manager: r.manager,
		catalog: r.catalog,
		guard:   r.guard,
		audit:   r.audit,
		logger:  logs.logger(),
		cfg:     resolved{endpoints: []string{"public"}},
	}
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

// serveAnonymous calls one handler with no identity on the request, the way an
// anonymous request reaches it when the middleware let it through.
func (r *rig) serveAnonymous(handler gin.HandlerFunc, method, target string, params gin.Params) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.dispatch(handler, method, target, params, nil, func(ctx context.Context) context.Context {
		return authntest.Anonymous(r.t, ctx, authn.AccessOptional)
	})
}

// serveRaw sends a body that is a string as written, not a marshalled value, so
// a case can put a body the handler cannot read in front of it.
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
	c.Set(realmKey, "public")
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
	req = req.WithContext(withCtx(req.Context()))
	c.Request = req
	c.Params = params
	// The mounted route puts the endpoint name on the request so the handler's
	// own 401 carries the realm challenge; a case that calls a handler directly
	// stands in for that by putting the rig's endpoint here.
	c.Set(realmKey, "public")
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

// TestTheStubsStartEmpty holds the fixtures' own invariant: a stub at its zero
// value has recorded nothing, so a case that reads a count is reading calls it
// made and not a fixture that began with some.
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
}
