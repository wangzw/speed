package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The token surface's routes, under the base path the case configures for the
// authentication routes. They are mounted on the account endpoint, beside the
// login flow.
const (
	tokenPath       = "/auth/token"
	tokenRevokePath = "/auth/token/revoke"
	jwksPath        = "/auth/jwks.json"
)

// refreshTable is the table the token module owns and its migration builds.
const refreshTable = "authn_refresh_tokens"

// The private members a JWK carries when it was published with its private half.
// A public JWK set carries none of them.
var jwkPrivateMembers = []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"}

// bearerHeader sends an access token as the bearer credential the gateway's
// token verifier claims.
type bearerHeaderCredential string

func (b bearerHeaderCredential) apply(r *nethttp.Request) {
	r.Header.Set("Authorization", "Bearer "+string(b))
}

// bearerHeader is an access token presented as Authorization: Bearer.
func bearerHeader(value string) credential { return bearerHeaderCredential(value) }

// accessRefresh is a token pair a login handed out.
type accessRefresh struct {
	Access  string
	Refresh string
}

// tokenBody is the body of a completed token login and of a refresh grant,
// written with the field names a client writes.
type tokenBody struct {
	State        string `json:"state"`
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// signInForToken runs a two-step password login asking for a token pair and
// returns it. The flow is cookie-bound, exactly as the cookie login is; the
// difference is the delivery the start asks for.
func (h *crmHost) signInForToken(t *testing.T, u userRef) accessRefresh {
	t.Helper()
	start := h.post(t, accountEndpoint, loginFlowPath, "", noCredential{}, map[string]any{
		"method":   "password",
		"delivery": "token",
		"input":    map[string]string{"identifier": u.Email, "password": u.Password},
	})
	expect(t, "starting a token login for "+u.Email, start, nethttp.StatusOK)
	var flow loginFlowReply
	start.decode(t, &flow)
	if flow.Next.Kind != "complete" {
		t.Fatalf("a token login for %s answered next %q, want complete", u.Email, flow.Next.Kind)
	}
	flowCookie, ok := cookieOf(start.header, flowCookieName)
	if !ok {
		t.Fatalf("a token login for %s set no %s cookie", u.Email, flowCookieName)
	}
	answer := h.send(t, request{
		method: nethttp.MethodPost, endpoint: accountEndpoint, path: loginAnswerPath,
		header: map[string]string{"Cookie": flowCookieName + "=" + flowCookie},
		body:   map[string]any{"seq": flow.Seq, "step": "complete"},
	})
	expect(t, "completing a token login for "+u.Email, answer, nethttp.StatusOK)
	var got tokenBody
	answer.decode(t, &got)
	if got.TokenType != "Bearer" || got.AccessToken == "" || got.RefreshToken == "" {
		t.Fatalf("the token login for %s answered %+v, want a Bearer pair", u.Email, got)
	}
	return accessRefresh{Access: got.AccessToken, Refresh: got.RefreshToken}
}

// postForm makes a form request to the account endpoint. The token endpoints
// read application/x-www-form-urlencoded, which the shared client — which always
// writes JSON — cannot send.
func (h *crmHost) postForm(t *testing.T, path string, form url.Values) response {
	t.Helper()
	resp, err := h.doForm(path, form)
	if err != nil {
		t.Fatalf("posting a form to %s: %v", path, err)
	}
	return resp
}

// doForm is postForm's body without the failure: it reports a request that could
// not be made rather than failing the case, so it can run off the case's
// goroutine.
func (h *crmHost) doForm(path string, form url.Values) (response, error) {
	r, err := nethttp.NewRequest(nethttp.MethodPost, h.url(accountEndpoint)+path, strings.NewReader(form.Encode()))
	if err != nil {
		return response{}, err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(r)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, err
	}
	return response{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

// requireOAuthError fails the case unless the response is the RFC 6749 §5.2
// failure body of the given error member.
func requireOAuthError(t *testing.T, what string, got response, code string) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	got.decode(t, &body)
	if body.Error != code {
		t.Errorf("%s was answered error=%q, want %q; body %q", what, body.Error, code, got.body)
	}
}

// TestMigrateBuildsTheRefreshTokenTable is the case for the token module's table
// reaching the migration set. The token module is named on the serving command
// alone; its declaration travels through the stack node's static inclusion, so
// the table is built by every command that applies migrations, migrate included.
func TestMigrateBuildsTheRefreshTokenTable(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		if got := c.tables(); !slices.Contains(got, refreshTable) {
			t.Errorf("migrate left no %s table; the database holds %v", refreshTable, got)
		}
	})
}

