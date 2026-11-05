package policy

import (
	"embed"

	"github.com/vislake/speed/pkg/i18n"
)

// roleLocalesFS carries this module's own role catalog: the description of each
// builtin role the host seeds, one file per locale. It is declared to the i18n
// engine as a second i18n.Catalog resource, so the role descriptions resolve in
// the same index as every other module's messages while staying apart from the
// permission descriptions in locales/.
//
// The keys are the ones the host writes into a builtin role's description key:
// authz.role.platformAdmin and authz.role.tenantAdmin. A role view renders the
// stored key through the localizer, so an untranslated key falls back to the
// key itself rather than to empty text.
//
//go:embed rolelocales/*.json
var roleLocalesFS embed.FS

// roleMessageCatalog is the role catalog resource this module declares to the
// engine.
var roleMessageCatalog = i18n.Catalog{FS: roleLocalesFS, Pattern: "rolelocales/*.json"}
