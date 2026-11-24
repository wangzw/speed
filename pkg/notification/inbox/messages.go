package inbox

import (
	"context"
	"embed"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the titles and
// details of the refusals it writes, one file per locale.
//
//go:embed locales/*.json
var localesFS embed.FS

var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}

const (
	titleSuffix  = ".title"
	detailSuffix = ".detail"
)

// englishTitles and englishDetails are the fallbacks this module writes
// when no message localizer is assembled, or when a key no catalog
// carries is asked for.
var (
	englishTitles = map[string]string{
		badRequestCode:      "Bad Request",
		messageNotFoundCode: "Not Found",
	}
	englishDetails = map[string]string{
		badRequestCode:      "The request could not be read.",
		messageNotFoundCode: "The message is not one this recipient can see.",
	}
)

func (s *inboxStore) text(ctx context.Context, key i18n.Key, fallback string) string {
	if s.localizer == nil {
		return fallback
	}
	if rendered := s.localizer.Localize(ctx, key, nil); rendered != string(key) {
		return rendered
	}
	return fallback
}

// refusal builds the fault for one refusal: its status, its type and its
// rendered title and detail. An empty code writes the 503 — no problem type —
// whose cause stays in the log and whose body carries only a generic title.
func (s *inboxStore) refusal(ctx context.Context, status int, code string) speedhttp.Fault {
	fault := speedhttp.Fault{Status: status}
	if code == "" {
		fault.Title = s.text(ctx, i18n.Key("notification.inbox.unavailable"+titleSuffix), "Service Unavailable")
		return fault
	}
	fault.Type = typePrefix + code
	fault.Title = s.text(ctx, i18n.Key("notification.inbox."+code+titleSuffix), englishTitles[code])
	fault.Detail = s.text(ctx, i18n.Key("notification.inbox."+code+detailSuffix), englishDetails[code])
	return fault
}
