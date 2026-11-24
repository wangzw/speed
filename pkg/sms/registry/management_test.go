package registry

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/sms"
)

// TestRegisterWritesAndPublishes checks that a registration writes the row
// pending and enabled, returns the spec and publishes a change event.
func TestRegisterWritesAndPublishes(t *testing.T) {
	r := newSQLiteRig(t)
	rec, err := r.s.Register(context.Background(), "otp",
		spec([]string{"code", "ttl"}, map[string]string{"en": "en-code", "zh": "zh-code"}))
	if err != nil {
		t.Fatalf("registering: %v", err)
	}
	if rec.Name != "otp" || rec.Source != sms.SourceRuntime || rec.Approvals["en"].State != sms.ApprovalPending ||
		!rec.Enabled || rec.Version != 1 {
		t.Errorf("the registered record reads as %+v", rec)
	}
	if got := r.approvalOf("otp", "en"); got != string(sms.ApprovalPending) {
		t.Errorf("the row's review is %q, want pending", got)
	}
	if r.rowCount() != 1 {
		t.Errorf("the table holds %d rows, want 1", r.rowCount())
	}
	if _, err := r.s.Resolve(sms.Message{
		Template: "otp", Locale: "en",
		Params: map[string]string{"code": "x", "ttl": "y"},
	}); !errors.Is(err, sms.ErrNotSendable) {
		t.Errorf("a freshly registered template resolved with %v, want ErrNotSendable", err)
	}
	if len(r.bus.events()) != 1 {
		t.Errorf("the bus published %d events, want 1", len(r.bus.events()))
	}
}

// TestRegisterRefusesConfigurationName checks that a configured name is ErrImmutable.
func TestRegisterRefusesConfigurationName(t *testing.T) {
	r := configuredRig(t)
	if _, err := r.s.Register(context.Background(), "welcome",
		spec([]string{"n"}, map[string]string{"en": "c"})); !errors.Is(err, sms.ErrImmutable) {
		t.Errorf("registering a configured name returned %v, want ErrImmutable", err)
	}
}

// TestRegisterRefusesDuplicateName checks ErrTemplateExists and that no duplicate row is appended.
func TestRegisterRefusesDuplicateName(t *testing.T) {
	forEachDialect(t, func(t *testing.T, database db.Database, _ string) {
		r := newRig(t, database, newProvider(), settingsOf(t, nil, 0))
		r.load()
		if _, err := r.s.Register(context.Background(), "otp",
			spec([]string{"code"}, map[string]string{"en": "c"})); err != nil {
			t.Fatalf("registering: %v", err)
		}
		if _, err := r.s.Register(context.Background(), "otp",
			spec([]string{"code"}, map[string]string{"en": "c"})); !errors.Is(err, sms.ErrTemplateExists) {
			t.Errorf("a second registration returned %v, want ErrTemplateExists", err)
		}
		if r.rowCount() != 1 {
			t.Errorf("the table holds %d rows, want 1", r.rowCount())
		}
	})
}

// TestRegisterValidatesSpec checks that a no-language spec, an empty code and an
// undeclared placeholder are each refused and nothing is written.
func TestRegisterValidatesSpec(t *testing.T) {
	r := newSQLiteRig(t)
	if _, err := r.s.Register(context.Background(), "bad", sms.TemplateSpec{}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Errorf("a spec with no language returned %v, want ErrInvalidConfig", err)
	}
	if _, err := r.s.Register(context.Background(), "bad",
		sms.TemplateSpec{Params: []string{"a"}, Codes: map[string]string{"en": ""}}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Errorf("a spec with an empty code returned %v, want ErrInvalidConfig", err)
	}
	if _, err := r.s.Register(context.Background(), "bad",
		sms.TemplateSpec{Params: []string{"a"}, Bodies: map[string]string{"en": "hi ${b}"}}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Errorf("a body naming an undeclared placeholder returned %v, want ErrInvalidConfig", err)
	}
	if r.rowCount() != 0 {
		t.Errorf("a refused registration wrote %d rows", r.rowCount())
	}
}

