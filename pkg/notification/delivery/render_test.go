package delivery

// The email channel still renders its time segments on the server, in the
// recipient's stored zone. A zone read that fails must not be read as "no zone
// stored": that would freeze the instant at UTC. The read is reported, the
// channel fails, no text is rendered, and the job retries.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/notification"
)

// emailCatalog is a one-locale catalog carrying an email subject and a
// plain-text body whose time segment uses the conventional layout with an
// explicit offset.
func emailCatalog() i18n.Catalog {
	return i18n.Catalog{
		FS: fstest.MapFS{
			"locales/en-US.json": &fstest.MapFile{Data: []byte(
				`{"billing.invoice_paid.email.subject":"Transfer due",` +
					`"billing.invoice_paid.email.body_text":"Expires {{at, time, 2006-01-02 15:04 -0700}}"}`,
			)},
		},
		Pattern: "locales/*.json",
	}
}

// failingThenStoredProfile reports a read failure once, then answers the stored
// zone, so one case can hold both the refusal and the retry that renders.
type failingThenStoredProfile struct {
	mu    sync.Mutex
	calls int
	zone  string
	err   error
}

func (p *failingThenStoredProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return "", false, nil
}

func (p *failingThenStoredProfile) TimeZoneFor(context.Context, string) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		return "", false, p.err
	}
	return p.zone, true, nil
}

// readCalls reports how many times the zone was read.
func (p *failingThenStoredProfile) readCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestTheEmailChannelFailsWithoutRenderingWhenTheZoneReadFails holds the two
// halves of the failure: the first consumption returns the read's error, sends
// no mail and records the channel as failed; the second consumption, whose read
// succeeds, renders the instant at the stored zone. A server that treated the
// failure as "unset" would send a `+0000` body on the first consumption, which
// this case would see in the carrier's messages. The case also pins the read
// count — one per consumption, so a job that read the zone twice would fail it
// — and the failure line the read writes: one Error naming the type and the
// cause, never the recipient.
func TestTheEmailChannelFailsWithoutRenderingWhenTheZoneReadFails(t *testing.T) {
	instant := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	readErr := errors.New("the account store is unreachable")
	profile := &failingThenStoredProfile{zone: "Asia/Shanghai", err: readErr}
	logs := &logBuffer{}
	r := newRig(t, func(h *host) {
		h.mail = true
		h.realLocalizer = true
		h.profile = profile
		h.logger = bufferLogger{buf: logs}
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:             typeName,
			Group:           "billing",
			DefaultChannels: []notification.Channel{notification.ChannelEmail},
			Unsubscribable:  true,
		}}
		h.resources = []any{emailCatalog()}
	})
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelEmail)
	job := jobOf(t, dispatch("u1", "occ-1", map[string]any{
		"to": "u1@example.test",
		"at": instant,
	}))

	if _, err := r.p.Handle(ctx, job, nil); !errors.Is(err, readErr) {
		t.Fatalf("the first consumption ended with %v, want the zone read's failure", err)
	}
	if got := r.mailSender.sendCount(); got != 0 {
		t.Fatalf("the carrier was reached %d times, want 0: no text may be rendered from a failed zone read", got)
	}
	if got := profile.readCalls(); got != 1 {
		t.Fatalf("the failed consumption read the recipient's zone %d times, want 1", got)
	}
	rows := r.rows("t1")
	if len(rows) != 1 || rows[0].channel != "email" || rows[0].status != statusFailed {
		t.Fatalf("the ledger holds %+v, want one failed email row", rows)
	}

	// The failure is reported once, with the type and the cause, never the
	// recipient: it is what tells an operator a persistent read failure is
	// holding mail back.
	reported := logs.lines("the recipient's time zone could not be read")
	if len(reported) != 1 {
		t.Fatalf("the zone-read failure was logged %d times, want 1: %q", len(reported), logs.String())
	}
	for _, want := range []string{"level=ERROR", "type_key=" + typeName, "cause=", readErr.Error()} {
		if !strings.Contains(reported[0], want) {
			t.Errorf("the failure line %q does not carry %q", reported[0], want)
		}
	}
	if strings.Contains(reported[0], "u1") {
		t.Errorf("the failure line %q names the recipient", reported[0])
	}

	// The retry reads the stored zone and renders the instant there.
	readsBeforeRetry := profile.readCalls()
	if _, err := r.p.Handle(ctx, job, nil); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if got := profile.readCalls() - readsBeforeRetry; got != 1 {
		t.Fatalf("the successful consumption read the recipient's zone %d times, want 1", got)
	}
	sent := r.mailSender.messages()
	if len(sent) != 1 {
		t.Fatalf("the carrier accepted %d messages, want 1", len(sent))
	}
	if want := "2026-11-20 17:30 +0800"; !strings.Contains(sent[0].Text, want) {
		t.Errorf("the body is %q, want the instant in the recipient's zone as %q", sent[0].Text, want)
	}
	if strings.Contains(sent[0].Text, "+0000") {
		t.Errorf("the body is %q, want no UTC-frozen instant", sent[0].Text)
	}
	if strings.Contains(sent[0].Subject, "{{") || strings.Contains(sent[0].Text, "{{") {
		t.Errorf("the email %q/%q carries a placeholder left as written", sent[0].Subject, sent[0].Text)
	}
}

// TestAnSmsTimeParameterIsUTCAtWholeSecondPrecision holds the wire shape of an
// sms time parameter: RFC 3339 in UTC, at second precision. Go's Format omits
// the sub-second part when the layout carries none, so a JSON stringification —
// which would carry nanoseconds — is not the answer. A value in another zone is
// written as the same instant in UTC.
func TestAnSmsTimeParameterIsUTCAtWholeSecondPrecision(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 30, 0, 123456789, time.UTC)
	got, err := stringify(at)
	if err != nil {
		t.Fatalf("stringify: %v", err)
	}
	if want := "2026-11-20T09:30:00Z"; got != want {
		t.Errorf("stringified instant = %q, want %q", got, want)
	}

	shanghai := time.Date(2026, 11, 20, 17, 30, 0, 123456789,
		time.FixedZone("CST", 8*60*60))
	got, err = stringify(shanghai)
	if err != nil {
		t.Fatalf("stringify: %v", err)
	}
	if want := "2026-11-20T09:30:00Z"; got != want {
		t.Errorf("stringified instant = %q, want the same moment in UTC as %q", got, want)
	}
}
