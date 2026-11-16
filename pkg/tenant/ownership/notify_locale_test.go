package ownership_test

// The language the notices go out in. A notice is for its recipient, so the
// dispatch's locale is the recipient's stored preference, and the deployment's
// configured default when there is none to read. The requester's own request
// context carries the requester's language and must not reach it: a transfer's
// confirmation and decline are answered by one account and delivered to
// another. A preference the seam cannot read is a separate state from an
// absent one: it takes the same fallback and leaves a warning naming the
// notice's type and tenant, without the recipient.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	speedlog "github.com/vislake/speed/pkg/log"
)

// storedLocale writes the language an account carries as its stored
// preference, through the account administration surface.
func (a *assembly) storedLocale(t *testing.T, who authn.Account, tag string) {
	t.Helper()
	if err := a.admin.SetAttributes(t.Context(), who.ID, map[string]string{i18n.LocaleAttributeKey: tag}); err != nil {
		t.Fatalf("storing the language %q on %q: %v", tag, who.ID, err)
	}
}

// errUnreadableLocale is the failure the profile stand-in reports.
var errUnreadableLocale = errors.New("the account store is unavailable")

// unreadableProfile is a profile whose preference read fails. It reports the
// failure through the seam's error, which is what tells this state apart from
// an account that stores no preference.
type unreadableProfile struct{}

func (unreadableProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return "", false, errUnreadableLocale
}

func (unreadableProfile) TimeZoneFor(context.Context, string) (string, bool) { return "", false }

// storedProfile is a profile that answers one stored preference for every
// account.
type storedProfile struct{ locale i18n.Locale }

func (p storedProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return p.locale, p.locale != "", nil
}

func (storedProfile) TimeZoneFor(context.Context, string) (string, bool) { return "", false }

// logRecorder keeps every record a case's module writes.
type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func newLogRecorder() *logRecorder { return &logRecorder{} }

func (*logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec.Clone())
	return nil
}

func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *logRecorder) WithGroup(string) slog.Handler      { return r }

// atLevel returns the records written at one level.
func (r *logRecorder) atLevel(level slog.Level) []slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []slog.Record
	for _, rec := range r.records {
		if rec.Level == level {
			out = append(out, rec)
		}
	}
	return out
}

// recordingLogger is the assembly's logger capability, holding a recorder. Its
// redaction rule set accepts every registration: this case holds the language a
// notice goes out in, not what a log line masks.
type recordingLogger struct {
	handler *logRecorder
}

func (l recordingLogger) Named(string) *slog.Logger   { return slog.New(l.handler) }
func (recordingLogger) Redaction() speedlog.Redaction { return acceptingRedaction{} }

// acceptingRedaction takes every rule the assembled modules register.
type acceptingRedaction struct{}

func (acceptingRedaction) AddKeys(...string)                   {}
func (acceptingRedaction) AddPattern(string, speedlog.Matcher) {}

// attr reads one attribute of a record.
func attr(rec slog.Record, key string) (string, bool) {
	var value string
	var found bool
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			value = a.Value.String()
			found = true
			return false
		}
		return true
	})
	return value, found
}

