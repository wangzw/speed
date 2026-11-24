package login

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

var (
	bounceActionPattern = regexp.MustCompile(`<form id="resume" method="post" action="([^"]*)">`)
	bounceInputPattern  = regexp.MustCompile(`<input type="hidden" name="([^"]*)" value="([^"]*)">`)
	bounceScriptPattern = regexp.MustCompile(`<script nonce="([^"]*)">`)
	bouncePolicyPattern = regexp.MustCompile(`^default-src 'none'; script-src 'nonce-([^']+)'; form-action ([^;]+); frame-ancestors 'none'; base-uri 'none'$`)
)

const crossSiteIDP = "https://idp.example.org"

type bounced struct {
	reply  *httptest.ResponseRecorder
	action string
	fields [][2]string
}

func (b bounced) form() string {
	pairs := make([]string, 0, len(b.fields))
	for _, f := range b.fields {
		pairs = append(pairs, url.QueryEscape(f[0])+"="+url.QueryEscape(f[1]))
	}
	return strings.Join(pairs, "&")
}

func (r *rig) postWith(path, body string, hdr ...string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return r.m.Do(req)
}

func (r *rig) bounceThrough(t *testing.T, provider, body string) bounced {
	t.Helper()
	rec := r.postWith("/api/v1/auth/callback/"+provider, body,
		"Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "navigate", "Origin", crossSiteIDP)
	if rec.Code != http.StatusOK {
		t.Fatalf("the cross-site callback post answered %d, want the 200 bounce page: %s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	action := bounceActionPattern.FindStringSubmatch(page)
	if action == nil {
		t.Fatalf("the bounce page carries no resume form: %s", page)
	}
	out := bounced{reply: rec, action: html.UnescapeString(action[1])}
	for _, m := range bounceInputPattern.FindAllStringSubmatch(page, -1) {
		out.fields = append(out.fields, [2]string{html.UnescapeString(m[1]), html.UnescapeString(m[2])})
	}
	return out
}

func (r *rig) resume(t *testing.T, page bounced, cookie string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	headers := []string{"Sec-Fetch-Site", "same-origin", "Origin", mounttest.DefaultPublicURL}
	if cookie != "" {
		headers = append(headers, "Cookie", cookie)
	}
	return r.postWith(page.action, page.form(), append(headers, hdr...)...)
}

// TestACookielessCrossSitePostToTheCallbackIsAnsweredTheBouncePage holds the page's echoed fields, nonce-bound script and headers.
func TestACookielessCrossSitePostToTheCallbackIsAnsweredTheBouncePage(t *testing.T) {
	m := &redirectMethod{name: "saml"}
	r := newRigMethod(t, m)
	before := r.kv.keys()

	page := r.bounceThrough(t, "ent-1", "SAMLResponse=PHNhbWw%2BCg%3D%3D&RelayState=relay-1")
	rec := page.reply

	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("the bounce page is served as %q, want text/html; charset=utf-8", got)
	}
	for name, want := range map[string]string{
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "same-origin",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("the bounce page's %s is %q, want %q", name, got, want)
		}
	}
	policy := bouncePolicyPattern.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
	if policy == nil {
		t.Fatalf("the bounce page's policy is %q, want the nonce policy", rec.Header().Get("Content-Security-Policy"))
	}
	scripts := bounceScriptPattern.FindAllStringSubmatch(rec.Body.String(), -1)
	if len(scripts) != 1 || scripts[0][1] != policy[1] {
		t.Errorf("the page's scripts carry nonces %v, want exactly one carrying the policy's %q", scripts, policy[1])
	}
	if strings.Count(rec.Body.String(), "<script") != 1 {
		t.Errorf("the bounce page carries %d script elements, want one", strings.Count(rec.Body.String(), "<script"))
	}
	if page.action != "/api/v1/auth/callback/ent-1/resume" {
		t.Errorf("the bounce form posts to %q, want the provider's resume route", page.action)
	}
	want := [][2]string{{"RelayState", "relay-1"}, {"SAMLResponse", "PHNhbWw+Cg=="}}
	if !slices.Equal(page.fields, want) {
		t.Errorf("the bounce form carries %v, want the posted fields %v", page.fields, want)
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the callback route asked the method %d times, want none", n)
	}
	if after := r.kv.keys(); !slices.Equal(before, after) {
		t.Errorf("the callback route wrote records: before %v, after %v", before, after)
	}

	again := r.bounceThrough(t, "ent-1", "SAMLResponse=x")
	next := bouncePolicyPattern.FindStringSubmatch(again.reply.Header().Get("Content-Security-Policy"))
	if next == nil || next[1] == policy[1] {
		t.Errorf("two bounce pages carry the nonces %q and %v, want a fresh one per response", policy[1], next)
	}
}

// TestTheBouncePagesFormActionAdmitsEveryTrustedOrigin holds the targets the page's policy lets its form reach.
func TestTheBouncePagesFormActionAdmitsEveryTrustedOrigin(t *testing.T) {
	r := newRigMethod(t, &redirectMethod{name: "saml"})
	r.p.trustedOrigins = []string{mounttest.DefaultPublicURL, "https://app.example.org", "http://127.0.0.1:9102"}

	page := r.bounceThrough(t, "ent-1", "SAMLResponse=x")
	policy := bouncePolicyPattern.FindStringSubmatch(page.reply.Header().Get("Content-Security-Policy"))
	if policy == nil {
		t.Fatalf("the bounce page's policy is %q, want the nonce policy", page.reply.Header().Get("Content-Security-Policy"))
	}
	got := slices.Sorted(slices.Values(strings.Fields(policy[2])))
	want := slices.Sorted(slices.Values(append([]string{"'self'"}, r.p.trustedOrigins...)))
	if !slices.Equal(got, want) {
		t.Errorf("the bounce page's form-action admits %v, want its own origin and every trusted origin %v", got, want)
	}
}

// TestTheBouncePageEscapesWhatItEchoes holds that posted markup reaches the page as text and comes back unchanged in the form.
func TestTheBouncePageEscapesWhatItEchoes(t *testing.T) {
	r := newRigMethod(t, &redirectMethod{name: "saml"})
	value := `"><script>alert(1)</script><input name="x`
	name := `a"b<c>`
	page := r.bounceThrough(t, "ent-1", url.QueryEscape(name)+"="+url.QueryEscape(value))
	body := page.reply.Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, `<input name="x`) {
		t.Fatalf("the bounce page carries the posted markup as markup: %s", body)
	}
	if want := [][2]string{{name, value}}; !slices.Equal(page.fields, want) {
		t.Errorf("the bounce form carries %v, want the posted field %v back unchanged", page.fields, want)
	}

	odd := r.bounceThrough(t, url.PathEscape(`p"><x`), "SAMLResponse=x")
	if strings.Contains(odd.reply.Body.String(), `p"><x`) {
		t.Errorf("the bounce page carries the provider segment as markup: %s", odd.reply.Body.String())
	}
	if odd.action != "/api/v1/auth/callback/"+url.PathEscape(`p"><x`)+"/resume" {
		t.Errorf("the bounce form posts to %q, want the escaped provider's resume route", odd.action)
	}
}

