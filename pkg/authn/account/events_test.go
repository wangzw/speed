package account

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// bindingEvent decodes the one event of a type the bus holds, failing the case if
// there is not exactly one.
func bindingEvent[T any](t *testing.T, h *harness, eventType string) T {
	t.Helper()
	events := h.bus.of(eventType)
	if len(events) != 1 {
		t.Fatalf("%d %s events, want one", len(events), eventType)
	}
	var payload T
	if err := event.DecodePayload(events[0].Payload, &payload); err != nil {
		t.Fatalf("decoding %s: %v", eventType, err)
	}
	return payload
}

// Binding and unbinding an identifier each publishes a change of it.
func TestIdentifierChangesArePublished(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("binder", "")
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierPhone, "+8613812345678", admin); err != nil {
			t.Fatalf("adding: %v", err)
		}
		added := bindingEvent[authn.IdentifierChanged](t, h, authn.EventIdentifierChanged)
		if added.AccountID != id.String() || added.Kind != "phone" || added.Change != "added" {
			t.Errorf("the added event is %+v", added)
		}
		identifiers, err := h.p.Identifiers(ctx, id)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		var phoneID string
		for _, i := range identifiers {
			if i.Kind == authn.IdentifierPhone {
				phoneID = i.ID
			}
		}
		h.bus.events = nil
		if err := h.p.RemoveIdentifier(ctx, id, phoneID); err != nil {
			t.Fatalf("removing: %v", err)
		}
		removed := bindingEvent[authn.IdentifierChanged](t, h, authn.EventIdentifierChanged)
		if removed.Change != "removed" || removed.Kind != "phone" {
			t.Errorf("the removed event is %+v", removed)
		}
	})
}

// A password write publishes a change, and a rehash after a successful check is
// not one: it is the same password rewritten at the current cost.
func TestAPasswordChangeIsPublishedButARehashIsNot(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("rehasher", "")
		if err := h.p.ImportHash(ctx, id, cheapHash(t, "correct horse battery"), false); err != nil {
			t.Fatalf("importing: %v", err)
		}
		before := len(h.bus.of(authn.EventPasswordChanged))
		if before != 1 {
			t.Fatalf("%d password-change events after the import, want one", before)
		}
		// A successful check rewrites the weak entry, and that is not a change of
		// the password.
		if _, _, err := h.p.Verify(ctx, "rehasher", "correct horse battery"); err != nil {
			t.Fatalf("verifying: %v", err)
		}
		if after := len(h.bus.of(authn.EventPasswordChanged)); after != before {
			t.Errorf("the rehash published a password change: %d before, %d after", before, after)
		}
	})
}

// A membership's changes are published as they happen: added, suspended, resumed,
// and the SSO exemption set and cleared.
func TestMembershipChangesArePublished(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("shifting", "")
		changes := func() []string {
			var got []string
			for _, e := range h.bus.of(authn.EventMembershipChanged) {
				var payload authn.MembershipChanged
				if err := event.DecodePayload(e.Payload, &payload); err != nil {
					t.Fatalf("decoding: %v", err)
				}
				if e.TenantID != "acme" {
					t.Errorf("the membership event carries the tenant %q, want it in the envelope as acme", e.TenantID)
				}
				got = append(got, payload.Change)
			}
			return got
		}
		steps := []struct {
			m    authn.Membership
			want string
		}{
			{authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}, "added"},
			{authn.Membership{Account: id, Tenant: "acme", Active: false, Source: "admin"}, "suspended"},
			{authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}, "resumed"},
			{authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin", SSOExempt: true}, "sso-exempt-set"},
			{authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}, "sso-exempt-cleared"},
		}
		for i, step := range steps {
			h.bus.events = nil
			if err := h.p.Put(ctx, step.m); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			got := changes()
			if len(got) != 1 || got[0] != step.want {
				t.Errorf("step %d published %v, want %q", i, got, step.want)
			}
		}
	})
}

// A back-off that begins on an account publishes the engagement, carrying the
// account identifier.
func TestAnAccountEngagementIsPublished(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("engaged", "")
		for i := 0; i <= defaults.ThrottleFreeAttempts; i++ {
			h.p.fail(ctx, authn.ScopeAccount, "k", id)
		}
		events := h.bus.of(authn.EventThrottleEngaged)
		if len(events) != 1 {
			t.Fatalf("%d engagement events, want one", len(events))
		}
		var payload authn.ThrottleEngaged
		if err := event.DecodePayload(events[0].Payload, &payload); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if payload.Scope != string(authn.ScopeAccount) || payload.AccountID != id.String() {
			t.Errorf("the engagement is %+v, want the account scope and the identifier", payload)
		}
	})
}

// A created account's event is published to the bus and not staged in the
// outbox: it is the account's first fact and nothing has to be undone with it.
func TestACreatedEventGoesToTheBusAndNotTheOutbox(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.withOutbox()
		h.user("busgoer", "")
		if got := h.bus.of(authn.EventAccountCreated); len(got) != 1 {
			t.Errorf("%d created events on the bus, want one", len(got))
		}
		if slicesContains(h.staged(), authn.EventAccountCreated) {
			t.Error("the created event was staged in the outbox, want it published instead")
		}
	})
}
