package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// configuredRig is a loaded rig whose configuration source declares one
// template, so a case drives both sources at once.
func configuredRig(t *testing.T) *rig {
	t.Helper()
	cfg := settingsOf(t, map[string]sms.TemplateSpec{
		"welcome": spec([]string{"name"}, map[string]string{"en": "cfg-en", "zh": "cfg-zh"}),
	}, 0)
	r := newRig(t, openDialect(t, sqliteDialect), newProvider(), cfg)
	r.load()
	return r
}

// TestResolvePicksFromEitherSource drives the two sources and the priority
// between them: a configured message is resolved through the declaration, a
// runtime message through the snapshot, and a name neither holds is refused.
func TestResolvePicksFromEitherSource(t *testing.T) {
	r := configuredRig(t)
	r.plant("otp", []string{"code", "ttl"}, map[string]string{"en": "rt-en"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.load()

	got, err := r.s.Resolve(sms.Message{
		To: "+8613800138000", Template: "welcome", Locale: "en",
		Params: map[string]string{"name": "Ann"}, DedupeID: "d1",
	})
	if err != nil {
		t.Fatalf("resolving a configured template: %v", err)
	}
	if got.Code != "cfg-en" || got.Template != "welcome" || got.Locale != "en" || got.To != "+8613800138000" ||
		got.DedupeID != "d1" {
		t.Errorf("the configured template resolved to %+v", got)
	}
	if len(got.Ordered) != 1 || got.Ordered[0] != "Ann" || got.Params["name"] != "Ann" {
		t.Errorf("the configured parameters are %+v / %+v", got.Params, got.Ordered)
	}

	got, err = r.s.Resolve(sms.Message{
		To: "+8613800138001", Template: "otp", Locale: "en",
		Params: map[string]string{"code": "1234", "ttl": "5"},
	})
	if err != nil {
		t.Fatalf("resolving a runtime template: %v", err)
	}
	if got.Code != "rt-en" {
		t.Errorf("the runtime template resolved to code %q, want rt-en", got.Code)
	}
	// Ordered follows the declared parameter order, not the map's.
	if len(got.Ordered) != 2 || got.Ordered[0] != "1234" || got.Ordered[1] != "5" {
		t.Errorf("the runtime parameters are ordered %+v", got.Ordered)
	}

	if _, err := r.s.Resolve(sms.Message{Template: "nope", Locale: "en", Params: map[string]string{}}); !errors.Is(err, sms.ErrRejected) {
		t.Errorf("an unknown template resolved with %v, want ErrRejected", err)
	}
}

// TestResolveRefusesNotSendable covers a runtime template that is disabled and
// one whose vendor review has not come back approved: both are ErrNotSendable,
// told apart from a caller defect.
func TestResolveRefusesNotSendable(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("off", []string{"code"}, map[string]string{"en": "c1"}, map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, false, base)
	r.plant("waiting", []string{"code"}, map[string]string{"en": "c2"}, map[string]sms.Approval{"en": {State: sms.ApprovalPending}}, true, base)
	r.load()

	if _, err := r.s.Resolve(sms.Message{Template: "off", Locale: "en", Params: map[string]string{"code": "x"}}); !errors.Is(err, sms.ErrNotSendable) {
		t.Errorf("a disabled template resolved with %v, want ErrNotSendable", err)
	}
	if _, err := r.s.Resolve(sms.Message{Template: "waiting", Locale: "en", Params: map[string]string{"code": "x"}}); !errors.Is(err, sms.ErrNotSendable) {
		t.Errorf("a pending template resolved with %v, want ErrNotSendable", err)
	}
}

// TestResolveIsPerLanguage covers the review's granularity: a template approved
// in one language and still pending in another resolves in the approved language
// and is refused with ErrNotSendable in the pending one. This is the case a
// single aggregate verdict got wrong: a pending second language must not block a
// message in a language the vendor already approved.
func TestResolveIsPerLanguage(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("otp", []string{"code"}, map[string]string{"en": "c-en", "zh": "c-zh"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}, "zh": {State: sms.ApprovalPending}}, true, base)
	r.load()

	got, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "en", Params: map[string]string{"code": "x"}})
	if err != nil {
		t.Fatalf("resolving the approved language: %v", err)
	}
	if got.Code != "c-en" {
		t.Errorf("the approved language resolved to code %q, want c-en", got.Code)
	}
	if _, err := r.s.Resolve(sms.Message{
		Template: "otp", Locale: "zh", Params: map[string]string{"code": "x"},
	}); !errors.Is(err, sms.ErrNotSendable) {
		t.Errorf("resolving the pending language returned %v, want ErrNotSendable", err)
	}
}

