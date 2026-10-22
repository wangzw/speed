package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/sms"
)

// TestNewRefusesAConfiguredTemplateTheCarrierCannotCarry covers the first of the
// three shape checks: a configured template that declares vendor codes on a
// carrier which sends only a rendered body stops the module in New, naming the
// template and the sentinel.
func TestNewRefusesAConfiguredTemplateTheCarrierCannotCarry(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(configWith{templates: map[string]sms.TemplateSpec{
		"code.only": spec([]string{"code"}, map[string]string{"en": "c"}),
	}}, config.ModuleName)
	reg.Register(providingDB(openHandle(t)))
	reg.Register(providingProvider(newModelProvider(textCarrier())))
	reg.Register(Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{"test.database", "test.provider", ModuleName},
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("driving with a template the carrier cannot carry returned %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "code.only") {
		t.Errorf("the failure does not name the template: %v", err)
	}
}

// TestSnapshotLoadRefusesARowTheCarrierCannotCarry covers the second shape
// check: a row written under a carrier that sends by code is refused when a
// carrier that sends only a body mounts the database, rather than surfacing at
// the first send.
func TestSnapshotLoadRefusesARowTheCarrierCannotCarry(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect), newModelProvider(textCarrier()), settingsOf(t, nil, 0))
	r.plant("legacy", []string{"code"}, map[string]string{"en": "c"},
		map[string]sms.Approval{"en": {State: sms.ApprovalNotRequired}}, true, base)

	err := r.s.load(context.Background())
	if !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("loading a row the carrier cannot carry returned %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "legacy") {
		t.Errorf("the failure does not name the template: %v", err)
	}
}

// TestSnapshotLoadRefusesARowWithAnUnknownKind covers the declaration check the
// load repeats over every row. A row reaches the table only through a write that
// already checked it, but the table is also a thing people edit, and a kind
// outside the closed set would otherwise fall through to the carrier's default
// spelling at the first send.
func TestSnapshotLoadRefusesARowWithAnUnknownKind(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect), newModelProvider(textCarrier()), settingsOf(t, nil, 0))
	r.plantRow(r.provider.Name(), "promo", sms.Kind("promo"), []string{"code"}, nil,
		map[string]string{"en": "hi ${code}"}, nil,
		map[string]sms.Approval{"en": {State: sms.ApprovalNotRequired}}, true, base)

	err := r.s.load(context.Background())
	if !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("loading a row with an unknown kind returned %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "promo") {
		t.Errorf("the failure does not name the template: %v", err)
	}
}

// TestWritesRefuseATemplateTheCarrierCannotCarry covers the third shape check,
// over Register and Offer, and that nothing reaches the vendor or the table.
func TestWritesRefuseATemplateTheCarrierCannotCarry(t *testing.T) {
	provider := newModelProvider(textCarrier())
	r := newRig(t, openDialect(t, sqliteDialect), provider, settingsOf(t, nil, 0))
	r.load()

	if _, err := r.s.Register(context.Background(), "otp",
		spec([]string{"code"}, map[string]string{"en": "c"})); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Errorf("registering codes on a body-only carrier returned %v, want ErrInvalidConfig", err)
	}
	if _, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "hi ${code}", Kind: sms.KindNotification,
		Locale: "en", Params: []string{"code"}, Attributes: map[string]string{"NOT_A_KEY": "x"},
	}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Errorf("offering an attribute the carrier does not accept returned %v, want ErrInvalidConfig", err)
	}
	if r.rowCount() != 0 {
		t.Errorf("a refused write left %d rows", r.rowCount())
	}
	if len(provider.submissions()) != 0 {
		t.Errorf("a refused offer reached the vendor %d times", len(provider.submissions()))
	}
}

// TestWritesRefuseAnAttributeKeyAndAnOverlongBody covers the remaining two
// shape rules: an attribute key outside the carrier's declaration and a body
// over its bound are each ErrInvalidConfig naming the template.
func TestWritesRefuseAnAttributeKeyAndAnOverlongBody(t *testing.T) {
	model := textCarrier()
	model.MaxBodyRunes = 8
	r := newRig(t, openDialect(t, sqliteDialect), newModelProvider(model), settingsOf(t, nil, 0))
	r.load()

	if _, err := r.s.Register(context.Background(), "otp", sms.TemplateSpec{
		Params:     []string{"code"},
		Bodies:     map[string]string{"en": "hi ${code}"},
		Attributes: map[string]string{"NOT_A_KEY": "x"},
	}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Errorf("registering an unaccepted attribute returned %v, want ErrInvalidConfig", err)
	}
	if _, err := r.s.Register(context.Background(), "otp", sms.TemplateSpec{
		Params: []string{"code"},
		Bodies: map[string]string{"en": "0123456789"},
	}); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Errorf("registering an overlong body returned %v, want ErrInvalidConfig", err)
	}
	if _, err := r.s.Register(context.Background(), "otp", sms.TemplateSpec{
		Params:     []string{"code"},
		Bodies:     map[string]string{"en": "hi"},
		Attributes: map[string]string{"IN_TEMPLATE_ID": "t-1"},
	}); err != nil {
		t.Errorf("registering an accepted attribute and body returned %v", err)
	}
}

