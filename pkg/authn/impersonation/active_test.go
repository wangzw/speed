package impersonation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestActiveHoldsForAHealthyRecord checks that a record with an unexpired end, a
// live session, enabled actor and target and a member target is active and unwritten.
func TestActiveHoldsForAHealthyRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		got, active, err := r.p.Active(ctx, g.ID)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if !active || got.ID != g.ID {
			t.Errorf("Active = (%+v, %v), want the record active", got, active)
		}
		if ended, _, _ := r.stored(g.ID); ended != nil {
			t.Errorf("a valid record was written back ended: %v", *ended)
		}
	})
}

// TestActiveLapsesForEachReason checks that each lapse cause makes Active false,
// writes its reason back with no initiator, and publishes the ended event except for an expiry.
func TestActiveLapsesForEachReason(t *testing.T) {
	for _, c := range []struct {
		name        string
		mutate      func(r *rig)
		want        string
		wantEvent   bool
		wantEndedBy string
	}{
		{"expired", func(r *rig) { r.clock.advance(31 * time.Minute) }, authn.EndExpired, false, ""},
		{"session ended", func(r *rig) { r.sessions.session.Active = false }, authn.EndSessionEnded, true, "session-ended"},
		{"session gone", func(r *rig) { r.sessions.statusErr = authn.ErrSessionNotFound }, authn.EndSessionEnded, true, "session-ended"},
		{"actor disabled", func(r *rig) {
			r.accounts.put(actorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
		}, authn.EndActorDisabled, true, "disabled"},
		{"actor gone", func(r *rig) { r.accounts.remove(actorID) }, authn.EndActorDisabled, true, "disabled"},
		{"target disabled", func(r *rig) {
			r.accounts.put(targetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
		}, authn.EndTargetDisabled, true, "disabled"},
		{"target gone", func(r *rig) { r.accounts.remove(targetID) }, authn.EndTargetDisabled, true, "disabled"},
		{"membership removed", func(r *rig) { r.memberships.set(targetID, tenantA, false) }, authn.EndMembershipRemoved, true, "membership-removed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				c.mutate(r)

				got, active, err := r.p.Active(ctx, g.ID)
				if err != nil {
					t.Fatalf("Active: %v", err)
				}
				if active {
					t.Fatal("a lapsed record reads as active")
				}
				endedAt, endedBy, reason := r.stored(g.ID)
				if endedAt == nil || !withinASecond(got.EndedAt, *endedAt) {
					t.Errorf("Active returned the end at %v, the store holds %v", got.EndedAt, endedAt)
				}
				if reason == nil || *reason != c.want {
					t.Errorf("the end reason is %q, want %q", reasonText(reason), c.want)
				}
				if endedBy != nil {
					t.Errorf("a lapse wrote an initiator %q, want none", *endedBy)
				}
				events := r.bus.ofType(authn.EventImpersonationEnded)
				if c.wantEvent && len(events) != 1 {
					t.Errorf("%d ended events were published, want 1", len(events))
				}
				if !c.wantEvent && len(events) != 0 {
					t.Errorf("an expiry published %d ended events, want none", len(events))
				}
				if c.wantEvent {
					if got := r.endedPayload().EndedBy; got != c.wantEndedBy {
						t.Errorf("the ended event's cause is %q, want %q", got, c.wantEndedBy)
					}
				}
			})
		})
	}
}

// TestActiveIsFalseForAnEndedRecord checks that an already ended record reads as false at once and is not written again.
func TestActiveIsFalseForAnEndedRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		id := adminIdentity(t)
		g, err := r.p.Start(ctx, id, request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if endErr := r.p.End(ctx, id, "the call is over"); endErr != nil {
			t.Fatalf("End: %v", endErr)
		}
		endedAt, _, _ := r.stored(g.ID)
		events := len(r.bus.ofType(authn.EventImpersonationEnded))

		got, active, err := r.p.Active(ctx, g.ID)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if active {
			t.Errorf("an ended record reads as active: %+v", got)
		}
		if again, _, _ := r.stored(g.ID); again == nil || endedAt == nil || !again.Equal(*endedAt) {
			t.Errorf("Active rewrote an ended record: %v then %v", endedAt, again)
		}
		if len(r.bus.ofType(authn.EventImpersonationEnded)) != events {
			t.Error("Active published an ended event for an already ended record")
		}
	})
}

