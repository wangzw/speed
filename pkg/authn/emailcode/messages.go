package emailcode

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/i18n"
)

//go:embed locales/*.json
var localesFS embed.FS

const catalogPattern = "locales/*.json"

func messageCatalog() i18n.Catalog {
	return i18n.Catalog{FS: localesFS, Pattern: catalogPattern}
}

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

func catalogCarriesReset(fsys fs.FS, pattern string) bool {
	names, err := fs.Glob(fsys, pattern)
	if err != nil || len(names) == 0 {
		return false
	}
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return false
		}
		var entries map[string]string
		if err := json.Unmarshal(raw, &entries); err != nil {
			return false
		}
		for _, key := range resetKeys {
			if strings.TrimSpace(entries[string(key)]) == "" {
				return false
			}
		}
	}
	return true
}
