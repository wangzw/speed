package impersonation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/event"
)

// TestEndEndsTheRecordOnTheSession checks that an explicit end writes the reason,
// the initiator and one audit row, counts and publishes, and leaves the carrier alone.
func TestEndEndsTheRecordOnTheSession(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		id := adminIdentity(t)
		g, err := r.p.Start(ctx, id, request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.audit.rows = nil

		if err := r.p.End(ctx, id, "the call is over"); err != nil {
			t.Fatalf("End: %v", err)
		}
		_, endedBy, reason := r.stored(g.ID)
		if reason == nil || *reason != authn.EndExplicit {
			t.Errorf("the end reason is %q, want explicit", reasonText(reason))
		}
		if endedBy == nil || *endedBy != string(actorID) {
			t.Errorf("the initiator is %v, want the actor's key", endedBy)
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionEnd || rows[0].Outcome != outcomeSuccess || rows[0].Purpose != "the call is over" {
			t.Errorf("the audit rows are %+v, want one end row carrying the reason", rows)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
		var payload authn.ImpersonationEnded
		events := r.bus.ofType(authn.EventImpersonationEnded)
		if len(events) != 1 {
			t.Fatalf("%d ended events, want 1", len(events))
		}
		if err := event.DecodePayload(events[0].Payload, &payload); err != nil {
			t.Fatalf("decoding the ended payload: %v", err)
		}
		if payload.GrantID != g.ID || payload.EndedBy != "actor" {
			t.Errorf("the ended payload is %+v, want the grant and the actor category", payload)
		}
		if len(r.sessions.setCalls) != 0 {
			t.Errorf("End touched the session marker: %v", r.sessions.setCalls)
		}
	})
}

// TestEndFromTheImpersonatingIdentity checks that an impersonating identity ends the record on its own session.
func TestEndFromTheImpersonatingIdentity(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := r.p.End(ctx, impersonatingIdentity(t, sessionBackend), ""); err != nil {
			t.Fatalf("End from an impersonating identity: %v", err)
		}
		if _, endedBy, _ := r.stored(g.ID); endedBy == nil || *endedBy != string(actorID) {
			t.Errorf("the initiator is %v, want the actor's key %q", endedBy, actorID)
		}
	})
}

// TestEndIsNotImpersonatingWithoutARecord checks that a session with no unended record gets ErrNotImpersonating.
func TestEndIsNotImpersonatingWithoutARecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		err := r.p.End(context.Background(), adminIdentity(t), "")
		if !errors.Is(err, authn.ErrNotImpersonating) {
			t.Fatalf("End = %v, want ErrNotImpersonating", err)
		}
	})
}

// TestEndToleratesAFailedAudit checks that a failed audit write is logged and never stops the end.
func TestEndToleratesAFailedAudit(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		id := adminIdentity(t)
		g, err := r.p.Start(ctx, id, request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.audit.err = errFromStore
		if err := r.p.End(ctx, id, ""); err != nil {
			t.Fatalf("End with a failed audit = %v, want nil", err)
		}
		_, _, reason := r.stored(g.ID)
		if reason == nil || *reason != authn.EndExplicit {
			t.Errorf("the end reason is %q, want explicit despite the failed audit", reasonText(reason))
		}
	})
}

// TestRevokeEndsByIdForceAndIsIdempotent checks that a revoke writes its reason and initiator, publishes once, and is a no-op the second time.
func TestRevokeEndsByIdForceAndIsIdempotent(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.audit.rows = nil
		by := adminIdentity(t)

		if err := r.p.Revoke(ctx, g.ID, by, "abuse reported"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		_, endedBy, reason := r.stored(g.ID)
		if reason == nil || *reason != authn.EndRevoked {
			t.Errorf("the end reason is %q, want revoked", reasonText(reason))
		}
		if endedBy == nil || *endedBy != string(actorID) {
			t.Errorf("the revoker is %v, want the revoker's key", endedBy)
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionRevoke || rows[0].Object != "account:"+string(targetID) {
			t.Errorf("the audit rows are %+v, want one revoke row naming the target as an account object", rows)
		}
		events := r.bus.ofType(authn.EventImpersonationEnded)
		if len(events) != 1 {
			t.Fatalf("%d ended events, want 1", len(events))
		}
		var payload authn.ImpersonationEnded
		if err := event.DecodePayload(events[0].Payload, &payload); err != nil {
			t.Fatalf("decoding the ended payload: %v", err)
		}
		if payload.EndedBy != "revoked" {
			t.Errorf("the ended payload's cause is %q, want revoked", payload.EndedBy)
		}

		if err := r.p.Revoke(ctx, g.ID, by, "again"); err != nil {
			t.Fatalf("the second Revoke = %v, want nil", err)
		}
		if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 1 {
			t.Errorf("%d ended events after a second revoke, want 1", got)
		}
		if len(r.audit.entries()) != 1 {
			t.Errorf("the second revoke wrote another audit row: %+v", r.audit.entries())
		}
	})
}