// TestResumingTheBouncedPostCompletesTheLogin holds that the bounce and the page's same-origin resume complete an SP-initiated login.
func TestResumingTheBouncedPostCompletesTheLogin(t *testing.T) {
	var seen url.Values
	m := &redirectMethod{name: "saml"}
	m.answer = func(_ authn.FlowState, in authn.StepInput) (authn.Step, error) {
		seen = in.Form
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodSAML}}), nil
	}
	r := newRigMethod(t, m)
	r.allow("acct1")
	r.sessions.cookie = "__Host-sid"
	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"saml","input":{"provider":"ent-1"},"return_to":"/after"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)

	page := r.bounceThrough(t, "ent-1", "SAMLResponse=assertion&RelayState=relay-1")
	if n := m.calls.Load(); n != 0 {
		t.Fatalf("the bounce asked the method %d times, want none", n)
	}
	out := r.resume(t, page, cookie+"; __Host-sid=old-session", "Sec-Fetch-Mode", "navigate")

	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/after" {
		t.Fatalf("the resumed post answered %d to %q, want 303 to the return target: %s",
			out.Code, out.Header().Get("Location"), out.Body.String())
	}
	if n := m.calls.Load(); n != 1 {
		t.Errorf("the method was asked %d times, want once", n)
	}
	if seen.Get("SAMLResponse") != "assertion" || seen.Get("RelayState") != "relay-1" {
		t.Errorf("the method was given the form %v, want the fields the provider posted", seen)
	}
	if session := cookieNamed(t, out, "__Host-sid"); session.Value == "" {
		t.Error("the completed login wrote no session cookie")
	}
	if !slices.Equal(r.sessions.revoked, []authn.SessionID{"old-session"}) {
		t.Errorf("the sessions revoked are %v, want the one the resumed post came with", r.sessions.revoked)
	}
	if st := flowRecordOf(t, r, cookie).State; st != stateDone {
		t.Errorf("the flow state is %q, want done", st)
	}
}

