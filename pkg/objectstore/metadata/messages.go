package metadata

import (
	"context"
	"embed"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the titles and details of
// the refusals it writes, and the descriptions of its two permissions, one file
// per locale. It is declared to the i18n engine as an i18n.Catalog resource, so
// the engine merges it with every other module's into one index and these texts
// render in a request's language beside the rest.
//
//go:embed locales/*.json
var localesFS embed.FS

// messageCatalog is the catalog resource this module declares to the engine.
var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}

// The suffix each rendered problem part is keyed under, relative to the code.
const (
	titleSuffix  = ".title"
	detailSuffix = ".detail"
)

// englishTitles and englishDetails are the fallbacks this module writes when no
// message localizer is assembled, or when a key no catalog carries is asked for.
// The catalog carries both languages, so the fallback only reaches an assembly
// that took no localizer in.
var (
	englishTitles = map[string]string{
		badRequestCode: "Bad Request",
		notFoundCode:   "Not Found",
		conflictCode:   "Conflict",
	}
	englishDetails = map[string]string{
		badRequestCode: "The request could not be read.",
		notFoundCode:   "The object is not available.",
		conflictCode:   "The object's state does not allow this action.",
	}
)

// text renders a catalogue key in the request's language, falling back to the
// given English text when no localizer is assembled or the key is not carried.
func (p *product) text(ctx context.Context, key i18n.Key, fallback string) string {
	if p.localizer == nil {
		return fallback
	}
	if rendered := p.localizer.Localize(ctx, key, nil); rendered != string(key) {
		return rendered
	}
	return fallback
}

// refusal builds the fault for one refusal: its status, its type and its
// rendered title and detail. An empty code writes the 503 — no problem type of
// any kind beyond the design's own — whose cause stays in the log and whose body
// carries only a generic title.
func (p *product) refusal(ctx context.Context, status int, code string) speedhttp.Fault {
	fault := speedhttp.Fault{Status: status}
	if code == "" {
		fault.Title = p.text(ctx, i18n.Key("objectstore.unavailable"+titleSuffix), "Service Unavailable")
		return fault
	}
	fault.Type = typePrefix + code
	fault.Title = p.text(ctx, i18n.Key("objectstore."+code+titleSuffix), englishTitles[code])
	fault.Detail = p.text(ctx, i18n.Key("objectstore."+code+detailSuffix), englishDetails[code])
	return fault
}