// TestSetEnabledChangesTheState checks that an effective enable or disable bumps the version, flips the snapshot and publishes.
func TestSetEnabledChangesTheState(t *testing.T) {
	r := newSQLiteRig(t)
	if _, err := r.s.Register(context.Background(), "otp", spec([]string{"code"}, map[string]string{"en": "c"})); err != nil {
		t.Fatalf("registering: %v", err)
	}
	if err := r.s.SetEnabled(context.Background(), "otp", false); err != nil {
		t.Fatalf("disabling: %v", err)
	}
	if r.versionOf("otp") != 2 {
		t.Errorf("the version after disabling is %d, want 2", r.versionOf("otp"))
	}
	if r.s.snapshot.Load().byName["otp"].enabled {
		t.Errorf("the snapshot still holds otp enabled")
	}
	if len(r.bus.events()) != 2 {
		t.Errorf("the bus published %d events, want 2", len(r.bus.events()))
	}
}

// TestSetEnabledIsIdempotent checks that setting the current state changes nothing and publishes nothing.
func TestSetEnabledIsIdempotent(t *testing.T) {
	r := newSQLiteRig(t)
	if _, err := r.s.Register(context.Background(), "otp", spec([]string{"code"}, map[string]string{"en": "c"})); err != nil {
		t.Fatalf("registering: %v", err)
	}
	if err := r.s.SetEnabled(context.Background(), "otp", true); err != nil {
		t.Fatalf("re-enabling: %v", err)
	}
	if r.versionOf("otp") != 1 {
		t.Errorf("the version after an idempotent call is %d, want 1", r.versionOf("otp"))
	}
	if len(r.bus.events()) != 1 {
		t.Errorf("the bus published %d events, want 1 (the registration only)", len(r.bus.events()))
	}
}

// TestSetEnabledRefusesImmutableAndMissing checks the two refusals a management call makes.
func TestSetEnabledRefusesImmutableAndMissing(t *testing.T) {
	r := configuredRig(t)
	if err := r.s.SetEnabled(context.Background(), "welcome", false); !errors.Is(err, sms.ErrImmutable) {
		t.Errorf("enabling a configured template returned %v, want ErrImmutable", err)
	}
	if err := r.s.SetEnabled(context.Background(), "ghost", false); !errors.Is(err, sms.ErrNoTemplate) {
		t.Errorf("enabling an unknown template returned %v, want ErrNoTemplate", err)
	}
}

// TestRemoveDropsAndPublishes checks that a removal deletes the row, publishes a
// deleted event and drops the template from the set in force.
func TestRemoveDropsAndPublishes(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("otp", []string{"code"}, map[string]string{"en": "c"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.load()
	if err := r.s.Remove(context.Background(), "otp"); err != nil {
		t.Fatalf("removing: %v", err)
	}
	if r.rowCount() != 0 {
		t.Errorf("the table holds %d rows after a removal, want 0", r.rowCount())
	}
	evts := r.bus.events()
	if len(evts) != 1 {
		t.Fatalf("the bus published %d events, want 1", len(evts))
	}
	if change, ok := evts[0].Payload.(Change); !ok || change.Name != "otp" || !change.Deleted {
		t.Errorf("the change event carries %+v", evts[0].Payload)
	}
	if _, err := r.s.Resolve(sms.Message{
		Template: "otp", Locale: "en",
		Params: map[string]string{"code": "x"},
	}); !errors.Is(err, sms.ErrRejected) {
		t.Errorf("a removed template resolved with %v, want ErrRejected", err)
	}
}

// TestRemoveRefusesImmutableAndMissing checks the two refusals a removal makes.
func TestRemoveRefusesImmutableAndMissing(t *testing.T) {
	r := configuredRig(t)
	if err := r.s.Remove(context.Background(), "welcome"); !errors.Is(err, sms.ErrImmutable) {
		t.Errorf("removing a configured template returned %v, want ErrImmutable", err)
	}
	if err := r.s.Remove(context.Background(), "ghost"); !errors.Is(err, sms.ErrNoTemplate) {
		t.Errorf("removing an unknown template returned %v, want ErrNoTemplate", err)
	}
}

// TestOfferRecordsVendorCode checks that an offer calls the vendor once, stores its code and starts the template pending.
func TestOfferRecordsVendorCode(t *testing.T) {
	r := newSQLiteRig(t)
	r.provider.offerCode = "vendor-123"
	rec, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "code ${code}", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"},
	})
	if err != nil {
		t.Fatalf("offering: %v", err)
	}
	if rec.Name != "otp" || rec.Approvals["en"].State != sms.ApprovalPending || !rec.Enabled {
		t.Errorf("the offered record reads as %+v", rec)
	}
	if len(rec.Locales) != 1 || rec.Locales[0] != "en" {
		t.Errorf("the offered record's languages are %+v", rec.Locales)
	}
	if len(r.provider.submissions()) != 1 {
		t.Errorf("the vendor was offered %d submissions, want 1", len(r.provider.submissions()))
	}
	if r.s.snapshot.Load().byName["otp"].codes["en"] != "vendor-123" {
		t.Errorf("the stored code is %q, want vendor-123", r.s.snapshot.Load().byName["otp"].codes["en"])
	}
}