// A notice goes out in the recipient's stored language, not in the language of
// the request context that triggered it. The request context carries the
// requester's language throughout: the target asks nothing and is notified in
// English, and the previous owner answers the confirmation in English while the
// result notice reaches the previous owner, who reads Chinese.
func TestTheNoticeGoesOutInTheRecipientsStoredLanguage(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		a.storedLocale(t, owner, "zh-CN")
		a.storedLocale(t, target, "en-US")

		request, err := a.writer.RequestTransfer(i18n.WithLocale(t.Context(), "zh-CN"),
			"acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		requested := a.dispatcher.all()
		if len(requested) != 1 {
			t.Fatalf("the module dispatched %d notice(s), want the request notice", len(requested))
		}
		if got := requested[0].Locale; got != "en-US" {
			t.Errorf("the request notice's locale is %q, want the target's stored %q: "+
				"the notice is the target's, not the requester's", got, "en-US")
		}

		if confirmErr := a.target.ConfirmTransfer(i18n.WithLocale(t.Context(), "en-US"),
			request.ID, target.ID.String()); confirmErr != nil {
			t.Fatalf("confirming the transfer: %v", confirmErr)
		}
		notices := a.dispatcher.all()
		if len(notices) != 2 {
			t.Fatalf("the module dispatched %d notice(s), want the request and the confirmation", len(notices))
		}
		if got := notices[1].Locale; got != "zh-CN" {
			t.Errorf("the confirmation's locale is %q, want the previous owner's stored %q: "+
				"the confirming account's own language must not decide it", got, "zh-CN")
		}

		// The decline answers the same question from the other side: the
		// target declines under its own language, and the result notice reaches
		// the account that made the request, in that account's stored language.
		second, err := a.writer.RequestTransfer(i18n.WithLocale(t.Context(), "en-US"),
			"acme", owner.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the second transfer: %v", err)
		}
		if err := a.target.DeclineTransfer(i18n.WithLocale(t.Context(), "zh-CN"),
			second.ID, owner.ID.String()); err != nil {
			t.Fatalf("declining the second transfer: %v", err)
		}
		notices = a.dispatcher.all()
		if len(notices) != 4 {
			t.Fatalf("the module dispatched %d notice(s), want the two requests and their results", len(notices))
		}
		if got := notices[2].Locale; got != "zh-CN" {
			t.Errorf("the second request's locale is %q, want the target's stored %q", got, "zh-CN")
		}
		if got := notices[3].Locale; got != "en-US" {
			t.Errorf("the decline's locale is %q, want the requester's stored %q: "+
				"the declining account's own language must not decide it", got, "en-US")
		}
	})
}

// With no stored preference on the recipient, a notice falls back to the
// deployment's configured default, and that fallback is not a failed read: no
// warning is written. The requester's language is the other one on purpose, so
// following the request context and falling back to the old hard-coded "en-US"
// both fail this case.
func TestTheNoticeFallsBackToTheDeploymentDefaultLocale(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		request, err := a.writer.RequestTransfer(i18n.WithLocale(t.Context(), "en-US"),
			"acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		notices := a.dispatcher.all()
		if len(notices) != 1 {
			t.Fatalf("the module dispatched %d notice(s), want the request notice", len(notices))
		}
		if got := notices[0].Locale; got != testDefaultLocale {
			t.Errorf("the notice's locale is %q, want the deployment default %q: "+
				"not the requester's %q and not a fixed one", got, testDefaultLocale, "en-US")
		}
		for _, rec := range a.logs.atLevel(slog.LevelWarn) {
			if strings.Contains(rec.Message, "language preference could not be read") {
				t.Errorf("an absent preference wrote %q; only a failed read is a warning", rec.Message)
			}
		}

		// The fallback never blocks the write it describes.
		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || !found || pending.ID != request.ID {
			t.Errorf("the pending read answered %+v found=%t err=%v, want the request", pending, found, err)
		}
	})
}

// A preference the seam cannot read is not the same state as one the account
// does not store: the notice falls back to the deployment default, the write
// stands, and a warning names the notice's type and tenant. The recipient's
// identifier stays out of the line.
func TestTheNoticeFallsBackToTheDefaultWhenThePreferenceReadFails(t *testing.T) {
	withI18nProfile(t, standIn("stand-in.i18n-profile", unreadableProfile{}, (*i18n.Profile)(nil)),
		func(t *testing.T, a *assembly) {
			owner := a.user(t, "alice")
			target := a.user(t, "bob")
			a.organization(t, "acme", "acme-org", owner)
			a.join(t, "acme", target)

			request, err := a.writer.RequestTransfer(i18n.WithLocale(t.Context(), "en-US"),
				"acme", target.ID.String(), "")
			if err != nil {
				t.Fatalf("requesting the transfer: %v", err)
			}
			notices := a.dispatcher.all()
			if len(notices) != 1 {
				t.Fatalf("the module dispatched %d notice(s), want the request notice", len(notices))
			}
			if got := notices[0].Locale; got != testDefaultLocale {
				t.Errorf("the notice's locale is %q, want the deployment default %q: "+
					"an unreadable preference borrows neither the requester's language nor a fixed one",
					got, testDefaultLocale)
			}

			var warned []slog.Record
			for _, rec := range a.logs.atLevel(slog.LevelWarn) {
				if strings.Contains(rec.Message, "language preference could not be read") {
					warned = append(warned, rec)
				}
			}
			if len(warned) != 1 {
				t.Fatalf("a failed preference read wrote %d warning(s), want one: "+
					"an absent preference is the only silent fallback", len(warned))
			}
			if got, _ := attr(warned[0], "type"); got != "tenant.owner.transfer_requested" {
				t.Errorf("the warning names type %q, want the notice's declared type", got)
			}
			if got, _ := attr(warned[0], "tenant"); got != "acme" {
				t.Errorf("the warning names tenant %q, want the notice's tenant", got)
			}
			if _, found := attr(warned[0], "account"); found {
				t.Errorf("the warning carries the recipient's identifier; the line names the notice, not the person")
			}
			warned[0].Attrs(func(attribute slog.Attr) bool {
				if strings.Contains(attribute.Value.String(), target.ID.String()) {
					t.Errorf("the warning's %q attribute carries the recipient's identifier %q",
						attribute.Key, target.ID)
				}
				return true
			})

			// The notice is best effort and the preference read is part of it.
			pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
			if err != nil || !found || pending.ID != request.ID {
				t.Errorf("the pending read answered %+v found=%t err=%v, want the request", pending, found, err)
			}
		})
}