// TestRevokeOfALapsedRecordEndsItByItsOwnReason checks that an expired record or one whose session is gone is ended by its own reason.
func TestRevokeOfALapsedRecordEndsItByItsOwnReason(t *testing.T) {
	for _, c := range []struct {
		name      string
		mutate    func(r *rig)
		want      string
		wantEvent bool
	}{
		{"expired", func(r *rig) { r.clock.advance(31 * time.Minute) }, authn.EndExpired, false},
		{"session ended", func(r *rig) { r.sessions.session.Active = false }, authn.EndSessionEnded, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				r.audit.rows = nil
				c.mutate(r)

				if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); err != nil {
					t.Fatalf("Revoke of a lapsed record = %v, want nil", err)
				}
				_, endedBy, reason := r.stored(g.ID)
				if reason == nil || *reason != c.want {
					t.Errorf("the end reason is %q, want %q", reasonText(reason), c.want)
				}
				if endedBy != nil {
					t.Errorf("the revoke recorded an initiator %q, want none", *endedBy)
				}
				if rows := r.audit.entries(); len(rows) != 0 {
					t.Errorf("a revoke of a lapsed record wrote audit rows: %+v", rows)
				}
				events := r.bus.ofType(authn.EventImpersonationEnded)
				if c.wantEvent && len(events) != 1 {
					t.Errorf("%d ended events were published, want the lapse's own one", len(events))
				}
				if !c.wantEvent && len(events) != 0 {
					t.Errorf("an expiry published %d ended events, want none", len(events))
				}
				if c.wantEvent && r.endedPayload().EndedBy != "session-ended" {
					t.Errorf("the ended event's cause is %q, want session-ended", r.endedPayload().EndedBy)
				}
				if got := r.counter(outcomeEnded); got != 1 {
					t.Errorf("the ended counter reads %d, want 1", got)
				}
			})
		})
	}
}

// TestRevokeOfAnUnknownRecordIsNotImpersonating checks that revoking a record that is not there is ErrNotImpersonating.
func TestRevokeOfAnUnknownRecordIsNotImpersonating(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		err := r.p.Revoke(context.Background(), "no-such-record", authn.Identity{}, authn.EndRevoked)
		if !errors.Is(err, authn.ErrNotImpersonating) {
			t.Fatalf("Revoke = %v, want ErrNotImpersonating", err)
		}
	})
}

// TestRevokeByTheStatelessTokenLeavesNoInitiator checks that a revoke by a zero identity leaves ended_by empty.
func TestRevokeByTheStatelessTokenLeavesNoInitiator(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := r.p.Revoke(ctx, g.ID, authn.Identity{}, authn.EndRevoked); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if _, endedBy, _ := r.stored(g.ID); endedBy != nil {
			t.Errorf("a stateless revoke recorded an initiator %q, want none", *endedBy)
		}
	})
}

// TestListPagesByCursorAndFilters checks that List returns records in identifier order, pages by cursor and applies the actor and target filters.
func TestListPagesByCursorAndFilters(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		r.insertRecord("rec-1", actorID, targetID, tenantA)
		r.insertRecord("rec-2", actorID, targetID, tenantB)
		r.insertRecord("rec-3", "acct-other", "acct-other-target", tenantA)

		page, next, err := r.p.List(ctx, authn.GrantQuery{Limit: 2})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) != 2 || next != "rec-2" {
			t.Fatalf("the first page is %d records with cursor %q, want 2 and rec-2", len(page), next)
		}
		if page[0].ID != "rec-1" || page[1].ID != "rec-2" {
			t.Errorf("the first page is %v, want rec-1 and rec-2", page)
		}
		page2, next2, err := r.p.List(ctx, authn.GrantQuery{Limit: 2, After: next})
		if err != nil {
			t.Fatalf("the second List: %v", err)
		}
		if len(page2) != 1 || page2[0].ID != "rec-3" || next2 != "" {
			t.Errorf("the second page is %v with cursor %q, want rec-3 and none", page2, next2)
		}

		byActor, _, err := r.p.List(ctx, authn.GrantQuery{Actor: actorID})
		if err != nil {
			t.Fatalf("List by actor: %v", err)
		}
		if len(byActor) != 2 {
			t.Errorf("the actor filter returned %d records, want 2", len(byActor))
		}
		byTarget, _, err := r.p.List(ctx, authn.GrantQuery{Target: targetID})
		if err != nil {
			t.Fatalf("List by target: %v", err)
		}
		if len(byTarget) != 2 {
			t.Errorf("the target filter returned %d records, want 2", len(byTarget))
		}
		for _, rec := range byTarget {
			if rec.Target != targetID {
				t.Errorf("the target filter returned %v, want only records of the queried target", rec)
			}
		}
	})
}

