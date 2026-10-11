package ratelimit

import (
	"context"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The callers a request can be made as. Each is the options authntest builds an
// identity from, so the identity a request carries is one the gateway would
// stamp, and the key it is counted under is read off that identity rather than
// written down beside the test.
const (
	// callerHeader names the caller a request is made as. A request that names
	// none is made as ordinaryCaller.
	callerHeader = "X-Test-Caller"

	// ordinaryCaller is an account acting for itself, with every field at the
	// default authntest gives it.
	ordinaryCaller = "ordinary"
	// otherCaller is a second account acting for itself.
	otherCaller = "other"
	// victimCaller is the account an operator impersonates, acting for itself.
	victimCaller = "victim"
	// operatorCaller is the account that impersonates the victim, acting for
	// itself.
	operatorCaller = "operator"
	// impersonationCaller is the operator's hands on the victim's account: the
	// account is the victim and the actor is the operator.
	impersonationCaller = "impersonation"
)

// Account names of the callers.
const (
	otherAccount    authn.AccountID = "other-account"
	victimAccount   authn.AccountID = "victim-account"
	operatorAccount authn.AccountID = "operator-account"
)

// The login session and the tenant an impersonation carries.
const impersonationSession authn.SessionID = "0190f3b2-8f3a-7c3e-9d11-5b6f2a1c4e77"

const impersonationScope = "acme"

// callers are the identities the stamping layer can put on a request, by name.
var callers = map[string][]authntest.Option{
	ordinaryCaller: nil,
	otherCaller:    {authntest.WithAccount(otherAccount)},
	victimCaller:   {authntest.WithAccount(victimAccount)},
	operatorCaller: {authntest.WithAccount(operatorAccount)},
	impersonationCaller: {
		authntest.WithAccount(victimAccount),
		authntest.WithActor(operatorAccount),
		authntest.WithSession(impersonationSession),
		authntest.WithScope(impersonationScope),
	},
}

// identityOf reads back the identity authntest builds for opts, through
// authn.FromContext as a module reading a request's identity does.
func identityOf(t testing.TB, opts ...authntest.Option) authn.Identity {
	t.Helper()
	id, ok := authn.FromContext(authntest.Context(t, context.Background(), opts...))
	if !ok {
		t.Fatal("authntest.Context left no identity in the context")
	}
	return id
}

// callerKey is the key a request made as the named caller is counted under.
func callerKey(t testing.TB, name string) string {
	t.Helper()
	opts, ok := callers[name]
	if !ok {
		t.Fatalf("no caller is named %q", name)
	}
	return identityOf(t, opts...).Key()
}

// accountKey is the key of an account acting for itself, for the cases that
// count under one identity and care about no other.
func accountKey(t testing.TB) string {
	t.Helper()
	return callerKey(t, ordinaryCaller)
}

// checkCallers builds every caller once on the case's own goroutine, so that
// the stamping layer, which builds them again on a request's, never meets a
// combination authntest refuses.
func checkCallers(t testing.TB) {
	t.Helper()
	for name := range callers {
		callerKey(t, name)
	}
}

// stampingLayer stands where the gateway stands: it puts the identity of the
// caller a request names on the request's context, through authntest, and puts
// none on a request that carries anonymousHeader. It sits outside every other
// layer on the endpoint.
func stampingLayer(t testing.TB) speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:  "stand-in.identity",
		Order: -1000,
		Handler: func(c *gin.Context) {
			if _, anonymous := c.Request.Header[anonymousHeader]; anonymous {
				c.Next()
				return
			}
			name := c.GetHeader(callerHeader)
			if name == "" {
				name = ordinaryCaller
			}
			opts, ok := callers[name]
			if !ok {
				t.Errorf("a request names the caller %q, which no case declares", name)
				c.AbortWithStatus(nethttp.StatusInternalServerError)
				return
			}
			c.Request = c.Request.WithContext(authntest.Context(t, c.Request.Context(), opts...))
			c.Next()
		},
	}
}

// as is the header a request names its caller by.
func as(name string) map[string]string { return map[string]string{callerHeader: name} }

