package http

import (
	"context"
	"embed"
	nethttp "net/http"
	"strconv"

	"github.com/vislake/speed/pkg/i18n"
)

//go:embed locales/*.json
var localesFS embed.FS

var messageCatalog = i18n.Catalog{FS: localesFS, Pattern: "locales/*.json"}

func refusalKey(status int) i18n.Key {
	return i18n.Key("http." + strconv.Itoa(status))
}

func refusalTitle(ctx context.Context, localizer i18n.Localizer, status int) string {
	if localizer != nil {
		key := refusalKey(status)
		if text := localizer.Localize(ctx, key, nil); text != string(key) {
			return text
		}
	}
	return nethttp.StatusText(status)
}
