package emailcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/i18n/catalog"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
)

const (
	mailModule     = "stand-in.mail"
	localizerModul = "stand-in.localizer"
	address        = "ada@example.com"
	testCode       = "241907"
)

// recordingSender is a mail.Sender that keeps what it was handed and fails on
// demand.
type recordingSender struct {
	mu       sync.Mutex
	messages []mail.Message
	err      error
}

var _ mail.Sender = (*recordingSender)(nil)

func (s *recordingSender) Name() string { return "recording" }

func (s *recordingSender) Send(_ context.Context, m mail.Message) (mail.Receipt, error) {
	s.mu.Lock()
	s.messages = append(s.messages, m)
	s.mu.Unlock()
	if s.err != nil {
		return mail.Receipt{}, s.err
	}
	return mail.Receipt{ProviderID: "recording"}, nil
}

func (s *recordingSender) sent() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.messages...)
}

// standIn is a module that declares the token names and delivers instance from
// its New, exclusively when told to.
func standIn(name string, instance any, exclusive bool, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: exclusive}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// driveEmailcode builds an assembly of this module, the i18n engine and the
// mail stand-in, and answers the EmailCodeSender it delivered.
func driveEmailcode(t *testing.T, sender mail.Sender) (authn.EmailCodeSender, error) {
	t.Helper()
	reader := authntest.ConfigReaderAll(t, "{}", Module(), catalog.Module())
	reg := core.New()
	reg.Register(Module())
	reg.Register(catalog.Module())
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(standIn(mailModule, sender, true, (*mail.Sender)(nil)))

	var got authn.EmailCodeSender
	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageNew,
		Participants: []string{
			ModuleName, catalog.ModuleName, config.ModuleName, mailModule,
		},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			got, err = core.Resolve[authn.EmailCodeSender](reg)
			return err
		},
	})
	return got, err
}

// TestASendLogsOneOutcomeLine holds the external-call rule: each send records
// its success or failure, the carrier's receipt and its duration, and the line
// carries neither the code nor the address.
func TestASendLogsOneOutcomeLine(t *testing.T) {
	var out bytes.Buffer
	ctx := log.WithLogger(context.Background(),
		slog.New(slog.NewTextHandler(&out, nil)))

	sender := &recordingSender{}
	channel, err := driveEmailcode(t, sender)
	if err != nil {
		t.Fatalf("driving the email channel: %v", err)
	}
	if err := channel.SendCode(ctx, address, testCode, 10*time.Minute, "en-US"); err != nil {
		t.Fatalf("sending the code: %v", err)
	}
	success := out.String()
	for _, want := range []string{"outcome=sent", "provider_id=recording", "duration_ms="} {
		if !strings.Contains(success, want) {
			t.Errorf("the success line lacks %q: %s", want, success)
		}
	}
	for _, leak := range []string{testCode, address} {
		if strings.Contains(success, leak) {
			t.Errorf("the success line carries %q: %s", leak, success)
		}
	}

	out.Reset()
	sender.err = fmt.Errorf("%w: the relay refused it", mail.ErrRejected)
	err = channel.SendCode(ctx, address, testCode, 10*time.Minute, "en-US")
	if !errors.Is(err, mail.ErrRejected) {
		t.Fatalf("the failed send returned %v, want one wrapping ErrRejected", err)
	}
	failure := out.String()
	for _, want := range []string{"outcome=rejected", "cause=", "duration_ms="} {
		if !strings.Contains(failure, want) {
			t.Errorf("the failure line lacks %q: %s", want, failure)
		}
	}
	for _, leak := range []string{testCode, address} {
		if strings.Contains(failure, leak) {
			t.Errorf("the failure line carries %q: %s", leak, failure)
		}
	}
}

// TestTheDescriptorNamesWhatItDeliversAndRequires holds the shape a command
// tree relies on: the module is authn.emailcode, it delivers the email channel
// exclusively, it requires the mail sending capability, and it declares its
// catalog with no configuration item of its own.
func TestTheDescriptorNamesWhatItDeliversAndRequires(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.emailcode" {
		t.Errorf("module name = %q (constant %q), want authn.emailcode", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want exactly the email channel", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.EmailCodeSender)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.EmailCodeSender exclusively", p)
	}
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the mail sending capability alone", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*mail.Sender)(nil)) || r.Optional {
		t.Errorf("Requires[0] = %+v, want mail.Sender as a required dependency", r)
	}
	var catalogs int
	for _, res := range m.Resources {
		switch res.(type) {
		case i18n.Catalog:
			catalogs++
		case config.Schema:
			t.Errorf("the module declares a configuration schema, want none")
		}
	}
	if catalogs != 1 {
		t.Errorf("Resources carries %d i18n.Catalog values, want one", catalogs)
	}
	got, err := m.Prepare(t.Context(), core.New())
	if err != nil || got.State != core.StateEnabled {
		t.Errorf("Prepare = %+v, %v; want StateEnabled and no error", got, err)
	}
}