// TestActiveIsFalseWhenTheEndArrivesExactly checks that an end time equal to the current instant has lapsed.
func TestActiveIsFalseWhenTheEndArrivesExactly(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}

		r.clock.advance(30 * time.Minute)
		_, active, err := r.p.Active(ctx, g.ID)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if active {
			t.Error("a record whose end time equals the instant still reads as active")
		}
		if _, _, reason := r.stored(g.ID); reason == nil || *reason != authn.EndExpired {
			t.Errorf("the end reason is %q, want expired", reasonText(reason))
		}
	})
}

// TestALapseIsWrittenOnceAndOnlyOnce checks that a second read of the same lapsed record writes nothing and leaves the first end date.
func TestALapseIsWrittenOnceAndOnlyOnce(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.clock.advance(31 * time.Minute)

		if _, active, err := r.p.Active(ctx, g.ID); err != nil || active {
			t.Fatalf("the first Active = (active %v, err %v), want a lapse", active, err)
		}
		first, _, _ := r.stored(g.ID)
		r.clock.advance(time.Minute)
		if _, active, err := r.p.Active(ctx, g.ID); err != nil || active {
			t.Fatalf("the second Active = (active %v, err %v), want a lapse", active, err)
		}
		second, _, _ := r.stored(g.ID)
		if first == nil || second == nil || !first.Equal(*second) {
			t.Errorf("the end was rewritten: %v then %v", first, second)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
	})
}

// TestASessionCanStartAgainAfterALapse checks that the same session can start another record once the first has lapsed.
func TestASessionCanStartAgainAfterALapse(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		first, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("the first Start: %v", err)
		}
		r.clock.advance(31 * time.Minute)
		if _, active, aerr := r.p.Active(ctx, first.ID); aerr != nil || active {
			t.Fatalf("the lapse was not read: (active %v, err %v)", active, aerr)
		}

		second, err := r.p.Start(ctx, adminIdentity(t, authntest.WithAuthTime(r.clock.read())), request())
		if err != nil {
			t.Fatalf("the second Start after a lapse: %v", err)
		}
		if second.ID == first.ID {
			t.Error("the second start reused the first record")
		}
		if got := r.recordCount(); got != 2 {
			t.Errorf("the table holds %d records, want the ended one and the new one", got)
		}
	})
}

// TestStartEndsALapsedRecordInItsOwnTransaction checks that a start ends its
// session's lapsed unended record in its own transaction before inserting the new one.
func TestStartEndsALapsedRecordInItsOwnTransaction(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		first, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("the first Start: %v", err)
		}

		r.clock.advance(31 * time.Minute)

		second, err := r.p.Start(ctx, adminIdentity(t, authntest.WithAuthTime(r.clock.read())), request())
		if err != nil {
			t.Fatalf("the second Start: %v", err)
		}
		_, _, reason := r.stored(first.ID)
		if reason == nil || *reason != authn.EndExpired {
			t.Errorf("the lapsed record's end reason is %q, want expired", reasonText(reason))
		}
		if second.ID == first.ID {
			t.Error("the second start reused the first record")
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
		if events := r.bus.ofType(authn.EventImpersonationEnded); len(events) != 0 {
			t.Errorf("an expiry published %d ended events, want none", len(events))
		}
		endedAt, _, _ := r.stored(first.ID)
		if endedAt == nil || !withinASecond(*endedAt, r.clock.read()) {
			t.Errorf("the lapse was written at %v, want the instant it was judged %v", endedAt, r.clock.read())
		}
	})
}

// TestActiveReturnsFalseForAnUnknownRecord checks that a record that is not there reads as false and is never written.
func TestActiveReturnsFalseForAnUnknownRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		_, active, err := r.p.Active(context.Background(), "no-such-record")
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if active {
			t.Error("an unknown record reads as active")
		}
	})
}

// TestActiveReturnsAnErrorWhenADependencyFails checks that a dependency that cannot answer is an error and never a valid record.
func TestActiveReturnsAnErrorWhenADependencyFails(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.accounts.err = errFromStore
		_, active, err := r.p.Active(ctx, g.ID)
		if !errors.Is(err, errFromStore) {
			t.Fatalf("Active = %v, want the dependency's error", err)
		}
		if active {
			t.Error("a failed dependency read the record as active")
		}
	})
}

