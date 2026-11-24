package delivery

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the reasons this module
// writes into the ledger for the channels it judged itself.
//
//go:embed locales/*.json
var localesFS embed.FS

var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}