// TestListReturnsNoCursorWhenThePageIsExactlyFull checks that a result of exactly the limit carries no next cursor.
func TestListReturnsNoCursorWhenThePageIsExactlyFull(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		r.insertRecord("rec-1", actorID, targetID, tenantA)
		r.insertRecord("rec-2", actorID, targetID, tenantA)

		page, next, err := r.p.List(ctx, authn.GrantQuery{Limit: 2})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) != 2 {
			t.Fatalf("the page holds %d records, want the full 2", len(page))
		}
		if next != "" {
			t.Errorf("a page of exactly the limit handed back the cursor %q, want none", next)
		}
	})
}

// TestListSettlesTheLapsedRecordsOnItsPage checks that List writes each lapsed record on the page back with its own reason.
func TestListSettlesTheLapsedRecordsOnItsPage(t *testing.T) {
	for _, c := range []struct {
		name      string
		mutate    func(r *rig)
		want      string
		wantEvent bool
	}{
		{"expired", func(r *rig) { r.clock.advance(31 * time.Minute) }, authn.EndExpired, false},
		{"session ended", func(r *rig) { r.sessions.session.Active = false }, authn.EndSessionEnded, true},
		{"the earlier cause wins", func(r *rig) {
			r.clock.advance(31 * time.Minute)
			r.sessions.session.Active = false
		}, authn.EndExpired, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				r.audit.rows = nil
				c.mutate(r)

				page, _, err := r.p.List(ctx, authn.GrantQuery{})
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if len(page) != 1 {
					t.Fatalf("the page holds %d records, want 1", len(page))
				}
				if page[0].EndedAt.IsZero() || page[0].EndReason != c.want {
					t.Errorf("List returned the record (ended at %v, reason %q), want it ended as %q",
						page[0].EndedAt, page[0].EndReason, c.want)
				}
				endedAt, endedBy, reason := r.stored(g.ID)
				if endedAt == nil || reason == nil || *reason != c.want {
					t.Errorf("the stored end is (%v, %q), want %q written back", endedAt, reasonText(reason), c.want)
				}
				if endedAt == nil || !withinASecond(page[0].EndedAt, *endedAt) {
					t.Errorf("List returned the end at %v, the store holds %v", page[0].EndedAt, endedAt)
				}
				if endedBy != nil {
					t.Errorf("a lapse wrote an initiator %q, want none", *endedBy)
				}
				if rows := r.audit.entries(); len(rows) != 0 {
					t.Errorf("List wrote audit rows: %+v", rows)
				}
				events := r.bus.ofType(authn.EventImpersonationEnded)
				if c.wantEvent && len(events) != 1 {
					t.Errorf("%d ended events were published, want 1", len(events))
				}
				if !c.wantEvent && len(events) != 0 {
					t.Errorf("an expiry published %d ended events, want none", len(events))
				}
				if c.wantEvent && r.endedPayload().EndedBy != "session-ended" {
					t.Errorf("the ended event's cause is %q, want session-ended", r.endedPayload().EndedBy)
				}
				if got := r.counter(outcomeEnded); got != 1 {
					t.Errorf("the ended counter reads %d, want 1", got)
				}

				if _, _, err := r.p.List(ctx, authn.GrantQuery{}); err != nil {
					t.Fatalf("the second List: %v", err)
				}
				if got := r.counter(outcomeEnded); got != 1 {
					t.Errorf("a second List counted the end again: %d", got)
				}
				if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != len(events) {
					t.Errorf("a second List published another ended event: %d then %d", len(events), got)
				}
				if again, _, _ := r.stored(g.ID); again == nil || !again.Equal(*endedAt) {
					t.Errorf("a second List rewrote the end: %v then %v", endedAt, again)
				}
			})
		})
	}
}

// TestListLeavesAValidRecordAlone checks that a record still in force is returned as stored, with nothing written, counted or published.
func TestListLeavesAValidRecordAlone(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.audit.rows = nil

		page, _, err := r.p.List(ctx, authn.GrantQuery{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) != 1 || page[0].ID != g.ID {
			t.Fatalf("the page is %v, want the one record %s", page, g.ID)
		}
		if !page[0].EndedAt.IsZero() || page[0].EndReason != "" {
			t.Errorf("a valid record was returned ended: %+v", page[0])
		}
		if endedAt, _, _ := r.stored(g.ID); endedAt != nil {
			t.Errorf("a valid record was written back ended: %v", *endedAt)
		}
		if got := r.counter(outcomeEnded); got != 0 {
			t.Errorf("the ended counter reads %d, want 0", got)
		}
		if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 0 {
			t.Errorf("%d ended events were published, want none", got)
		}
	})
}