// TestTheLapseCauseFollowsTheFixedOrder checks that when several conditions fail
// at once, the first in the closed order names the written reason and event cause.
func TestTheLapseCauseFollowsTheFixedOrder(t *testing.T) {
	for _, c := range []struct {
		name        string
		mutate      func(r *rig)
		want        string
		wantEndedBy string
	}{
		{
			name: "the bound session precedes the actor",
			mutate: func(r *rig) {
				r.sessions.session.Active = false
				r.accounts.put(actorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
			},
			want:        authn.EndSessionEnded,
			wantEndedBy: "session-ended",
		},
		{
			name: "the actor precedes the target",
			mutate: func(r *rig) {
				r.accounts.put(actorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
				r.accounts.put(targetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
			},
			want:        authn.EndActorDisabled,
			wantEndedBy: "disabled",
		},
		{
			name: "the target precedes the membership",
			mutate: func(r *rig) {
				r.accounts.put(targetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
				r.memberships.set(targetID, tenantA, false)
			},
			want:        authn.EndTargetDisabled,
			wantEndedBy: "disabled",
		},
		{
			name: "every cause fails at once",
			mutate: func(r *rig) {
				r.sessions.session.Active = false
				r.accounts.put(actorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
				r.accounts.put(targetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
				r.memberships.set(targetID, tenantA, false)
			},
			want:        authn.EndSessionEnded,
			wantEndedBy: "session-ended",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				c.mutate(r)

				_, active, err := r.p.Active(ctx, g.ID)
				if err != nil {
					t.Fatalf("Active: %v", err)
				}
				if active {
					t.Fatal("a lapsed record reads as active")
				}
				if _, _, reason := r.stored(g.ID); reason == nil || *reason != c.want {
					t.Errorf("the end reason is %q, want %q", reasonText(reason), c.want)
				}
				if got := r.endedPayload().EndedBy; got != c.wantEndedBy {
					t.Errorf("the ended event's cause is %q, want %q", got, c.wantEndedBy)
				}
			})
		})
	}
}

// TestActiveReturnsAnErrorForEveryFailedDependencyRead checks that each failed
// dependency read is its own error, is never a lapse, and writes nothing.
func TestActiveReturnsAnErrorForEveryFailedDependencyRead(t *testing.T) {
	for _, c := range []struct {
		name   string
		inject func(r *rig)
		clear  func(r *rig)
	}{
		{
			name:   "the bound session",
			inject: func(r *rig) { r.sessions.statusErr = errFromStore },
			clear:  func(r *rig) { r.sessions.statusErr = nil },
		},
		{
			name:   "the target account",
			inject: func(r *rig) { r.accounts.failFor(targetID, errFromStore) },
			clear:  func(r *rig) { r.accounts.failFor(targetID, nil) },
		},
		{
			name:   "the membership",
			inject: func(r *rig) { r.memberships.err = errFromStore },
			clear:  func(r *rig) { r.memberships.err = nil },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				c.inject(r)

				_, active, err := r.p.Active(ctx, g.ID)
				if !errors.Is(err, errFromStore) {
					t.Fatalf("Active = %v, want the dependency's error", err)
				}
				if active {
					t.Error("a failed dependency read the record as active")
				}
				if endedAt, endedBy, reason := r.stored(g.ID); endedAt != nil || endedBy != nil || reason != nil {
					t.Errorf("a failed dependency read wrote the record ended: (%v, %v, %q)", endedAt, endedBy, reasonText(reason))
				}

				c.clear(r)
				if _, active, err := r.p.Active(ctx, g.ID); err != nil || !active {
					t.Errorf("after the dependency recovered Active = (active %v, err %v), want the record still valid", active, err)
				}
			})
		})
	}
}