// A failed read in one profile does not end the search: every profile is asked,
// a later one's stored preference decides the notice, and the failure leaves
// exactly one warning naming the notice and not the recipient.
func TestTheNoticeAsksEveryProfileAfterAFailedRead(t *testing.T) {
	withI18nProfiles(t, []core.Module{
		profileStandIn("stand-in.a-unreadable-profile", unreadableProfile{}),
		profileStandIn("stand-in.b-stored-profile", storedProfile{locale: "ja-JP"}),
	}, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		request, err := a.writer.RequestTransfer(i18n.WithLocale(t.Context(), "en-US"),
			"acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		notices := a.dispatcher.all()
		if len(notices) != 1 {
			t.Fatalf("the module dispatched %d notice(s), want the request notice", len(notices))
		}
		if got := notices[0].Locale; got != "ja-JP" {
			t.Errorf("the notice's locale is %q, want the second profile's stored %q: "+
				"one profile's failed read says nothing about the next", got, "ja-JP")
		}

		var warned []slog.Record
		for _, rec := range a.logs.atLevel(slog.LevelWarn) {
			if strings.Contains(rec.Message, "language preference could not be read") {
				warned = append(warned, rec)
			}
		}
		if len(warned) != 1 {
			t.Fatalf("the failed read wrote %d warning(s), want exactly one for one failing profile", len(warned))
		}
		if got, _ := attr(warned[0], "type"); got != "tenant.owner.transfer_requested" {
			t.Errorf("the warning names type %q, want the notice's declared type", got)
		}
		if got, _ := attr(warned[0], "tenant"); got != "acme" {
			t.Errorf("the warning names tenant %q, want the notice's tenant", got)
		}
		warned[0].Attrs(func(attribute slog.Attr) bool {
			if strings.Contains(attribute.Value.String(), target.ID.String()) {
				t.Errorf("the warning's %q attribute carries the recipient's identifier %q",
					attribute.Key, target.ID)
			}
			return true
		})

		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || !found || pending.ID != request.ID {
			t.Errorf("the pending read answered %+v found=%t err=%v, want the request", pending, found, err)
		}
	})
}

// Two profiles whose reads fail leave one warning between them: the warning is
// about this notice, not about each seam, so the second failure writes nothing
// and the notice still falls back to the deployment default.
func TestTheNoticeWarnsOnceHoweverManyProfileReadsFail(t *testing.T) {
	withI18nProfiles(t, []core.Module{
		profileStandIn("stand-in.a-unreadable-profile", unreadableProfile{}),
		profileStandIn("stand-in.b-unreadable-profile", unreadableProfile{}),
	}, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		request, err := a.writer.RequestTransfer(i18n.WithLocale(t.Context(), "en-US"),
			"acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		notices := a.dispatcher.all()
		if len(notices) != 1 {
			t.Fatalf("the module dispatched %d notice(s), want the request notice", len(notices))
		}
		if got := notices[0].Locale; got != testDefaultLocale {
			t.Errorf("the notice's locale is %q, want the deployment default %q: "+
				"no profile answered, so the default decides", got, testDefaultLocale)
		}

		var warned []slog.Record
		for _, rec := range a.logs.atLevel(slog.LevelWarn) {
			if strings.Contains(rec.Message, "language preference could not be read") {
				warned = append(warned, rec)
			}
		}
		if len(warned) != 1 {
			t.Fatalf("two failed reads wrote %d warning(s), want one: "+
				"the line describes the notice, not each seam that failed", len(warned))
		}
		if got, _ := attr(warned[0], "type"); got != "tenant.owner.transfer_requested" {
			t.Errorf("the warning names type %q, want the notice's declared type", got)
		}
		if got, _ := attr(warned[0], "tenant"); got != "acme" {
			t.Errorf("the warning names tenant %q, want the notice's tenant", got)
		}

		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || !found || pending.ID != request.ID {
			t.Errorf("the pending read answered %+v found=%t err=%v, want the request", pending, found, err)
		}
	})
}