// TestAPasswordLoginIssuedAsATokenGuardsTheAPI is the case for a token login and
// the bearer verifier. A login asked for delivery=token answers an access token
// and a refresh token; the access token reaches a guarded route as
// Authorization: Bearer, and the gateway stamps the account the login resolved.
// A credential that is absent and one that is forged are refused before
// resolution.
func TestAPasswordLoginIssuedAsATokenGuardsTheAPI(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		pair := h.signInForToken(t, alice)

		guarded := h.get(t, apiEndpoint, "/members", acme.Alias, bearerHeader(pair.Access))
		expect(t, "a request bearing the login's access token", guarded, nethttp.StatusOK)
		var list memberList
		guarded.decode(t, &list)
		if list.Tenant != acme.ID || list.Caller != alice.ID || list.Role != roleOwner {
			t.Errorf("the bearer request was answered for tenant %q caller %q role %q, want %q %q %q",
				list.Tenant, list.Caller, list.Role, acme.ID, alice.ID, roleOwner)
		}

		expect(t, "a request with no credential",
			h.get(t, apiEndpoint, "/members", acme.Alias, noCredential{}), nethttp.StatusUnauthorized)
		expect(t, "a request with a forged token",
			h.get(t, apiEndpoint, "/members", acme.Alias, bearerHeader("not-a-token")), nethttp.StatusUnauthorized)
	})
}

// TestTheJWKSSetIsServed is the case for the published key set. GET
// /auth/jwks.json answers the verification keys as a JWK set, under its own
// content type and public cache lifetime, and carries no private field.
func TestTheJWKSSetIsServed(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()

		resp := h.get(t, accountEndpoint, jwksPath, "", noCredential{})
		expect(t, "GET /auth/jwks.json", resp, nethttp.StatusOK)
		if ct := resp.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/jwk-set+json") {
			t.Errorf("the key set was served as %q, want application/jwk-set+json", ct)
		}
		// The key set is the one response of the capability without the
		// mounter's no-store, and its lifetime is the wait a rotation counts on.
		if got := resp.header.Get("Cache-Control"); got != "public, max-age=300" {
			t.Errorf("the key set was served with Cache-Control %q, want the public cache public, max-age=300", got)
		}

		var set struct {
			Keys []struct {
				Kid string `json:"kid"`
				Alg string `json:"alg"`
				Use string `json:"use"`
			} `json:"keys"`
		}
		resp.decode(t, &set)
		if len(set.Keys) == 0 {
			t.Fatal("the key set holds no key")
		}
		for _, k := range set.Keys {
			if k.Kid == "" || k.Alg != "ES256" || k.Use != "sig" {
				t.Errorf("a published JWK reads kid=%q alg=%q use=%q, want a non-empty kid, ES256 and sig",
					k.Kid, k.Alg, k.Use)
			}
		}

		var raw struct {
			Keys []map[string]json.RawMessage `json:"keys"`
		}
		if err := json.Unmarshal(resp.body, &raw); err != nil {
			t.Fatalf("reading the key set %q: %v", resp.body, err)
		}
		for _, k := range raw.Keys {
			for _, member := range jwkPrivateMembers {
				if _, ok := k[member]; ok {
					t.Errorf("a published JWK carries the private member %q: %s", member, resp.body)
				}
			}
		}
	})
}

// TestARefreshTokenRotatesOnceAndItsReuseEndsTheSession is the case for the
// refresh rotation and the reuse detection. The first presentation rotates the
// token and answers a fresh pair; presenting the original again, past the grace
// window, is a reuse that ends the login session the family hangs on, so neither
// the rotated refresh token nor the access token it came with is usable after.
func TestARefreshTokenRotatesOnceAndItsReuseEndsTheSession(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		first := h.signInForToken(t, alice)

		rotated := h.postForm(t, tokenPath, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {first.Refresh},
		})
		expect(t, "refreshing with the login's refresh token", rotated, nethttp.StatusOK)
		var second tokenBody
		rotated.decode(t, &second)
		if second.AccessToken == "" || second.RefreshToken == "" || second.RefreshToken == first.Refresh {
			t.Fatalf("the rotation answered %+v, want a fresh pair", second)
		}

		// The grace window is the declared default of zero, so presenting the
		// original token again is a reuse: the whole login session ends.
		reuse := h.postForm(t, tokenPath, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {first.Refresh},
		})
		expect(t, "reusing the original refresh token", reuse, nethttp.StatusBadRequest)
		requireOAuthError(t, "a reused refresh token", reuse, "invalid_grant")

		// The session is gone, so the rotated refresh token is refused too, and
		// the access token that came with it no longer guards the API.
		after := h.postForm(t, tokenPath, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {second.RefreshToken},
		})
		expect(t, "the rotated refresh token after the session was revoked", after, nethttp.StatusBadRequest)

		expect(t, "the access token after the session was revoked",
			h.get(t, apiEndpoint, "/members", acme.Alias, bearerHeader(second.AccessToken)), nethttp.StatusUnauthorized)
	})
}

