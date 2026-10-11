package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// fallbackMsg is the record the entry point's fallback writes when it catches a
// panic. A case that wants to know which layer answered one reads for it.
const fallbackMsg = "a request panicked and the endpoint's recovery layer caught it"

// problemBody decodes the entry point's default error response, keeping every
// member it carries so that a case can say which ones are there.
func problemBody(t *testing.T, what string, got reply) map[string]any {
	t.Helper()
	if media := got.header.Get("Content-Type"); media != "application/problem+json" {
		t.Errorf("%s was written as %q, want application/problem+json", what, media)
	}
	var members map[string]any
	if err := json.Unmarshal([]byte(got.body), &members); err != nil {
		t.Errorf("%s carries a body that is not a JSON object: %q", what, got.body)
		return nil
	}
	return members
}

// TestARefusalIsWrittenThroughTheEntryPointsErrorResponse holds that the gateway
// decides the status and the header and the entry point decides the rest.
//
// The status is the gateway's conclusion, the challenge is set before the
// response is written and survives it, and the body is the entry point's
// default problem details object carrying the status and the standard title of
// it and nothing else: why the request was refused, and which backend said so,
// reach the log and no further. A body that carried the backend's cause would
// hand a caller the text of a backend's own error.
func TestARefusalIsWrittenThroughTheEntryPointsErrorResponse(t *testing.T) {
	const cause = "wrong password for alice in db-7"
	for _, c := range []struct {
		name      string
		verify    func(context.Context, authn.Credential) authn.Verdict
		edit      func(*nethttp.Request)
		status    int
		title     string
		challenge bool
	}{
		{
			name:      "a request with no credential",
			edit:      func(*nethttp.Request) {},
			status:    nethttp.StatusUnauthorized,
			title:     "Unauthorized",
			challenge: true,
		},
		{
			name: "a credential that does not hold",
			verify: func(context.Context, authn.Credential) authn.Verdict {
				return authn.Denied(errors.New(cause))
			},
			edit:      asAlice,
			status:    nethttp.StatusUnauthorized,
			title:     "Unauthorized",
			challenge: true,
		},
		{
			name: "a backend that cannot answer",
			verify: func(context.Context, authn.Credential) authn.Verdict {
				return authn.Unavailable(errors.New("the store at db-7 is unreachable"))
			},
			edit:   asAlice,
			status: nethttp.StatusServiceUnavailable,
			title:  "Service Unavailable",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			backend := basicBackend("static", "static.")
			backend.verify = c.verify

			var got reply
			liveAssembly{
				backends: []authn.Verifier{backend},
				routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/orders"}},
			}.serve(t, func(l *live) {
				got = l.send("public", "GET", "/orders", c.edit)
			})

			requireStatus(t, c.name, got, c.status)
			members := problemBody(t, c.name, got)
			if members["status"] != float64(c.status) || members["title"] != c.title ||
				members["type"] != "about:blank" {
				t.Errorf("%s carries %v, want the problem details for status %d titled %q",
					c.name, members, c.status, c.title)
			}
			if len(members) != 3 {
				t.Errorf("%s carries the members %v, want the type, the title and the status "+
					"alone: the cause and the attribution belong to the log", c.name, members)
			}
			for _, leaked := range []string{"db-7", "static", "alice"} {
				if strings.Contains(got.body, leaked) {
					t.Errorf("%s carries %q in its body %q, which is what only the log is "+
						"for", c.name, leaked, got.body)
				}
			}

			challenge := got.header.Get(challengeHeader)
			switch {
			case c.challenge && challenge != `Basic realm="public"`:
				t.Errorf("%s carries the challenge %q, want %q", c.name, challenge,
					`Basic realm="public"`)
			case !c.challenge && challenge != "":
				t.Errorf("%s carries the challenge %q, but only a 401 offers one", c.name, challenge)
			}
		})
	}
}

// shapedResponder is the error shape of a module that owns an API's responses.
// It records what it was asked to write, which is how a case reads what the
// gateway handed the entry point.
type shapedResponder struct {
	mu    sync.Mutex
	asked []speedhttp.Problem
}