// An answering profile ends the search: the later profile is never asked, so
// its failed read leaves no warning and the first answer decides the notice.
func TestTheNoticeStopsAtTheFirstAnswer(t *testing.T) {
	withI18nProfiles(t, []core.Module{
		profileStandIn("stand-in.a-stored-profile", storedProfile{locale: "ja-JP"}),
		profileStandIn("stand-in.b-unreadable-profile", unreadableProfile{}),
	}, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		request, err := a.writer.RequestTransfer(i18n.WithLocale(t.Context(), "en-US"),
			"acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		notices := a.dispatcher.all()
		if len(notices) != 1 {
			t.Fatalf("the module dispatched %d notice(s), want the request notice", len(notices))
		}
		if got := notices[0].Locale; got != "ja-JP" {
			t.Errorf("the notice's locale is %q, want the first profile's stored %q: "+
				"the search ends at an answer", got, "ja-JP")
		}
		for _, rec := range a.logs.atLevel(slog.LevelWarn) {
			if strings.Contains(rec.Message, "language preference could not be read") {
				t.Errorf("a profile the search never reached wrote %q; an answer ends the search",
					rec.Message)
			}
		}

		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || !found || pending.ID != request.ID {
			t.Errorf("the pending read answered %+v found=%t err=%v, want the request", pending, found, err)
		}
	})
}

// An assembly with no stored preference and no i18n engine has no language to
// send in: the notice is skipped rather than sent in a fixed one, and the
// request it describes stands.
func TestTheNoticeIsSkippedWhenNoLanguageIsAvailable(t *testing.T) {
	withNoI18nEngine(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if notices := a.dispatcher.all(); len(notices) != 0 {
			t.Errorf("the module dispatched %d notice(s) with no language available, want none", len(notices))
		}
		var warned []slog.Record
		for _, rec := range a.logs.atLevel(slog.LevelWarn) {
			if strings.Contains(rec.Message, "no i18n engine is assembled") {
				warned = append(warned, rec)
			}
		}
		if len(warned) != 1 {
			t.Fatalf("the skipped notice wrote %d warning(s), want exactly one: "+
				"a notice no language can carry leaves a trace", len(warned))
		}
		if got, _ := attr(warned[0], "type"); got != "tenant.owner.transfer_requested" {
			t.Errorf("the warning names type %q, want the notice's declared type", got)
		}
		if got, _ := attr(warned[0], "tenant"); got != "acme" {
			t.Errorf("the warning names tenant %q, want the notice's tenant", got)
		}
		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || !found || pending.ID != request.ID {
			t.Errorf("the pending read answered %+v found=%t err=%v, want the request", pending, found, err)
		}
	})
}

// A stored preference is a language on its own: an assembly with no i18n engine
// still sends the notice in it, because the resolver is only the fallback and
// not the source.
func TestTheNoticeUsesAStoredPreferenceWithoutAnEngine(t *testing.T) {
	withNoI18nEngine(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		a.storedLocale(t, target, "zh-CN")

		request, err := a.writer.RequestTransfer(i18n.WithLocale(t.Context(), "en-US"),
			"acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		notices := a.dispatcher.all()
		if len(notices) != 1 {
			t.Fatalf("the module dispatched %d notice(s), want the request notice: "+
				"a stored preference needs no engine", len(notices))
		}
		if got := notices[0].Locale; got != "zh-CN" {
			t.Errorf("the notice's locale is %q, want the target's stored %q", got, "zh-CN")
		}
		for _, rec := range a.logs.atLevel(slog.LevelWarn) {
			if strings.Contains(rec.Message, "no i18n engine is assembled") {
				t.Errorf("a stored preference wrote %q; the engine is a fallback, not a requirement", rec.Message)
			}
		}
		pending, found, err := a.writer.PendingTransfer(t.Context(), "acme")
		if err != nil || !found || pending.ID != request.ID {
			t.Errorf("the pending read answered %+v found=%t err=%v, want the request", pending, found, err)
		}
	})
}