// TestRevokingARefreshTokenEndsItsSession is the case for RFC 7009. The
// revocation endpoint answers 200 whatever it was handed, and revoking the
// refresh token ends the login session the family hangs on: the access token the
// login issued no longer guards the API, and the refresh token gets no new pair.
func TestRevokingARefreshTokenEndsItsSession(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		pair := h.signInForToken(t, alice)

		revoked := h.postForm(t, tokenRevokePath, url.Values{"token": {pair.Refresh}})
		expect(t, "revoking the login's refresh token", revoked, nethttp.StatusOK)
		if body := strings.TrimSpace(string(revoked.body)); body != "" {
			t.Errorf("the revocation answered a body %q, want none", body)
		}

		expect(t, "the access token after its refresh token was revoked",
			h.get(t, apiEndpoint, "/members", acme.Alias, bearerHeader(pair.Access)), nethttp.StatusUnauthorized)

		refreshed := h.postForm(t, tokenPath, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {pair.Refresh},
		})
		expect(t, "the revoked refresh token", refreshed, nethttp.StatusBadRequest)
		requireOAuthError(t, "a revoked refresh token", refreshed, "invalid_grant")
	})
}

// TestTwoRefreshesOfOneTokenLeaveOneWinner is the case for the conditional update
// that arbitrates concurrent refreshes. Two presentations of the same refresh
// token at once are decided by the affected-row count of the consuming update:
// exactly one wins and gets a new pair, the other is refused. The refused one is
// outside the (zero) grace window and so is a reuse, but the property here is the
// count of winners, which no interleaving may make two.
func TestTwoRefreshesOfOneTokenLeaveOneWinner(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		pair := h.signInForToken(t, alice)

		results := make([]response, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i], errs[i] = h.doForm(tokenPath, url.Values{
					"grant_type":    {"refresh_token"},
					"refresh_token": {pair.Refresh},
				})
			}(i)
		}
		wg.Wait()

		var won, refused int
		for i, res := range results {
			if errs[i] != nil {
				t.Fatalf("a concurrent refresh could not be made: %v", errs[i])
			}
			switch res.status {
			case nethttp.StatusOK:
				won++
			case nethttp.StatusBadRequest:
				refused++
			default:
				t.Errorf("a concurrent refresh was answered %d %q", res.status, res.body)
			}
		}
		if won != 1 || refused != 1 {
			t.Errorf("two refreshes of one token left %d success and %d refused, want exactly one of each", won, refused)
		}
	})
}

