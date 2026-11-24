package invitemail

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/i18n"
)

const (
	subjectKey    i18n.Key = "authn.invitemail.subject"
	textKey       i18n.Key = "authn.invitemail.body_text"
	htmlKey       i18n.Key = "authn.invitemail.body_html"
	textByKey     i18n.Key = "authn.invitemail.body_text_by"
	htmlByKey     i18n.Key = "authn.invitemail.body_html_by"
	expiryTextKey i18n.Key = "authn.invitemail.expiry_text"
	expiryHTMLKey i18n.Key = "authn.invitemail.expiry_html"
)

var messageKeys = []i18n.Key{
	subjectKey, textKey, htmlKey, textByKey, htmlByKey, expiryTextKey, expiryHTMLKey,
}

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