// TestOfferRecordsTheBodyLocallyWhenTheCarrierDoesNotReview covers a carrier
// with no per-template review: Offer calls no vendor, records the body and the
// kind, starts the language NotRequired, and the template resolves and is in
// force at once.
func TestOfferRecordsTheBodyLocallyWhenTheCarrierDoesNotReview(t *testing.T) {
	provider := newModelProvider(textCarrier())
	r := newRig(t, openDialect(t, sqliteDialect), provider, settingsOf(t, nil, 0))
	r.load()

	rec, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "code ${code}", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"}, Attributes: map[string]string{"IN_TEMPLATE_ID": "t-1"},
	})
	if err != nil {
		t.Fatalf("offering: %v", err)
	}
	if len(provider.submissions()) != 0 {
		t.Errorf("the vendor was reached %d times for a carrier with no content resource", len(provider.submissions()))
	}
	if rec.Approvals["en"].State != sms.ApprovalNotRequired {
		t.Errorf("the language started %q, want not-required", rec.Approvals["en"].State)
	}
	if rec.Bodies["en"] != "code ${code}" {
		t.Errorf("the recorded body is %q, want the submission's", rec.Bodies["en"])
	}
	if rec.Kind != sms.KindVerification || rec.Carrier != "fake" {
		t.Errorf("the record reads as %+v", rec)
	}
	if len(rec.Locales) != 1 || rec.Locales[0] != "en" {
		t.Errorf("the record's languages are %+v, want [en]", rec.Locales)
	}

	got, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "en", Params: map[string]string{"code": "1234"}})
	if err != nil {
		t.Fatalf("resolving a not-required template: %v", err)
	}
	if got.Body != "code 1234" || got.Code != "" || got.Kind != sms.KindVerification {
		t.Errorf("the resolved form is %+v, want the rendered body and the template's kind", got)
	}
	if got.Attributes["IN_TEMPLATE_ID"] != "t-1" {
		t.Errorf("the resolved attributes are %+v, want the recorded registration identifier", got.Attributes)
	}

	declared := r.s.Declared()
	if len(declared) != 1 || declared[0].Name != "otp" || declared[0].Kind != sms.KindVerification {
		t.Errorf("Declared returned %+v, want the not-required template", declared)
	}
}

// TestOfferTakesUpTheVendorCodeOnAContentCarrier covers a carrier that creates a
// vendor resource and has no per-template review: the vendor is called once, its
// code and the body are both stored, and the language is sendable at once.
func TestOfferTakesUpTheVendorCodeOnAContentCarrier(t *testing.T) {
	provider := newModelProvider(contentCarrier())
	provider.offerCode = "HX123"
	r := newRig(t, openDialect(t, sqliteDialect), provider, settingsOf(t, nil, 0))
	r.load()

	rec, err := r.s.Offer(context.Background(), sms.Submission{
		Name: "otp", Content: "code ${code}", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"},
	})
	if err != nil {
		t.Fatalf("offering: %v", err)
	}
	if len(provider.submissions()) != 1 {
		t.Errorf("the vendor was offered %d submissions, want 1", len(provider.submissions()))
	}
	if rec.Approvals["en"].State != sms.ApprovalNotRequired {
		t.Errorf("the language started %q, want not-required", rec.Approvals["en"].State)
	}
	stored := r.s.snapshot.Load().byName["otp"]
	if stored.codes["en"] != "HX123" {
		t.Errorf("the stored code is %q, want HX123", stored.codes["en"])
	}
	if stored.bodies["en"] != "code ${code}" {
		t.Errorf("the stored body is %q, want the submission's", stored.bodies["en"])
	}
	got, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "en", Params: map[string]string{"code": "1234"}})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got.Code != "HX123" || got.Body != "code 1234" {
		t.Errorf("the resolved form is %+v, want both the code and the rendered body", got)
	}
}

// TestOfferRefusesALanguageHeldOnlyAsABody covers the effective locale set in
// conflict detection: a language a name holds a body for is held, so a second
// offer of it is ErrTemplateExists even though no code exists for it.
func TestOfferRefusesALanguageHeldOnlyAsABody(t *testing.T) {
	provider := newModelProvider(textCarrier())
	r := newRig(t, openDialect(t, sqliteDialect), provider, settingsOf(t, nil, 0))
	r.load()

	sub := sms.Submission{
		Name: "otp", Content: "code ${code}", Kind: sms.KindVerification,
		Locale: "en", Params: []string{"code"},
	}
	if _, err := r.s.Offer(context.Background(), sub); err != nil {
		t.Fatalf("offering the first language: %v", err)
	}
	if _, err := r.s.Offer(context.Background(), sub); !errors.Is(err, sms.ErrTemplateExists) {
		t.Errorf("offering a language held only as a body returned %v, want ErrTemplateExists", err)
	}
}

