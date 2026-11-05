package invitemail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
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
	maillog "github.com/vislake/speed/pkg/mail/log"
)

const (
	mailModule = "stand-in.mail"

	tenant    = "acme"
	address   = "ada@example.com"
	testToken = "spd_inv_0123456789abcdef"

	baseURL        = "https://app.example.com"
	configuredBody = `{"authn":{"invitemail":{"accept-base-url":"https://app.example.com/"}}}`
)

var expiresAt = time.Date(2026, 11, 12, 17, 30, 0, 0, time.UTC)

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

// standIn is a module that delivers instance from its New.
func standIn(name string, instance any, exclusive bool, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, token := range tokens {
		provides[i] = core.Provision{Token: token, Exclusive: exclusive}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// driveInviteMail builds an assembly of this module, the i18n engine, the
// configuration reader and the mail stand-in, and answers the notifier it
// delivered.
func driveInviteMail(t *testing.T, body string, sender mail.Sender) (authn.InviteNotifier, error) {
	t.Helper()
	reader := authntest.ConfigReaderAll(t, body, Module(), catalog.Module())
	reg := core.New()
	reg.Register(Module())
	reg.Register(catalog.Module())
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(standIn(mailModule, sender, true, (*mail.Sender)(nil)))

	var got authn.InviteNotifier
	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageNew,
		Participants: []string{
			ModuleName, catalog.ModuleName, config.ModuleName, mailModule,
		},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			got, err = core.Resolve[authn.InviteNotifier](reg)
			return err
		},
	})
	return got, err
}

// TestTheDescriptorNamesWhatItDeliversAndRequires holds the shape a command
// tree relies on: the module is authn.invitemail, it delivers the invitation
// notifier exclusively, it requires the mail sending capability, and it
// declares both its configuration section and its catalog.
func TestTheDescriptorNamesWhatItDeliversAndRequires(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.invitemail" {
		t.Errorf("module name = %q (constant %q), want authn.invitemail", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the invitation notifier alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.InviteNotifier)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.InviteNotifier exclusively", p)
	}
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the mail sending capability alone", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*mail.Sender)(nil)) || r.Optional {
		t.Errorf("Requires[0] = %+v, want mail.Sender as a required dependency", r)
	}
	var schemas, catalogs int
	for _, res := range m.Resources {
		switch res.(type) {
		case config.Schema:
			schemas++
		case i18n.Catalog:
			catalogs++
		}
	}
	if schemas != 1 || catalogs != 1 {
		t.Errorf("Resources carries %d config.Schema and %d i18n.Catalog values, want one of each",
			schemas, catalogs)
	}
}

// TestAnEmptyBaseURLReportsNotConfigured holds the deployment that carries the
// module but sends no mail: construction succeeds, the carrier is handed
// nothing, and the outcome is not_configured rather than a failure.
func TestAnEmptyBaseURLReportsNotConfigured(t *testing.T) {
	sender := &recordingSender{}
	notifier, err := driveInviteMail(t, "{}", sender)
	if err != nil {
		t.Fatalf("assembling the module over an empty configuration: %v", err)
	}
	delivery, err := notifier.Notify(t.Context(), authn.InviteNotice{
		Tenant: tenant, Email: address, Token: testToken, ExpiresAt: expiresAt, Locale: "en-US",
	})
	if err != nil {
		t.Fatalf("Notify = %v, want no error for an unconfigured channel", err)
	}
	if delivery != authn.InviteNotConfigured {
		t.Errorf("Notify = %v, want InviteNotConfigured", delivery)
	}
	if got := sender.sent(); len(got) != 0 {
		t.Errorf("the carrier received %d message(s), want none", len(got))
	}
}

