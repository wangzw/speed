package tenantadmin

import (
	"context"
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the titles and
// descriptions the tenant surface renders for the items it exposes, one file
// per locale. It is declared to the i18n engine as an i18n.Catalog resource, so
// the engine merges it with every other module's into one index and these texts
// render in a request's language beside the rest.
//
//go:embed locales/*.json
var localesFS embed.FS

// messageCatalog is the catalog resource this module declares to the engine.
var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}

// text renders a catalog key in the request's language, falling back to the
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