// TestListDoesNotJudgeThroughTheCheckTTLCache checks that a page is judged from the store and not from the cached verification answer.
func TestListDoesNotJudgeThroughTheCheckTTLCache(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(r *rig)
		want   string
	}{
		{"expired after the cache was warmed", func(r *rig) { r.clock.advance(31 * time.Minute) }, authn.EndExpired},
		{"the bound session ended after the cache was warmed", func(r *rig) { r.sessions.session.Active = false }, authn.EndSessionEnded},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}

				r.p.sessions = newCachingSessions(r.sessions, time.Minute, r.clock.read)

				if _, active, aerr := r.p.Active(ctx, g.ID); aerr != nil || !active {
					t.Fatalf("warming the cache: Active = (active %v, err %v), want the valid record", active, aerr)
				}
				c.mutate(r)

				page, _, err := r.p.List(ctx, authn.GrantQuery{})
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if len(page) != 1 {
					t.Fatalf("the page holds %d records, want 1", len(page))
				}
				if page[0].EndedAt.IsZero() || page[0].EndReason != c.want {
					t.Errorf("List returned the record (ended at %v, reason %q), want it ended as %q from the store",
						page[0].EndedAt, page[0].EndReason, c.want)
				}
				if _, _, reason := r.stored(g.ID); reason == nil || *reason != c.want {
					t.Errorf("the stored end reason is %q, want %q", reasonText(reason), c.want)
				}
			})
		})
	}
}

// TestListSettlesOnceWhenTwoReplicasJudgeTheSameRecord checks that two replicas
// listing one lapsed record settle it once: only the winning update publishes and counts.
func TestListSettlesOnceWhenTwoReplicasJudgeTheSameRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.audit.rows = nil
		r.sessions.session.Active = false

		var (
			mu      sync.Mutex
			arrived int
			release = make(chan struct{})
		)
		r.sessions.onStatus = func() {
			mu.Lock()
			arrived++
			first := arrived == 1
			if arrived == 2 {
				close(release)
			}
			mu.Unlock()
			if first {
				<-release
			}
		}

		replicas := []*product{r.p, r.replica()}
		var wg sync.WaitGroup
		errs := make([]error, len(replicas))
		pages := make([][]authn.ImpersonationGrant, len(replicas))
		for i, p := range replicas {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pages[i], _, errs[i] = p.List(ctx, authn.GrantQuery{})
			}()
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("replica %d: List = %v, want nil", i, err)
			}
		}
		for i, page := range pages {
			if len(page) != 1 || page[0].EndReason != authn.EndSessionEnded {
				t.Errorf("replica %d returned %v, want the record ended as session-ended", i, page)
			}
		}
		if _, _, reason := r.stored(g.ID); reason == nil || *reason != authn.EndSessionEnded {
			t.Errorf("the stored end reason is %q, want session-ended", reasonText(reason))
		}
		if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 1 {
			t.Errorf("the race published %d ended events, want 1", got)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
	})
}

// TestEndIsWrittenOnceAndOnlyOnce checks that two concurrent ends of one record write one audit row, one event and one count.
func TestEndIsWrittenOnceAndOnlyOnce(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		id := adminIdentity(t)
		if _, err := r.p.Start(ctx, id, request()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.audit.rows = nil

		var (
			mu      sync.Mutex
			arrived int
			release = make(chan struct{})
		)
		r.p.clock = func() time.Time {
			mu.Lock()
			arrived++
			if arrived == 2 {
				close(release)
			}
			mu.Unlock()
			<-release
			return base
		}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = r.p.End(ctx, id, "")
			}()
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				t.Errorf("a concurrent End returned %v, want nil", err)
			}
		}
		if got := len(r.audit.entries()); got != 1 {
			t.Errorf("the race wrote %d end audit rows, want 1", got)
		}
		if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 1 {
			t.Errorf("the race published %d ended events, want 1", got)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
	})
}

