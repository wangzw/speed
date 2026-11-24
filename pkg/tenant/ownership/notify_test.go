package ownership_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/i18n/catalog"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/tenant/ownership"
)

func driveLocalizer(t *testing.T) i18n.Localizer {
	t.Helper()
	modules := []core.Module{ownership.Module(), catalog.Module()}
	reader := configtest.Reader(t, `{}`, modules...)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	for _, module := range modules {
		reg.Register(module)
	}
	var localizer i18n.Localizer
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{config.ModuleName, catalog.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			localizer, err = core.Resolve[i18n.Localizer](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("driving the i18n engine over this module's catalog: %v", err)
	}
	return localizer
}

// TestTheTransferRequestNoticeMarksItsExpiry checks that the in-app render replaces the expiry time with a marker naming the parameter.
func TestTheTransferRequestNoticeMarksItsExpiry(t *testing.T) {
	localizer := driveLocalizer(t)
	expires := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	for _, locale := range []i18n.Locale{"zh-CN", "en-US"} {
		ctx := i18n.WithTimeRender(
			i18n.WithLocale(t.Context(), locale),
			i18n.TimeRenderMarked,
		)
		body := localizer.Localize(ctx, "tenant.owner.transfer_requested.in_app.body", i18n.Args{
			"tenant":     i18n.String("acme"),
			"expires_at": i18n.Time(expires),
		})
		if want := "time:expires_at"; !strings.Contains(body, want) {
			t.Errorf("%s body = %q, want the expiry as the marker %q", locale, body, want)
		}
		if strings.Contains(body, "2026-11-20") {
			t.Errorf("%s body = %q, want no formatted instant in the marked render", locale, body)
		}
	}
}

// TestTheTransferRequestNoticeCatalogLayoutCarriesNoOffset checks that the catalog's time layout carries no zone token.
func TestTheTransferRequestNoticeCatalogLayoutCarriesNoOffset(t *testing.T) {
	const segmentOpen = "{{expires_at, time, "
	for _, locale := range []i18n.Locale{"zh-CN", "en-US"} {
		path := filepath.Join("locales", string(locale)+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		text := string(raw)
		start := strings.Index(text, segmentOpen)
		if start == -1 {
			t.Fatalf("%s carries no time segment for expires_at", path)
		}
		rest := text[start+len(segmentOpen):]
		end := strings.Index(rest, "}}")
		if end == -1 {
			t.Fatalf("%s leaves the expires_at time segment unterminated", path)
		}
		layout := rest[:end]
		for _, token := range []string{
			"MST",
			"-070000", "-07:00:00", "-0700", "-07:00", "-07",
			"Z070000", "Z07:00:00", "Z0700", "Z07:00", "Z07",
		} {
			if strings.Contains(layout, token) {
				t.Errorf("%s layout = %q, want no zone token %q", path, layout, token)
			}
		}
	}
}

// TestTheTransferRequestNoticeRendersItsExpiry checks that the request notice renders its expiry time in both languages.
func TestTheTransferRequestNoticeRendersItsExpiry(t *testing.T) {
	localizer := driveLocalizer(t)
	expires := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	for _, locale := range []i18n.Locale{"zh-CN", "en-US"} {
		ctx := i18n.WithLocale(t.Context(), locale)
		body := localizer.Localize(ctx, "tenant.owner.transfer_requested.in_app.body", i18n.Args{
			"tenant":     i18n.String("acme"),
			"expires_at": i18n.Time(expires),
		})
		if strings.Contains(body, "{{") {
			t.Errorf("%s body = %q, want the expiry formatted, not the placeholder left as written", locale, body)
		}
		if !strings.Contains(body, "2026-11-20") {
			t.Errorf("%s body = %q, want the expiry rendered as a date", locale, body)
		}
	}
}
