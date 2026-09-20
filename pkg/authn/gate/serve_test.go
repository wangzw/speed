package gate

import (
	"context"
	"encoding/base64"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/identity"
)

// arrival is what the layer behind the gateway saw.
type arrival struct {
	reached bool
	id      identity.Identity
	carried bool
}

// run puts one request through the gateway's layer on an endpoint and reports
// the response together with what reached the layer behind it.
func run(g *gateway, endpoint string, r *nethttp.Request) (*httptest.ResponseRecorder, *arrival) {
	handler, seen := layerOn(g, endpoint)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w, seen
}

// layerOn builds the gateway's layer for one endpoint, without serving
// anything through it yet. The layer is assembled in Init, where the ownership
// table exists, so a case that wants to break the gateway's state has to build
// the layer first and break it afterwards.
func layerOn(g *gateway, endpoint string) (nethttp.Handler, *arrival) {
	seen := &arrival{}
	handler := g.middlewareFor(endpoint).Wrap(nethttp.HandlerFunc(
		func(_ nethttp.ResponseWriter, passed *nethttp.Request) {
			seen.reached = true
			seen.id, seen.carried = identity.From(passed.Context())
		}))
	return handler, seen
}

// TestAnExemptRouteReachesNoBackendAtAll holds the ordering the whole exemption
// mechanism rests on.
//
// The exemption is judged before the Authorization header is read. Were it
// judged after, a public health check would consume a backend call on every
// probe, and a backend that is down would take the health check down with it —
// the one route that must answer when everything else cannot.
func TestAnExemptRouteReachesNoBackendAtAll(t *testing.T) {
	var asked atomic.Int64
	backend := enumerating("local", "alice")
	backend.asked = &asked

	g := testGateway(t, []string{"public"}, backend)
	g.Exempt("public", "GET /health")
	sealed(t, g, routerWith("public"))

	// The request carries a credential, so that what is being held is the
	// ordering and not merely the absence of a header.
	w, seen := run(g, "public", withBasic(request("GET", "/health"), "alice", "secret"))

	if w.Code != nethttp.StatusOK {
		t.Errorf("an exempt route answered %d, want %d", w.Code, nethttp.StatusOK)
	}
	if !seen.reached {
		t.Error("the layer behind the gateway was not reached on an exempt route")
	}
	if asked.Load() != 0 {
		t.Errorf("a backend was asked %d time(s) on an exempt route, want none: the exemption is "+
			"judged before the credential is read", asked.Load())
	}
	if seen.carried {
		t.Error("an exempt request carries an identity, but nothing authenticated it")
	}
}