// TestListSettlesEveryLapsedRowOfAPage checks that every unended record on a page is judged and written back, not only the first or last.
func TestListSettlesEveryLapsedRowOfAPage(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		ids := []string{"rec-1", "rec-2", "rec-3"}
		for _, id := range ids {
			r.insertRecord(id, actorID, targetID, tenantA)
		}
		r.sessions.session.Active = false

		page, _, err := r.p.List(ctx, authn.GrantQuery{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) != len(ids) {
			t.Fatalf("the page holds %d records, want %d", len(page), len(ids))
		}
		for _, g := range page {
			if g.EndedAt.IsZero() || g.EndReason != authn.EndSessionEnded {
				t.Errorf("%s was returned (ended at %v, reason %q), want session-ended", g.ID, g.EndedAt, g.EndReason)
			}
		}
		for _, id := range ids {
			if endedAt, _, reason := r.stored(id); endedAt == nil || reason == nil || *reason != authn.EndSessionEnded {
				t.Errorf("%s was stored (ended at %v, reason %q), want session-ended", id, endedAt, reasonText(reason))
			}
		}
		if got := r.counter(outcomeEnded); got != int64(len(ids)) {
			t.Errorf("the ended counter reads %d, want %d", got, len(ids))
		}
		if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != len(ids) {
			t.Errorf("%d ended events were published, want %d", got, len(ids))
		}
	})
}

// TestListJudgesEachRowsOwnMembership checks that a page judges each row's own membership whichever order the rows are read in.
func TestListJudgesEachRowsOwnMembership(t *testing.T) {
	for _, c := range []struct {
		name      string
		memberID  string
		removedID string
	}{
		{"the member is judged first", "rec-1", "rec-2"},
		{"the removed member is judged first", "rec-2", "rec-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				r.accounts.put(target2ID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
				r.memberships.set(target2ID, tenantA, false)
				r.insertRecord(c.memberID, actorID, targetID, tenantA)
				r.insertRecord(c.removedID, actorID, target2ID, tenantA)

				page, _, err := r.p.List(ctx, authn.GrantQuery{})
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if len(page) != 2 {
					t.Fatalf("the page holds %d records, want 2", len(page))
				}
				for _, g := range page {
					switch g.ID {
					case c.memberID:
						if !g.EndedAt.IsZero() {
							t.Errorf("%s, whose target is a member, was ended as %q", g.ID, g.EndReason)
						}
					case c.removedID:
						if g.EndedAt.IsZero() || g.EndReason != authn.EndMembershipRemoved {
							t.Errorf("%s was returned (ended at %v, reason %q), want membership-removed", g.ID, g.EndedAt, g.EndReason)
						}
					}
				}
				if got := r.counter(outcomeEnded); got != 1 {
					t.Errorf("the ended counter reads %d, want 1", got)
				}
				if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 1 {
					t.Errorf("%d ended events were published, want 1", got)
				}
			})
		})
	}
}

// TestAPageReadsSharedAnswersOnce checks that rows sharing an actor, target and
// tenant are judged from one read of each, while the bound session is read per record.
func TestAPageReadsSharedAnswersOnce(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		for _, id := range []string{"rec-1", "rec-2", "rec-3"} {
			r.insertRecord(id, actorID, targetID, tenantA)
		}
		r.accounts.resetReads()
		r.memberships.resetReads()

		page, _, err := r.p.List(ctx, authn.GrantQuery{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) != 3 {
			t.Fatalf("the page holds %d records, want 3", len(page))
		}
		for _, g := range page {
			if !g.EndedAt.IsZero() {
				t.Errorf("%s was ended as %q, want it left alone", g.ID, g.EndReason)
			}
		}
		if got := r.accounts.readCount(); got != 2 {
			t.Errorf("the page read %d accounts, want 2 for the shared actor and target", got)
		}
		if got := r.memberships.readCount(); got != 1 {
			t.Errorf("the page read %d memberships, want 1 for the shared pair", got)
		}
	})
}

// TestTheJudgeDoesNotOutliveTheListCall checks that each call judges from its own moment rather than from a judge held across calls.
func TestTheJudgeDoesNotOutliveTheListCall(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, _, err = r.p.List(ctx, authn.GrantQuery{}); err != nil {
			t.Fatalf("the first List: %v", err)
		}

		r.memberships.set(targetID, tenantA, false)
		page, _, err := r.p.List(ctx, authn.GrantQuery{})
		if err != nil {
			t.Fatalf("the second List: %v", err)
		}
		if len(page) != 1 || page[0].EndedAt.IsZero() || page[0].EndReason != authn.EndMembershipRemoved {
			t.Errorf("the second page is %+v, want %s ended as membership-removed", page, g.ID)
		}
	})
}