// TestTheAccessTokenCarriesTheConfiguredClaims is the case for the access token's
// shape. The token is a JWT with the at+jwt type and the configured algorithm in
// its header, and its claims name the configured issuer, audience and client
// identifier, the account the login resolved and the session it was issued for,
// with a lifetime inside the fixed hour.
func TestTheAccessTokenCarriesTheConfiguredClaims(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		pair := h.signInForToken(t, alice)
		issuer := "https://" + hostName + ".test"

		var header struct {
			Alg string `json:"alg"`
			Typ string `json:"typ"`
			Kid string `json:"kid"`
		}
		decodeJWTPart(t, pair.Access, 0, &header)
		if header.Alg != "ES256" || header.Typ != "at+jwt" || header.Kid == "" {
			t.Errorf("the access token header reads alg=%q typ=%q kid=%q, want ES256, at+jwt and a kid",
				header.Alg, header.Typ, header.Kid)
		}

		var claims struct {
			Iss      string   `json:"iss"`
			Sub      string   `json:"sub"`
			Aud      []string `json:"aud"`
			Exp      int64    `json:"exp"`
			Iat      int64    `json:"iat"`
			Jti      string   `json:"jti"`
			Sid      string   `json:"sid"`
			ClientID string   `json:"client_id"`
		}
		decodeJWTPart(t, pair.Access, 1, &claims)
		if claims.Iss != issuer {
			t.Errorf("the token's iss reads %q, want the configured %q", claims.Iss, issuer)
		}
		if !slices.Contains(claims.Aud, issuer) {
			t.Errorf("the token's aud reads %v, want the configured %q among it", claims.Aud, issuer)
		}
		if claims.Sub != string(alice.ID) {
			t.Errorf("the token's sub reads %q, want the account the login resolved, %q", claims.Sub, alice.ID)
		}
		if claims.Sid == "" || claims.Jti == "" {
			t.Errorf("the token carries sid=%q jti=%q, want both present", claims.Sid, claims.Jti)
		}
		if claims.ClientID != hostName {
			t.Errorf("the token's client_id reads %q, want the configured %q", claims.ClientID, hostName)
		}
		if lifetime := claims.Exp - claims.Iat; lifetime <= 0 || lifetime > 3600 {
			t.Errorf("the token's lifetime reads %ds, want a positive value within the fixed hour", lifetime)
		}
	})
}

// decodeJWTPart decodes one dot-separated part of a JWT into v, failing the case
// when the token is not the three-part shape or the part does not decode.
func decodeJWTPart(t *testing.T, token string, index int, v any) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("the access token %q is not a three-part JWT", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[index])
	if err != nil {
		t.Fatalf("decoding the access token's part %d: %v", index, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("reading the access token's part %d %q: %v", index, raw, err)
	}
}

// TestThePublishedKeySetCarriesEveryConfiguredKey is the case for the key set a
// rotation counts on. A key named in verify-key-files is one the module verifies
// and publishes but does not sign with, so a deployment pre-publishes the next
// signing key by putting it there: the published set carries both keys.
func TestThePublishedKeySetCarriesEveryConfiguredKey(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		extra := c.mintKeyFile("rotated-key.pem")
		c.config.set("authn.token.verify-key-files", []string{extra})
		h := c.serve()

		resp := h.get(t, accountEndpoint, jwksPath, "", noCredential{})
		expect(t, "GET /auth/jwks.json with a pre-published key", resp, nethttp.StatusOK)
		var set struct {
			Keys []struct {
				Kid string `json:"kid"`
			} `json:"keys"`
		}
		resp.decode(t, &set)
		kids := map[string]bool{}
		for _, k := range set.Keys {
			kids[k.Kid] = true
		}
		if len(kids) < 2 {
			t.Errorf("the key set publishes %d distinct kid(s), want the signing key and the pre-published one",
				len(kids))
		}
	})
}

// TestABearerThatFailsItsChecksIsRefused is the case for the checks a bearer
// credential passes before an identity is built. An unsigned token with alg none,
// a valid token whose signature was altered, and a token past the length limit
// are each refused by the gateway, before resolution.
func TestABearerThatFailsItsChecksIsRefused(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		pair := h.signInForToken(t, alice)
		for _, tc := range []struct {
			name  string
			value string
		}{
			{"an unsigned token with alg none", unsignedToken("none")},
			{"a token whose signature was altered", tamperSignature(pair.Access)},
			{"a token past the length limit", strings.Repeat("a", 9000)},
		} {
			expect(t, tc.name, h.get(t, apiEndpoint, "/members", acme.Alias, bearerHeader(tc.value)),
				nethttp.StatusUnauthorized)
		}
	})
}

// unsignedToken builds a JWT with the given header algorithm and an empty
// signature: the "alg: none" shape a verifier must refuse whatever the claims
// say.
func unsignedToken(alg string) string {
	enc := base64.RawURLEncoding.EncodeToString
	header := enc([]byte(`{"alg":"` + alg + `","typ":"at+jwt","kid":"forged"}`))
	payload := enc([]byte(`{"iss":"https://` + hostName + `.test","sub":"a","aud":["https://` + hostName +
		`.test"],"exp":4000000000,"iat":1,"jti":"j","sid":"s","client_id":"x"}`))
	return header + "." + payload + "."
}

// tamperSignature alters the first character of a token's signature, so the
// token's signature no longer holds.
func tamperSignature(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return token
	}
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	parts[2] = string(sig)
	return strings.Join(parts, ".")
}