// TestTheJudgeReadsTheRecordsOwnSession checks that each path asks the session store about the record's bound session and no other identifier.
func TestTheJudgeReadsTheRecordsOwnSession(t *testing.T) {
	t.Run("Active", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			ctx := context.Background()
			g, err := r.p.Start(ctx, adminIdentity(t), request())
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			sessions := newPerSessionSessions(sessionID)
			r.p.sessions = sessions

			_, active, err := r.p.Active(ctx, g.ID)
			if err != nil || !active {
				t.Fatalf("Active = (active %v, err %v), want the valid record", active, err)
			}
			if endedAt, _, _ := r.stored(g.ID); endedAt != nil {
				t.Errorf("the record was judged on a session that is not its own: ended at %v", *endedAt)
			}
			if asked := sessions.askedIDs(); len(asked) != 1 || asked[0] != sessionID {
				t.Errorf("the judgement read the sessions %v, want %q alone", asked, sessionID)
			}
		})
	})

	t.Run("List", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			ctx := context.Background()
			g, err := r.p.Start(ctx, adminIdentity(t), request())
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			sessions := newPerSessionSessions(sessionID)
			r.p.sessions = sessions

			page, _, err := r.p.List(ctx, authn.GrantQuery{})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(page) != 1 || !page[0].EndedAt.IsZero() {
				t.Errorf("the page is %+v, want the record still unended", page)
			}
			if endedAt, _, _ := r.stored(g.ID); endedAt != nil {
				t.Errorf("the record was judged on a session that is not its own: ended at %v", *endedAt)
			}
			if asked := sessions.askedIDs(); len(asked) != 1 || asked[0] != sessionID {
				t.Errorf("the judgement read the sessions %v, want %q alone", asked, sessionID)
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
			sessions := newPerSessionSessions(sessionID)
			r.p.sessions = sessions

			if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if _, _, reason := r.stored(g.ID); reason == nil || *reason != authn.EndRevoked {
				t.Errorf("the end reason is %q, want revoked: the record was judged on a session that is not its own", reasonText(reason))
			}
			if asked := sessions.askedIDs(); len(asked) != 1 || asked[0] != sessionID {
				t.Errorf("the judgement read the sessions %v, want %q alone", asked, sessionID)
			}
		})
	})
}

// TestActiveJudgesThroughTheCheckTTLCache checks that Active answers from the
// check-ttl cache inside the window and reads the store and writes the lapse after it.
func TestActiveJudgesThroughTheCheckTTLCache(t *testing.T) {
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
		r.sessions.session.Active = false

		if _, active, err := r.p.Active(ctx, g.ID); err != nil || !active {
			t.Errorf("inside the check-ttl window Active = (active %v, err %v), want the cached answer", active, err)
		}
		if endedAt, _, _ := r.stored(g.ID); endedAt != nil {
			t.Errorf("the cached window wrote the record ended: %v", *endedAt)
		}

		r.clock.advance(2 * time.Minute)
		if _, active, err := r.p.Active(ctx, g.ID); err != nil || active {
			t.Errorf("after the check-ttl window Active = (active %v, err %v), want the lapse", active, err)
		}
		if _, _, reason := r.stored(g.ID); reason == nil || *reason != authn.EndSessionEnded {
			t.Errorf("the end reason is %q, want session-ended", reasonText(reason))
		}
	})
}

// TestActiveSettlesOnceWhenTwoReplicasJudgeTheSameLapsedRecord checks that two
// replicas racing on one lapsed record settle it once: the loser counts and publishes nothing.
func TestActiveSettlesOnceWhenTwoReplicasJudgeTheSameLapsedRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
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
		actives := make([]bool, len(replicas))
		for i, p := range replicas {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, actives[i], errs[i] = p.Active(ctx, g.ID)
			}()
		}
		wg.Wait()

		for i := range replicas {
			if errs[i] != nil {
				t.Errorf("replica %d: Active = %v, want nil", i, errs[i])
			}
			if actives[i] {
				t.Errorf("replica %d read a lapsed record as active", i)
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

// TestASettleWriteBackFailureIsAnError checks that a failed write-back fails Active, List and Revoke and writes or announces nothing.
func TestASettleWriteBackFailureIsAnError(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, dialect string) {
		r := newRig(t, h)
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.blockEndWrites(t, dialect)
		r.clock.advance(31 * time.Minute)

		_, active, err := r.p.Active(ctx, g.ID)
		if err == nil {
			t.Fatal("Active = nil error, want the write-back failure")
		}
		if active {
			t.Error("a failed write-back read the record as active")
		}
		if endedAt, _, _ := r.stored(g.ID); endedAt != nil {
			t.Errorf("a failed write-back ended the record: %v", *endedAt)
		}

		if _, _, err := r.p.List(ctx, authn.GrantQuery{}); !errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("List = %v, want ErrUnavailable", err)
		}
		r.audit.rows = nil
		if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); !errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("Revoke = %v, want ErrUnavailable", err)
		}
		if rows := r.audit.entries(); len(rows) != 0 {
			t.Errorf("a failed write-back wrote audit rows: %+v", rows)
		}
		if events := r.bus.ofType(authn.EventImpersonationEnded); len(events) != 0 {
			t.Errorf("a failed write-back published %d ended events, want none", len(events))
		}
		if endedAt, _, _ := r.stored(g.ID); endedAt != nil {
			t.Errorf("a failed write-back ended the record: %v", *endedAt)
		}
	})
}