// TestTheConfiguredBaseURLIsTheSiteRoot holds the link the notice carries: the
// configured origin plus the public acceptance landing the demo host serves at
// /invitations/:token. A trailing slash on the configuration is normalized, so
// the link never carries a doubled slash.
func TestTheConfiguredBaseURLIsTheSiteRoot(t *testing.T) {
	sender := &recordingSender{}
	notifier, err := driveInviteMail(t, configuredBody, sender)
	if err != nil {
		t.Fatalf("assembling the module: %v", err)
	}
	if _, err := notifier.Notify(t.Context(), authn.InviteNotice{
		Tenant: tenant, Email: address, Token: testToken, ExpiresAt: expiresAt, Locale: "en-US",
	}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	sent := sender.sent()
	if len(sent) != 1 {
		t.Fatalf("the carrier received %d message(s), want one", len(sent))
	}
	want := baseURL + "/invitations/" + testToken
	for name, body := range map[string]string{"text": sent[0].Text, "html": sent[0].HTML} {
		if !strings.Contains(body, want) {
			t.Errorf("the %s body does not carry the acceptance link %q: %s", name, want, body)
		}
		if strings.Contains(body, "//invitations/") {
			t.Errorf("the %s body carries a doubled slash: %s", name, body)
		}
	}
}

// TestConfigRejectsARelativeOrNonHTTPURL holds the startup domain: only an
// absolute http or https URL can have the landing path joined to it.
func TestConfigRejectsARelativeOrNonHTTPURL(t *testing.T) {
	for _, value := range []string{
		"app.example.com",
		"/invitations",
		"//app.example.com",
		"ftp://app.example.com",
		"http://",
		"mailto:ops@example.com",
		"https://app.example.com/?next=/invitations",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := (moduleConfig{AcceptBaseURL: value}).resolve()
			if err == nil {
				t.Fatalf("resolve(%q) accepted a value it cannot join a link to", value)
			}
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Errorf("resolve(%q) = %v, want one wrapping authn.ErrInvalidConfig", value, err)
			}
			if !strings.Contains(err.Error(), acceptBaseURLKey) {
				t.Errorf("the failure does not name %q: %v", acceptBaseURLKey, err)
			}
			if strings.Contains(err.Error(), value) && value != "" {
				t.Errorf("the failure repeats the refused value %q: %v", value, err)
			}
		})
	}
}

// TestConfigNormalizesATrailingSlash holds the canonical form the join needs.
func TestConfigNormalizesATrailingSlash(t *testing.T) {
	for value, want := range map[string]string{
		"https://app.example.com":      "https://app.example.com",
		"https://app.example.com/":     "https://app.example.com",
		"https://app.example.com/sub":  "https://app.example.com/sub",
		"https://app.example.com/sub/": "https://app.example.com/sub",
		"http://127.0.0.1:8080/":       "http://127.0.0.1:8080",
	} {
		got, err := (moduleConfig{AcceptBaseURL: value}).resolve()
		if err != nil {
			t.Errorf("resolve(%q): %v", value, err)
			continue
		}
		if got.acceptBaseURL != want {
			t.Errorf("resolve(%q) = %q, want %q", value, got.acceptBaseURL, want)
		}
	}
}

// TestStartupRefusesAConfigItCannotJoinLinksWith holds the same validation at
// the descriptor's Prepare stage: a relative URL stops the construction instead
// of reaching the first invitation.
func TestStartupRefusesAConfigItCannotJoinLinksWith(t *testing.T) {
	for _, value := range []string{"app.example.com", "/invitations", "ftp://app.example.com"} {
		t.Run(value, func(t *testing.T) {
			body := fmt.Sprintf(`{"authn":{"invitemail":{"accept-base-url":%q}}}`, value)
			_, err := driveInviteMail(t, body, &recordingSender{})
			if err == nil {
				t.Fatalf("the module started over %q", value)
			}
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Errorf("the startup failure = %v, want one wrapping authn.ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), acceptBaseURLKey) {
				t.Errorf("the startup failure does not name %q: %v", acceptBaseURLKey, err)
			}
		})
	}
}

// TestNoCarrierStandsTheModuleDown holds the documented deployment that switches
// the mail capability off: mail.carrier "none" leaves no sending provider in
// the run, so this module has to stand itself down with the capability instead
// of failing the assembly over a dependency no module delivers.
func TestNoCarrierStandsTheModuleDown(t *testing.T) {
	reader := authntest.ConfigReaderAll(t, `{"mail":{"carrier":"none"}}`, Module(), catalog.Module(), maillog.Module())
	reg := core.New()
	reg.Register(Module())
	reg.Register(catalog.Module())
	reg.Register(config.Module())
	reg.Register(maillog.Module())
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName, catalog.ModuleName, config.ModuleName, maillog.ModuleName},
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if err != nil {
		t.Fatalf("assembling the module with mail.carrier \"none\": %v", err)
	}
	state, ok := reg.Enablement(ModuleName)
	if !ok {
		t.Fatalf("the assembly reports no enablement for %q", ModuleName)
	}
	if state.State != core.StateDisabled {
		t.Errorf("%s is %v, want StateDisabled when the mail capability is off", ModuleName, state.State)
	}
	if !strings.Contains(state.Reason, `"none"`) {
		t.Errorf("the disabled reason does not name the selection: %q", state.Reason)
	}
}

