package policy

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the description of every
// permission this module declares, one file per locale.
//
//go:embed locales/*.json
var localesFS embed.FS

var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}