func (s *shapedResponder) RespondError(w nethttp.ResponseWriter, _ *nethttp.Request, p speedhttp.Problem) {
	s.mu.Lock()
	s.asked = append(s.asked, p)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(p.Status)
	_, _ = fmt.Fprintf(w, "shaped %d", p.Status)
}

// problems returns what the responder has been asked to write.
func (s *shapedResponder) problems() []speedhttp.Problem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]speedhttp.Problem(nil), s.asked...)
}

// TestADeliveredErrorResponderDecidesTheShapeAndTheHeaderStays holds the other
// half: when a module delivers the error shape, it decides the whole of it, and
// the header the gateway set before asking for the response is still on it.
//
// What the gateway hands over is the status. The body, the media type and
// everything about the shape are the responder's, and a 401 that lost its
// challenge on the way through would leave the caller not knowing what to send.
func TestADeliveredErrorResponderDecidesTheShapeAndTheHeaderStays(t *testing.T) {
	responder := &shapedResponder{}
	delivers := core.Module{
		Name:     "stand-in.responder",
		Provides: []core.Provision{{Token: (*speedhttp.ErrorResponder)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return responder, nil },
	}

	// The first request carries no credential and is refused by the gateway
	// before any backend is asked; the second reaches the backend, which
	// cannot answer.
	unreachable := basicBackend("static", "static.")
	unreachable.verify = func(context.Context, authn.Credential) authn.Verdict {
		return authn.Unavailable(errors.New("the store at db-7 is unreachable"))
	}
	var refused, outage reply
	liveAssembly{
		backends: []authn.Verifier{unreachable},
		routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/orders"}},
		modules:  []core.Module{delivers},
	}.serve(t, func(l *live) {
		refused = l.send("public", "GET", "/orders")
		outage = l.send("public", "GET", "/orders", asAlice)
	})

	requireStatus(t, "a refusal with the responder delivered", refused, nethttp.StatusUnauthorized)
	if refused.body != "shaped 401" {
		t.Errorf("a refusal was written as %q, want the responder's own shape", refused.body)
	}
	if media := refused.header.Get("Content-Type"); media != "text/plain; charset=utf-8" {
		t.Errorf("a refusal was written with the media type %q, want the responder's", media)
	}
	if got := refused.header.Get(challengeHeader); got != `Basic realm="public"` {
		t.Errorf("a refusal carries the challenge %q, want %q: the header set before the "+
			"response is asked for stays on it", got, `Basic realm="public"`)
	}

	requireStatus(t, "an outage with the responder delivered", outage, nethttp.StatusServiceUnavailable)
	if outage.body != "shaped 503" {
		t.Errorf("an outage was written as %q, want the responder's own shape", outage.body)
	}

	asked := responder.problems()
	if len(asked) != 2 {
		t.Fatalf("the responder was asked to write %d response(s), want the two the gateway "+
			"refused: the gateway writes through it and nowhere else", len(asked))
	}
	for _, p := range asked {
		if p.Type != "" || p.Title != "" || p.Detail != "" || p.Instance != "" || p.Cause != nil {
			t.Errorf("the gateway handed the responder %+v, want the status alone: why a request "+
				"was refused and whose refusal it was are for the log", p)
		}
	}
}

// TestABackendPanicIsAnsweredByTheGatewayNotTheFallback holds where a backend's
// panic is caught.
//
// The entry point has a fallback that answers any panic with a 500, and it
// cannot say which layer or module crashed. The gateway catches a backend's
// panic at the call site, which is what answers it as the outage it is — a 503,
// charged to that backend, with its stack in the log — and what lets every
// other backend go on being answered by the same process. A 500 here would be
// the fallback's answer, with no backend named.
func TestABackendPanicIsAnsweredByTheGatewayNotTheFallback(t *testing.T) {
	crashing := basicBackend("static", "static.")
	crashing.verify = func(context.Context, authn.Credential) authn.Verdict {
		panic("the backend's store is nil")
	}
	healthy := passes(schemeBackend("tokens", "bearer"))

	var crashed, survived reply
	logs := liveAssembly{
		backends: []authn.Verifier{crashing, healthy},
		routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/orders"}},
	}.serve(t, func(l *live) {
		crashed = l.send("public", "GET", "/orders", asAlice)
		survived = l.send("public", "GET", "/orders", asBearer("t"))
	})

	requireStatus(t, "a request whose backend panicked", crashed, nethttp.StatusServiceUnavailable)
	if members := problemBody(t, "the answer to a backend's panic", crashed); members["status"] != float64(503) {
		t.Errorf("the answer to a backend's panic carries %v, want the problem details for 503", members)
	}
	if got := crashed.header.Get(challengeHeader); got != "" {
		t.Errorf("the answer to a backend's panic carries the challenge %q, but only a 401 offers one", got)
	}
	requireStatus(t, "a request for another backend after the panic", survived, nethttp.StatusOK)

	var charged slog.Record
	found := false
	for _, r := range logs.recordsOf() {
		if strings.Contains(r.Message, "the backend could not be asked about this credential") {
			charged, found = r, true
		}
	}
	if !found {
		t.Fatal("the gateway wrote no record of the backend it could not ask")
	}
	if charged.Level != slog.LevelError {
		t.Errorf("the backend's panic was logged at %v, want %v", charged.Level, slog.LevelError)
	}
	if got := attrText(charged, "attributed_to"); got != "static" {
		t.Errorf("the backend's panic was charged to %q, want the backend that panicked", got)
	}
	if got := attrText(charged, "stack"); got == "" {
		t.Error("the record of a backend's panic carries no stack")
	}
	if _, fell := logs.recordNamed(fallbackMsg); fell {
		t.Error("the entry point's fallback caught a panic, so the gateway did not catch the " +
			"backend's at the call site")
	}
}

// TestAHandlerPanicBehindTheGatewayIsTheEntryPointsToAnswer holds the boundary
// of that recover: it is the gateway's own, and stops where the gateway's code
// does. A panic in a handler the gateway let through belongs to the fallback,
// which answers it as the 500 it is and writes its own record; the gateway
// neither catches it nor writes a line about a defect that is not its.
func TestAHandlerPanicBehindTheGatewayIsTheEntryPointsToAnswer(t *testing.T) {
	var crashed reply
	logs := liveAssembly{
		backends: []authn.Verifier{passes(basicBackend("static", "static."))},
		routes: []liveRoute{{
			endpoint: "public", method: "GET", path: "/boom",
			handler: func(*gin.Context) { panic("the handler's table is nil") },
		}},
	}.serve(t, func(l *live) {
		crashed = l.send("public", "GET", "/boom", asAlice)
	})

	requireStatus(t, "a request whose handler panicked", crashed, nethttp.StatusInternalServerError)
	if _, fell := logs.recordNamed(fallbackMsg); !fell {
		t.Error("the entry point's fallback wrote no record of the handler's panic")
	}
	for _, r := range logs.recordsOf() {
		if strings.Contains(r.Message, "the gateway panicked") {
			t.Errorf("the gateway wrote %q about a panic that was not in its code", r.Message)
		}
	}
}

// TestAPassHandsTheIdentityToTheHandlerBehindTheEntryPoint holds that the
// identity the gateway stamps is the one the handler reads from the request it
// is given, through the real chain: the layer replaces the request, and the
// handler is the next link of that chain.
func TestAPassHandsTheIdentityToTheHandlerBehindTheEntryPoint(t *testing.T) {
	var got reply
	liveAssembly{
		backends: []authn.Verifier{passes(basicBackend("static", "static."))},
		routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/orders"}},
	}.serve(t, func(l *live) {
		got = l.send("public", "GET", "/orders", asAlice)
	})

	requireStatus(t, "a request with a good credential", got, nethttp.StatusOK)
	if want := "reached as static.alice [required]"; got.body != want {
		t.Errorf("the handler saw %q, want %q", got.body, want)
	}
}
