package policy

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the description of every
// permission this module declares, one file per locale. It is declared to the
// i18n engine as an i18n.Catalog resource, so the engine merges it with every
// other module's into one index and the key a catalog read hands out resolves
// to words rather than to itself.
//
//go:embed locales/*.json
var localesFS embed.FS

// messageCatalog is the catalog resource this module declares to the engine.
var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}