// TestOfferVendorFailureLeavesNoRow checks that a failed vendor call writes nothing.
func TestOfferVendorFailureLeavesNoRow(t *testing.T) {
	r := newSQLiteRig(t)
	r.provider.offerErr = errors.New("the vendor refused the offer")
	_, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "code ${code}", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"},
	})
	if err == nil {
		t.Fatal("offering with a failing vendor returned nil")
	}
	if r.rowCount() != 0 {
		t.Errorf("a failed offer wrote %d rows, want 0", r.rowCount())
	}
}

// TestOfferRefusesImmutableAndDuplicate checks the two refusals an offer makes, with neither reaching the vendor.
func TestOfferRefusesImmutableAndDuplicate(t *testing.T) {
	r := configuredRig(t)
	sub := sms.Submission{
		Name: "welcome", Content: "hi ${name}", Kind: sms.KindNotification,
		Locale: "en", Params: []string{"name"},
	}
	if _, err := r.s.Offer(context.Background(), sub); !errors.Is(err, sms.ErrImmutable) {
		t.Errorf("offering a configured name returned %v, want ErrImmutable", err)
	}
	if _, err := r.s.Register(context.Background(), "otp", spec([]string{"code"}, map[string]string{"en": "c"})); err != nil {
		t.Fatalf("registering: %v", err)
	}
	sub.Name = "otp"
	sub.Content = "code ${code}"
	sub.Params = []string{"code"}
	if _, err := r.s.Offer(context.Background(), sub); !errors.Is(err, sms.ErrTemplateExists) {
		t.Errorf("offering a duplicate name returned %v, want ErrTemplateExists", err)
	}
	if len(r.provider.submissions()) != 0 {
		t.Errorf("the vendor was offered %d submissions, want 0", len(r.provider.submissions()))
	}
}

// TestOfferMergesANewLanguage checks that a second language merges into a held name and the record carries both codes.
func TestOfferMergesANewLanguage(t *testing.T) {
	r := newSQLiteRig(t)
	r.provider.offerCode = "code-en"
	rec, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "code ${code}", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"},
	})
	if err != nil {
		t.Fatalf("offering the first language: %v", err)
	}
	if len(rec.Locales) != 1 || rec.Locales[0] != "en" {
		t.Fatalf("the offered record's languages are %+v, want [en]", rec.Locales)
	}

	r.provider.offerCode = "code-zh"
	rec, err = r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "验证码 ${code}", Kind: sms.KindVerification,
		Locale: "zh", Params: []string{"code"},
	})
	if err != nil {
		t.Fatalf("offering a second language: %v", err)
	}
	if len(rec.Locales) != 2 || rec.Locales[0] != "en" || rec.Locales[1] != "zh" {
		t.Errorf("the merged record's languages are %+v, want [en zh]", rec.Locales)
	}
	stored := r.s.snapshot.Load().byName["otp"]
	if stored.codes["en"] != "code-en" || stored.codes["zh"] != "code-zh" {
		t.Errorf("the merged codes are %+v, want en=code-en and zh=code-zh", stored.codes)
	}
	if len(stored.approvals) != 2 || stored.approvals["en"].State != sms.ApprovalPending ||
		stored.approvals["zh"].State != sms.ApprovalPending {
		t.Errorf("the merged reviews are %+v, want both pending", stored.approvals)
	}
	if r.rowCount() != 1 {
		t.Errorf("the table holds %d rows, want 1", r.rowCount())
	}
	if len(r.provider.submissions()) != 2 {
		t.Errorf("the vendor was offered %d submissions, want 2", len(r.provider.submissions()))
	}
}

