package emailcode

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/i18n"
)

// localesFS carries this module's message catalog: one file per language, each
// naming the subject and the two bodies of the verification message. It is
// declared to the i18n engine as an i18n.Catalog resource, so the engine merges
// it with every other module's and a Translate of one of these keys resolves to
// words rather than to itself.
//
//go:embed locales/*.json
var localesFS embed.FS

// catalogPattern selects the message files under localesFS, one per locale.
const catalogPattern = "locales/*.json"

// messageCatalog is the catalog resource this module declares to the engine.
func messageCatalog() i18n.Catalog {
	return i18n.Catalog{FS: localesFS, Pattern: catalogPattern}
}

// catalogLocales lists the locales the embedded catalog carries, read off the
// file names, sorted. The startup check renders every message in every one of
// them, so a catalog with a language that is missing a message fails before any
// code is sent rather than at the first request in that language.
func catalogLocales() ([]i18n.Locale, error) {
	names, err := fs.Glob(localesFS, catalogPattern)
	if err != nil {
		return nil, fmt.Errorf("the message catalog pattern %q is malformed: %w", catalogPattern, err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("the message catalog %q carries no file", catalogPattern)
	}
	locales := make([]i18n.Locale, 0, len(names))
	for _, name := range names {
		tag := strings.TrimSuffix(path.Base(name), path.Ext(name))
		if tag == "" {
			return nil, fmt.Errorf("the message catalog %q names no locale", name)
		}
		locales = append(locales, i18n.Locale(tag))
	}
	slices.Sort(locales)
	return locales, nil
}
