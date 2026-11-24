package journal

import (
	"context"
	"encoding/json"
	"maps"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
)

func impersonated(t *testing.T) context.Context {
	t.Helper()
	return authntest.Context(t, context.Background(),
		authntest.WithAccount("customer-1"),
		authntest.WithActor("admin-9"),
		authntest.WithSession("0198f0aa-0000-7000-8000-000000000002"),
		authntest.WithScope("acme"))
}

func detailOfRow(t *testing.T, r row) map[string]string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal([]byte(r.Detail), &out); err != nil {
		t.Fatalf("the detail %q is not a JSON object of strings: %v", r.Detail, err)
	}
	return out
}

func wantImpersonation(t *testing.T, what string, r row) {
	t.Helper()
	if r.Actor != "admin-9" {
		t.Errorf("%s: the actor column reads %q, want the actor admin-9: the row says who did it", what, r.Actor)
	}
	detail := detailOfRow(t, r)
	for key, want := range map[string]string{
		"on_behalf_of":        "customer-1",
		"impersonation_scope": "acme",
		"session":             "0198f0aa-0000-7000-8000-000000000002",
	} {
		if detail[key] != want {
			t.Errorf("%s: the detail key %q reads %q, want %q: the detail says on whose behalf", what, key, detail[key], want)
		}
	}
}

// TestAnImpersonationAddsItsKeysToTheRowOfEveryPath holds the detail keys an
// impersonating identity adds on each of the four write paths.
func TestAnImpersonationAddsItsKeysToTheRowOfEveryPath(t *testing.T) {
	t.Run("the seam", func(t *testing.T) {
		handle := open(t)
		j := newTestJournal(handle, settings{})
		if err := j.Record(impersonated(t), audit.Entry{Action: "role.grant", Detail: map[string]string{"role": "admin"}}); err != nil {
			t.Fatalf("recording: %v", err)
		}
		rows := auditRows(t, handle)
		if len(rows) != 1 {
			t.Fatalf("want one row, got %d", len(rows))
		}
		wantImpersonation(t, "the seam", rows[0])
		if got := detailOfRow(t, rows[0])["role"]; got != "admin" {
			t.Errorf("the caller's own detail key reads %q, want it kept beside the added ones", got)
		}
	})

	t.Run("a statement", func(t *testing.T) {
		handle := open(t)
		if err := handle.WithContext(impersonated(t)).Create(&order{ID: "o1", Note: "x"}).Error; err != nil {
			t.Fatalf("writing the order: %v", err)
		}
		rows := auditRows(t, handle)
		if len(rows) != 1 {
			t.Fatalf("want one row, got %d", len(rows))
		}
		if rows[0].Source != string(audit.SourceStatement) {
			t.Fatalf("the row's source is %q, want a statement row", rows[0].Source)
		}
		wantImpersonation(t, "a statement", rows[0])
		if got := detailOfRow(t, rows[0])["rows_affected"]; got != "1" {
			t.Errorf("the statement's own detail reads %q, want it kept", got)
		}
	})

	t.Run("a request", func(t *testing.T) {
		handle := open(t)
		j := newTestJournal(handle, settings{})
		r := httptest.NewRequest("POST", "/contacts/c1", nil).WithContext(impersonated(t))
		j.recordRequest(r, "public", "/contacts/:id", "200")

		rows := auditRows(t, handle)
		if len(rows) != 1 {
			t.Fatalf("want one row, got %d", len(rows))
		}
		wantImpersonation(t, "a request", rows[0])
		if got := detailOfRow(t, rows[0])["endpoint"]; got != "public" {
			t.Errorf("the request's own detail reads %q, want it kept", got)
		}
	})

	t.Run("an event published under the identity's context", func(t *testing.T) {
		handle := open(t)
		bus := newRecordingBus()
		j := newTestJournal(handle, settings{events: []string{"order.updated"}})
		j.subscribe(bus)
		if err := bus.Publish(impersonated(t), event.Event{Type: "order.updated", Payload: map[string]any{"object": "o1"}}); err != nil {
			t.Fatalf("publishing: %v", err)
		}
		rows := auditRows(t, handle)
		if len(rows) != 1 {
			t.Fatalf("want one row, got %d", len(rows))
		}
		wantImpersonation(t, "an event", rows[0])
	})
}

