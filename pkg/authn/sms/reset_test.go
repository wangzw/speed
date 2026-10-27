package sms

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/sms"
)

// resetRegistry is the rig's registry with the optional authn.code.reset
// template declared in the same shape the required ones carry.
func resetRegistry() *fakeRegistry {
	registry := newRegistry()
	declared := declaredTemplates()
	declared = append(declared, sms.Template{
		Name:    templateReset,
		Params:  append([]string(nil), templateParams...),
		Locales: append([]string(nil), testLocales...),
	})
	registry.setDeclared(declared)
	return registry
}

// newResetRig is the rig whose carrier declares the reset template, so the reset
// purpose is available.
func newResetRig(t *testing.T) *rig {
	t.Helper()
	return newRigOver(t, openSQLite(t), resetRegistry(), newSender(), testDefaultLocale)
}

// resetTo sends a delivered reset-purpose code for number. It sets no account:
// the reset purpose never has one.
func (r *rig) resetTo(number string) (string, error) {
	return r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeReset,
		Phone:   number,
		Deliver: true,
	})
}

// TestTheResetPurposeHasItsOwnGlobalBudget holds the per-purpose global budget:
// the reset purpose's rows reach authn.sms.global-hourly-reset and refuse the
// next reset, while the login purpose keeps its own budget and still sends.
func TestTheResetPurposeHasItsOwnGlobalBudget(t *testing.T) {
	r := newResetRig(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(globalResetPath, 1)

	if _, err := r.resetTo("+8613800138010"); err != nil {
		t.Fatalf("the first reset send: %v", err)
	}
	r.clock.advance(31 * time.Second)
	_, err := r.resetTo("+8613800138011")
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the second reset send returned %v, want ErrThrottled (the reset budget)", err)
	}
	// The login purpose has its own budget, untouched by the reset one.
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("a login send was refused by the reset budget: %v", err)
	}
}

// TestTheLoginBudgetDoesNotRefuseAReset holds the other side of the split: a
// spent login budget refuses login sends and leaves a reset send alone.
func TestTheLoginBudgetDoesNotRefuseAReset(t *testing.T) {
	r := newResetRig(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(globalLoginPath, 1)

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the first login send: %v", err)
	}
	r.clock.advance(31 * time.Second)
	if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the second login send returned %v, want ErrThrottled (the login budget)", err)
	}
	if _, err := r.resetTo("+8613800138020"); err != nil {
		t.Fatalf("a reset send was refused by the login budget: %v", err)
	}
}

// TestTheResetPurposeSharesTheNumberSourceAndResendBudgets holds the dimensions
// a reset shares with every other purpose: the resend interval, the number's
// hourly count and the source address's hourly count each refuse a reset send
// that reaches them.
func TestTheResetPurposeSharesTheNumberSourceAndResendBudgets(t *testing.T) {
	t.Run("the resend interval", func(t *testing.T) {
		r := newResetRig(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 2*time.Minute)

		if _, err := r.resetTo(phone); err != nil {
			t.Fatalf("the first reset send: %v", err)
		}
		_, err := r.resetTo(phone)
		if !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a resend within the interval returned %v, want ErrThrottled", err)
		}
	})

	t.Run("the number budget", func(t *testing.T) {
		r := newResetRig(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perPhoneHourlyPath, 1)

		if _, err := r.resetTo(phone); err != nil {
			t.Fatalf("the first reset send: %v", err)
		}
		r.clock.advance(31 * time.Second)
		_, err := r.resetTo(phone)
		if !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a second reset within the hour returned %v, want ErrThrottled (the number budget)", err)
		}
	})

	t.Run("the source budget", func(t *testing.T) {
		r := newResetRig(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perSourceHourlyPath, 1)

		source := "203.0.113.9"
		if _, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeReset, Phone: "+8613800138030", Source: source, Deliver: true,
		}); err != nil {
			t.Fatalf("the first reset send: %v", err)
		}
		r.clock.advance(31 * time.Second)
		_, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeReset, Phone: "+8613800138031", Source: source, Deliver: true,
		})
		if !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a second reset from the same source returned %v, want ErrThrottled (the source budget)", err)
		}
	})
}

// TestAResetSendFillsNoAccountAndSkipsTheAccountBudget holds the account
// dimension's absence: a reset row carries no account even when the caller sets
// one, and per-account-hourly does not count reset sends.
func TestAResetSendFillsNoAccountAndSkipsTheAccountBudget(t *testing.T) {
	r := newResetRig(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perAccountHourlyPath, 1)

	ctx := context.Background()
	id, err := r.p.Send(ctx, authn.SendCode{
		Purpose: authn.SMSPurposeReset, Phone: "+8613800138040", Account: acct, Deliver: true,
	})
	if err != nil {
		t.Fatalf("the first reset send: %v", err)
	}
	row, ok, err := r.p.register.read(ctx, id)
	if err != nil || !ok {
		t.Fatalf("reading the reset row: %v (found %v)", err, ok)
	}
	if row.AccountID != nil {
		t.Errorf("the reset row carries the account %q, want none", *row.AccountID)
	}

	// A second reset for another number of the same account is not refused by the
	// account budget: the purpose has no account dimension.
	r.clock.advance(31 * time.Second)
	if _, err := r.p.Send(ctx, authn.SendCode{
		Purpose: authn.SMSPurposeReset, Phone: "+8613800138041", Account: acct, Deliver: true,
	}); err != nil {
		t.Fatalf("the second reset send was refused by the account budget: %v", err)
	}
}

