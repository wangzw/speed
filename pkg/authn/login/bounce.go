package login

import (
	"bytes"
	"crypto/rand"
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The response headers the bounce page is answered with. The policy admits the
// one script that carries this response's nonce and no other resource, lets the
// form submission reach only this origin and the trusted origins, refuses to be
// framed and refuses a base element. same-origin keeps the resume post's Origin
// header the site's own origin, where no-referrer would send it as null.
const (
	bounceCSPHeader      = "Content-Security-Policy"
	bounceReferrerHeader = "Referrer-Policy"
	bounceReferrerPolicy = "same-origin"
	bounceNoSniffHeader  = "X-Content-Type-Options"
	bounceNoSniff        = "nosniff"
	bounceMediaType      = "text/html; charset=utf-8"
)

// bouncePage is the page a cross-site callback post is answered with. It posts
// the received form fields, escaped, to the provider's resume route as a
// same-origin navigation, which carries the Lax flow cookie the cross-site post
// did not. The script submits through the form prototype so that a posted field
// named "submit" cannot shadow the method. The page carries no product text: the
// submit button a browser without scripts shows is labelled by the browser in
// its own language.
var bouncePage = template.Must(template.New("bounce").Parse(`<!doctype html>
<html><head><meta charset="utf-8"></head><body>
<form id="resume" method="post" action="{{.Action}}">
{{- range .Fields}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end -}}
<noscript><input type="submit"></noscript></form>
<script nonce="{{.Nonce}}">HTMLFormElement.prototype.submit.call(document.getElementById("resume"))</script>
</body></html>
`))

// bounceField is one posted form field the page posts on unchanged.
type bounceField struct {
	Name  string
	Value string
}

// bounceView is what the page is rendered from.
type bounceView struct {
	Action string
	Nonce  string
	Fields []bounceField
}

// bounce answers a provider's cross-site callback post with the page that
// re-posts it to the provider's resume route. It parses the form within the
// callback's body limit and does nothing else: it reads no flow, writes no
// record and asks no method, because the request is outside the cross-origin
// check and carries no binding. A body over the limit or one that does not
// parse is refused as a failed login.
func (r *routes) bounce(c *call, provider string) (speedhttp.Reply, error) {
	form, ok := formValues(c)
	if !ok {
		c.unauthorized(mount.CodeLoginFailed)
		return c.result()
	}
	view := bounceView{
		Action: r.resumePath(provider),
		Nonce:  rand.Text(),
		Fields: bounceFields(form),
	}
	var page bytes.Buffer
	if err := bouncePage.Execute(&page, view); err != nil {
		c.serverError()
		return c.result()
	}
	policy := "default-src 'none'; script-src 'nonce-" + view.Nonce + "'; form-action " + r.formActionSources() +
		"; frame-ancestors 'none'; base-uri 'none'"
	reply := speedhttp.BytesReply(http.StatusOK, bounceMediaType, page.Bytes())
	return speedhttp.WithHeaders(reply, speedhttp.HeaderValue{Name: bounceCSPHeader, Value: policy}), nil
}

// formActionSources is the source list of the bounce page's form-action: this
// origin and every trusted origin, the public URL's among them. Chromium and
// WebKit hold form-action to every redirect the submission follows, and the
// resume route answers with a 303 to the flow's return target or to the failure
// landing. A relative target is this origin and an absolute one passed
// checkReturnTo against the trusted origins, so the list admits every target the
// resume route redirects to.
func (r *routes) formActionSources() string {
	return strings.Join(append([]string{"'self'"}, r.trustedOrigins...), " ")
}

// resumePath is the absolute path of the provider's resume route on this
// deployment: the ordinary base, then the declared route with the provider
// segment escaped into it. It is built from the route's declaration and the
// path parameter the callback route matched, never from a field or header of
// the request.
func (r *routes) resumePath(provider string) string {
	rel := mountRel(pathCallbackResume)
	return r.mounter.BaseIn("") + strings.Replace(rel, ":provider", url.PathEscape(provider), 1)
}

// bounceFields lists the posted fields in name order, each value of a repeated
// name in the order it was posted, so the resume post carries the form as it
// arrived.
func bounceFields(form url.Values) []bounceField {
	fields := make([]bounceField, 0, len(form))
	for _, name := range slices.Sorted(maps.Keys(form)) {
		for _, value := range form[name] {
			fields = append(fields, bounceField{Name: name, Value: value})
		}
	}
	return fields
}
