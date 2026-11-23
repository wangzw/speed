package main

// The SMS carrier, tested where it is compiled. A normal deployment's carriers
// answer a per-template review they do not have; this one answers a real
// per-language review from its configuration, so the template registry starts a
// runtime template pending in every language and a language's verdict can come
// from sms.log.approvals.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/sms"
	smslog "github.com/vislake/speed/pkg/sms/log"
	smsregistry "github.com/vislake/speed/pkg/sms/registry"
)

// TestE2ESMSDeclaresPerTemplateApprovalAndStartsPending holds the carrier's
// declaration and its initial verdict: the vendor reviews each template, and a
// code the carrier has not been told about answers pending — the state the
// registry records for a language a runtime template was registered in.
func TestE2ESMSDeclaresPerTemplateApprovalAndStartsPending(t *testing.T) {
	t.Parallel()
	carrier := &smslog.Product{}
	if !carrier.Model().Review.PerTemplate {
		t.Fatalf("the carrier declares PerTemplate %t, want true: the template registry "+
			"would otherwise register every language as not-required",
			carrier.Model().Review.PerTemplate)
	}
	for _, code := range []string{"rollout-en-US", "rollout-zh-CN"} {
		got, err := carrier.Approval(t.Context(), code)
		if err != nil {
			t.Fatalf("Approval(%q): %v", code, err)
		}
		if got.State != sms.ApprovalPending {
			t.Errorf("Approval(%q).State = %q, want %q", code, got.State, sms.ApprovalPending)
		}
	}
}