// TestTheResetTemplateIsOptional holds the availability rule: the reset purpose
// is supported exactly when the carrier declares authn.code.reset, and the
// login, mfa and enroll purposes are supported either way. A carrier that
// declares no reset template starts normally.
func TestTheResetTemplateIsOptional(t *testing.T) {
	t.Run("declared", func(t *testing.T) {
		r := newResetRig(t)
		support, ok := any(r.p).(authn.PurposeSupport)
		if !ok {
			t.Fatal("the product does not publish PurposeSupport")
		}
		for _, purpose := range []string{authn.SMSPurposeLogin, authn.SMSPurposeMFA, authn.SMSPurposeEnroll, authn.SMSPurposeReset} {
			if !support.SupportsPurpose(purpose) {
				t.Errorf("SupportsPurpose(%q) = false, want true", purpose)
			}
		}
		if support.SupportsPurpose("unknown") {
			t.Error("SupportsPurpose(unknown) = true, want false")
		}
	})

	t.Run("absent", func(t *testing.T) {
		registry := newRegistry()
		if err := checkSender(registry.Declared(), testDefaultLocale); err != nil {
			t.Fatalf("a carrier with no reset template was refused at startup: %v", err)
		}
		r := newRigOver(t, openSQLite(t), registry, newSender(), testDefaultLocale)
		for _, purpose := range []string{authn.SMSPurposeLogin, authn.SMSPurposeMFA, authn.SMSPurposeEnroll} {
			if !r.p.SupportsPurpose(purpose) {
				t.Errorf("SupportsPurpose(%q) = false, want true", purpose)
			}
		}
		if r.p.SupportsPurpose(authn.SMSPurposeReset) {
			t.Error("SupportsPurpose(reset) = true over a carrier that declares no reset template")
		}
	})
}

// TestACarrierThatDeclaresAResetTemplateItCannotCarryIsRefused holds the shape
// check on the optional template: declaring it obliges the carrier to satisfy
// the same parameters, languages and default-language rule as the required
// templates, and a declaration that does not is a startup failure naming it.
func TestACarrierThatDeclaresAResetTemplateItCannotCarryIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		reset  sms.Template
		detail string
	}{
		{
			name:   "no language",
			reset:  sms.Template{Name: templateReset, Params: append([]string(nil), templateParams...)},
			detail: templateReset,
		},
		{
			name: "without the default language",
			reset: sms.Template{
				Name: templateReset, Params: append([]string(nil), templateParams...), Locales: []string{"fr-FR"},
			},
			detail: testDefaultLocale,
		},
		{
			name: "without a parameter",
			reset: sms.Template{
				Name: templateReset, Params: []string{"code"}, Locales: append([]string(nil), testLocales...),
			},
			detail: "ttl_minutes",
		},
		{
			name: "with a parameter this module never sends",
			reset: sms.Template{
				Name:    templateReset,
				Params:  append(append([]string(nil), templateParams...), "sign"),
				Locales: append([]string(nil), testLocales...),
			},
			detail: "sign",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			declared := append(declaredTemplates(), c.reset)
			err := checkSender(declared, testDefaultLocale)
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("checkSender returned %v, want ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), c.detail) {
				t.Errorf("the failure %v does not name %q", err, c.detail)
			}
		})
	}
}

// TestADeclaredResetTemplateThatCannotBeCarriedStopsTheNewStage holds the
// startup stage, not only the helper: a registry that declares a broken reset
// template fails this module's construction and never reaches its action.
func TestADeclaredResetTemplateThatCannotBeCarriedStopsTheNewStage(t *testing.T) {
	registry := newRegistry()
	declared := declaredTemplates()
	declared = append(declared, sms.Template{
		Name:    templateReset,
		Params:  append([]string(nil), templateParams...),
		Locales: []string{"fr-FR"},
	})
	registry.setDeclared(declared)

	err := assembly{registry: registry, sender: newSender(), migrate: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action over a reset template the carrier cannot carry")
		return nil
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("the drive returned %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), templateReset) {
		t.Errorf("the failure %v does not name the reset template", err)
	}
}

// TestAResetSendCarriesTheResetTemplate holds the purpose mapping on the send
// path: a reset message is resolved under authn.code.reset with the two
// parameters the template declares.
func TestAResetSendCarriesTheResetTemplate(t *testing.T) {
	r := newResetRig(t)
	r.allow()

	id, err := r.resetTo(phone)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := len(r.p.pool.queue); got != 1 {
		t.Fatalf("the queue holds %d messages, want 1", got)
	}
	msg := <-r.p.pool.queue
	if msg.To != phone || msg.Template != templateReset || msg.DedupeID != id {
		t.Errorf("queued message = %+v, want To %s, Template %s, DedupeID %s",
			msg, phone, templateReset, id)
	}
	if msg.Params["code"] == "" || msg.Params["ttl_minutes"] == "" {
		t.Errorf("the reset message carries the parameters %v, want a code and its lifetime", msg.Params)
	}
}