// TestListLeavesEndedRecordsOutOfTheJudgement checks that already ended records
// are not judged again, so a failed dependency read leaves the page intact and Revoke is a no-op.
func TestListLeavesEndedRecordsOutOfTheJudgement(t *testing.T) {
	for _, c := range []struct {
		name   string
		inject func(r *rig)
	}{
		{"the account store", func(r *rig) { r.accounts.err = errFromStore }},
		{"the session store", func(r *rig) { r.sessions.statusErr = errFromStore }},
		{"the membership store", func(r *rig) { r.memberships.err = errFromStore }},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				id := adminIdentity(t)
				g, err := r.p.Start(ctx, id, request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				if err = r.p.End(ctx, id, "the call is over"); err != nil {
					t.Fatalf("End: %v", err)
				}
				r.audit.rows = nil
				events := len(r.bus.ofType(authn.EventImpersonationEnded))

				c.inject(r)
				page, _, err := r.p.List(ctx, authn.GrantQuery{})
				if err != nil {
					t.Fatalf("List with a failed dependency read = %v, want the ended page", err)
				}
				if len(page) != 1 || page[0].EndReason != authn.EndExplicit {
					t.Errorf("the page is %+v, want the record as explicit", page)
				}
				if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != events {
					t.Errorf("List published %d ended events, want the %d already published", got, events)
				}

				if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); err != nil {
					t.Errorf("Revoke of an ended record with a failed dependency read = %v, want nil", err)
				}
				if _, _, reason := r.stored(g.ID); reason == nil || *reason != authn.EndExplicit {
					t.Errorf("the end reason is %q, want explicit left alone", reasonText(reason))
				}
				if rows := r.audit.entries(); len(rows) != 0 {
					t.Errorf("Revoke of an ended record wrote audit rows: %+v", rows)
				}
				if got := r.counter(outcomeEnded); got != 1 {
					t.Errorf("the ended counter reads %d, want the 1 from End", got)
				}
			})
		})
	}
}

// TestListReturnsUnavailableWhenADependencyFails checks that a failed dependency read fails the whole page and leaves the record unended.
func TestListReturnsUnavailableWhenADependencyFails(t *testing.T) {
	for _, c := range []struct {
		name   string
		inject func(r *rig)
	}{
		{"the bound session", func(r *rig) { r.sessions.statusErr = errFromStore }},
		{"the target account", func(r *rig) { r.accounts.failFor(targetID, errFromStore) }},
		{"the membership", func(r *rig) { r.memberships.err = errFromStore }},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				c.inject(r)

				page, next, err := r.p.List(ctx, authn.GrantQuery{})
				if !errors.Is(err, authn.ErrUnavailable) {
					t.Fatalf("List = %v, want ErrUnavailable", err)
				}
				if page != nil || next != "" {
					t.Errorf("a failed page returned %v with cursor %q, want none", page, next)
				}
				if endedAt, _, _ := r.stored(g.ID); endedAt != nil {
					t.Errorf("a failed page ended the record: %v", *endedAt)
				}
				if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 0 {
					t.Errorf("a failed page published %d ended events, want none", got)
				}
			})
		})
	}
}

// TestListKeepsTheRowsItAlreadySettledWhenALaterRowFails checks that a row settled
// before a later row fails stays settled, is announced once, and the retry completes the page.
func TestListKeepsTheRowsItAlreadySettledWhenALaterRowFails(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		r.accounts.put(target2ID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		r.memberships.set(target2ID, tenantA, true)
		r.insertRecord("rec-1", actorID, targetID, tenantA)
		r.insertRecord("rec-2", actorID, target2ID, tenantA)
		sessions := newPerSessionSessions("sess-rec-2")
		sessions.setActive("sess-rec-1", false)
		r.p.sessions = sessions
		r.accounts.failFor(target2ID, errFromStore)

		page, _, err := r.p.List(ctx, authn.GrantQuery{})
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("List = %v, want ErrUnavailable", err)
		}
		if page != nil {
			t.Errorf("a failed page returned %v, want none", page)
		}
		if _, _, reason := r.stored("rec-1"); reason == nil || *reason != authn.EndSessionEnded {
			t.Errorf("the row the page settled before the failure is %q, want session-ended", reasonText(reason))
		}
		if endedAt, _, _ := r.stored("rec-2"); endedAt != nil {
			t.Errorf("the failing row was ended: %v", *endedAt)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
		events := len(r.bus.ofType(authn.EventImpersonationEnded))
		if events != 1 {
			t.Errorf("%d ended events were published, want 1", events)
		}

		r.accounts.failFor(target2ID, nil)
		page, _, err = r.p.List(ctx, authn.GrantQuery{})
		if err != nil {
			t.Fatalf("the retried List: %v", err)
		}
		if len(page) != 2 {
			t.Errorf("the retried page holds %d records, want 2", len(page))
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the retry counted the settled row again: %d", got)
		}
		if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != events {
			t.Errorf("the retry published another ended event: %d then %d", events, got)
		}
	})
}