// TestARequestWithNoUsableCredentialIsRefusedByTheGateway holds the refusals
// the gateway reaches without asking anybody.
//
// Two Authorization headers is the case worth naming: picking one of them would
// let a caller who can add a header choose which credential is judged, and
// which one that is would be the header order.
func TestARequestWithNoUsableCredentialIsRefusedByTheGateway(t *testing.T) {
	for _, c := range []struct {
		name    string
		prepare func(*nethttp.Request) *nethttp.Request
	}{
		{
			name:    "no Authorization header",
			prepare: func(r *nethttp.Request) *nethttp.Request { return r },
		},
		{
			name: "an empty Authorization header",
			prepare: func(r *nethttp.Request) *nethttp.Request {
				r.Header.Set(authHeader, "   ")
				return r
			},
		},
		{
			name: "two Authorization headers",
			prepare: func(r *nethttp.Request) *nethttp.Request {
				r.Header.Add(authHeader, "Basic YWxpY2U6c2VjcmV0")
				r.Header.Add(authHeader, "Basic Ym9iOnNlY3JldA==")
				return r
			},
		},
		{
			name: "a scheme nobody claims, carrying no readable subject",
			prepare: func(r *nethttp.Request) *nethttp.Request {
				r.Header.Set(authHeader, "Bearer an.opaque.token")
				return r
			},
		},
		{
			name: "a Basic credential that is not base64",
			prepare: func(r *nethttp.Request) *nethttp.Request {
				r.Header.Set(authHeader, "Basic not-base64!")
				return r
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked atomic.Int64
			backend := passes(enumerating("local", "alice"))
			backend.asked = &asked

			g := testGateway(t, []string{"public"}, backend)
			sealed(t, g, routerWith("public"))

			w, seen := run(g, "public", c.prepare(request("GET", "/orders")))

			if w.Code != nethttp.StatusUnauthorized {
				t.Errorf("the request answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
			}
			if seen.reached {
				t.Error("the layer behind the gateway was reached by a request the gateway refused")
			}
			if asked.Load() != 0 {
				t.Errorf("a backend was asked %d time(s) about a credential the gateway could not "+
					"resolve an owner for, want none", asked.Load())
			}
			if got := g.tally()["denied"]; got != 1 {
				t.Errorf("the refusal was counted %d time(s) in the denied bucket, want 1", got)
			}
		})
	}
}

// TestTheGatewayReadsASubjectOutOfBasicAndNothingElse holds the one credential
// format this module understands by itself.
//
// Step 1 splits on whether a backend claims the credential's scheme whole. When
// none does, the gateway has to read the subject, and it can only do that where
// a standard fixes where the subject sits — Basic, and no other. A gateway that
// guessed at another scheme's layout would hand a backend a subject read out of
// a structure nobody promised, which is a silent misreading rather than a
// refusal.
//
// The case is written with a credential that is valid base64 of user:password
// under a scheme nobody claims. A malformed value would be refused for being
// malformed, and would hold nothing about the scheme at all.
func TestTheGatewayReadsASubjectOutOfBasicAndNothingElse(t *testing.T) {
	readable := base64.StdEncoding.EncodeToString([]byte("alice:secret"))

	for _, c := range []struct {
		scheme string
		want   int
		why    string
	}{
		{"Basic", nethttp.StatusOK, "the one scheme a standard fixes the subject's place in"},
		{"Digest", nethttp.StatusUnauthorized, "nobody claims it, and its value is not a " +
			"base64 user:password however much it happens to look like one here"},
		{"Bearer", nethttp.StatusUnauthorized, "the same, for the scheme most likely to be " +
			"mistaken for one the gateway understands"},
	} {
		t.Run(c.scheme, func(t *testing.T) {
			var asked atomic.Int64
			backend := passes(enumerating("local", "alice"))
			backend.asked = &asked

			g := testGateway(t, []string{"public"}, backend)
			sealed(t, g, routerWith("public"))

			r := request("GET", "/orders")
			r.Header.Set(authHeader, c.scheme+" "+readable)
			w, _ := run(g, "public", r)

			if w.Code != c.want {
				t.Errorf("a %s credential answered %d, want %d: %s",
					c.scheme, w.Code, c.want, c.why)
			}
			if c.want == nethttp.StatusUnauthorized && asked.Load() != 0 {
				t.Errorf("a backend was asked %d time(s) about a scheme the gateway cannot "+
					"read and nobody claimed, want none", asked.Load())
			}
		})
	}
}

// TestOwnershipIsPutToExactlyOneBackend holds the four-step sequence: which
// backend is asked, and with what subject.
//
// The sequence is the whole of it. A credential that reaches the end of it
// without an owner is refused, never handed to a backend picked some other way,
// which is what stops one backend from answering about another's subjects.
func TestOwnershipIsPutToExactlyOneBackend(t *testing.T) {
	yes := func(context.Context, string) (bool, error) { return true, nil }
	no := func(context.Context, string) (bool, error) { return false, nil }

	t.Run("a scheme claimed whole reaches its claimant, which reads the subject itself",
		func(t *testing.T) {
			var handed string
			issuer := claimingScheme("issuer", "Bearer")
			issuer.verify = func(_ context.Context, subject string, c authn.Credential) authn.Verdict {
				handed = subject
				return authn.Authenticated("carol")
			}

			g := testGateway(t, []string{"public"}, issuer, passes(enumerating("local", "alice")))
			sealed(t, g, routerWith("public"))

			r := request("GET", "/orders")
			r.Header.Set(authHeader, "Bearer an.opaque.token")
			w, seen := run(g, "public", r)

			if w.Code != nethttp.StatusOK {
				t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
			}
			if handed != "" {
				t.Errorf("the gateway handed the subject %q to a backend that claims the scheme "+
					"whole, want none: that backend reads the subject out of the credential",
					handed)
			}
			if seen.id.Owner() != "issuer" {
				t.Errorf("the identity is owned by %q, want the backend that claimed the scheme",
					seen.id.Owner())
			}
		})

	t.Run("a subject naming its owner reaches that owner, with the name stripped",
		func(t *testing.T) {
			var handed string
			second := enumerating("second", "alice")
			second.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
				handed = subject
				return authn.Authenticated(subject)
			}

			g := testGateway(t, []string{"public"},
				passes(enumerating("first", "bob")), second)
			sealed(t, g, routerWith("public"))

			w, seen := run(g, "public",
				withBasic(request("GET", "/orders"), "second/alice", "secret"))

			if w.Code != nethttp.StatusOK {
				t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
			}
			if handed != "alice" {
				t.Errorf("the backend was asked about %q, want the bare subject", handed)
			}
			if got := seen.id.Key(); got != "second/alice" {
				t.Errorf("the identity reads %q, want the qualified form", got)
			}
		})

	t.Run("a subject naming an owner no backend reports is refused", func(t *testing.T) {
		var asked atomic.Int64
		backend := passes(enumerating("local", "alice"))
		backend.asked = &asked

		g := testGateway(t, []string{"public"}, backend)
		sealed(t, g, routerWith("public"))

		w, _ := run(g, "public", withBasic(request("GET", "/orders"), "absent/alice", "secret"))

		if w.Code != nethttp.StatusUnauthorized {
			t.Errorf("the request answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
		}
		if asked.Load() != 0 {
			t.Errorf("a backend was asked %d time(s) about a subject qualified with another "+
				"backend's name, want none", asked.Load())
		}
	})

	t.Run("an enumerated subject reaches the backend that wrote it out", func(t *testing.T) {
		var otherAsked atomic.Int64
		other := passes(enumerating("other", "bob"))
		other.asked = &otherAsked

		g := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")), other)
		sealed(t, g, routerWith("public"))

		w, seen := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

		if w.Code != nethttp.StatusOK {
			t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
		}
		if seen.id.Owner() != "local" {
			t.Errorf("the identity is owned by %q, want the backend that enumerated the subject",
				seen.id.Owner())
		}
		if otherAsked.Load() != 0 {
			t.Errorf("the backend that did not claim the subject was asked %d time(s), want none",
				otherAsked.Load())
		}
	})

	t.Run("a bare subject exactly one open claim owns reaches that claim", func(t *testing.T) {
		owner := openly("directory", yes)
		owner.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
			return authn.Authenticated(subject)
		}

		g := testGateway(t, []string{"public"}, owner, openly("elsewhere", no))
		sealed(t, g, routerWith("public"))

		w, seen := run(g, "public", withBasic(request("GET", "/orders"), "dave", "secret"))

		if w.Code != nethttp.StatusOK {
			t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
		}
		if got := seen.id.Key(); got != "directory/dave" {
			t.Errorf("the identity reads %q, want the open claimant's qualification", got)
		}
	})

	t.Run("a bare subject no open claim owns is refused", func(t *testing.T) {
		g := testGateway(t, []string{"public"}, openly("directory", no))
		sealed(t, g, routerWith("public"))

		w, _ := run(g, "public", withBasic(request("GET", "/orders"), "nobody", "secret"))

		if w.Code != nethttp.StatusUnauthorized {
			t.Errorf("the request answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
		}
	})
}

// TestQualifyingASubjectPicksTheBackendWithoutGrantingItAuthority holds the
// difference between disambiguating and escalating.
//
// The owner-qualified writing form exists so a caller under several backends can
// say which one it belongs to. It must not be able to do more than that: if
// naming a backend were enough to reach it, a caller could take a subject the
// gateway would refuse outright — nobody claims it — and get it put to a backend
// by prefixing a name it can guess. Exit binding would not catch that, because
// the subject a backend returns on this path is the subject it was asked about,
// so the only thing standing here is the ownership check itself.
func TestQualifyingASubjectPicksTheBackendWithoutGrantingItAuthority(t *testing.T) {
	owns := func(_ context.Context, subject string) (bool, error) {
		return subject == "dave", nil
	}

	for _, c := range []struct {
		name    string
		written string
		want    int
		why     string
	}{
		{
			name:    "a subject the named backend enumerated",
			written: "local/alice",
			want:    nethttp.StatusOK,
			why:     "the qualification is what the writing form is for",
		},
		{
			name:    "a subject the named backend answers for openly",
			written: "directory/dave",
			want:    nethttp.StatusOK,
			why:     "an open claim answers ownership for itself, qualified or not",
		},
		{
			name:    "a subject the named backend does not claim",
			written: "local/mallory",
			want:    nethttp.StatusUnauthorized,
			why: "naming a backend must not reach it with a subject it is not the " +
				"authority for — bare mallory is refused, and the prefix must not change that",
		},
		{
			name:    "a subject the named open claim says is not its own",
			written: "directory/mallory",
			want:    nethttp.StatusUnauthorized,
			why:     "the open claim answered no, and the qualification does not overrule it",
		},
		{
			name:    "a subject qualified with a backend that claims only a scheme",
			written: "issuer/admin",
			want:    nethttp.StatusUnauthorized,
			why: "a whole-scheme claim says which credentials come here, not which " +
				"subjects belong here, so it is the authority for none of them",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			directory := openly("directory", owns)
			directory.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
				return authn.Authenticated(subject)
			}

			g := testGateway(t, []string{"public"},
				passes(enumerating("local", "alice")),
				directory,
				passes(claimingScheme("issuer", "Bearer")))
			sealed(t, g, routerWith("public"))

			w, _ := run(g, "public", withBasic(request("GET", "/orders"), c.written, "secret"))
			if w.Code != c.want {
				t.Errorf("%q answered %d, want %d: %s", c.written, w.Code, c.want, c.why)
			}
		})
	}
}