// windowIndex is the window the pinned clock stands in, for a window of one
// minute.
func windowIndex() int64 { return pinnedAt.UnixNano() / int64(time.Minute) }

// The layer the module's own descriptor registers reads the identity off the
// request's context and counts under its key. Two callers each spend a count of
// their own, so a layer that counted every identified request under one key, or
// under an empty one, would refuse the other caller's first request.
func TestTheLayerCountsARequestUnderTheKeyOfTheIdentityItCarries(t *testing.T) {
	counters := newFakeKV()
	l := pinned("public")
	l.counters = counters
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 1}}
	h := startHost(t, hostOptions{limiter: l, viaModule: true})

	if got := h.send(t, nethttp.MethodGet, "/things", as(ordinaryCaller)); got.status != nethttp.StatusNoContent {
		t.Fatalf("the first request of one caller was answered %d, want the route's 204", got.status)
	}
	if got := h.send(t, nethttp.MethodGet, "/things", as(otherCaller)); got.status != nethttp.StatusNoContent {
		t.Fatalf("the first request of another caller was answered %d, want the route's 204: the count is not per identity", got.status)
	}
	requireProblem(t, h.send(t, nethttp.MethodGet, "/things", as(ordinaryCaller)), nethttp.StatusTooManyRequests)

	if got := counters.value(countKey("", callerKey(t, ordinaryCaller), windowIndex())); got != 2 {
		t.Errorf("the first caller's window holds %v, want both of its requests, the refused one included", got)
	}
	if got := counters.value(countKey("", callerKey(t, otherCaller), windowIndex())); got != 1 {
		t.Errorf("the second caller's window holds %v, want its one request", got)
	}
	if got := counters.size(); got != 2 {
		t.Errorf("the store holds %d keys, want one per caller", got)
	}
}

// An impersonation is counted under the account whose hands are on the request,
// not under the account it impersonates: it spends the operator's allowance and
// leaves the victim's alone. The limit is one, so each direction is visible in
// what a later request of the other account is answered with: the victim's own
// first request still passes after the impersonation, and the operator's own
// first request is already refused.
func TestAnImpersonationIsCountedUnderTheActorAndNotTheImpersonatedAccount(t *testing.T) {
	impersonation := identityOf(t, callers[impersonationCaller]...)
	if impersonation.Account() != victimAccount || impersonation.Actor() != operatorAccount {
		t.Fatalf("the impersonation is account %q by actor %q, want %q by %q",
			impersonation.Account(), impersonation.Actor(), victimAccount, operatorAccount)
	}
	if impersonation.Key() != string(operatorAccount) {
		t.Fatalf("the impersonation's key is %q, want the actor's account %q", impersonation.Key(), operatorAccount)
	}

	counters := newFakeKV()
	l := pinned("public")
	l.counters = counters
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 1}}
	h := startHost(t, hostOptions{limiter: l, viaModule: true})

	if got := h.send(t, nethttp.MethodGet, "/things", as(impersonationCaller)); got.status != nethttp.StatusNoContent {
		t.Fatalf("the impersonation's request was answered %d, want the route's 204", got.status)
	}
	if got := h.send(t, nethttp.MethodGet, "/things", as(victimCaller)); got.status != nethttp.StatusNoContent {
		t.Fatalf("the victim's own request was answered %d, want 204: the impersonation was counted against the victim", got.status)
	}
	requireProblem(t, h.send(t, nethttp.MethodGet, "/things", as(operatorCaller)), nethttp.StatusTooManyRequests)

	if got := counters.value(countKey("", string(operatorAccount), windowIndex())); got != 2 {
		t.Errorf("the operator's window holds %v, want the impersonation and its own refused request", got)
	}
	if got := counters.value(countKey("", string(victimAccount), windowIndex())); got != 1 {
		t.Errorf("the victim's window holds %v, want its own request alone", got)
	}
	if got := counters.size(); got != 2 {
		t.Errorf("the store holds %d keys, want one for the operator and one for the victim", got)
	}
}
