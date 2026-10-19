package preferences

import (
	"context"
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the titles and details of
// the refusals it writes, one file per locale. It is declared to the i18n engine
// as an i18n.Catalog resource, so the engine merges it with every other module's
// into one index and these texts render in a request's language beside the rest.
//
//go:embed locales/*.json
var localesFS embed.FS

// messageCatalog is the catalog resource this module declares to the engine.
var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}

// The problem types this module writes. typePrefix is the notification
// capability's namespace; the codes are this surface's kinds within it. A 503
// carries no type: it says a dependency could not answer, not which kind of
// refusal it was.
const (
	typePrefix            = "urn:speed:notification:"
	badRequestCode        = "bad-request"
	notUnsubscribableCode = "not-unsubscribable"
)

// The suffix each rendered problem part is keyed under, relative to the code.
const (
	titleSuffix  = ".title"
	detailSuffix = ".detail"
)

// englishTitles and englishDetails are the fallbacks this module writes when no
// message localizer is assembled, or when a key no catalog carries.
var (
	englishTitles = map[string]string{
		badRequestCode:        "Bad Request",
		notUnsubscribableCode: "Not Unsubscribable",
	}
	englishDetails = map[string]string{
		badRequestCode:        "The request could not be read.",
		notUnsubscribableCode: "The notification type cannot be unsubscribed.",
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
