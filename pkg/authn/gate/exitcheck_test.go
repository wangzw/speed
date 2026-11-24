package gate

import (
	"context"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// anchor is the instant the unit cases below take as the gateway's clock.
var anchor = time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

// The backends the unit cases check a principal against: one declaring no
// capability, one declaring both, and two holding prefixes of their own.
var (
	plainBackend   = &fakeVerifier{name: "plain", accepts: authn.Accepts{Schemes: []string{"a"}}}
	capableBackend = &fakeVerifier{name: "capable", accepts: authn.Accepts{Schemes: []string{"b"}, Impersonation: true, SSO: true}}
	corpBackend    = &fakeVerifier{name: "corp", accepts: authn.Accepts{Schemes: []string{"c"}, Accounts: "corp."}}
	staticBackend  = &fakeVerifier{name: "static", accepts: authn.Accepts{Schemes: []string{"basic"}, Accounts: "static."}}
)

func exitTable(t *testing.T) *dispatch {
	t.Helper()
	d, err := buildDispatch([]authn.Verifier{plainBackend, capableBackend, corpBackend, staticBackend})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func backendNamed(t *testing.T, d *dispatch, name string) *backend {
	t.Helper()
	for _, b := range d.backends {
		if b.name == name {
			return b
		}
	}
	t.Fatalf("no backend %q", name)
	return nil
}

// wellFormed is a principal that passes the exit check for a library backend.
func wellFormed() authn.Principal {
	return authn.Principal{
		Account:   "alice",
		Methods:   []authn.Method{authn.MethodStatic},
		Assurance: authn.AssuranceSingle,
		AuthTime:  anchor,
	}
}

func impersonating() authn.Principal {
	p := wellFormed()
	p.Actor, p.Session, p.Scope = "admin-9", "0198f0aa-0000-7000-8000-000000000002", "tenant-a"
	return p
}

func attributes(n, value int) map[string]string {
	out := make(map[string]string, n)
	for i := range n {
		out["key"+string(rune('a'+i/26))+string(rune('a'+i%26))] = strings.Repeat("v", value)
	}
	return out
}

// TestThePrincipalABackendHandsOverIsHeldToItsShapesAndBounds holds every exit-check bound from both sides.
func TestThePrincipalABackendHandsOverIsHeldToItsShapesAndBounds(t *testing.T) {
	d := exitTable(t)
	capable := backendNamed(t, d, "capable")
	allMethods := []authn.Method{
		authn.MethodPassword, authn.MethodSMS, authn.MethodTOTP, authn.MethodWebAuthn,
		authn.MethodOAuth, authn.MethodOIDC, authn.MethodSAML, authn.MethodStatic,
		authn.MethodClientSecret,
	}
	key64 := strings.Repeat("k", 64)
	manyLong := func(first int) func(*authn.Principal) {
		return func(p *authn.Principal) {
			p.Attributes = make(map[string]string, 32)
			for i := range 32 {
				value := 100
				if i == 0 {
					value = first
				}
				p.Attributes[strings.Repeat("k", 26)+string(rune('a'+i/26))+string(rune('a'+i%26))] = strings.Repeat("v", value)
			}
		}
	}

	for _, c := range []struct {
		name   string
		spoil  func(*authn.Principal)
		refuse bool
	}{
		{"a well-formed principal", func(*authn.Principal) {}, false},

		{"an empty account", func(p *authn.Principal) { p.Account = "" }, true},
		{"an account with a character outside the format", func(p *authn.Principal) { p.Account = "al ice" }, true},
		{"an account with a newline", func(p *authn.Principal) { p.Account = "alice\nroot" }, true},
		{"an account of 64 bytes", func(p *authn.Principal) { p.Account = authn.AccountID(strings.Repeat("a", 64)) }, false},
		{"an account of 65 bytes", func(p *authn.Principal) { p.Account = authn.AccountID(strings.Repeat("a", 65)) }, true},

		{"no method", func(p *authn.Principal) { p.Methods = nil }, true},
		{"an empty method list", func(p *authn.Principal) { p.Methods = []authn.Method{} }, true},
		{"a method outside the closed set", func(p *authn.Principal) { p.Methods = []authn.Method{"magic"} }, true},
		{"a method named twice", func(p *authn.Principal) {
			p.Methods = []authn.Method{authn.MethodPassword, authn.MethodPassword}
		}, true},
		{"eight different methods", func(p *authn.Principal) { p.Methods = allMethods[:8] }, false},
		{"nine different methods", func(p *authn.Principal) { p.Methods = allMethods[:9] }, true},

		{"the multi-factor assurance", func(p *authn.Principal) { p.Assurance = authn.AssuranceMulti }, false},
		{"an assurance above the defined levels", func(p *authn.Principal) { p.Assurance = authn.AssuranceMulti + 1 }, true},

		{"no authentication time", func(p *authn.Principal) { p.AuthTime = time.Time{} }, true},
		{"an authentication time long past", func(p *authn.Principal) { p.AuthTime = anchor.Add(-72 * time.Hour) }, false},
		{"an authentication time 120 seconds ahead", func(p *authn.Principal) { p.AuthTime = anchor.Add(120 * time.Second) }, false},
		{"an authentication time 120 seconds and a nanosecond ahead", func(p *authn.Principal) {
			p.AuthTime = anchor.Add(120*time.Second + time.Nanosecond)
		}, true},
		{"an authentication time 121 seconds ahead", func(p *authn.Principal) { p.AuthTime = anchor.Add(121 * time.Second) }, true},

		{"32 attributes", func(p *authn.Principal) { p.Attributes = attributes(32, 8) }, false},
		{"33 attributes", func(p *authn.Principal) { p.Attributes = attributes(33, 8) }, true},
		{"a key in capitals", func(p *authn.Principal) { p.Attributes = map[string]string{"Team": "ops"} }, true},
		{"an empty key", func(p *authn.Principal) { p.Attributes = map[string]string{"": "ops"} }, true},
		{"a key that begins with a dot", func(p *authn.Principal) { p.Attributes = map[string]string{".team": "ops"} }, true},
		{"a key with a space", func(p *authn.Principal) { p.Attributes = map[string]string{"the team": "ops"} }, true},
		{"a key of 64 bytes", func(p *authn.Principal) { p.Attributes = map[string]string{key64: "ops"} }, false},
		{"a key of 65 bytes", func(p *authn.Principal) { p.Attributes = map[string]string{key64 + "k": "ops"} }, true},
		{"a key with dots, underscores and dashes", func(p *authn.Principal) {
			p.Attributes = map[string]string{"idp.team_a-1": "ops"}
		}, false},
		{"a value of 256 bytes", func(p *authn.Principal) {
			p.Attributes = map[string]string{"team": strings.Repeat("v", 256)}
		}, false},
		{"a value of 257 bytes", func(p *authn.Principal) {
			p.Attributes = map[string]string{"team": strings.Repeat("v", 257)}
		}, true},
		{"a value that is not UTF-8", func(p *authn.Principal) { p.Attributes = map[string]string{"team": "\xff\xfe"} }, true},
		{"a value with a control character", func(p *authn.Principal) {
			p.Attributes = map[string]string{"team": "ops\x07"}
		}, true},
		{"a value with a newline", func(p *authn.Principal) { p.Attributes = map[string]string{"team": "a\nb"} }, true},
		{"keys and values of 4096 bytes together", manyLong(100), false},
		{"keys and values of 4097 bytes together", manyLong(101), true},

		{"an impersonation", func(p *authn.Principal) { *p = impersonating() }, false},
		{"an actor that is the account", func(p *authn.Principal) {
			*p = impersonating()
			p.Actor = p.Account
		}, true},
		{"an actor with no session", func(p *authn.Principal) {
			*p = impersonating()
			p.Session = ""
		}, true},
		{"an actor with no scope", func(p *authn.Principal) {
			*p = impersonating()
			p.Scope = ""
		}, true},
		{"an actor that is not an account identifier", func(p *authn.Principal) {
			*p = impersonating()
			p.Actor = "ad min"
		}, true},
		{"a scope with no actor, from a backend that may impersonate", func(p *authn.Principal) { p.Scope = "tenant-a" }, true},
		{"a session with no actor", func(p *authn.Principal) { p.Session = "0198f0aa-0000-7000-8000-000000000002" }, false},
		{"a scope with a space", func(p *authn.Principal) {
			*p = impersonating()
			p.Scope = "tenant a"
		}, true},
		{"a scope with a newline", func(p *authn.Principal) {
			*p = impersonating()
			p.Scope = "tenant\na"
		}, true},
		{"a scope with a control character", func(p *authn.Principal) {
			*p = impersonating()
			p.Scope = "tenant\x07a"
		}, true},

		{"an SSO provider", func(p *authn.Principal) { p.Provider = "0198f0aa-0000-7000-8000-000000000001" }, false},
		{"an SSO provider in capitals", func(p *authn.Principal) { p.Provider = "0198F0AA-0000-7000-8000-00000000000A" }, true},
		{"an SSO provider with one capital", func(p *authn.Principal) { p.Provider = "0198f0aa-0000-7000-8000-00000000000A" }, true},
		{"an SSO provider with no dashes", func(p *authn.Principal) { p.Provider = "0198f0aa000070008000000000000001" }, true},
		{"an SSO provider in braces", func(p *authn.Principal) { p.Provider = "{0198f0aa-0000-7000-8000-000000000001}" }, true},
		{"an SSO provider with a urn prefix", func(p *authn.Principal) {
			p.Provider = "urn:uuid:0198f0aa-0000-7000-8000-000000000001"
		}, true},
		{"an SSO provider one character long past", func(p *authn.Principal) { p.Provider = "0198f0aa-0000-7000-8000-0000000000011" }, true},
		{"an SSO provider that is not UUID text", func(p *authn.Principal) { p.Provider = "okta" }, true},
		{"an SSO provider one character short", func(p *authn.Principal) { p.Provider = "0198f0aa-0000-7000-8000-00000000000" }, true},
		{"an SSO provider with a stray character", func(p *authn.Principal) { p.Provider = "0198f0aa-0000-7000-8000-00000000000g" }, true},
		{"an SSO provider with the dashes elsewhere", func(p *authn.Principal) { p.Provider = "0198f0aa00-00-7000-8000-000000000001" }, true},
		{"an IdP's assurance with its provider", func(p *authn.Principal) {
			p.IDPAssured, p.Assurance, p.Provider = true, authn.AssuranceMulti, "0198f0aa-0000-7000-8000-000000000001"
		}, false},
		{"an IdP's assurance without a provider", func(p *authn.Principal) {
			p.IDPAssured, p.Assurance = true, authn.AssuranceMulti
		}, true},
		{"an IdP's assurance at a single factor", func(p *authn.Principal) {
			p.IDPAssured, p.Provider = true, "0198f0aa-0000-7000-8000-000000000001"
		}, true},
		{"an IdP's assurance with the level unstated", func(p *authn.Principal) {
			p.IDPAssured, p.Assurance, p.Provider = true, authn.AssuranceNone, "0198f0aa-0000-7000-8000-000000000001"
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := wellFormed()
			c.spoil(&p)

			_, reason := checkPrincipal(p, anchor, capable, d)
			if c.refuse && reason == "" {
				t.Errorf("the principal passed the exit check, want it refused")
			}
			if !c.refuse && reason != "" {
				t.Errorf("the principal was refused (%s), want it to pass", reason)
			}
		})
	}
}

// TestTheWorstCaseAttributeSetAnSSOBackendHandsOverPassesTheExitCheck holds the largest attribute set the exit check admits.
func TestTheWorstCaseAttributeSetAnSSOBackendHandsOverPassesTheExitCheck(t *testing.T) {
	d := exitTable(t)
	capable := backendNamed(t, d, "capable")

	const (
		accountEntries, accountBytes = 24, 2 << 10
		idpEntries                   = 8
		idpKey, idpValue             = 32, 128
	)
	attrs := make(map[string]string, accountEntries+idpEntries)
	perEntry, extra := accountBytes/accountEntries, accountBytes%accountEntries
	for i := range accountEntries {
		size := perEntry
		if i < extra {
			size++
		}
		key := fmt.Sprintf("acct-%02d", i)
		attrs[key] = strings.Repeat("a", size-len(key))
	}
	for i := range idpEntries {
		key := fmt.Sprintf("idp.%02d", i)
		key += strings.Repeat("k", idpKey-len(key))
		attrs[key] = strings.Repeat("v", idpValue)
	}
	total := 0
	for k, v := range attrs {
		total += len(k) + len(v)
	}
	if len(attrs) != 32 || total != 3328 {
		t.Fatalf("the case built %d entries of %d bytes, want 32 entries of 3328 bytes", len(attrs), total)
	}

	p := wellFormed()
	p.Attributes = attrs
	if _, reason := checkPrincipal(p, anchor, capable, d); reason != "" {
		t.Errorf("the worst-case attribute set was refused (%s), want it to pass", reason)
	}

	attrs["one-more"] = "x"
	if _, reason := checkPrincipal(p, anchor, capable, d); reason == "" {
		t.Error("a 33rd entry passed the exit check, want it refused")
	}
}

// TestACapabilityABackendDidNotDeclareIsNotOneItMayUse holds a capability refused for a backend that did not declare it.
func TestACapabilityABackendDidNotDeclareIsNotOneItMayUse(t *testing.T) {
	d := exitTable(t)
	plain, capable := backendNamed(t, d, "plain"), backendNamed(t, d, "capable")
	provider := "0198f0aa-0000-7000-8000-000000000001"

	for _, c := range []struct {
		name string
		p    authn.Principal
	}{
		{"an impersonation", impersonating()},
		{"an SSO provider", func() authn.Principal { p := wellFormed(); p.Provider = provider; return p }()},
		{"an IdP's assurance", func() authn.Principal {
			p := wellFormed()
			p.IDPAssured, p.Assurance, p.Provider = true, authn.AssuranceMulti, provider
			return p
		}()},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, reason := checkPrincipal(c.p, anchor, capable, d); reason != "" {
				t.Fatalf("a backend that declared the capability was refused: %s", reason)
			}
			_, reason := checkPrincipal(c.p, anchor, plain, d)
			if reason == "" {
				t.Fatal("a backend that did not declare the capability was let use it")
			}
			if !strings.Contains(reason, "did not declare") {
				t.Errorf("the reason reads %q, want it to say the capability was not declared", reason)
			}
		})
	}

	onlySSO := &backend{name: "sso", sso: true}
	if _, reason := checkPrincipal(impersonating(), anchor, onlySSO, d); reason == "" {
		t.Error("a backend that declared SSO alone was let impersonate")
	}
	onlyImp := &backend{name: "imp", impersonation: true}
	p := wellFormed()
	p.Provider = provider
	if _, reason := checkPrincipal(p, anchor, onlyImp, d); reason == "" {
		t.Error("a backend that declared impersonation alone was let hand back a provider")
	}
}