// TestABareSubjectAndItsQualifiedFormAgree holds the two writing forms against
// each other. A subject the gateway refuses bare must stay refused however it is
// qualified, and one it admits bare must be admitted qualified — otherwise the
// qualification is a second, weaker way in.
func TestABareSubjectAndItsQualifiedFormAgree(t *testing.T) {
	for _, subject := range []string{"alice", "mallory"} {
		t.Run(subject, func(t *testing.T) {
			bare := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
			sealed(t, bare, routerWith("public"))
			bareCode, _ := run(bare, "public", withBasic(request("GET", "/orders"), subject, "secret"))

			named := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
			sealed(t, named, routerWith("public"))
			namedCode, _ := run(named, "public",
				withBasic(request("GET", "/orders"), "local/"+subject, "secret"))

			if bareCode.Code != namedCode.Code {
				t.Errorf("%q answers %d bare and %d as local/%s; the qualification says which "+
					"backend, never whether", subject, bareCode.Code, namedCode.Code, subject)
			}
		})
	}
}

// TestTwoOpenClaimsOnOneSubjectAreRefusedWithTheWayOut holds the overlap the
// build could not detect, and the one thing the log line has to carry.
//
// Nothing else in the process can tell the operator which backends collided or
// how to get past it, so the refusal names both and gives the qualified form
// that resolves it.
func TestTwoOpenClaimsOnOneSubjectAreRefusedWithTheWayOut(t *testing.T) {
	yes := func(context.Context, string) (bool, error) { return true, nil }

	g := testGateway(t, []string{"public"}, openly("first", yes), openly("second", yes))
	sealed(t, g, routerWith("public"))

	// The request path is held first, then the refusal itself is read for
	// the attributes that only reach the log.
	w, seen := run(g, "public", withBasic(request("GET", "/orders"), "dave", "secret"))
	if w.Code != nethttp.StatusUnauthorized {
		t.Errorf("a subject two open claims both own answered %d, want %d",
			w.Code, nethttp.StatusUnauthorized)
	}
	if seen.reached {
		t.Error("the layer behind the gateway was reached by a subject with two authorities")
	}

	deny := mustRefuse(t, g, "dave")
	if deny.status != nethttp.StatusUnauthorized {
		t.Errorf("the refusal answers %d, want %d", deny.status, nethttp.StatusUnauthorized)
	}
	if deny.backend != "" {
		t.Errorf("the refusal is charged to %q, want the gateway: the judgement is the "+
			"ownership table's, and the table is the gateway's", deny.backend)
	}
	rendered := renderAttrs(deny.attrs)
	for _, want := range []string{"first", "second", "first/dave", "second/dave"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the refusal's attributes read %q and do not carry %q; nothing else in the "+
				"process can tell the operator who collided or how to get past it",
				rendered, want)
		}
	}
}