// TestResolveRuntimeChecksLanguageAndParams covers the runtime path's own
// structural checks: a language with no code, a missing parameter and a surplus
// parameter are each ErrRejected.
func TestResolveRuntimeChecksLanguageAndParams(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("otp", []string{"code"}, map[string]string{"en": "c"}, map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.load()

	if _, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "fr", Params: map[string]string{"code": "x"}}); !errors.Is(err, sms.ErrRejected) {
		t.Errorf("a missing language resolved with %v, want ErrRejected", err)
	}
	if _, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "en", Params: map[string]string{}}); !errors.Is(err, sms.ErrRejected) {
		t.Errorf("a missing parameter resolved with %v, want ErrRejected", err)
	}
	if _, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "en", Params: map[string]string{"code": "x", "extra": "y"}}); !errors.Is(err, sms.ErrRejected) {
		t.Errorf("a surplus parameter resolved with %v, want ErrRejected", err)
	}
}

// TestResolveEmptyLocaleIsRejected covers the empty-locale check, which is the
// caller's defect whichever source holds the template.
func TestResolveEmptyLocaleIsRejected(t *testing.T) {
	r := configuredRig(t)
	if _, err := r.s.Resolve(sms.Message{Template: "welcome", Params: map[string]string{"name": "A"}}); !errors.Is(err, sms.ErrRejected) {
		t.Errorf("an empty locale resolved with %v, want ErrRejected", err)
	}
	r.plant("otp", []string{"code"}, map[string]string{"en": "c"}, map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.load()
	if _, err := r.s.Resolve(sms.Message{Template: "otp", Params: map[string]string{"code": "x"}}); !errors.Is(err, sms.ErrRejected) {
		t.Errorf("an empty locale on a runtime template resolved with %v, want ErrRejected", err)
	}
}

// TestRuntimeReadBeforeLoadIsUnavailable covers the before-load behaviour: a
// runtime resolve, a list and a management call are each a failure that says
// the state is unavailable, never that the set is empty or that a template is
// absent.
func TestRuntimeReadBeforeLoadIsUnavailable(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect), newProvider(), settingsOf(t, nil, 0))
	// No load: the state Init leaves is absent.

	if _, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "en", Params: map[string]string{"code": "x"}}); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("resolving before the load returned %v, want ErrNotLoaded", err)
	}
	if _, err := r.s.List(context.Background()); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("listing before the load returned %v, want ErrNotLoaded", err)
	}
	if err := r.s.SetEnabled(context.Background(), "otp", false); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("SetEnabled before the load returned %v, want ErrNotLoaded", err)
	}
	if err := r.s.Remove(context.Background(), "otp"); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("Remove before the load returned %v, want ErrNotLoaded", err)
	}
}