// TestTheNamespaceCheckAppliesToTheActorAsWellAsTheAccount holds both identifiers of an impersonation to the backend's namespace.
func TestTheNamespaceCheckAppliesToTheActorAsWellAsTheAccount(t *testing.T) {
	d := exitTable(t)
	capable := backendNamed(t, d, "capable")

	for _, c := range []struct {
		name           string
		account, actor authn.AccountID
		inside         bool
	}{
		{"both in the library", "alice", "admin-9", true},
		{"the account in another backend's namespace", "corp.alice", "admin-9", false},
		{"the actor in another backend's namespace", "alice", "corp.admin", false},
		{"the actor in the static namespace", "alice", "static.ops", false},
		{"the account in the static namespace", "static.ops", "admin-9", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := impersonating()
			p.Account, p.Actor = c.account, c.actor
			_, reason := checkPrincipal(p, anchor, capable, d)
			if c.inside && reason != "" {
				t.Errorf("refused (%s), want it to pass", reason)
			}
			if !c.inside && !strings.Contains(reason, "namespace") {
				t.Errorf("the reason reads %q, want a refusal for the namespace", reason)
			}
		})
	}
}

// TestAnUnstatedAssuranceIsStampedAsASingleFactor holds the zero assurance stamped as a single factor.
func TestAnUnstatedAssuranceIsStampedAsASingleFactor(t *testing.T) {
	d := exitTable(t)
	plain := backendNamed(t, d, "plain")
	for _, c := range []struct {
		given, want authn.Assurance
	}{
		{authn.AssuranceNone, authn.AssuranceSingle},
		{authn.AssuranceSingle, authn.AssuranceSingle},
		{authn.AssuranceMulti, authn.AssuranceMulti},
	} {
		p := wellFormed()
		p.Assurance = c.given

		got, reason := checkPrincipal(p, anchor, plain, d)
		if reason != "" {
			t.Fatalf("a principal with the assurance %v was refused: %s", c.given, reason)
		}
		if got.Assurance != c.want {
			t.Errorf("the assurance %v is stamped as %v, want %v", c.given, got.Assurance, c.want)
		}
	}
}