// TestAnOpenClaimThatCannotAnswerEndsTheRequestAsAnOutage holds the one place a
// failure must not be read as a "no".
//
// A claim that could not be asked is not a claim that said the subject is not
// its own. Reading it as one would hand that backend's subjects to whoever
// answers next, which is an authentication decision made by a backend that was
// never consulted.
func TestAnOpenClaimThatCannotAnswerEndsTheRequestAsAnOutage(t *testing.T) {
	yes := func(context.Context, string) (bool, error) { return true, nil }

	for _, c := range []struct {
		name string
		owns func(context.Context, string) (bool, error)
	}{
		{
			name: "the claim reports an error",
			owns: func(context.Context, string) (bool, error) {
				return false, errors.New("the directory is unreachable")
			},
		},
		{
			name: "the claim panics",
			owns: func(context.Context, string) (bool, error) {
				panic("the directory client is nil")
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			// The other claim answers yes, so what is held is that a
			// failure anywhere ends the request, not that no owner
			// was found.
			healthy := openly("healthy", yes)
			healthy.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
				return authn.Authenticated(subject)
			}

			g := testGateway(t, []string{"public"}, healthy, openly("broken", c.owns))
			sealed(t, g, routerWith("public"))

			w, seen := run(g, "public", withBasic(request("GET", "/orders"), "dave", "secret"))

			if w.Code != nethttp.StatusServiceUnavailable {
				t.Errorf("the request answered %d, want %d: a claim that could not be asked "+
					"is not one that said no", w.Code, nethttp.StatusServiceUnavailable)
			}
			if seen.reached {
				t.Error("the layer behind the gateway was reached although an open claim failed")
			}
			if got := g.tally()["unavailable"]; got != 1 {
				t.Errorf("the outage was counted %d time(s) in the unavailable bucket, want 1", got)
			}
		})
	}
}

