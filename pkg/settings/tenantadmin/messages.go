package tenantadmin

import (
	"context"
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

//go:embed locales/*.json
var localesFS embed.FS

var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}

func (p *product) text(ctx context.Context, key i18n.Key, fallback string) string {
	if p.localizer == nil {
		return fallback
	}
	if rendered := p.localizer.Localize(ctx, key, nil); rendered != string(key) {
		return rendered
	}
	return fallback
}
