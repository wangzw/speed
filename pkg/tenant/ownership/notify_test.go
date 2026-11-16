package ownership_test

// The notice rendering cases: a dispatch carries typed parameters and the
// engine interpolates them in the recipient's locale, so a message that names
// a time must use the engine's time form rather than a bare placeholder.

import (
	"context"
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

// driveLocalizer builds the i18n engine over the catalogs this module declares.
// The module is registered for its resources alone and does not take part in
// the drive, so its dependencies need no stand-ins.
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

// TestTheTransferRequestNoticeMarksItsExpiry holds the body the in-app channel
// writes: the marked render replaces the time segment with a marker naming the
// parameter and formats nothing, so the layout it carries has no effect on the
// delivered text. The catalog's layout therefore names no offset: an offset
// would suggest a zone decision this channel does not make, and the reader's
// display zone decides it instead.
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
		if strings.Contains(body, "+0000") || strings.Contains(body, "-0700") {
			t.Errorf("%s body = %q, want no offset in the marked render", locale, body)
		}
	}
}

// TestTheTransferRequestNoticeRendersItsExpiry holds the request body in both
// languages: the expiry parameter is a time.Time, which the engine interpolates
// only through a {{name, time, layout}} segment, so a bare placeholder would
// reach the recipient as the literal "{{expires_at}}".
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
