package policy

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

//go:embed rolelocales/*.json
var roleLocalesFS embed.FS

var roleMessageCatalog = i18n.Catalog{FS: roleLocalesFS, Pattern: "rolelocales/*.json"}