// TestE2ESMSTemplateStartsPendingAndSettlesOneLanguage drives the real
// template registry over the log SMS carrier: a runtime template registered with
// a code per language starts pending in both, the control moves one language's
// verdict and the poller records it there alone, and disabling the template
// leaves both languages' review states reported.
func TestE2ESMSTemplateStartsPendingAndSettlesOneLanguage(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "sms-test.db")
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"sms":{"log":{"approvals":{`+
		`"rollout-en-US":{"state":"approved","reason":"pinned"}}},`+
		`"registry":{"poll-interval":"10ms"}}}`, dsn)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), smslog.Module(), smsregistry.Module())

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(smslog.Module())
	reg.Register(smsregistry.Module())
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageInit,
		Participants: []string{
			sqlite.ModuleName,
			smslog.ModuleName,
			smsregistry.ModuleName,
		},
		Action: func(ctx context.Context, reg *core.Registry) error {
			templates, err := core.Resolve[sms.Registry](reg)
			if err != nil {
				return fmt.Errorf("taking up the template registry: %w", err)
			}
			record, err := templates.Register(ctx, "rollout", sms.TemplateSpec{
				Kind:   sms.KindNotification,
				Params: []string{"code"},
				Codes: map[string]string{
					"en-US": "rollout-en-US",
					"zh-CN": "rollout-zh-CN",
				},
			})
			if err != nil {
				return fmt.Errorf("registering the template: %w", err)
			}
			for _, locale := range []string{"en-US", "zh-CN"} {
				if got := record.Approvals[locale].State; got != sms.ApprovalPending {
					return fmt.Errorf("the template starts %q in %s, want pending", got, locale)
				}
			}

			deadline := time.Now().Add(coretest.Scale(5 * time.Second))
			for {
				records, listErr := templates.List(ctx)
				if listErr != nil {
					return fmt.Errorf("listing the templates: %w", listErr)
				}
				listed, ok := recordNamed(records, "rollout")
				if !ok {
					return fmt.Errorf("the registered template is not in the list")
				}
				if listed.Approvals["en-US"].State == sms.ApprovalApproved {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("the poller had not recorded the English verdict: %+v", listed.Approvals)
				}
				time.Sleep(5 * time.Millisecond)
			}

			records, err := templates.List(ctx)
			if err != nil {
				return fmt.Errorf("listing the templates: %w", err)
			}
			listed, ok := recordNamed(records, "rollout")
			if !ok {
				return fmt.Errorf("the registered template is not in the list")
			}
			if got := listed.Approvals["en-US"].State; got != sms.ApprovalApproved {
				return fmt.Errorf("the English verdict became %q, want approved", got)
			}
			if got := listed.Approvals["zh-CN"].State; got != sms.ApprovalPending {
				return fmt.Errorf("the Chinese verdict became %q, want it left pending", got)
			}
			if !listed.Enabled {
				return fmt.Errorf("the template is disabled before the case disables it")
			}

			if enableErr := templates.SetEnabled(ctx, "rollout", false); enableErr != nil {
				return fmt.Errorf("disabling the template: %w", enableErr)
			}
			records, err = templates.List(ctx)
			if err != nil {
				return fmt.Errorf("listing the templates after disabling: %w", err)
			}
			listed, ok = recordNamed(records, "rollout")
			if !ok {
				return fmt.Errorf("the disabled template is not in the list")
			}
			if listed.Enabled {
				return fmt.Errorf("the template still reports enabled after disabling")
			}
			if got := listed.Approvals["en-US"].State; got != sms.ApprovalApproved {
				return fmt.Errorf("disabling changed the English verdict to %q", got)
			}
			if got := listed.Approvals["zh-CN"].State; got != sms.ApprovalPending {
				return fmt.Errorf("disabling changed the Chinese verdict to %q", got)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestE2ECarrierNamesTheMessageKindItSent holds the carrier's log record:
// a notice says a message was sent and carries no verification_code, while a
// verification message keeps the verification-code line and its code. The two
// kinds travel through one carrier, so the declaration's kind is what keeps a
// reader from reading a notice as a code.
func TestE2ECarrierNamesTheMessageKindItSent(t *testing.T) {
	t.Parallel()
	notice := sms.Resolved{
		To:       e2eSMSTestPhone,
		Template: demoNotificationSMSTemplate,
		Locale:   e2eDefaultLocale,
		Kind:     sms.KindNotification,
		Params:   map[string]string{"to": e2eSMSTestPhone},
	}
	msg, attrs := smslog.Record(notice)
	if strings.Contains(msg, "verification") {
		t.Errorf("the notice line is %q; a notification must not claim to be a verification code", msg)
	}
	if value, ok := e2eSMSAttr(attrs, "verification_code"); ok {
		t.Errorf("the notice line carries verification_code=%q; a notification carries no code", value)
	}
	for _, key := range []string{"to", "template", "locale"} {
		if _, ok := e2eSMSAttr(attrs, key); !ok {
			t.Errorf("the notice line carries no %q attribute: %v", key, attrs)
		}
	}

	verification := sms.Resolved{
		To:       e2eSMSTestPhone,
		Template: "authn.code.login",
		Locale:   e2eDefaultLocale,
		Kind:     sms.KindVerification,
		Params:   map[string]string{"code": "123456", "ttl_minutes": "5"},
	}
	msg, attrs = smslog.Record(verification)
	if want := "log sms: a verification code was sent"; msg != want {
		t.Errorf("the verification line is %q, want %q", msg, want)
	}
	if value, ok := e2eSMSAttr(attrs, "verification_code"); !ok || value != "123456" {
		t.Errorf("the verification line carries verification_code=%q (present %t), want %q",
			value, ok, "123456")
	}
}

// e2eSMSAttr reads one string attribute out of the pair list a log record is
// built from.
func e2eSMSAttr(attrs []any, key string) (string, bool) {
	for i := 0; i+1 < len(attrs); i += 2 {
		if name, ok := attrs[i].(string); ok && name == key {
			value, ok := attrs[i+1].(string)
			return value, ok
		}
	}
	return "", false
}

// recordNamed answers one record from a list by name.
func recordNamed(records []sms.Record, name string) (sms.Record, bool) {
	for _, record := range records {
		if record.Name == name {
			return record, true
		}
	}
	return sms.Record{}, false
}
