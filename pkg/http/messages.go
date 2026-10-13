package http

import (
	"context"
	"embed"
	nethttp "net/http"
	"strconv"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's own message catalog: the standard text of
// the refusals it writes itself, one file per locale. It is declared to the
// i18n engine as an i18n.Catalog resource, so the engine merges it with every
// other module's into one index and these texts render in a request's language
// beside the rest.
//
//go:embed locales/*.json
var localesFS embed.FS

// messageCatalog is the catalog resource this module declares to the engine.
var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}

// refusalKey is the message key a refusal of the given status is rendered
// under: the status as "http.<status>".
func refusalKey(status int) i18n.Key {
	return i18n.Key("http." + strconv.Itoa(status))
}

// refusalTitle renders the title of a refusal this module writes itself — the
// 404, the 405, the 413 and the 500 — in the request's language.
//
// This module renders its own four refusals because it produces them: the
// module that decides the shape of an error response no longer carries a
// language, so the title is finished text here before the Problem reaches
// whichever shape writes it. Localize resolves the locale from the context and
// falls back to the run's configured default, so the language is the request's
// when one was settled and the default otherwise.
//
// The localizer is an optional capability and so is a catalog entry. With no
// localizer assembled, or with a key no catalog carries, the title is
// net/http's standard text for the status, which is what an assembly without
// this capability wrote before. Localize hands back the key itself for a key no
// catalog carries, which is turned into that standard text here rather than
// written to a client: the title is never empty.
func refusalTitle(ctx context.Context, localizer i18n.Localizer, status int) string {
	if localizer != nil {
		key := refusalKey(status)
		if text := localizer.Localize(ctx, key, nil); text != string(key) {
			return text
		}
	}
	return nethttp.StatusText(status)
}