// TestOfferRefusesAHeldLanguage checks that a language the name already holds is ErrTemplateExists and never reaches the vendor.
func TestOfferRefusesAHeldLanguage(t *testing.T) {
	r := newSQLiteRig(t)
	if _, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "code ${code}", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"},
	}); err != nil {
		t.Fatalf("offering the first language: %v", err)
	}
	before := len(r.provider.submissions())

	_, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "code ${code} again", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"},
	})
	if !errors.Is(err, sms.ErrTemplateExists) {
		t.Errorf("offering a held language returned %v, want ErrTemplateExists", err)
	}
	if len(r.provider.submissions()) != before {
		t.Errorf("a duplicate language still reached the vendor (%d calls, want %d)",
			len(r.provider.submissions()), before)
	}
	if r.rowCount() != 1 {
		t.Errorf("the table holds %d rows, want 1", r.rowCount())
	}
}

// TestOfferRefusesAMismatchedShape checks that a new language whose parameters disagree with the held shape is ErrInvalidConfig.
func TestOfferRefusesAMismatchedShape(t *testing.T) {
	r := newSQLiteRig(t)
	if _, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "code ${code}", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"},
	}); err != nil {
		t.Fatalf("offering the first language: %v", err)
	}
	before := len(r.provider.submissions())

	_, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "hi ${name}", Kind: sms.KindVerification,
		Locale: "zh", Params: []string{"name"},
	})
	if !errors.Is(err, sms.ErrInvalidConfig) {
		t.Errorf("offering a different shape returned %v, want ErrInvalidConfig", err)
	}
	if len(r.provider.submissions()) != before {
		t.Errorf("a shape mismatch still reached the vendor (%d calls, want %d)",
			len(r.provider.submissions()), before)
	}
}

// TestRegisterMergesANewLanguage checks that Register merges a second language and both codes are kept.
func TestRegisterMergesANewLanguage(t *testing.T) {
	r := newSQLiteRig(t)
	if _, err := r.s.Register(context.Background(), "otp",
		spec([]string{"code"}, map[string]string{"en": "en-code"})); err != nil {
		t.Fatalf("registering the first language: %v", err)
	}
	rec, err := r.s.Register(context.Background(), "otp",
		spec([]string{"code"}, map[string]string{"zh": "zh-code"}))
	if err != nil {
		t.Fatalf("registering a second language: %v", err)
	}
	if len(rec.Locales) != 2 || rec.Locales[0] != "en" || rec.Locales[1] != "zh" {
		t.Errorf("the merged record's languages are %+v, want [en zh]", rec.Locales)
	}
	stored := r.s.snapshot.Load().byName["otp"]
	if stored.codes["en"] != "en-code" || stored.codes["zh"] != "zh-code" {
		t.Errorf("the merged codes are %+v, want both languages", stored.codes)
	}
	if r.rowCount() != 1 {
		t.Errorf("the table holds %d rows, want 1", r.rowCount())
	}
}