// TestTheReasonAnExitCheckGivesEchoesNothingTheBackendWrote holds that a refusal reason carries no value the backend wrote.
func TestTheReasonAnExitCheckGivesEchoesNothingTheBackendWrote(t *testing.T) {
	d := exitTable(t)
	capable := backendNamed(t, d, "capable")

	for name, spoil := range map[string]func(*authn.Principal){
		"an attribute value":   func(p *authn.Principal) { p.Attributes = map[string]string{"team": "s3cret-value\x07"} },
		"an account":           func(p *authn.Principal) { p.Account = "s3cret account" },
		"an actor":             func(p *authn.Principal) { *p = impersonating(); p.Actor = "s3cret actor" },
		"a scope":              func(p *authn.Principal) { *p = impersonating(); p.Scope = "s3cret scope" },
		"a provider":           func(p *authn.Principal) { p.Provider = "s3cret provider" },
		"an attribute key":     func(p *authn.Principal) { p.Attributes = map[string]string{"S3cret Key": "v"} },
		"a method":             func(p *authn.Principal) { p.Methods = []authn.Method{"s3cret-method"} },
		"a namespace breakout": func(p *authn.Principal) { p.Account = "corp.s3cret" },
	} {
		t.Run(name, func(t *testing.T) {
			p := wellFormed()
			spoil(&p)
			_, reason := checkPrincipal(p, anchor, capable, d)
			if reason == "" {
				t.Fatal("the spoiled principal passed the exit check")
			}
			if strings.Contains(strings.ToLower(reason), "s3cret") {
				t.Errorf("the reason %q carries what the backend wrote", reason)
			}
		})
	}
}