// TestTheNoticeRendersInBothLanguages holds the whole send: the message reaches
// the carrier addressed to the invitee, its subject and both bodies are the
// catalog's text interpolated with the tenant, the acceptance link and the
// deadline, and the two languages differ so a case that always answered one
// could not pass.
func TestTheNoticeRendersInBothLanguages(t *testing.T) {
	sender := &recordingSender{}
	notifier, err := driveInviteMail(t, configuredBody, sender)
	if err != nil {
		t.Fatalf("assembling the module: %v", err)
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		delivery, err := notifier.Notify(t.Context(), authn.InviteNotice{
			Tenant: tenant, Email: address, Token: testToken, ExpiresAt: expiresAt, Locale: locale,
		})
		if err != nil {
			t.Fatalf("Notify(%s): %v", locale, err)
		}
		if delivery != authn.InviteSent {
			t.Errorf("Notify(%s) = %v, want InviteSent", locale, delivery)
		}
	}
	sent := sender.sent()
	if len(sent) != 2 {
		t.Fatalf("the carrier received %d message(s), want two", len(sent))
	}
	link := baseURL + "/invitations/" + testToken
	deadline := formatExpiry(expiresAt)
	args := map[string]string{
		"tenant":     tenant,
		"accept_url": link,
		"expires_at": deadline,
	}
	for i, locale := range []string{"zh-CN", "en-US"} {
		entries := readCatalog(t, i18n.Locale(locale))
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
		if !strings.Contains(got.Subject, tenant) {
			t.Errorf("%s: the subject does not carry the tenant %q: %s", locale, tenant, got.Subject)
		}
		for name, body := range map[string]string{"text": got.Text, "html": got.HTML} {
			for _, want := range []string{tenant, link, deadline} {
				if !strings.Contains(body, want) {
					t.Errorf("%s: the %s does not carry %q: %s", locale, name, want, body)
				}
			}
		}
	}
	if sent[0].Subject == sent[1].Subject {
		t.Errorf("both languages rendered the same subject %q", sent[0].Subject)
	}
}

