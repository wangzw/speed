// Package respond renders the entry point's error responses as localized text.
// It registers a module named "i18n.respond" that delivers http.ErrorResponder,
// so a request answered with a problem gets a title in the request's language
// while the machine-readable Type is left exactly as the refusing layer wrote
// it.
//
// It carries its own catalog for the status-derived keys — "http.404" and the
// like — so that even a run with no catalog of its own renders a title rather
// than falling back to the standard library's English.
package respond

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"strconv"
	"strings"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
)

// ModuleName is this module's name in the registry.
const ModuleName = "i18n.respond"

// localesFS carries this module's own catalog, one file per locale.
//
//go:embed locales/*.json
var localesFS embed.FS

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// The responder is what the entry point takes up as its ErrorResponder.
var _ speedhttp.ErrorResponder = (*responder)(nil)

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
//
// The localizer renders the text and the resolver names the locale the response
// is written in; both are required, and both come from the engine. The
// capability is delivered non-exclusively: only one module may deliver
// ErrorResponder at a time — the entry point refuses an ambiguous lookup — but
// nothing here claims the seam for itself.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*i18n.Localizer)(nil)},
			{Token: (*i18n.Resolver)(nil)},
		},
		Provides: []core.Provision{
			{Token: (*speedhttp.ErrorResponder)(nil)},
		},
		Resources: []any{
			i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			localizer, err := core.Resolve[i18n.Localizer](reg)
			if err != nil {
				return nil, fmt.Errorf("i18n.respond: taking up the localizer that renders error text: %w", err)
			}
			resolver, err := core.Resolve[i18n.Resolver](reg)
			if err != nil {
				return nil, fmt.Errorf("i18n.respond: taking up the resolver that names the response language: %w", err)
			}
			return &responder{localizer: localizer, resolver: resolver}, nil
		},
	}
}

// responder renders one problem.
type responder struct {
	localizer i18n.Localizer
	resolver  i18n.Resolver
}

// RespondError writes p as a problem details object whose title is localized.
//
// The shape is the same as the default one: the message type, the status code
// and the other RFC 9457 members, in application/problem+json. What changes is
// the title, and one header: Content-Language names the language the title is
// in. The status code is p.Status, or 500 for a value that is not a final
// status; a Content-Length or Content-Encoding the caller set describes some
// other body and is dropped; every other header the caller set — a 401's
// WWW-Authenticate, a 429's Retry-After — stays.
func (r *responder) RespondError(w nethttp.ResponseWriter, req *nethttp.Request, p speedhttp.Problem) {
	ctx := req.Context()
	locale := r.resolver.LocaleOf(ctx)
	status := finalStatus(p.Status)
	body := problemBody{
		Type:     p.Type,
		Title:    r.title(ctx, locale, p, status),
		Status:   status,
		Detail:   p.Detail,
		Instance: p.Instance,
	}
	if body.Type == "" {
		body.Type = "about:blank"
	}
	w.Header().Del("Content-Length")
	w.Header().Del("Content-Encoding")
	w.Header().Set("Content-Type", problemMediaType)
	w.Header().Set("Content-Language", string(locale))
	encoded, _ := json.Marshal(body)
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

// title is the localized short summary of a problem. A problem that names a
// message key is rendered from it; one that names none but carries a title is
// taken as it stands; one that names only a type has its key derived from the
// type, and one that names neither falls back to the key of its status, so the
// built-in catalog answers the title of a refusal that carries no code. A key
// no catalog carries falls back to the standard library's text for the status.
func (r *responder) title(ctx context.Context, locale i18n.Locale, p speedhttp.Problem, status int) string {
	if p.Key != "" {
		if text, ok := r.lookup(locale, i18n.Key(p.Key), p.Args); ok {
			return text
		}
		return nethttp.StatusText(status)
	}
	if p.Title != "" {
		return p.Title
	}
	key := keyOfType(p.Type)
	if key == "" {
		key = statusKey(status)
	}
	if text, ok := r.lookup(locale, key, p.Args); ok {
		return text
	}
	return nethttp.StatusText(status)
}

// statusKey is the message key of a problem that names no code: the status its
// text is looked up under, as "http.404".
func statusKey(status int) i18n.Key {
	return i18n.Key("http." + strconv.Itoa(status))
}

// lookup renders key in locale, falling back to the default locale, and reports
// whether a catalog carried it.
func (r *responder) lookup(locale i18n.Locale, key i18n.Key, args i18n.Args) (string, bool) {
	if text, ok := r.localizer.Translate(locale, key, args); ok {
		return text, true
	}
	if locale != i18n.Default {
		if text, ok := r.localizer.Translate(i18n.Default, key, args); ok {
			return text, true
		}
	}
	return "", false
}

// keyOfType turns a problem type into the message key that names its text. The
// types this system writes are "urn:speed:<module>:<code>"; the key is the
// module and code joined, as "authn.login-failed". A type that is empty or the
// RFC 9457 default names no message.
func keyOfType(problemType string) i18n.Key {
	if problemType == "" || problemType == "about:blank" {
		return ""
	}
	stripped := strings.TrimPrefix(problemType, "urn:speed:")
	return i18n.Key(strings.ReplaceAll(stripped, ":", "."))
}

// finalStatus reads a status the way the default shape does: a value outside
// 200 through 599 is not a final status and is written as 500.
func finalStatus(status int) int {
	if status < nethttp.StatusOK || status > 599 {
		return nethttp.StatusInternalServerError
	}
	return status
}

// problemMediaType is the media type of the response.
const problemMediaType = "application/problem+json"

// problemBody is the response's JSON object. Its member names are the ones
// RFC 9457 defines.
type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}