// TestAPassThatFailsTheExitCheckIsRefusedAsTheGatewaysOwnDefectFinding holds a bad principal refused as the gateway's own fault.
func TestAPassThatFailsTheExitCheckIsRefusedAsTheGatewaysOwnDefectFinding(t *testing.T) {
	for _, c := range []struct {
		name  string
		spoil func(*authn.Principal)
	}{
		{"an account outside the format", func(p *authn.Principal) { p.Account = "al ice" }},
		{"an actor nobody declared", func(p *authn.Principal) { *p = impersonating() }},
		{"an SSO provider nobody declared", func(p *authn.Principal) { p.Provider = "0198f0aa-0000-7000-8000-000000000001" }},
		{"no method", func(p *authn.Principal) { p.Methods = nil }},
		{"an authentication time from the future", func(p *authn.Principal) { p.AuthTime = time.Now().Add(time.Hour) }},
		{"an attribute outside its bound", func(p *authn.Principal) { p.Attributes = attributes(33, 1) }},
		{"an account in another backend's namespace", func(p *authn.Principal) { p.Account = "static.ops" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			spoiling := schemeBackend("spoiling", "bearer")
			spoiling.verify = func(context.Context, authn.Credential) authn.Verdict {
				p := principalOf(spoiling, "alice")
				c.spoil(&p)
				return authn.Authenticated(p)
			}
			g := testGateway(t, []string{"public"}, spoiling, passes(basicBackend("static", "static.")))
			sealed(t, g, routerWith("public"))

			w, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "t") })

			if w.Code != nethttp.StatusUnauthorized {
				t.Errorf("the pass answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
			}
			if got := attrText(rec, "outcome"); got != "denied" {
				t.Errorf("the pass is counted as %q, want denied", got)
			}
			if got := attrText(rec, "attributed_to"); got != ModuleName {
				t.Errorf("the refusal is charged to %q, want the gateway: the judgement is its own", got)
			}
			if rec.Level != slog.LevelError {
				t.Errorf("the refusal is written at %v, want %v", rec.Level, slog.LevelError)
			}
			if got := attrText(rec, "backend"); got != "spoiling" {
				t.Errorf("the record names the backend %q, want the one that spoiled the principal", got)
			}
		})
	}
}

