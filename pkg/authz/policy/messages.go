package policy

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

//go:embed locales/*.json
var localesFS embed.FS

var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}