// TestACrossSitePostToTheResumeRouteIsRefusedBeforeTheHandler holds that the resume route keeps the check, so the method is never asked and the flow stays first.
func TestACrossSitePostToTheResumeRouteIsRefusedBeforeTheHandler(t *testing.T) {
	m := &redirectMethod{name: "saml"}
	r := newRigMethod(t, m)
	r.allow("acct1")
	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"saml","input":{"provider":"ent-1"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)

	out := r.postWith("/api/v1/auth/callback/ent-1/resume", "SAMLResponse=forged&RelayState=attacker",
		"Cookie", cookie, "Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "navigate", "Origin", crossSiteIDP)
	if out.Code != http.StatusUnauthorized {
		t.Errorf("a cross-site post to the resume route answered %d, want 401: %s", out.Code, out.Body.String())
	}
	if strings.Contains(out.Body.String(), "flow-invalid") || strings.Contains(out.Body.String(), "login-failed") {
		t.Errorf("a cross-site post to the resume route reached the handler: %s", out.Body.String())
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times, want none", n)
	}
	if st := flowRecordOf(t, r, cookie).State; st != stateFirst {
		t.Errorf("the flow state is %q, want it still at first", st)
	}
}

// TestAResumedResponseForAnotherFlowFailsTheVictimsFlow holds that a response with the wrong RelayState is refused and spends the victim's flow with no session.
func TestAResumedResponseForAnotherFlowFailsTheVictimsFlow(t *testing.T) {
	m := &redirectMethod{name: "saml"}
	m.answer = func(_ authn.FlowState, in authn.StepInput) (authn.Step, error) {
		if in.Form.Get("RelayState") != "victim-relay" {
			return authn.StepRefused(authn.ErrInvalidCredentials), nil
		}
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodSAML}}), nil
	}
	r := newRigMethod(t, m)
	r.allow("acct1")
	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"saml","input":{"provider":"ent-1"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)

	page := r.bounceThrough(t, "ent-1", "SAMLResponse=attacker-assertion&RelayState=attacker-relay")
	out := r.resume(t, page, cookie)
	if out.Code != http.StatusUnauthorized || !strings.Contains(out.Body.String(), "login-failed") {
		t.Errorf("a resumed response for another flow answered %d, want 401 login-failed: %s", out.Code, out.Body.String())
	}
	if len(r.sessions.created) != 0 {
		t.Errorf("a response for another flow made %d sessions, want none", len(r.sessions.created))
	}
	if st := flowRecordOf(t, r, cookie).State; st != stateFailed {
		t.Errorf("the victim's flow state is %q, want the failed tombstone", st)
	}
}
