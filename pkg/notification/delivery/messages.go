package delivery

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the reasons this module
// writes into the ledger for the channels it judged itself, one file per locale.
// It is declared to the i18n engine as an i18n.Catalog resource, so the engine
// merges it with every other module's into one index.
//
// The text of a notification itself is not here. A type's channel text is keyed
// "<type_key>.<channel>.<part>" and belongs to the business module that declares
// the type; this module renders it but owns none of it.
//
//go:embed locales/*.json
var localesFS embed.FS

// messageCatalog is the catalog resource this module declares to the engine.
var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}