// TestWhatABackendAnswersDecidesTheStatusAndTheBucket holds the three-tier
// conclusion, and holds the zero verdict apart from a refusal.
//
// An operator has to be able to tell a wrong password from a broken plugin, and
// both from a backend that is down: a refusal is the caller's problem, an outage
// is the operator's, and a verdict nobody spoke is the plugin author's.
func TestWhatABackendAnswersDecidesTheStatusAndTheBucket(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func() authn.Verdict
		panics bool
		status int
		bucket string
	}{
		{
			name:   "the credential holds",
			answer: func() authn.Verdict { return authn.Authenticated("alice") },
			status: nethttp.StatusOK,
			bucket: "authenticated",
		},
		{
			name:   "the credential does not hold",
			answer: func() authn.Verdict { return authn.Denied(errors.New("wrong password")) },
			status: nethttp.StatusUnauthorized,
			bucket: "denied",
		},
		{
			name:   "the backend cannot say right now",
			answer: func() authn.Verdict { return authn.Unavailable(errors.New("down")) },
			status: nethttp.StatusServiceUnavailable,
			bucket: "unavailable",
		},
		{
			name:   "the backend returned without answering",
			answer: func() authn.Verdict { return authn.Verdict{} },
			status: nethttp.StatusUnauthorized,
			bucket: "unspoken",
		},
		{
			name:   "the backend panicked",
			panics: true,
			status: nethttp.StatusServiceUnavailable,
			bucket: "unavailable",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			backend := enumerating("local", "alice")
			backend.verify = func(_ context.Context, _ string, _ authn.Credential) authn.Verdict {
				if c.panics {
					panic("the backend's store is nil")
				}
				return c.answer()
			}

			g := testGateway(t, []string{"public"}, backend)
			sealed(t, g, routerWith("public"))

			w, _ := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

			if w.Code != c.status {
				t.Errorf("the request answered %d, want %d", w.Code, c.status)
			}
			if got := g.tally()[c.bucket]; got != 1 {
				t.Errorf("the conclusion was counted %d time(s) in the %s bucket, want 1: "+
					"%v", got, c.bucket, g.tally())
			}
		})
	}
}

// TestABackendCannotMintASubjectOutsideItsOwnClaim holds exit binding, which is
// the only gateway-produced refusal that happens after a backend has answered.
//
// Without it a backend claiming one small slice of the subject space could
// return any subject at all and step straight over the ownership table — which
// is to say a plugin anyone can write could issue the administrator's identity.
func TestABackendCannotMintASubjectOutsideItsOwnClaim(t *testing.T) {
	yes := func(context.Context, string) (bool, error) { return true, nil }

	// Each case names a backend that would otherwise be let through: the
	// path that reached it does not constrain the subject it returns, so
	// exit binding is the only thing standing between it and the identity
	// it is reaching for.
	for _, c := range []struct {
		name string
		// overreaching is the backend that answers, and returns the
		// subject named.
		overreaching func(returns string) authn.Verifier
		// send prepares the request that reaches it.
		send     func() *nethttp.Request
		returned string
		why      string
	}{
		{
			name: "a backend claiming a scheme whole, returning another backend's subject",
			overreaching: func(returns string) authn.Verifier {
				v := claimingScheme("issuer", "Bearer")
				v.verify = func(_ context.Context, _ string, _ authn.Credential) authn.Verdict {
					return authn.Authenticated(returns)
				}
				return v
			},
			send: func() *nethttp.Request {
				r := request("GET", "/orders")
				r.Header.Set(authHeader, "Bearer an.opaque.token")
				return r
			},
			returned: "root",
			why: "a scheme claim says nothing about subjects, so without this check any " +
				"plugin claiming a scheme could issue the administrator's identity",
		},
		{
			name: "a backend with an open claim, returning another backend's subject",
			overreaching: func(returns string) authn.Verifier {
				v := openly("directory", yes)
				v.verify = func(_ context.Context, _ string, _ authn.Credential) authn.Verdict {
					return authn.Authenticated(returns)
				}
				return v
			},
			send: func() *nethttp.Request {
				return withBasic(request("GET", "/orders"), "dave", "secret")
			},
			returned: "root",
			why:      "the other backend enumerated it, so it is that backend's to vouch for",
		},
		{
			name: "a backend returning a subject carrying the separator",
			overreaching: func(returns string) authn.Verifier {
				v := openly("directory", yes)
				v.verify = func(_ context.Context, _ string, _ authn.Credential) authn.Verdict {
					return authn.Authenticated(returns)
				}
				return v
			},
			send: func() *nethttp.Request {
				return withBasic(request("GET", "/orders"), "dave", "secret")
			},
			returned: "other/root",
			why:      "it could not be told from an owner qualification once written down",
		},
		{
			name: "a backend returning a subject it has no claim over",
			overreaching: func(returns string) authn.Verifier {
				v := enumerating("local", "alice")
				v.verify = func(_ context.Context, _ string, _ authn.Credential) authn.Verdict {
					return authn.Authenticated(returns)
				}
				return v
			},
			send: func() *nethttp.Request {
				return withBasic(request("GET", "/orders"), "alice", "secret")
			},
			returned: "invented",
			why:      "the backend enumerated neither it nor an open claim to cover it",
		},
		{
			name: "a backend returning an empty subject",
			overreaching: func(returns string) authn.Verifier {
				v := claimingScheme("issuer", "Bearer")
				v.verify = func(_ context.Context, _ string, _ authn.Credential) authn.Verdict {
					return authn.Authenticated(returns)
				}
				return v
			},
			send: func() *nethttp.Request {
				r := request("GET", "/orders")
				r.Header.Set(authHeader, "Bearer an.opaque.token")
				return r
			},
			returned: "",
			why:      "an identity naming nobody is not an identity",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := testGateway(t, []string{"public"},
				c.overreaching(c.returned), passes(enumerating("other", "root")))
			sealed(t, g, routerWith("public"))

			w, seen := run(g, "public", c.send())

			if w.Code != nethttp.StatusUnauthorized {
				t.Errorf("a pass returning %q answered %d, want %d: %s",
					c.returned, w.Code, nethttp.StatusUnauthorized, c.why)
			}
			if seen.carried {
				t.Errorf("the request carried the identity %q although the subject falls "+
					"outside the backend's claim", seen.id.Key())
			}
		})
	}
}