// TestTheCarrierFailureIsReportedAsFailed holds the failure half of the seam:
// a refusal is failed with the carrier's error, and the error carries no token.
func TestTheCarrierFailureIsReportedAsFailed(t *testing.T) {
	sender := &recordingSender{err: fmt.Errorf("a carrier detail: %w", mail.ErrRejected)}
	notifier, err := driveInviteMail(t, configuredBody, sender)
	if err != nil {
		t.Fatalf("assembling the module: %v", err)
	}
	delivery, err := notifier.Notify(t.Context(), authn.InviteNotice{
		Tenant: tenant, Email: address, Token: testToken, ExpiresAt: expiresAt, Locale: "en-US",
	})
	if delivery != authn.InviteFailed {
		t.Errorf("Notify = %v, want InviteFailed", delivery)
	}
	if !errors.Is(err, mail.ErrRejected) {
		t.Fatalf("Notify error = %v, want one wrapping mail.ErrRejected", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("the error carries the token: %v", err)
	}
	if len(sender.sent()) != 1 {
		t.Errorf("the carrier was handed %d message(s), want the one it refused", len(sender.sent()))
	}
}

// TestAnUnknownLocaleFallsBack holds the fallback order: a language the catalog
// does not carry, an English region it does not carry, a Chinese region it does
// not carry, and no locale at all each render a message rather than the key.
func TestAnUnknownLocaleFallsBack(t *testing.T) {
	cases := []struct {
		locale string
		from   i18n.Locale
	}{
		{"fr-FR", "en-US"},
		{"", "en-US"},
		{"en-GB", "en-US"},
		{"zh-HK", "zh-CN"},
	}
	link := baseURL + "/invitations/" + testToken
	args := map[string]string{
		"tenant":     tenant,
		"accept_url": link,
		"expires_at": formatExpiry(expiresAt),
	}
	for _, tc := range cases {
		sender := &recordingSender{}
		notifier, err := driveInviteMail(t, configuredBody, sender)
		if err != nil {
			t.Fatalf("%q: assembling the module: %v", tc.locale, err)
		}
		if _, err := notifier.Notify(t.Context(), authn.InviteNotice{
			Tenant: tenant, Email: address, Token: testToken, ExpiresAt: expiresAt, Locale: tc.locale,
		}); err != nil {
			t.Fatalf("%q: Notify: %v", tc.locale, err)
		}
		entries := readCatalog(t, tc.from)
		got := sender.sent()[0]
		if want := interpolate(entries[string(subjectKey)], args); got.Subject != want {
			t.Errorf("%q: Subject = %q, want %q", tc.locale, got.Subject, want)
		}
	}
}

// TestTheNoticeLeavesNoTokenAddressOrBodyInTheLog holds the seam's promise: the
// one outcome line names the carrier's receipt and the duration, and carries
// neither the acceptance token nor the recipient.
func TestTheNoticeLeavesNoTokenAddressOrBodyInTheLog(t *testing.T) {
	var out bytes.Buffer
	ctx := log.WithLogger(context.Background(),
		slog.New(slog.NewTextHandler(&out, nil)))

	sender := &recordingSender{}
	notifier, err := driveInviteMail(t, configuredBody, sender)
	if err != nil {
		t.Fatalf("assembling the module: %v", err)
	}
	if _, err := notifier.Notify(ctx, authn.InviteNotice{
		Tenant: tenant, Email: address, Token: testToken, ExpiresAt: expiresAt, Locale: "en-US",
	}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	success := out.String()
	for _, want := range []string{"outcome=sent", "provider_id=recording", "duration_ms="} {
		if !strings.Contains(success, want) {
			t.Errorf("the success line lacks %q: %s", want, success)
		}
	}
	for _, leak := range []string{testToken, address, tenant} {
		if strings.Contains(success, leak) {
			t.Errorf("the success line carries %q: %s", leak, success)
		}
	}

	out.Reset()
	sender.err = fmt.Errorf("%w: the relay refused it", mail.ErrRejected)
	if _, err := notifier.Notify(ctx, authn.InviteNotice{
		Tenant: tenant, Email: address, Token: testToken, ExpiresAt: expiresAt, Locale: "en-US",
	}); err == nil {
		t.Fatal("a refused notice reported no error")
	}
	failure := out.String()
	for _, want := range []string{"outcome=rejected", "cause=", "duration_ms="} {
		if !strings.Contains(failure, want) {
			t.Errorf("the failure line lacks %q: %s", want, failure)
		}
	}
	for _, leak := range []string{testToken, address, tenant} {
		if strings.Contains(failure, leak) {
			t.Errorf("the failure line carries %q: %s", leak, failure)
		}
	}
}

// TestTheCatalogCarriesTheSameLeavesInBothLanguages holds the compiled-in
// catalog: zh-CN and en-US carry exactly the same keys, every message is
// non-empty, and both bodies interpolate the tenant, the link and the deadline.
func TestTheCatalogCarriesTheSameLeavesInBothLanguages(t *testing.T) {
	zh := readCatalog(t, "zh-CN")
	en := readCatalog(t, "en-US")
	if len(zh) != len(messageKeys) {
		t.Errorf("zh-CN carries %d leaves, want %d", len(zh), len(messageKeys))
	}
	for key := range zh {
		if _, ok := en[key]; !ok {
			t.Errorf("en-US lacks %q, which zh-CN carries", key)
		}
	}
	for key := range en {
		if _, ok := zh[key]; !ok {
			t.Errorf("zh-CN lacks %q, which en-US carries", key)
		}
	}
	for _, locale := range []i18n.Locale{"zh-CN", "en-US"} {
		entries := readCatalog(t, locale)
		for _, key := range messageKeys {
			text, ok := entries[string(key)]
			if !ok || strings.TrimSpace(text) == "" {
				t.Errorf("%s carries no %q", locale, key)
				continue
			}
			if key == subjectKey {
				continue
			}
			for _, param := range []string{"{{tenant}}", "{{accept_url}}", "{{expires_at}}"} {
				if !strings.Contains(text, param) {
					t.Errorf("%s %q does not interpolate %s", locale, key, param)
				}
			}
		}
	}
}

func readCatalog(t *testing.T, locale i18n.Locale) map[string]string {
	t.Helper()
	raw, err := fs.ReadFile(localesFS, path.Join("locales", string(locale)+".json"))
	if err != nil {
		t.Fatalf("reading the %s catalog: %v", locale, err)
	}
	entries := map[string]string{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decoding the %s catalog: %v", locale, err)
	}
	return entries
}

func interpolate(text string, args map[string]string) string {
	pairs := make([]string, 0, len(args)*2)
	for key, value := range args {
		pairs = append(pairs, "{{"+key+"}}", value)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