// TestABackendReturnsOnlyAccountsInsideItsOwnNamespace holds the account namespace check on the request path.
func TestABackendReturnsOnlyAccountsInsideItsOwnNamespace(t *testing.T) {
	for _, c := range []struct {
		name    string
		backend string
		account authn.AccountID
		refused bool
	}{
		{"an account inside a declared prefix", "corp", "corp.alice", false},
		{"another backend's declared prefix", "corp", "ldap.alice", true},
		{"a bare name with no prefix", "corp", "alice", true},
		{"a prefix that only shares letters", "corp", "corporate.alice", true},
		{"the prefix with no one after it", "corp", "corp.", true},
		{"the static prefix, from a backend that is not static", "corp", "static.ops", true},

		{"an account in the static namespace", "static", "static.ops", false},
		{"the static backend handing back a library account", "static", "alice", true},
		{"the static backend handing back another prefix", "static", "corp.alice", true},

		{"a library account from a library backend", "library", "0198f0aa-0000-7000-8000-00000000000a", false},
		{"the static prefix, from a library backend", "library", "static.ops", true},
		{"another backend's prefix, from a library backend", "library", "corp.alice", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			corp := &fakeVerifier{name: "corp", accepts: authn.Accepts{Schemes: []string{"c"}, Accounts: "corp."}}
			ldap := &fakeVerifier{name: "ldap", accepts: authn.Accepts{Schemes: []string{"l"}, Accounts: "ldap."}}
			static := basicBackend("static", "static.")
			library := schemeBackend("library", "bearer")
			byName := map[string]*fakeVerifier{"corp": corp, "static": static, "library": library}
			tested := byName[c.backend]
			tested.verify = func(context.Context, authn.Credential) authn.Verdict {
				p := principalOf(tested, "x")
				p.Account = c.account
				return authn.Authenticated(p)
			}
			g := testGateway(t, []string{"public"}, corp, ldap, static, library)
			sealed(t, g, routerWith("public"))

			var credential func(*nethttp.Request) *nethttp.Request
			switch c.backend {
			case "corp":
				credential = func(r *nethttp.Request) *nethttp.Request { r.Header.Set(authHeader, "c x"); return r }
			case "static":
				credential = func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "a", "b") }
			default:
				credential = func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "t") }
			}
			w, seen := run(g, "public", credential(request("GET", "/orders")))

			if c.refused {
				if w.Code != nethttp.StatusUnauthorized || seen.reached {
					t.Fatalf("the account %q for the backend %s answered %d (reached %v), want a refusal",
						c.account, c.backend, w.Code, seen.reached)
				}
				return
			}
			if w.Code != nethttp.StatusOK {
				t.Fatalf("the account %q for the backend %s answered %d, want a pass", c.account, c.backend, w.Code)
			}
			if seen.id.Account() != c.account {
				t.Errorf("the identity's account is %q, want %q", seen.id.Account(), c.account)
			}
		})
	}
}