// TestABackendMayReturnAnotherSubjectItIsItselfTheAuthorityFor holds the other
// side of exit binding, so that it is a boundary rather than a rule that the
// subject asked about is the subject returned.
//
// A backend that maps an alias onto a canonical name is doing something
// legitimate, as long as the name it lands on is one it is the authority for.
func TestABackendMayReturnAnotherSubjectItIsItselfTheAuthorityFor(t *testing.T) {
	mapping := enumerating("local", "alice", "alice.smith")
	mapping.verify = func(_ context.Context, _ string, _ authn.Credential) authn.Verdict {
		return authn.Authenticated("alice.smith")
	}

	g := testGateway(t, []string{"public"}, mapping)
	sealed(t, g, routerWith("public"))

	w, seen := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusOK {
		t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
	}
	if got := seen.id.Key(); got != "local/alice.smith" {
		t.Errorf("the identity reads %q, want the subject the backend landed on", got)
	}
}

// TestAPassingRequestCarriesTheIdentityTheGatewayStamped holds what the layers
// behind the gateway actually receive.
//
// The owner is not the backend's to write: it is taken from the name the
// authority reported and stamped by the gateway, and there is no field on a
// verdict a backend could put it in. The qualification is never dropped, even
// with one backend running: a bare "alice" and a qualified "local/alice" would
// otherwise both name the caller, and a policy written against either would
// silently miss the other the day a second backend arrives.
func TestAPassingRequestCarriesTheIdentityTheGatewayStamped(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
	sealed(t, g, routerWith("public"))

	w, seen := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusOK {
		t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
	}
	if !seen.carried {
		t.Fatal("a request the gateway passed carries no identity, so every layer behind it " +
			"reads the caller as anonymous")
	}
	if got := seen.id.Subject(); got != "alice" {
		t.Errorf("the identity's subject is %q, want %q", got, "alice")
	}
	if got := seen.id.Owner(); got != "local" {
		t.Errorf("the identity's owner is %q, want the name the authority reported", got)
	}
	if got := seen.id.Key(); got != "local"+authn.Separator+"alice" {
		t.Errorf("the identity reads %q, want the owner-qualified form", got)
	}
	if got := g.tally()["authenticated"]; got != 1 {
		t.Errorf("the pass was counted %d time(s) in the authenticated bucket, want 1", got)
	}
}