// TestAPageReportsTheEndAnotherCallerWrote checks that a caller whose conditional
// update lost reports the end that landed and counts and publishes nothing.
func TestAPageReportsTheEndAnotherCallerWrote(t *testing.T) {
	overwrite := func(t *testing.T, r *rig, id string) {
		t.Helper()
		r.sessions.onStatus = func() {
			err := r.h.Exec("UPDATE "+grantTable+" SET ended_at = ?, ended_by = ?, end_reason = ? WHERE id = ? AND ended_at IS NULL",
				base.UTC(), string(actorID), authn.EndRevoked, id).Error
			if err != nil {
				t.Errorf("writing the competing end: %v", err)
			}
		}
	}

	t.Run("List", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			ctx := context.Background()
			g, err := r.p.Start(ctx, adminIdentity(t), request())
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			r.sessions.session.Active = false
			overwrite(t, r, g.ID)

			page, _, err := r.p.List(ctx, authn.GrantQuery{})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(page) != 1 {
				t.Fatalf("the page holds %d records, want 1", len(page))
			}
			if page[0].EndReason != authn.EndRevoked || page[0].EndedBy != string(actorID) {
				t.Errorf("the page reports (%q, %q), want the end that landed (%q, %q)",
					page[0].EndReason, page[0].EndedBy, authn.EndRevoked, actorID)
			}
			if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 0 {
				t.Errorf("the losing caller published %d ended events, want none", got)
			}
			if got := r.counter(outcomeEnded); got != 0 {
				t.Errorf("the losing caller counted %d ends, want none", got)
			}
		})
	})

	t.Run("Revoke", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			ctx := context.Background()
			g, err := r.p.Start(ctx, adminIdentity(t), request())
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			r.audit.rows = nil
			r.sessions.session.Active = false
			overwrite(t, r, g.ID)

			if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); err != nil {
				t.Fatalf("Revoke = %v, want nil: another caller ended the record", err)
			}
			if _, _, reason := r.stored(g.ID); reason == nil || *reason != authn.EndRevoked {
				t.Errorf("the end reason is %q, want the end that landed", reasonText(reason))
			}
			if rows := r.audit.entries(); len(rows) != 0 {
				t.Errorf("the losing revoke wrote audit rows: %+v", rows)
			}
			if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 0 {
				t.Errorf("the losing revoke published %d ended events, want none", got)
			}
			if got := r.counter(outcomeEnded); got != 0 {
				t.Errorf("the losing revoke counted %d ends, want none", got)
			}
		})
	})
}

// TestRevokeReturnsUnavailableWhenADependencyFails checks that a failed dependency
// read is ErrUnavailable and leaves the record unended with no audit row or event.
func TestRevokeReturnsUnavailableWhenADependencyFails(t *testing.T) {
	for _, c := range []struct {
		name   string
		inject func(r *rig)
	}{
		{"the bound session", func(r *rig) { r.sessions.statusErr = errFromStore }},
		{"the target account", func(r *rig) { r.accounts.failFor(targetID, errFromStore) }},
		{"the membership", func(r *rig) { r.memberships.err = errFromStore }},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				r.audit.rows = nil
				c.inject(r)

				if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); !errors.Is(err, authn.ErrUnavailable) {
					t.Fatalf("Revoke = %v, want ErrUnavailable", err)
				}
				if endedAt, endedBy, reason := r.stored(g.ID); endedAt != nil || endedBy != nil || reason != nil {
					t.Errorf("a failed revoke wrote the record ended: (%v, %v, %q)", endedAt, endedBy, reasonText(reason))
				}
				if rows := r.audit.entries(); len(rows) != 0 {
					t.Errorf("a failed revoke wrote audit rows: %+v", rows)
				}
				if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 0 {
					t.Errorf("a failed revoke published %d ended events, want none", got)
				}
				if got := r.counter(outcomeEnded); got != 0 {
					t.Errorf("a failed revoke counted %d ends, want none", got)
				}
			})
		})
	}
}

// TestRevokeDoesNotJudgeThroughTheCheckTTLCache checks that Revoke judges the bound
// session from the store, so a cached live answer cannot hide a dead session.
func TestRevokeDoesNotJudgeThroughTheCheckTTLCache(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.p.sessions = newCachingSessions(r.sessions, time.Minute, r.clock.read)

		if _, active, err := r.p.Active(ctx, g.ID); err != nil || !active {
			t.Fatalf("warming the cache: Active = (active %v, err %v), want the valid record", active, err)
		}
		r.audit.rows = nil
		r.sessions.session.Active = false

		if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		_, endedBy, reason := r.stored(g.ID)
		if reason == nil || *reason != authn.EndSessionEnded {
			t.Errorf("the end reason is %q, want session-ended from the store", reasonText(reason))
		}
		if endedBy != nil {
			t.Errorf("a lapsed revoke wrote an initiator %q, want none", *endedBy)
		}
		if rows := r.audit.entries(); len(rows) != 0 {
			t.Errorf("a lapsed revoke wrote audit rows: %+v", rows)
		}
		if got := r.endedPayload().EndedBy; got != "session-ended" {
			t.Errorf("the ended event's cause is %q, want session-ended", got)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
	})
}