// TestTheCatalogCarriesEveryMessageInBothLanguages holds the compiled-in
// catalog: zh-CN and en-US, each with the subject and both bodies, each body
// interpolating the code and its lifetime, and no message left empty.
func TestTheCatalogCarriesEveryMessageInBothLanguages(t *testing.T) {
	locales, err := catalogLocales()
	if err != nil {
		t.Fatalf("listing the catalog: %v", err)
	}
	want := []i18n.Locale{"en-US", "zh-CN"}
	if len(locales) != len(want) || locales[0] != want[0] || locales[1] != want[1] {
		t.Fatalf("the catalog carries %v, want %v", locales, want)
	}
	for _, locale := range locales {
		entries := readCatalog(t, locale)
		for _, key := range messageKeys {
			text, ok := entries[string(key)]
			if !ok || strings.TrimSpace(text) == "" {
				t.Errorf("%s carries no %q", locale, key)
				continue
			}
			if key != subjectKey {
				for _, param := range []string{"{{code}}", "{{ttl_minutes}}"} {
					if !strings.Contains(text, param) {
						t.Errorf("%s %q does not interpolate %s", locale, key, param)
					}
				}
			}
		}
	}
}

// TestSendCodeRendersInTheRequestedLanguage holds the whole send: the message
// reaches the carrier addressed to the code's holder, its subject and both
// bodies are the catalog's text with the code and the lifetime interpolated, and
// the two languages differ so a case that always answered one could not pass.
func TestSendCodeRendersInTheRequestedLanguage(t *testing.T) {
	sender := &recordingSender{}
	product, err := driveEmailcode(t, sender)
	if err != nil {
		t.Fatalf("assembling the module: %v", err)
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		if err := product.SendCode(t.Context(), address, testCode, 10*time.Minute, locale); err != nil {
			t.Fatalf("SendCode(%s): %v", locale, err)
		}
	}
	sent := sender.sent()
	if len(sent) != 2 {
		t.Fatalf("the carrier received %d message(s), want two", len(sent))
	}
	for i, locale := range []string{"zh-CN", "en-US"} {
		entries := readCatalog(t, i18n.Locale(locale))
		args := map[string]string{"code": testCode, "ttl_minutes": "10"}
		got := sent[i]
		if len(got.To) != 1 || got.To[0] != address {
			t.Errorf("%s: To = %v, want [%s]", locale, got.To, address)
		}
		if want := interpolate(entries[string(subjectKey)], args); got.Subject != want {
			t.Errorf("%s: Subject = %q, want %q", locale, got.Subject, want)
		}
		if want := interpolate(entries[string(textKey)], args); got.Text != want {
			t.Errorf("%s: Text = %q, want %q", locale, got.Text, want)
		}
		if want := interpolate(entries[string(htmlKey)], args); got.HTML != want {
			t.Errorf("%s: HTML = %q, want %q", locale, got.HTML, want)
		}
	}
	if sent[0].Subject == sent[1].Subject {
		t.Errorf("both languages rendered the same subject %q", sent[0].Subject)
	}
}

// TestAnUnknownLocaleFallsBackToEnglish holds the fallback order: a language the
// catalog does not carry, an English region it does not carry, a Chinese region
// it does not carry, and no locale at all each render a message rather than the
// key.
func TestAnUnknownLocaleFallsBackToEnglish(t *testing.T) {
	english := readCatalog(t, "en-US")
	chinese := readCatalog(t, "zh-CN")
	args := map[string]string{"code": testCode, "ttl_minutes": "10"}
	cases := []struct {
		locale string
		from   map[string]string
	}{
		{"fr-FR", english},
		{"", english},
		{"en-GB", english},
		{"zh-HK", chinese},
	}
	for _, tc := range cases {
		sender := &recordingSender{}
		product, err := driveEmailcode(t, sender)
		if err != nil {
			t.Fatalf("%q: assembling the module: %v", tc.locale, err)
		}
		if err := product.SendCode(t.Context(), address, testCode, 10*time.Minute, tc.locale); err != nil {
			t.Fatalf("%q: SendCode: %v", tc.locale, err)
		}
		got := sender.sent()[0]
		if want := interpolate(tc.from[string(subjectKey)], args); got.Subject != want {
			t.Errorf("%q: Subject = %q, want %q", tc.locale, got.Subject, want)
		}
	}
}