// TestRegisterMergePreservesASettledLanguage checks that a merge leaves an already-approved language untouched.
func TestRegisterMergePreservesASettledLanguage(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("otp", []string{"code"}, map[string]string{"en": "en-code"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.load()

	rec, err := r.s.Register(context.Background(), "otp",
		spec([]string{"code"}, map[string]string{"zh": "zh-code"}))
	if err != nil {
		t.Fatalf("merging a second language: %v", err)
	}
	if rec.Approvals["en"].State != sms.ApprovalApproved {
		t.Errorf("the English review became %q, want it left approved", rec.Approvals["en"].State)
	}
	if rec.Approvals["zh"].State != sms.ApprovalPending {
		t.Errorf("the Chinese review is %q, want pending", rec.Approvals["zh"].State)
	}
}

func concurrentRig(t *testing.T, dialect string) *rig {
	t.Helper()
	database := openDialectItems(t, dialect, dbtest.Items{"max-open-conns": 1})
	r := newRig(t, database, newProvider(), settingsOf(t, nil, 0))
	r.load()
	return r
}

// TestConcurrentRegisterSameName checks that concurrent registrations of one name leave exactly one row.
func TestConcurrentRegisterSameName(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			r := concurrentRig(t, d.name)
			const callers = 16
			errs := make([]error, callers)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range callers {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					_, errs[i] = r.s.Register(context.Background(), "otp",
						spec([]string{"code"}, map[string]string{"en": "c"}))
				}(i)
			}
			close(start)
			wg.Wait()

			won, lost := 0, 0
			for _, err := range errs {
				switch {
				case err == nil:
					won++
				case errors.Is(err, sms.ErrTemplateExists):
					lost++
				default:
					t.Errorf("an unexpected error: %v", err)
				}
			}
			if won != 1 || lost != callers-1 {
				t.Errorf("%d registrations won and %d were refused, want 1 and %d", won, lost, callers-1)
			}
			if r.rowCount() != 1 {
				t.Errorf("the table holds %d rows, want 1", r.rowCount())
			}
		})
	}
}

// TestConcurrentSetEnabled checks that concurrent calls setting the same state bump the version once and publish once.
func TestConcurrentSetEnabled(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			r := concurrentRig(t, d.name)
			if _, err := r.s.Register(context.Background(), "otp", spec([]string{"code"}, map[string]string{"en": "c"})); err != nil {
				t.Fatalf("registering: %v", err)
			}
			if err := r.s.SetEnabled(context.Background(), "otp", false); err != nil {
				t.Fatalf("disabling: %v", err)
			}
			before := len(r.bus.events())

			const callers = 16
			errs := make([]error, callers)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range callers {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					errs[i] = r.s.SetEnabled(context.Background(), "otp", true)
				}(i)
			}
			close(start)
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					t.Errorf("caller %d failed: %v", i, err)
				}
			}
			if !r.s.snapshot.Load().byName["otp"].enabled {
				t.Errorf("the template is not enabled after the concurrent calls")
			}
			if r.versionOf("otp") != 3 {
				t.Errorf("the version is %d, want 3 (one change from the %d calls)", r.versionOf("otp"), callers)
			}
			if got := len(r.bus.events()) - before; got != 1 {
				t.Errorf("the concurrent calls published %d events, want 1", got)
			}
		})
	}
}

// TestConcurrentRegisterMergesDifferentLanguages checks that two callers registering one
// name with a different language each both succeed and leave one row carrying both.
func TestConcurrentRegisterMergesDifferentLanguages(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			r := concurrentRig(t, d.name)
			locales := []string{"en", "zh"}
			errs := make([]error, len(locales))
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i, locale := range locales {
				wg.Add(1)
				go func(i int, locale string) {
					defer wg.Done()
					<-start
					_, errs[i] = r.s.Register(context.Background(), "otp",
						spec([]string{"code"}, map[string]string{locale: locale + "-code"}))
				}(i, locale)
			}
			close(start)
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					t.Errorf("registering the language %q failed: %v", locales[i], err)
				}
			}
			if r.rowCount() != 1 {
				t.Errorf("the table holds %d rows, want 1", r.rowCount())
			}
			stored := r.s.snapshot.Load().byName["otp"]
			if stored.codes["en"] != "en-code" || stored.codes["zh"] != "zh-code" {
				t.Errorf("the merged codes are %+v, want both languages", stored.codes)
			}
		})
	}
}
