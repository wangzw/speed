package admin

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's message catalog, one file per locale.
//
//go:embed locales/*.json
var localesFS embed.FS

var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}
