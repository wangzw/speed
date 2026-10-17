package impersonation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// TestEndEndsTheRecordOnTheSession holds the explicit end: the reason explicit,
// the initiator the caller's key, a best-effort audit row, the ended outcome
// counted, the ended event published with the cause category, and no carrier
// touched.
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
			t.Errorf("the end reason is %v, want explicit", reason)
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

// TestEndFromTheImpersonatingIdentity holds the path the delete route takes: the
// caller is the impersonating identity, whose session is the actor's, and the
// record is found on it. The initiator is the administrator's key, not the
// target's account.
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

// TestEndIsNotImpersonatingWithoutARecord holds the sentinel for a session with
// no unended record.
func TestEndIsNotImpersonatingWithoutARecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		err := r.p.End(context.Background(), adminIdentity(t), "")
		if !errors.Is(err, authn.ErrNotImpersonating) {
			t.Fatalf("End = %v, want ErrNotImpersonating", err)
		}
	})
}

// TestEndToleratesAFailedAudit holds the safe direction: a failed audit write is
// logged and never stops the end.
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
			t.Errorf("the end reason is %v, want explicit despite the failed audit", reason)
		}
	})
}

// TestRevokeEndsByIdForceAndIsIdempotent holds Revoke: the reason revoked, the
// initiator the revoker's key, one event, and a second call a no-op.
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
			t.Errorf("the end reason is %v, want revoked", reason)
		}
		if endedBy == nil || *endedBy != string(actorID) {
			t.Errorf("the revoker is %v, want the revoker's key", endedBy)
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionRevoke {
			t.Errorf("the audit rows are %+v, want one revoke row", rows)
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

		// Idempotent: a second revoke changes nothing and publishes nothing.
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

// TestRevokeOfALapsedRecordEndsItByItsOwnReason holds the judgement before the
// write: a record that is expired, or whose bound session is gone, is not
// stamped revoked. It ends with its own reason and no initiator, the revoke
// writes no revoke audit row, and the only ended event is the one the lapse
// owns, which an expiry does not publish.
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
					t.Errorf("the end reason is %v, want %q", reason, c.want)
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

// TestRevokeOfAnUnknownRecordIsNotImpersonating holds the sentinel, and that a
// revoke by the stateless token path leaves no initiator.
func TestRevokeOfAnUnknownRecordIsNotImpersonating(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		err := r.p.Revoke(context.Background(), "no-such-record", authn.Identity{}, authn.EndRevoked)
		if !errors.Is(err, authn.ErrNotImpersonating) {
			t.Fatalf("Revoke = %v, want ErrNotImpersonating", err)
		}
	})
}

// TestRevokeByTheStatelessTokenLeavesNoInitiator holds the shape the token
// module's revocation uses: a zero identity, so ended_by is left empty.
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

// TestListPagesByCursorAndFilters holds List: records come in identifier order,
// a page is bounded by the cursor, and the actor and target filters select. The
// third record carries another actor and another target, so that dropping either
// filter changes the result.
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

// TestListReturnsNoCursorWhenThePageIsExactlyFull holds the page boundary: a
// result of exactly the limit is the whole page and has no next page, so the
// cursor is empty.
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

// TestListSettlesTheLapsedRecordsOnItsPage holds the judgement before the
// return: an unended record on the page that has lapsed is written back with its
// own reason and the page carries the record as persisted. The reason follows
// the closed order, so a record that has both expired and lost its session ends
// expired. A second list writes nothing more.
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
					t.Errorf("the stored end is (%v, %v), want %q written back", endedAt, reason, c.want)
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

				// The record is settled, so a second page judges nothing and owns
				// nothing.
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

// TestListLeavesAValidRecordAlone holds the other side: a record still in force
// is returned as the store holds it, with nothing written, counted or published.
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

// TestListDoesNotJudgeThroughTheCheckTTLCache holds that a page is judged from
// the store and not from the answer the verification hot path has cached. Active
// warms the fake's check-ttl entry while the record is valid; the record then
// lapses and the page is listed anyway. A judgement served from the cached entry
// would call the record in force.
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
				// The check-ttl entry the verification hot path would serve.
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
					t.Errorf("the stored end reason is %v, want %q", reason, c.want)
				}
			})
		})
	}
}

// TestListSettlesOnceWhenTwoReplicasJudgeTheSameRecord holds the conditional
// update as the arbiter under a race: two replicas list the same lapsed record
// and both reach the update, and the one whose update changes the row owns the
// event and the count. The bound session has ended, so the winner publishes.
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
			t.Errorf("the stored end reason is %v, want session-ended", reason)
		}
		if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 1 {
			t.Errorf("the race published %d ended events, want 1", got)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
	})
}

// TestEndIsWrittenOnceAndOnlyOnce holds the conditional update as the arbiter
// under a race: two ends that both read the record unended reach the update
// together, and the loser writes nothing. A rendezvous in the clock parks each
// end between its read and its write, so both updates run against the row and
// the disagreement is exercised rather than raced for.
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

// insertRecord writes a record directly, the way another replica would.
func (r *rig) insertRecord(id string, actor, target authn.AccountID, tenant string) {
	r.t.Helper()
	err := r.h.Exec("INSERT INTO "+grantTable+
		" (id, actor_id, target_id, tenant_id, session_id, reason, started_at, ends_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, string(actor), string(target), tenant, "sess-"+id, "r", base.UTC(), base.Add(time.Minute).UTC()).Error
	if err != nil {
		r.t.Fatalf("inserting a record: %v", err)
	}
}