// TestNotRequiredIsSendableAndUnknownIsNot covers the two states the sendable
// rule turns on: a template whose carrier has no review is in force and
// resolves, while one whose review this process merely does not follow is
// neither.
func TestNotRequiredIsSendableAndUnknownIsNot(t *testing.T) {
	r := newSQLiteRig(t)
	r.plantRow("fake", "no-review", sms.KindNotification, nil, map[string]string{"en": "c1"}, nil, nil,
		map[string]sms.Approval{"en": {State: sms.ApprovalNotRequired}}, true, base)
	r.plantRow("fake", "untracked", sms.KindNotification, nil, map[string]string{"en": "c2"}, nil, nil,
		map[string]sms.Approval{"en": {State: sms.ApprovalUnknown}}, true, base)
	r.load()

	if _, err := r.s.Resolve(sms.Message{Template: "no-review", Locale: "en"}); err != nil {
		t.Errorf("resolving a not-required template returned %v", err)
	}
	if _, err := r.s.Resolve(sms.Message{Template: "untracked", Locale: "en"}); !errors.Is(err, sms.ErrNotSendable) {
		t.Errorf("resolving an untracked template returned %v, want ErrNotSendable", err)
	}
	declared := r.s.Declared()
	if len(declared) != 1 || declared[0].Name != "no-review" {
		t.Errorf("Declared returned %+v, want only the not-required template", declared)
	}
}

// TestARuntimeBodyOnlyRowResolvesToTheRenderedBody covers a row whose locale set
// comes from the body table alone, which is the shape a body-sending carrier
// stores.
func TestARuntimeBodyOnlyRowResolvesToTheRenderedBody(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect), newModelProvider(textCarrier()), settingsOf(t, nil, 0))
	r.plantRow("fake", "notice", sms.KindMarketing, []string{"name"},
		nil, map[string]string{"en": "hello ${name}"}, map[string]string{"IN_TEMPLATE_ID": "t-9"},
		map[string]sms.Approval{"en": {State: sms.ApprovalNotRequired}}, true, base)
	r.load()

	got, err := r.s.Resolve(sms.Message{
		Template: "notice", Locale: "en", Params: map[string]string{"name": "Ann"},
		Originator: "primary", ValidFor: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got.Body != "hello Ann" || got.Code != "" || got.Kind != sms.KindMarketing {
		t.Errorf("the resolved form is %+v", got)
	}
	if got.Originator != "primary" || got.ValidFor != 5*time.Minute || got.Attributes["IN_TEMPLATE_ID"] != "t-9" {
		t.Errorf("the resolved send fields are %+v", got)
	}
}

// TestTwoCarriersShareOneDatabaseWithoutSeeingEachOthersTemplates covers the
// carrier column: each carrier's reads are filtered by its own name, and a
// database that holds no legacy row carries no ownership marker.
func TestTwoCarriersShareOneDatabaseWithoutSeeingEachOthersTemplates(t *testing.T) {
	database := openDialect(t, sqliteDialect)
	alpha := newNamedProvider("alpha")
	alpha.model = contentCarrier()
	beta := newNamedProvider("beta")
	beta.model = contentCarrier()
	a := newRig(t, database, alpha, settingsOf(t, nil, 0))
	a.load()
	b := newRigOn(t, a.h, beta, settingsOf(t, nil, 0))
	b.load()

	if _, err := a.s.Register(context.Background(), "only-alpha",
		spec([]string{"code"}, map[string]string{"en": "a"})); err != nil {
		t.Fatalf("registering on alpha: %v", err)
	}
	if _, err := b.s.Register(context.Background(), "only-beta",
		spec([]string{"code"}, map[string]string{"en": "b"})); err != nil {
		t.Fatalf("registering on beta: %v", err)
	}
	a.load()
	b.load()

	if got := a.s.Declared(); len(got) != 1 || got[0].Name != "only-alpha" {
		t.Errorf("alpha declared %+v, want only its own template", got)
	}
	if got := b.s.Declared(); len(got) != 1 || got[0].Name != "only-beta" {
		t.Errorf("beta declared %+v, want only its own template", got)
	}
	if _, err := a.s.Resolve(sms.Message{Template: "only-beta", Locale: "en"}); !errors.Is(err, sms.ErrRejected) {
		t.Errorf("alpha resolved beta's template with %v, want ErrRejected", err)
	}
	if a.rowCountFor("alpha") != 1 || a.rowCountFor("beta") != 1 {
		t.Errorf("the table holds alpha=%d beta=%d rows, want one each",
			a.rowCountFor("alpha"), a.rowCountFor("beta"))
	}
	if owner, ok := a.ownerOf(); ok {
		t.Errorf("a database with no legacy row carries the ownership marker %q", owner)
	}
}
