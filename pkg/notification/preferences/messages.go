package preferences

import (
	"context"
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the titles and
// details of the refusals it writes, one file per locale.
//
//go:embed locales/*.json
var localesFS embed.FS

var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}

const (
	typePrefix            = "urn:speed:notification:"
	badRequestCode        = "bad-request"
	notUnsubscribableCode = "not-unsubscribable"
)

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

func (p *product) text(ctx context.Context, key i18n.Key, fallback string) string {
	if p.localizer == nil {
		return fallback
	}
	if rendered := p.localizer.Localize(ctx, key, nil); rendered != string(key) {
		return rendered
	}
	return fallback
}