// TestTheGatewaysOwnDefectBecomesAStatusCode holds the recover the gateway does
// for itself.
//
// The entry point states plainly that it does not catch a handler's panic. The
// shape of not recovering here is therefore not a 500 — it is the connection
// being dropped with no status code reaching the caller at all, which every
// status-code-based monitor sees as nothing having happened.
func TestTheGatewaysOwnDefectBecomesAStatusCode(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
	sealed(t, g, routerWith("public"))

	// The layer is built first, the way Init builds it, and the gateway is
	// broken afterwards: a table that went missing under a running server is
	// a defect of this module, and it is the cheapest way to reach the panic
	// path without a backend being involved.
	handler, seen := layerOn(g, "public")
	g.table = nil

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusInternalServerError {
		t.Errorf("the gateway's own panic answered %d, want %d", w.Code,
			nethttp.StatusInternalServerError)
	}
	if seen.reached {
		t.Error("the layer behind the gateway was reached after the gateway panicked")
	}
	for bucket, count := range g.tally() {
		if count != 0 {
			t.Errorf("the gateway's own panic was counted in the %s bucket (%d), want none of "+
				"the four: those count judgements about a credential, and this is not one",
				bucket, count)
		}
	}
}

// TestALayerCannotBeBuiltBeforeTheOwnershipTable pins an ordering inside Init
// that has no other signal.
//
// The layer is built out of the table — the challenge it answers a refusal with
// names the schemes in it — so registering on the endpoints before the table
// exists takes it while it is still nil. Read from the outside that ordering
// looks like a free choice, and getting it wrong does not fail at registration
// or at startup: it fails on the first request, several frames from the cause.
func TestALayerCannotBeBuiltBeforeTheOwnershipTable(t *testing.T) {
	g := newGateway(settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 1})

	assertPanics(t, "building a layer before the ownership table", func() {
		g.middlewareFor("public")
	})
}

// TestTheGatewayStopsAcceptingBackendCallsWhenItIsStopped holds the Stop
// contract: a call that has not begun does not begin, and the request ends as
// an outage rather than as a refusal, because nothing was learned about the
// credential.
func TestTheGatewayStopsAcceptingBackendCallsWhenItIsStopped(t *testing.T) {
	var asked atomic.Int64
	backend := passes(enumerating("local", "alice"))
	backend.asked = &asked

	g := testGateway(t, []string{"public"}, backend)
	sealed(t, g, routerWith("public"))
	g.stop()

	w, _ := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("a request after Stop answered %d, want %d", w.Code,
			nethttp.StatusServiceUnavailable)
	}
	if asked.Load() != 0 {
		t.Errorf("a backend was asked %d time(s) after Stop, want none", asked.Load())
	}
}

// TestDrainingReturnsOnceTheCallsInFlightAreDone holds the Close contract on
// the ordinary path: nothing is in flight, so there is nothing to wait for.
func TestDrainingReturnsOnceTheCallsInFlightAreDone(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
	sealed(t, g, routerWith("public"))
	run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	g.stop()

	if err := g.drain(context.Background()); err != nil {
		t.Errorf("draining with nothing in flight gave %v, want it to return", err)
	}
}

// TestAnOutageTheGatewayCausedIsNotChargedToABackend holds the attribution of a
// 503.
//
// Stopping, and the in-flight gate being full, are this module's own limits: the
// backend was never asked. Charging them to it sends an operator to read the
// logs of a credential store that is working perfectly, while the thing that
// actually refused the request says nothing.
func TestAnOutageTheGatewayCausedIsNotChargedToABackend(t *testing.T) {
	t.Run("after the gateway has stopped", func(t *testing.T) {
		g := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
		sealed(t, g, routerWith("public"))
		g.stop()

		_, deny := g.identify(withBasic(request("GET", "/orders"), "alice", "secret"))
		if deny == nil {
			t.Fatal("a request after Stop was admitted")
		}
		if deny.status != nethttp.StatusServiceUnavailable {
			t.Errorf("the conclusion answers %d, want %d", deny.status,
				nethttp.StatusServiceUnavailable)
		}
		if deny.backend != "" {
			t.Errorf("the outage is charged to %q, want the gateway: the backend was never "+
				"asked", deny.backend)
		}
	})

	t.Run("when a backend reports a failure", func(t *testing.T) {
		broken := enumerating("local", "alice")
		broken.verify = func(_ context.Context, _ string, _ authn.Credential) authn.Verdict {
			return authn.Unavailable(errors.New("the store is unreachable"))
		}

		g := testGateway(t, []string{"public"}, broken)
		sealed(t, g, routerWith("public"))

		_, deny := g.identify(withBasic(request("GET", "/orders"), "alice", "secret"))
		if deny == nil {
			t.Fatal("a backend reporting a failure admitted the request")
		}
		if deny.backend != "local" {
			t.Errorf("the outage is charged to %q, want the backend that could not answer",
				deny.backend)
		}
	})
}