// TestAnIdentityThatIsNotImpersonatingAddsNothing holds the other side of
// the criterion.
func TestAnIdentityThatIsNotImpersonatingAddsNothing(t *testing.T) {
	for name, c := range map[string]struct {
		ctx   context.Context
		actor string
	}{
		"an ordinary identity": {authntest.Context(t, context.Background(), authntest.WithAccount("alice-1"),
			authntest.WithSession("0198f0aa-0000-7000-8000-000000000003")), "alice-1"},
		"no identity at all": {context.Background(), ""},
		"no identity on a route that accepts none": {
			authntest.Anonymous(t, context.Background(), authn.AccessOptional), "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := assemble(c.ctx, audit.SourceSeam, audit.Entry{Action: "x", Detail: map[string]string{"note": "n"}}, "")
			if got.Actor != c.actor {
				t.Errorf("the actor column reads %q, want %q", got.Actor, c.actor)
			}
			detail := detailOfRow(t, got)
			for _, key := range []string{"on_behalf_of", "impersonation_scope", "session"} {
				if _, found := detail[key]; found {
					t.Errorf("the detail carries %q for a request that is not an impersonation: %v", key, detail)
				}
			}
			if detail["note"] != "n" {
				t.Errorf("the caller's detail was lost: %v", detail)
			}
		})
	}
}

// TestACallerCannotWriteTheImpersonationKeysOfAnImpersonation holds that
// the three keys are this module's.
func TestACallerCannotWriteTheImpersonationKeysOfAnImpersonation(t *testing.T) {
	detail := map[string]string{"on_behalf_of": "somebody-else", "impersonation_scope": "another", "session": "forged", "note": "n"}
	got := assemble(impersonated(t), audit.SourceSeam, audit.Entry{Action: "x", Detail: detail}, "")

	wantImpersonation(t, "a row with the keys forged", got)
	if detail["on_behalf_of"] != "somebody-else" || detail["session"] != "forged" || len(detail) != 4 {
		t.Errorf("the caller's own map was changed: %v", detail)
	}
}

// TestTheTenantOfAnImpersonationIsTheContextsAndNotTheScope holds that
// the impersonation scope is detail and does not become the row's tenant.
func TestTheTenantOfAnImpersonationIsTheContextsAndNotTheScope(t *testing.T) {
	got := assemble(impersonated(t), audit.SourceSeam, audit.Entry{Action: "x"}, "")
	if got.TenantID != nil {
		t.Errorf("the tenant column reads %q for a context with no tenant, want none", *got.TenantID)
	}
}

// TestTheActorOfARowIsTheIdentitysKey holds that the actor column is the
// key and nothing the journal composes.
func TestTheActorOfARowIsTheIdentitysKey(t *testing.T) {
	for _, ctx := range []context.Context{impersonated(t), authntest.Context(t, context.Background())} {
		id, ok := authn.FromContext(ctx)
		if !ok {
			t.Fatal("no identity in the context")
		}
		if got := actorOf(ctx); got != id.Key() {
			t.Errorf("the actor is %q, want the identity's key %q", got, id.Key())
		}
	}
}

// TestARowThatIsNotAnImpersonationDropsTheCallersImpersonationKeys holds that the
// three impersonation keys are written for an impersonation alone.
func TestARowThatIsNotAnImpersonationDropsTheCallersImpersonationKeys(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"an ordinary identity": authntest.Context(t, context.Background(), authntest.WithAccount("alice-1"),
			authntest.WithSession("0198f0aa-0000-7000-8000-000000000003")),
		"no identity at all":                       context.Background(),
		"no identity on a route that accepts none": authntest.Anonymous(t, context.Background(), authn.AccessOptional),
	} {
		t.Run(name, func(t *testing.T) {
			detail := map[string]string{
				"on_behalf_of": "carried", "impersonation_scope": "elsewhere", "session": "mine", "note": "n",
			}
			got := detailOfRow(t, assemble(ctx, audit.SourceSeam, audit.Entry{Action: "x", Detail: detail}, ""))
			if want := map[string]string{"note": "n"}; !maps.Equal(got, want) {
				t.Errorf("the detail reads %v, want %v", got, want)
			}
			if len(detail) != 4 || detail["session"] != "mine" {
				t.Errorf("the caller's own map was changed: %v", detail)
			}
		})
	}
}