// TestRevokeIsWrittenOnceAndOnlyOnce checks that two concurrent revokes write one audit row, one event and one count.
func TestRevokeIsWrittenOnceAndOnlyOnce(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.audit.rows = nil
		by := adminIdentity(t)

		var (
			mu      sync.Mutex
			arrived int
			release = make(chan struct{})
		)
		r.p.clock = func() time.Time {
			mu.Lock()
			arrived++
			if arrived == 2 {
				close(release)
			}
			mu.Unlock()
			<-release
			return base
		}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = r.p.Revoke(ctx, g.ID, by, "abuse reported")
			}()
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				t.Errorf("a concurrent Revoke returned %v, want nil", err)
			}
		}
		if _, _, reason := r.stored(g.ID); reason == nil || *reason != authn.EndRevoked {
			t.Errorf("the end reason is %q, want revoked", reasonText(reason))
		}
		if got := len(r.audit.entries()); got != 1 {
			t.Errorf("the race wrote %d revoke audit rows, want 1", got)
		}
		if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 1 {
			t.Errorf("the race published %d ended events, want 1", got)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
	})
}

// TestTheEndIsWrittenAtTheInstantItIsJudged checks that every write path stamps the end with the instant it judged.
func TestTheEndIsWrittenAtTheInstantItIsJudged(t *testing.T) {
	for _, c := range []struct {
		name  string
		judge func(t *testing.T, r *rig, g authn.ImpersonationGrant)
	}{
		{
			name: "Active",
			judge: func(t *testing.T, r *rig, g authn.ImpersonationGrant) {
				r.clock.advance(31*time.Minute + 17*time.Second)
				if _, active, err := r.p.Active(context.Background(), g.ID); err != nil || active {
					t.Fatalf("Active = (active %v, err %v), want the lapse", active, err)
				}
			},
		},
		{
			name: "List",
			judge: func(t *testing.T, r *rig, _ authn.ImpersonationGrant) {
				r.clock.advance(31*time.Minute + 17*time.Second)
				if _, _, err := r.p.List(context.Background(), authn.GrantQuery{}); err != nil {
					t.Fatalf("List: %v", err)
				}
			},
		},
		{
			name: "End",
			judge: func(t *testing.T, r *rig, _ authn.ImpersonationGrant) {
				r.clock.advance(5*time.Minute + 17*time.Second)
				if err := r.p.End(context.Background(), adminIdentity(t), "the call is over"); err != nil {
					t.Fatalf("End: %v", err)
				}
			},
		},
		{
			name: "Revoke",
			judge: func(t *testing.T, r *rig, g authn.ImpersonationGrant) {
				r.clock.advance(5*time.Minute + 17*time.Second)
				if err := r.p.Revoke(context.Background(), g.ID, adminIdentity(t), "abuse reported"); err != nil {
					t.Fatalf("Revoke: %v", err)
				}
			},
		},
		{
			name: "Start settling a lapsed record",
			judge: func(t *testing.T, r *rig, _ authn.ImpersonationGrant) {
				r.clock.advance(31*time.Minute + 17*time.Second)
				id := adminIdentity(t, authntest.WithAuthTime(r.clock.read()))
				if _, err := r.p.Start(context.Background(), id, request()); err != nil {
					t.Fatalf("the second Start: %v", err)
				}
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				c.judge(t, r, g)
				endedAt, _, _ := r.stored(g.ID)
				if endedAt == nil {
					t.Fatal("the record was not ended")
				}
				if want := r.clock.read(); !withinASecond(*endedAt, want) {
					t.Errorf("the end was written at %v, want the instant it was judged %v", *endedAt, want)
				}
			})
		})
	}
}

func (r *rig) insertRecord(id string, actor, target authn.AccountID, tenant string) {
	r.t.Helper()
	err := r.h.Exec("INSERT INTO "+grantTable+
		" (id, actor_id, target_id, tenant_id, session_id, reason, started_at, ends_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, string(actor), string(target), tenant, "sess-"+id, "r", base.UTC(), base.Add(time.Minute).UTC()).Error
	if err != nil {
		r.t.Fatalf("inserting a record: %v", err)
	}
}
