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

const (
	callerHeader = "X-Test-Caller"

	ordinaryCaller      = "ordinary"
	otherCaller         = "other"
	victimCaller        = "victim"
	operatorCaller      = "operator"
	impersonationCaller = "impersonation"
)

const (
	otherAccount    authn.AccountID = "other-account"
	victimAccount   authn.AccountID = "victim-account"
	operatorAccount authn.AccountID = "operator-account"
)

const impersonationSession authn.SessionID = "0190f3b2-8f3a-7c3e-9d11-5b6f2a1c4e77"

const impersonationScope = "acme"

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

func identityOf(t testing.TB, opts ...authntest.Option) authn.Identity {
	t.Helper()
	id, ok := authn.FromContext(authntest.Context(t, context.Background(), opts...))
	if !ok {
		t.Fatal("authntest.Context left no identity in the context")
	}
	return id
}

func callerKey(t testing.TB, name string) string {
	t.Helper()
	opts, ok := callers[name]
	if !ok {
		t.Fatalf("no caller is named %q", name)
	}
	return identityOf(t, opts...).Key()
}

func accountKey(t testing.TB) string {
	t.Helper()
	return callerKey(t, ordinaryCaller)
}

func checkCallers(t testing.TB) {
	t.Helper()
	for name := range callers {
		callerKey(t, name)
	}
}

func stampingLayer(t testing.TB) speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:  "stand-in.identity",
		Stage: speedhttp.StageIdentify,
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

func as(name string) map[string]string { return map[string]string{callerHeader: name} }

func windowIndex() int64 { return pinnedAt.UnixNano() / int64(time.Minute) }

// TestTheLayerCountsARequestUnderTheKeyOfTheIdentityItCarries checks that two
// callers each spend a count of their own.
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
	requireThrottledProblem(t, h.send(t, nethttp.MethodGet, "/things", as(ordinaryCaller)))

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

// TestAnImpersonationIsCountedUnderTheActorAndNotTheImpersonatedAccount checks
// that an impersonation spends the operator's allowance, not the victim's.
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
	requireThrottledProblem(t, h.send(t, nethttp.MethodGet, "/things", as(operatorCaller)))

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