// TestTheCallersImpersonationKeysAreDroppedOnEveryPathThatTakesADetail
// holds the same rule where the caller's keys actually arrive.
func TestTheCallersImpersonationKeysAreDroppedOnEveryPathThatTakesADetail(t *testing.T) {
	forged := map[string]string{"on_behalf_of": "x", "impersonation_scope": "y", "session": "z", "note": "n"}
	check := func(t *testing.T, what string, r row) {
		t.Helper()
		if want := map[string]string{"note": "n"}; !maps.Equal(detailOfRow(t, r), want) {
			t.Errorf("%s: the detail reads %v, want only the caller's other key", what, detailOfRow(t, r))
		}
	}

	t.Run("the seam", func(t *testing.T) {
		handle := open(t)
		j := newTestJournal(handle, settings{})
		if err := j.Record(context.Background(), audit.Entry{Action: "x", Detail: forged}); err != nil {
			t.Fatalf("recording: %v", err)
		}
		rows := auditRows(t, handle)
		if len(rows) != 1 {
			t.Fatalf("want one row, got %d", len(rows))
		}
		check(t, "the seam", rows[0])
	})

	t.Run("an event", func(t *testing.T) {
		handle := open(t)
		bus := newRecordingBus()
		j := newTestJournal(handle, settings{events: []string{"order.updated"}})
		j.subscribe(bus)
		payload := map[string]any{"on_behalf_of": "x", "impersonation_scope": "y", "session": "z", "note": "n"}
		if err := bus.Publish(context.Background(), event.Event{Type: "order.updated", Payload: payload}); err != nil {
			t.Fatalf("publishing: %v", err)
		}
		rows := auditRows(t, handle)
		if len(rows) != 1 {
			t.Fatalf("want one row, got %d", len(rows))
		}
		check(t, "an event", rows[0])
	})
}

// TestAnEventDeliveredUnderABareContextHasNoActorAndNoImpersonationKeys
// holds the event path on a transport bus.
func TestAnEventDeliveredUnderABareContextHasNoActorAndNoImpersonationKeys(t *testing.T) {
	handle := open(t)
	bus := newRecordingBus()
	j := newTestJournal(handle, settings{events: []string{"order.updated"}})
	j.subscribe(bus)
	if err := bus.Publish(context.Background(), event.Event{Type: "order.updated", Payload: map[string]any{"object": "o1"}}); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	if rows[0].Actor != "" {
		t.Errorf("the actor column reads %q, want it empty", rows[0].Actor)
	}
	detail := detailOfRow(t, rows[0])
	for _, key := range []string{"on_behalf_of", "impersonation_scope", "session"} {
		if _, found := detail[key]; found {
			t.Errorf("the detail carries %q under a context with no identity: %v", key, detail)
		}
	}
}

// TestARequestWithoutAnIdentityRecordsNoActorAndNoImpersonationKeys holds the
// request row for an anonymous, exempt or system operation route.
func TestARequestWithoutAnIdentityRecordsNoActorAndNoImpersonationKeys(t *testing.T) {
	handle := open(t)
	j := newTestJournal(handle, settings{})
	j.recordRequest(httptest.NewRequest("POST", "/login", nil), "public", "/login", "200")

	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	if rows[0].Actor != "" {
		t.Errorf("the actor column reads %q for a request with no identity, want it empty", rows[0].Actor)
	}
	detail := detailOfRow(t, rows[0])
	for _, key := range []string{"on_behalf_of", "impersonation_scope", "session"} {
		if _, found := detail[key]; found {
			t.Errorf("the detail carries %q with no identity: %v", key, detail)
		}
	}
}

// TestTheKeysAnImpersonationAddsAreNotInTheMaskedSet holds that none of
// the three names is one the masking step replaces.
func TestTheKeysAnImpersonationAddsAreNotInTheMaskedSet(t *testing.T) {
	for _, key := range []string{"on_behalf_of", "impersonation_scope", "session"} {
		if slices.Contains(maskedKeySet, key) {
			t.Errorf("%q is in the masked set %v", key, maskedKeySet)
		}
	}
	got := assemble(impersonated(t), audit.SourceSeam,
		audit.Entry{Action: "x", Detail: map[string]string{"password": "hunter2"}}, "")
	if v := detailOfRow(t, got)["password"]; v != log.MaskedText {
		t.Errorf("the caller's masked key reads %q, want the mask", v)
	}
	wantImpersonation(t, "a row with a masked key beside", got)
}

// TestAnImpersonationWithoutASessionWritesNoSessionKey holds the session
// key's own rule.
func TestAnImpersonationWithoutASessionWritesNoSessionKey(t *testing.T) {
	detail := map[string]string{"session": "forged", "note": "n"}
	got := impersonationKeys(detail, "customer-1", "acme", "")
	want := map[string]string{"on_behalf_of": "customer-1", "impersonation_scope": "acme", "note": "n"}
	if !maps.Equal(got, want) {
		t.Errorf("the detail reads %v, want %v", got, want)
	}
	if detail["session"] != "forged" {
		t.Errorf("the caller's own map was changed: %v", detail)
	}
	if got := impersonationKeys(nil, "customer-1", "acme", "s-1"); got["session"] != "s-1" {
		t.Errorf("a session of the identity's is written as %q, want s-1", got["session"])
	}
}