// TestTheCarrierErrorPassesThrough holds that each of the mail capability's
// three sentinels reaches the caller as it was classified, and that the error
// text carries no code.
func TestTheCarrierErrorPassesThrough(t *testing.T) {
	for _, sentinel := range []error{mail.ErrThrottled, mail.ErrRejected, mail.ErrUncertain} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			sender := &recordingSender{err: fmt.Errorf("a carrier detail: %w", sentinel)}
			product, err := driveEmailcode(t, sender)
			if err != nil {
				t.Fatalf("assembling the module: %v", err)
			}
			err = product.SendCode(t.Context(), address, testCode, 10*time.Minute, "en-US")
			if !errors.Is(err, sentinel) {
				t.Fatalf("SendCode = %v, want %v", err, sentinel)
			}
			if strings.Contains(err.Error(), testCode) {
				t.Errorf("the error carries the code: %v", err)
			}
		})
	}
}

// TestACatalogMissingAMessageStopsTheStartup holds the guard: a localizer that
// does not carry one of the messages fails the module's construction, naming the
// key and locale and never a code.
func TestACatalogMissingAMessageStopsTheStartup(t *testing.T) {
	partial := &partialLocalizer{have: map[i18n.Locale]map[i18n.Key]string{
		"en-US": {subjectKey: "Your verification code"},
	}}
	reg := core.New()
	reg.Register(Module())
	reg.Register(standIn(mailModule, &recordingSender{}, true, (*mail.Sender)(nil)))
	reg.Register(standIn(localizerModul, partial, true, (*i18n.Localizer)(nil)))
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName, mailModule, localizerModul},
	})
	if err == nil {
		t.Fatal("the module started over a catalog that carries neither site message")
	}
	if !strings.Contains(err.Error(), string(textKey)) && !strings.Contains(err.Error(), string(htmlKey)) {
		t.Errorf("the failure names no missing key: %v", err)
	}
}

// TestAnAssemblyWithNoLocalizerStopsTheStartup holds the other startup failure:
// an assembly that supplies no i18n.Localizer at all cannot render anything, so
// the module's construction fails naming the localizer rather than reporting a
// single missing message.
func TestAnAssemblyWithNoLocalizerStopsTheStartup(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	reg.Register(standIn(mailModule, &recordingSender{}, true, (*mail.Sender)(nil)))
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName, mailModule},
	})
	if err == nil {
		t.Fatal("the module started with no localizer in the assembly")
	}
	if !strings.Contains(err.Error(), "localizer") {
		t.Errorf("the failure does not name the missing localizer: %v", err)
	}
	for _, key := range messageKeys {
		if strings.Contains(err.Error(), string(key)) {
			t.Errorf("the failure names a message key %q, want the missing localizer: %v", key, err)
		}
	}
}

// partialLocalizer carries the messages a case gave it and nothing else.
type partialLocalizer struct {
	have map[i18n.Locale]map[i18n.Key]string
}

func (l *partialLocalizer) Localize(_ context.Context, key i18n.Key, _ i18n.Args) string {
	for _, entries := range l.have {
		if text, ok := entries[key]; ok {
			return text
		}
	}
	return string(key)
}

func (l *partialLocalizer) Translate(locale i18n.Locale, key i18n.Key, _ i18n.Args) (string, bool) {
	text, ok := l.have[locale][key]
	return text, ok
}

// TestTheLifetimeIsRenderedInWholeMinutesRoundedUp holds the unit the messages
// interpolate: a lifetime is whole minutes, rounded up, never below one.
func TestTheLifetimeIsRenderedInWholeMinutesRoundedUp(t *testing.T) {
	for _, tc := range []struct {
		ttl  time.Duration
		want int
	}{
		{10 * time.Minute, 10},
		{90 * time.Second, 2},
		{time.Minute, 1},
		{time.Second, 1},
		{0, 1},
	} {
		if got := ttlMinutes(tc.ttl); got != tc.want {
			t.Errorf("ttlMinutes(%v) = %d, want %d", tc.ttl, got, tc.want)
		}
	}
}

// readCatalog reads one locale's message file out of the module's embedded
// catalog.
func readCatalog(t *testing.T, locale i18n.Locale) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("locales/" + string(locale) + ".json")
	if err != nil {
		t.Fatalf("reading the %s catalog: %v", locale, err)
	}
	var entries map[string]string
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("the %s catalog is not a JSON object of strings: %v", locale, err)
	}
	return entries
}

// interpolate substitutes the two placeholders the way the i18n engine does.
func interpolate(text string, args map[string]string) string {
	for name, value := range args {
		text = strings.ReplaceAll(text, "{{"+name+"}}", value)
	}
	return text
}
