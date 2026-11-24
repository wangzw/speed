package policy

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// roleLocalesFS carries this module's own role catalog: the description of each
// builtin role the host seeds, one file per locale.
//
//go:embed rolelocales/*.json
var roleLocalesFS embed.FS

var roleMessageCatalog = i18n.Catalog{FS: roleLocalesFS, Pattern: "rolelocales/*.json"}