// TestDeclaredMergesAndFilters covers Declared: the configuration templates plus
// the runtime ones that are both enabled and approved, in name order.
func TestDeclaredMergesAndFilters(t *testing.T) {
	r := configuredRig(t)
	r.plant("beta", []string{"code"}, map[string]string{"en": "c"}, map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.plant("alpha", []string{"code"}, map[string]string{"en": "c"}, map[string]sms.Approval{"en": {State: sms.ApprovalPending}}, true, base)
	r.plant("gamma", []string{"code"}, map[string]string{"en": "c"}, map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, false, base)
	r.load()

	got := r.s.Declared()
	var names []string
	for _, tmpl := range got {
		names = append(names, tmpl.Name)
	}
	// welcome (config) and beta (runtime, enabled and approved); alpha is
	// pending and gamma is disabled, so neither is in force.
	want := []string{"beta", "welcome"}
	if len(names) != len(want) {
		t.Fatalf("Declared returned %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("Declared returned %v, want %v", names, want)
			break
		}
	}
}

// TestListMergesSources covers List: the configuration records are read-only
// and always enabled with an untracked review; the runtime records carry their
// state and bookkeeping.
func TestListMergesSources(t *testing.T) {
	r := configuredRig(t)
	r.plant("otp", []string{"code", "ttl"}, map[string]string{"en": "c"}, map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.load()

	records, err := r.s.List(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("List returned %d records, want 2", len(records))
	}
	// Sorted by name: otp (runtime) then welcome (config).
	otp, welcome := records[0], records[1]
	if otp.Name != "otp" || otp.Source != sms.SourceRuntime || otp.Approvals["en"].State != sms.ApprovalApproved ||
		!otp.Enabled || otp.Version != 1 || otp.CreatedAt.IsZero() {
		t.Errorf("the runtime record reads as %+v", otp)
	}
	if len(otp.Params) != 2 || otp.Params[0] != "code" {
		t.Errorf("the runtime record's parameters are %+v", otp.Params)
	}
	if welcome.Name != "welcome" || welcome.Source != sms.SourceConfig || len(welcome.Approvals) != 0 ||
		!welcome.Enabled || welcome.Version != 0 || !welcome.CreatedAt.IsZero() {
		t.Errorf("the configured record reads as %+v", welcome)
	}
}

// TestDeclaredHandsOutCopies covers that a caller cannot write into the
// module's storage through what it read.
func TestDeclaredHandsOutCopies(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("otp", []string{"code"}, map[string]string{"en": "c"}, map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.load()

	first := r.s.Declared()
	if len(first) != 1 {
		t.Fatalf("Declared returned %d templates, want 1", len(first))
	}
	first[0].Params[0] = "mutated"
	first[0].Locales[0] = "mutated"
	second := r.s.Declared()
	if second[0].Params[0] != "code" || second[0].Locales[0] != "en" {
		t.Errorf("a caller's write reached the next read: %+v", second[0])
	}
}

// TestResolvedUsesTheWriteTimeClock is a guard that the runtime resolve carries
// every field the carrier needs and nothing the module invented.
func TestResolvedFieldsRoundTrip(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("otp", []string{"a", "b"}, map[string]string{"en": "code-en"}, map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.load()

	got, err := r.s.Resolve(sms.Message{
		To:       "+8613800138000",
		Template: "otp",
		Locale:   "en",
		Params:   map[string]string{"a": "1", "b": "2"},
		DedupeID: "abc",
	})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got.To != "+8613800138000" || got.Template != "otp" || got.Locale != "en" || got.Code != "code-en" ||
		got.DedupeID != "abc" || got.Params["a"] != "1" || got.Params["b"] != "2" ||
		len(got.Ordered) != 2 || got.Ordered[0] != "1" || got.Ordered[1] != "2" {
		t.Errorf("the resolved form is %+v", got)
	}
}

// TestChangedRowIsPublishedAsAnEvent covers that a write publishes the change
// event and that the handler reloads the snapshot.
func TestChangedRowIsPublishedAsAnEvent(t *testing.T) {
	r := newSQLiteRig(t)
	r.s.subscribe()
	if _, err := r.s.Register(context.Background(), "otp", spec([]string{"code"}, map[string]string{"en": "c"})); err != nil {
		t.Fatalf("registering: %v", err)
	}
	evts := r.bus.events()
	if len(evts) != 1 || evts[0].Type != ChangedEventType {
		t.Fatalf("the bus published %+v, want one %q", evts, ChangedEventType)
	}
	// A second replica applies the event by reloading; here the same snapshot
	// is rebuilt, so the row is still resolvable.
	r.bus.deliver(context.Background(), evts[0])
	if _, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "en", Params: map[string]string{"code": "x"}}); errors.Is(err, ErrNotLoaded) {
		t.Errorf("the snapshot was cleared by the reload: %v", err)
	}
}
