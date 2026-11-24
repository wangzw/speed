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

const (
	bounceCSPHeader      = "Content-Security-Policy"
	bounceReferrerHeader = "Referrer-Policy"
	bounceReferrerPolicy = "same-origin"
	bounceNoSniffHeader  = "X-Content-Type-Options"
	bounceNoSniff        = "nosniff"
	bounceMediaType      = "text/html; charset=utf-8"
)

// bouncePage's script submits through the form prototype so a posted field named submit cannot shadow it.
var bouncePage = template.Must(template.New("bounce").Parse(`<!doctype html>
<html><head><meta charset="utf-8"></head><body>
<form id="resume" method="post" action="{{.Action}}">
{{- range .Fields}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end -}}
<noscript><input type="submit"></noscript></form>
<script nonce="{{.Nonce}}">HTMLFormElement.prototype.submit.call(document.getElementById("resume"))</script>
</body></html>
`))

type bounceField struct {
	Name  string
	Value string
}

type bounceView struct {
	Action string
	Nonce  string
	Fields []bounceField
}

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

func (r *routes) formActionSources() string {
	return strings.Join(append([]string{"'self'"}, r.trustedOrigins...), " ")
}

// resumePath is built from the declared route and the path parameter, never from a field or header of the request.
func (r *routes) resumePath(provider string) string {
	rel := mountRel(pathCallbackResume)
	return r.mounter.BaseIn("") + strings.Replace(rel, ":provider", url.PathEscape(provider), 1)
}

func bounceFields(form url.Values) []bounceField {
	fields := make([]bounceField, 0, len(form))
	for _, name := range slices.Sorted(maps.Keys(form)) {
		for _, value := range form[name] {
			fields = append(fields, bounceField{Name: name, Value: value})
		}
	}
	return fields
}