// TestABackendIsHandedTheSchemeFolded holds the one normalisation the surface
// promises a backend.
//
// Authentication scheme names are case-insensitive on the wire. A backend left
// to compare the raw token would authenticate or refuse on the casing a client
// happened to send — a standards-conformant client refused, and one that sends
// lower case let through, with nothing to tell the two apart.
func TestABackendIsHandedTheSchemeFolded(t *testing.T) {
	for _, written := range []string{"Bearer", "bearer", "BEARER", "BeArEr"} {
		t.Run(written, func(t *testing.T) {
			var seen string
			issuer := claimingScheme("issuer", "Bearer")
			issuer.verify = func(_ context.Context, _ string, c authn.Credential) authn.Verdict {
				seen = c.Scheme
				return authn.Authenticated("carol")
			}

			g := testGateway(t, []string{"public"}, issuer)
			sealed(t, g, routerWith("public"))

			r := request("GET", "/orders")
			r.Header.Set(authHeader, written+" a.token")
			if w, _ := run(g, "public", r); w.Code != nethttp.StatusOK {
				t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
			}
			if seen != "bearer" {
				t.Errorf("the backend was handed the scheme %q, want it folded", seen)
			}
		})
	}
}

// TestARefusalTellsTheCallerWhatCredentialToSend holds the one header a 401 may
// not leave out.
//
// This module writes no response body, so the challenge is the whole of what a
// caller has to go on; a 401 carrying none says "you need a credential" without
// saying what kind, which is the one thing RFC 7235 does not allow a 401 to do.
func TestARefusalTellsTheCallerWhatCredentialToSend(t *testing.T) {
	g := testGateway(t, []string{"public"},
		enumerating("local", "alice"), claimingScheme("issuer", "Bearer"))
	sealed(t, g, routerWith("public"))

	w, _ := run(g, "public", request("GET", "/orders"))
	if w.Code != nethttp.StatusUnauthorized {
		t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
	}

	got := w.Header().Get(challengeHeader)
	for _, want := range []string{"Basic", "Bearer"} {
		if !strings.Contains(got, want) {
			t.Errorf("the challenge reads %q and does not offer %q, which is a scheme a "+
				"credential can actually arrive under here", got, want)
		}
	}
	// Basic requires a realm, and the protection space a credential is
	// worth something in is the endpoint it was issued for.
	if !strings.Contains(got, `realm="public"`) {
		t.Errorf("the challenge reads %q and names no realm; Basic requires one, and without "+
			"it a client cannot tell which protection space it is being asked for", got)
	}
}

// TestARealmCannotBreakOutOfTheChallenge holds the escaping of the one part of
// the header that comes from configuration.
//
// An endpoint name is written by whoever writes the config file. A quote in one
// would end the quoted realm early and let the rest be read as further challenge
// parameters, and a line ending would be read as the start of another header
// altogether.
func TestARealmCannotBreakOutOfTheChallenge(t *testing.T) {
	table, err := buildOwnership([]authn.Verifier{enumerating("local", "alice")})
	if err != nil {
		t.Fatalf("building the table: %v", err)
	}

	got := challengeFor(table, "pub\"lic\r\nX-Evil: 1")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("the challenge %q carries a line ending, so the rest of the endpoint name "+
			"would reach the caller as a header of its own", got)
	}
	if strings.Contains(got, `realm="pub"lic`) {
		t.Errorf("the challenge %q ends the quoted realm early, so the rest of the name is "+
			"read as further challenge parameters", got)
	}
	if !strings.Contains(got, `pub\"lic`) {
		t.Errorf("the challenge %q dropped the quote instead of escaping it, so two endpoints "+
			"whose names differ only there would share a realm", got)
	}
}

// TestAPassCarriesNoChallenge keeps the case above honest: the header belongs to
// a refusal, and a request that was admitted has nothing to be challenged about.
func TestAPassCarriesNoChallenge(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
	sealed(t, g, routerWith("public"))

	w, _ := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	if got := w.Header().Get(challengeHeader); got != "" {
		t.Errorf("an admitted request carries the challenge %q", got)
	}
}

// mustRefuse runs the ownership sequence for a bare subject and requires a
// refusal, handing it back so its attributes can be read.
func mustRefuse(t *testing.T, g *gateway, subject string) *refusal {
	t.Helper()
	found, deny := g.askOpenClaims(context.Background(), subject)
	if deny == nil {
		t.Fatalf("the subject %q resolved to %q, want a refusal", subject, found.claimant.name)
	}
	return deny
}

// renderAttrs flattens slog's alternating attribute form into something a test
// can search.
func renderAttrs(attrs []any) string {
	var b strings.Builder
	for _, a := range attrs {
		b.WriteString(strings.TrimSpace(strings.Trim(stringify(a), "{}")))
		b.WriteString(" ")
	}
	return b.String()
}

func stringify(a any) string {
	type stringer interface{ String() string }
	if s, ok := a.(stringer); ok {
		return s.String()
	}
	return ""
}