// TestAnUnstatedAssuranceReachesTheLayersBehindAsASingleFactor holds the stamped level as what a layer reads.
func TestAnUnstatedAssuranceReachesTheLayersBehindAsASingleFactor(t *testing.T) {
	b := basicBackend("static", "static.")
	b.verify = func(context.Context, authn.Credential) authn.Verdict {
		p := principalOf(b, "alice")
		p.Assurance = authn.AssuranceNone
		return authn.Authenticated(p)
	}
	g := testGateway(t, []string{"public"}, b)
	sealed(t, g, routerWith("public"))

	w, seen := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusOK {
		t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
	}
	if got := seen.id.Assurance(); got != authn.AssuranceSingle {
		t.Errorf("the identity behind the gateway has the assurance %v, want %v", got, authn.AssuranceSingle)
	}
}

// TestTheSkewAllowedAheadIsMeasuredAgainstTheGatewaysClock holds the skew allowed ahead against the gateway's clock.
func TestTheSkewAllowedAheadIsMeasuredAgainstTheGatewaysClock(t *testing.T) {
	for _, c := range []struct {
		name  string
		ahead time.Duration
		pass  bool
	}{
		{"the clock itself", 0, true},
		{"120 seconds ahead", 120 * time.Second, true},
		{"120 seconds and a nanosecond ahead", 120*time.Second + time.Nanosecond, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := schemeBackend("bearer", "bearer")
			b.verify = func(context.Context, authn.Credential) authn.Verdict {
				p := principalOf(b, "alice")
				p.AuthTime = anchor.Add(c.ahead)
				return authn.Authenticated(p)
			}
			g := testGateway(t, []string{"public"}, b)
			g.clock = func() time.Time { return anchor }
			sealed(t, g, routerWith("public"))

			if c.pass {
				w, _ := run(g, "public", withBearer(request("GET", "/orders"), "t"))
				if w.Code != nethttp.StatusOK {
					t.Fatalf("an authentication time %s ahead answered %d, want %d", c.ahead, w.Code, nethttp.StatusOK)
				}
				return
			}
			w, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "t") })
			if w.Code != nethttp.StatusUnauthorized {
				t.Errorf("an authentication time %s ahead answered %d, want %d", c.ahead, w.Code, nethttp.StatusUnauthorized)
			}
			if got := attrText(rec, "outcome"); got != "denied" {
				t.Errorf("the refusal is counted as %q, want denied", got)
			}
			if rec.Level != slog.LevelError {
				t.Errorf("the refusal is written at %v, want %v", rec.Level, slog.LevelError)
			}
		})
	}
}

// TestABackendNameThatIsNotTheShapeIsRefusedAtTheExitToo holds the recheck of the backend name.
func TestABackendNameThatIsNotTheShapeIsRefusedAtTheExitToo(t *testing.T) {
	d := exitTable(t)
	for name, spelt := range map[string]string{"empty": "", "with a space": "a b", "in capitals": "Static", "with a newline": "a\nb"} {
		t.Run(name, func(t *testing.T) {
			if _, reason := checkPrincipal(wellFormed(), anchor, &backend{name: spelt}, d); reason == "" {
				t.Errorf("a principal from a backend named %q passed", spelt)
			}
		})
	}
	if _, reason := checkPrincipal(wellFormed(), anchor, &backend{name: "plain.v2-a"}, d); reason != "" {
		t.Errorf("a principal from a well-named backend was refused: %s", reason)
	}
}
