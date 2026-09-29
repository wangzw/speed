package main

import (
	"fmt"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/ratelimit"
)

// These are the cases for the backends going away under a serving host: each
// cuts one link of a case laid out with newLinkedCase (links_test.go), or ends
// one of the host's sessions from the server's side, and holds the host to the
// failure behaviour the module on that link documents, and to its recovery
// once the link is back.

// recoveryLimit bounds the wait for a module to reach its backend again once a
// link is restored: the NATS clients reconnect every two seconds, a pool opens
// a connection on its next use, and the outbox's relay retries a refused event
// after a delay that doubles from a second.
var recoveryLimit = scaledForRace(45 * time.Second)

// recoveryPoll is how often a case sends a request while it waits for a
// recovery: every request an unreachable backend refuses writes a record, and
// polling at pollInterval would bury the one the case is waiting for.
const recoveryPoll = 250 * time.Millisecond

// pollEvery polls a condition at an interval of its own until it holds or the
// bound passes.
func pollEvery(within, interval time.Duration, holds func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if holds() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// recordWith reports whether some record in the stream carries every one of
// the key-and-value attribute pairs given, whatever its message.
func recordWith(text string, attrs ...string) bool {
	for _, rec := range records(text) {
		matched := true
		for i := 0; i < len(attrs); i += 2 {
			if rec[attrs[i]] != attrs[i+1] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// TestABearerTokenIsUnavailableWhileTheStoreIsCut is the case for the sessions
// backend while the key-value store it keeps the sessions in cannot be reached,
// on every stack whose store is reached over a connection.
//
// The backend cannot say whether the token names a live session, and that is
// not the token failing: it answers that it cannot answer, and the gateway
// answers 503 and attributes the answer to the backend. A backend that read a
// failed look-up as a missing session would have the token refused with 401,
// and every client signed out, for as long as the store is away. The Basic
// credential, which the store has no part in, is answered all along, and once
// the link is back the same token is answered again.
//
// On the NATS store the read does not fail while the client reconnects; it
// waits until the gateway's budget for the call has run out, which the
// gateway answers the same way and charges to the backend. There the case
// holds the answer and not the backend's own reading of the failure, which
// the other three stores show.
func TestABearerTokenIsUnavailableWhileTheStoreIsCut(t *testing.T) {
	forEachLinkedStack(t, reachesStore, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		ann := c.createUser("ann")
		c.grant(acme, ann, roleOwner)
		h := c.serve()
		token := bearer(h.login(t, ann))
		expect(t, "ann asking who she is with her token", h.get(t, accountEndpoint, "/me", "", token), nethttp.StatusOK)
		c.requireCarried(storeLink)

		c.cut(storeLink)
		if resp := h.get(t, accountEndpoint, "/me", "", token); resp.status != nethttp.StatusServiceUnavailable {
			t.Errorf("ann's token answered %d %s while the store is cut, want 503", resp.status, resp.body)
		}
		if !eventually(settleLimit, func() bool {
			return recordWith(h.output(), "status", "503", "outcome", "unavailable", "attributed-to", sessionsBackendName)
		}) {
			t.Errorf("no record attributes a 503 to the %s backend", sessionsBackendName)
		}
		expect(t, "ann asking who she is with her Basic credential while the store is cut",
			h.get(t, accountEndpoint, "/me", "", basic(ann)), nethttp.StatusOK)

		c.restore(storeLink)
		var last response
		if !pollEvery(recoveryLimit, recoveryPoll, func() bool {
			last = h.get(t, accountEndpoint, "/me", "", token)
			return last.status == nethttp.StatusOK
		}) {
			t.Errorf("ann's token answered %d %s %s after the store's link was restored, want 200",
				last.status, last.body, recoveryLimit)
		}
	})
}

// limiterQuiet is how long design-ratelimit has a failure class go without
// failing before the limiter writes that it is limiting again.
const limiterQuiet = time.Minute

// The lines the rate limiter writes on standard error for the class of failure
// an unreachable counter store trips first: the read of the previous window's
// count.
var (
	limiterLetThrough = ratelimit.ModuleName + ": letting requests through: reading the previous window's count failed"
	limiterBack       = ratelimit.ModuleName + ": back to limiting: reading the previous window's count failed"
)

// TestTheLimiterLetsRequestsThroughWhileItsStoreIsCut is the case for the rate
// limiter while the key-value store its counters live in cannot be reached, on
// every stack whose store is reached over a connection.
//
// acme's limit is three in an hour, and a fourth request is refused while the
// store answers. With the link cut the limiter cannot read the count and lets
// requests through rather than refusing them: five more are answered as they
// would be with no limiter. It says so once, on standard error, however many it
// lets through. Once the link is back and a minute has passed since the last
// request it let through, the next request it counts writes the one line that
// it is limiting again — no sooner, since a failure that comes and goes would
// otherwise write a pair of lines at every turn — and a request past the limit
// is refused once more.
//
// Every request authenticates with a Basic credential, which the store has no
// part in. A limiter that refused while it could not count, one that wrote a
// line for each request it let through, one that said it was back at the first
// request that counted and one that never said so each fail an assertion of
// their own.
func TestTheLimiterLetsRequestsThroughWhileItsStoreIsCut(t *testing.T) {
	forEachLinkedStack(t, reachesStore, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		acme := c.tenantNamed("acme")
		h.adminCreateTenant(t, acme)
		ann := h.adminCreateUser(t, c.userNamed("ann"))
		h.adminGrant(t, acme, ann, roleOwner)
		const limit = 3
		limitTenant(t, h, acme, limit)
		awaitLimit(t, h, acme, limit)
		members := func() response { return h.get(t, apiEndpoint, "/members", acme.Alias, basic(ann)) }
		for i := 1; i <= limit; i++ {
			expect(t, fmt.Sprintf("ann's request %d in acme", i), members(), nethttp.StatusOK)
		}
		expect(t, "ann's request past acme's limit", members(), nethttp.StatusTooManyRequests)
		c.requireCarried(storeLink)

		c.cut(storeLink)
		var lastLetThrough time.Time
		for i := 1; i <= limit+2; i++ {
			lastLetThrough = time.Now()
			if resp := members(); resp.status != nethttp.StatusOK {
				t.Errorf("ann's request %d past the limit while the store is cut answered %d %s, want 200",
					i, resp.status, resp.body)
			}
		}
		if n := lines(h.output(), limiterLetThrough); n != 1 {
			t.Errorf("the limiter wrote %d lines that it lets requests through, want one: %q", n, limiterLetThrough)
		}

		c.restore(storeLink)
		var back time.Time
		if !pollEvery(limiterQuiet+recoveryLimit, time.Second, func() bool {
			members()
			if lines(h.output(), limiterBack) == 0 {
				return false
			}
			back = time.Now()
			return true
		}) {
			t.Fatalf("the limiter wrote no %q within %s of the store's link being restored", limiterBack,
				limiterQuiet+recoveryLimit)
		}
		if quiet := back.Sub(lastLetThrough); quiet < limiterQuiet {
			t.Errorf("the limiter said it was limiting again %s after the last request it let through, "+
				"before the quiet period of %s", quiet, limiterQuiet)
		}
		if n, m := lines(h.output(), limiterLetThrough), lines(h.output(), limiterBack); n != 1 || m != 1 {
			t.Errorf("the limiter wrote %d lines letting requests through and %d back to limiting, want one each", n, m)
		}
		expect(t, "ann's request past acme's limit once the limiter counts again", members(),
			nethttp.StatusTooManyRequests)
	})
}

// relayRepublishedMsg is the record the outbox's relay writes when it
// publishes an event the bus had refused.
const relayRepublishedMsg = "outbox: a staged event the bus had refused was published"

// cutHold is how long a case keeps a link cut after the write it is about, and
// watches that nothing the cut should hold back gets through.
const cutHold = 3 * time.Second

// TestAWinMadeWhileTheBusIsCutIsCountedOnceTheBusIsBack is the case for the
// outbox across an outage of the bus, on every stack whose bus is reached over
// a connection.
//
// A deal moved into won while the link to the bus is cut is answered as any
// move is: the move stages its events in its own transaction and publishes
// nothing itself. While the link stays cut the deal.won stays staged and the
// win is not counted. The bus's publish fails at once on Redis and PostgreSQL,
// and the relay records the refusal and gives the row back; the NATS client
// holds a publish while it reconnects, within the relay's bound on one
// publish. Once the link is back the relay's publish goes through, the win is
// counted with the amount the move left, exactly, and it is counted once — a
// relay that had recorded a refusal records the later publication too — and
// the staged row is gone.
//
// A host that published after the commit would have no staged row to show and
// its win would never be counted.
func TestAWinMadeWhileTheBusIsCutIsCountedOnceTheBusIsBack(t *testing.T) {
	forEachLinkedStack(t, reachesBus, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		deal := w.createDeal(t, acme, alice, map[string]any{
			"account_id": "acc-1", "title": "won while the bus is away", "amount": pastFloat, "currency": "JPY",
			"stage": "qualify",
		})
		c.requireCarried(busLink)
		staged := func(id string) int { return c.count("SELECT count(*) FROM event_staged WHERE event_id = ?", id) }

		c.cut(busLink)
		moved := w.move(t, acme, alice, deal.ID, "won", 1)
		expect(t, "the move into won while the bus is cut", moved, nethttp.StatusOK)
		var got moveView
		moved.decode(t, &got)
		wonID := *got.Activity.WonEventID
		for deadline := time.Now().Add(cutHold); time.Now().Before(deadline); time.Sleep(recoveryPoll) {
			if n := staged(wonID); n != 1 {
				t.Fatalf("the outbox holds %d rows of the deal.won %s while the bus is cut, want the one the move staged",
					n, wonID)
			}
			if revenue, _ := revenueOf(t, w.h, acme, alice); len(revenue.Revenue) != 0 {
				t.Fatalf("acme's won revenue reads %+v while the bus is cut, want nothing yet", revenue.Revenue)
			}
		}

		c.restore(busLink)
		want := revenueView{Currency: "JPY", Deals: 1, Amount: pastFloat}
		var revenue revenueList
		if !pollEvery(recoveryLimit, recoveryPoll, func() bool {
			revenue, _ = revenueOf(t, w.h, acme, alice)
			return slices.Equal(revenue.Revenue, []revenueView{want})
		}) {
			t.Fatalf("acme's won revenue reads %+v %s after the bus's link was restored, want %+v",
				revenue.Revenue, recoveryLimit, want)
		}
		if !eventually(settleLimit, func() bool { return staged(wonID) == 0 }) {
			t.Errorf("the outbox still holds %d rows of the deal.won the bus took", staged(wonID))
		}
		if _, refused := findRecord(w.h.output(), relayRefusedMsg, "event_id", wonID); refused {
			awaitRecord(t, w.h, "for the refused deal.won published once the bus was back", relayRepublishedMsg,
				"event_id", wonID)
		}
		time.Sleep(duplicateWindow)
		awaitRevenue(t, w.h, acme, alice, want)
		if n := outcomesIn(w.h.output(), revenueModuleName, revenueOutcomes)[eventKey{acme.ID, wonID}][wonCountedMsg]; n != 1 {
			t.Errorf("the projection counted the deal.won %s %d times, want once", wonID, n)
		}
	})
}

// TestAPublishTheBusRefusesReachesNoConsumer is the case for the one thing a
// publisher learns from a failed publish, on every stack whose bus is reached
// over a connection: design-event has a publish that returns an error be one
// the bus did not accept. The operator's deal feed publishes while the link to
// the bus is cut, and answers 503 when the publish fails; once the link is
// back, no consumer on any process hears of the event, since a publisher told
// it was refused is free to publish it again under a new identifier, or not at
// all, and either would then be wrong. A bus that kept a refused publish and
// sent it once it reconnected would have the event counted.
func TestAPublishTheBusRefusesReachesNoConsumer(t *testing.T) {
	forEachLinkedStack(t, reachesBus, func(t *testing.T, c *crmCase) {
		c.config.set("dealfeed.enabled", true)
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()
		c.requireCarried(busLink)

		c.cut(busLink)
		refusedID := "refused-" + c.suffix
		resp := h.send(t, request{
			method: nethttp.MethodPost, endpoint: adminEndpoint, path: "/deal-events",
			cred: operator(), body: wonEvent(acme, refusedID, "refused", 10, alice),
		})
		if resp.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("the operator's publish while the bus is cut answered %d %s, want 503", resp.status, resp.body)
		}

		c.restore(busLink)
		acceptedID := "accepted-" + c.suffix
		if !pollEvery(recoveryLimit, time.Second, func() bool {
			resp := h.send(t, request{
				method: nethttp.MethodPost, endpoint: adminEndpoint, path: "/deal-events",
				cred: operator(), body: wonEvent(acme, acceptedID, "accepted", 20, alice),
			})
			return resp.status == nethttp.StatusAccepted
		}) {
			t.Fatalf("the operator's publish was not accepted within %s of the bus's link being restored", recoveryLimit)
		}
		awaitRecord(t, h, "for the publish the bus accepted", wonCountedMsg, "event_id", acceptedID)
		time.Sleep(duplicateWindow)
		if got := outcomesIn(h.output(), revenueModuleName, revenueOutcomes)[eventKey{acme.ID, refusedID}]; len(got) != 0 {
			t.Errorf("the deal.won whose publish the bus refused reached the projection once the bus was back: %s",
				formatCounts(got))
		}
	})
}

// busListenerLostMsg is the record the PostgreSQL bus writes when its
// listening session is gone.
const busListenerLostMsg = "event/postgres: the listening connection was lost; taking a new one"

// listeningSessions are the server's sessions of the case's database that last
// ran the PostgreSQL bus's LISTEN.
func (c *crmCase) listeningSessions() []int {
	c.t.Helper()
	rows, err := c.sql().Query("SELECT pid FROM pg_stat_activity WHERE datname = current_database() " +
		"AND pid <> pg_backend_pid() AND query = 'LISTEN speed_events' ORDER BY pid")
	if err != nil {
		c.t.Fatalf("reading the listening sessions: %v", err)
	}
	defer rows.Close()
	var pids []int
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil {
			c.t.Fatalf("scanning a listening session: %v", err)
		}
		pids = append(pids, pid)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading the listening sessions: %v", err)
	}
	return pids
}

// TestTheBusListensAgainOnceItsSessionIsKilled is the case for the PostgreSQL
// bus's listening session ended under it — an operator's pg_terminate_backend,
// a failover, a proxy timing the session out — on the stack whose bus it is.
//
// The bus reads its table on a timer as well, so delivery alone would go on
// without the session, a poll late each time; what design-event has the bus do
// is take a new session with the configuration the first was taken with and
// read the table at once. The case ends the one session from the server's side
// and holds the bus to recording the loss, to holding a new listening session
// under another process identifier, and to delivering a win made afterwards.
// A bus that polled on without listening again delivers the win and fails the
// look for the new session.
func TestTheBusListensAgainOnceItsSessionIsKilled(t *testing.T) {
	c := newCase(t, stackNamed(t, "postgres"))
	w := startDeals(t, c, nil)
	acme, alice := w.acme, w.asAlice
	definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
	var killed []int
	if !eventually(settleLimit, func() bool {
		killed = c.listeningSessions()
		return len(killed) == 1
	}) {
		t.Fatalf("the case's database holds the listening sessions %v, want the serving replica's one", killed)
	}
	var ended bool
	if err := c.sql().QueryRow("SELECT pg_terminate_backend($1)", killed[0]).Scan(&ended); err != nil || !ended {
		t.Fatalf("ending the listening session %d: %v (ended %v)", killed[0], err, ended)
	}
	awaitRecord(t, w.h, "for the listening session ended from the server", busListenerLostMsg)
	var now []int
	if !eventually(settleLimit, func() bool {
		now = c.listeningSessions()
		return len(now) == 1 && now[0] != killed[0]
	}) {
		t.Errorf("the case's database holds the listening sessions %v after %d was ended, want one other", now, killed[0])
	}
	won := winDeal(t, w.h, acme, alice, "after-the-kill", "EUR", 700)
	awaitRecord(t, w.h, "for the win made after the session was ended", wonCountedMsg, "event_id", *won.WonEventID)
	awaitRevenue(t, w.h, acme, alice, revenueView{Currency: "EUR", Deals: 1, Amount: 700})
}

// The lines the journal writes on standard error when writing its own rows
// fails, and when it works again.
var (
	journalWriteFailed = "audit: " + journal.ModuleName + ": writing an audit entry failed"
	journalRecovered   = "audit: " + journal.ModuleName + ": writing audit entries recovered after "
)

// journalRefusalName is what refuseJournalRows adds to the case's database.
const journalRefusalName = "crm_case_refuses_journal_rows"

// refuseJournalRows makes the journal's table refuse every row written to it
// from here on: a constraint no new row meets on PostgreSQL, checked for new
// rows alone, and a trigger on SQLite.
func (c *crmCase) refuseJournalRows() {
	c.t.Helper()
	if c.dialect == "postgres" {
		c.exec("ALTER TABLE audit_entries ADD CONSTRAINT " + journalRefusalName + " CHECK (false) NOT VALID")
		return
	}
	c.exec("CREATE TRIGGER " + journalRefusalName + " BEFORE INSERT ON audit_entries " +
		"BEGIN SELECT RAISE(ABORT, 'the case refuses journal rows'); END")
}

// allowJournalRows removes what refuseJournalRows added.
func (c *crmCase) allowJournalRows() {
	c.t.Helper()
	if c.dialect == "postgres" {
		c.exec("ALTER TABLE audit_entries DROP CONSTRAINT " + journalRefusalName)
		return
	}
	c.exec("DROP TRIGGER " + journalRefusalName)
}

// recoveredAfter reads how many failures the journal's recovery line counts.
func recoveredAfter(out string) (int, bool) {
	for line := range strings.SplitSeq(out, "\n") {
		if rest, ok := strings.CutPrefix(line, journalRecovered); ok {
			digits, _, _ := strings.Cut(rest, " ")
			n, err := strconv.Atoi(digits)
			return n, err == nil
		}
	}
	return 0, false
}

// TestTheJournalFailsOpenOnRequestsAndClosedOnStatements is the case for the
// journal when its own rows cannot be written, as design-audit has it: a
// request's row and the work it records are two things, so the request is
// answered as its handler answered it and the row is lost, with one line on
// standard error for the fault and one for its end; a statement's row and the
// statement are one unit of work, so the statement fails and writes nothing.
//
// On the stacks whose database is reached over a connection, the link to it is
// cut: the operator's listing of the tenants, which the roster answers from its
// snapshot, is answered in full while the journal cannot write the rows of
// those requests, and once the link is back the journal says how many it lost.
// On every stack the journal's table is made to refuse rows while the rest of
// the database works: an account's creation, whose insert the journal records
// on the statement, fails and leaves no account, while a listing of the
// members, which it records on the request alone, is answered in full.
func TestTheJournalFailsOpenOnRequestsAndClosedOnStatements(t *testing.T) {
	t.Run("with the database cut", func(t *testing.T) {
		forEachLinkedStack(t, reachesDatabase, func(t *testing.T, c *crmCase) {
			c.migrate()
			acme := c.createTenant("acme")
			h := c.serve()
			tenants := func() response { return h.get(t, adminEndpoint, "/tenants", "", operator()) }
			expect(t, "the operator listing the tenants", tenants(), nethttp.StatusOK)
			c.awaitJournal("the listing before the cut", func(r auditRow) bool {
				return r.source == "request" && r.object == listTenantsRoute && r.outcome == "200"
			})
			c.requireCarried(databaseLink)

			c.cut(databaseLink)
			const cutRequests = 3
			for i := 1; i <= cutRequests; i++ {
				resp := tenants()
				if resp.status != nethttp.StatusOK || !strings.Contains(string(resp.body), acme.ID) {
					t.Errorf("the operator's listing %d while the database is cut answered %d %s, want 200 naming %s",
						i, resp.status, resp.body, acme.ID)
				}
			}
			if n := lines(h.output(), journalWriteFailed); n != 1 {
				t.Errorf("the journal wrote %d lines that writing failed, want one for the fault: %q", n, journalWriteFailed)
			}

			c.restore(databaseLink)
			var lost int
			if !pollEvery(recoveryLimit, recoveryPoll, func() bool {
				tenants()
				var ok bool
				lost, ok = recoveredAfter(h.output())
				return ok
			}) {
				t.Fatalf("the journal wrote no %q within %s of the database's link being restored",
					journalRecovered, recoveryLimit)
			}
			if lost < cutRequests {
				t.Errorf("the journal counts %d rows lost, want at least the %d requests made while the database was cut",
					lost, cutRequests)
			}
		})
	})

	t.Run("with the journal's table refusing its rows", func(t *testing.T) {
		forEachStack(t, func(t *testing.T, c *crmCase) {
			c.migrate()
			acme := c.createTenant("acme")
			ann := c.createUser("ann")
			c.grant(acme, ann, roleOwner)
			h := c.serve()
			token := bearer(h.login(t, ann))
			expect(t, "ann listing acme's members", h.get(t, apiEndpoint, "/members", acme.Alias, token), nethttp.StatusOK)
			open := func(id string) response {
				return h.send(t, api(nethttp.MethodPost, "/accounts", acme, token, map[string]any{"id": id, "name": "Account " + id}))
			}

			c.refuseJournalRows()
			if resp := open("acc-unrecorded"); resp.status != nethttp.StatusInternalServerError {
				t.Errorf("the creation whose statement row is refused answered %d %s, want 500", resp.status, resp.body)
			}
			if n := c.count("SELECT count(*) FROM accounts WHERE tenant_id = ? AND id = ?", acme.ID, "acc-unrecorded"); n != 0 {
				t.Errorf("acme holds %d accounts acc-unrecorded, want none: the statement's row and its insert are one unit", n)
			}
			list := h.get(t, apiEndpoint, "/members", acme.Alias, token)
			if list.status != nethttp.StatusOK || !strings.Contains(string(list.body), ann.ID) {
				t.Errorf("ann's listing while the journal's rows are refused answered %d %s, want 200 naming her",
					list.status, list.body)
			}
			if n := lines(h.output(), journalWriteFailed); n != 1 {
				t.Errorf("the journal wrote %d lines that writing failed, want one for the fault", n)
			}

			c.allowJournalRows()
			expect(t, "ann listing acme's members once the rows are taken", h.get(t, apiEndpoint, "/members", acme.Alias,
				token), nethttp.StatusOK)
			if !eventually(settleLimit, func() bool { _, ok := recoveredAfter(h.output()); return ok }) {
				t.Errorf("the journal wrote no %q once its table took rows again", journalRecovered)
			}
			expect(t, "the same creation once the rows are taken", open("acc-unrecorded"), nethttp.StatusCreated)
			c.awaitJournal("the creation once the rows are taken", func(r auditRow) bool {
				return r.source == "statement" && r.action == "create" && r.object == accountTable && r.tenant == acme.ID
			})
		})
	})
}

// TestASettingWrittenWhileTheBusIsCutReachesTheOtherReplicaOnceTheBusIsBack
// is the case for the settings module across an outage of the bus, on every
// stack whose replicas share a bus reached over a connection.
//
// The periodic reload is off, so a change reaches the other replica through
// its event alone. A change written through one replica while the bus is cut
// is answered 200 and served there at once: the publish that fails comes after
// the commit and the replica's own reload, and neither undoes them. The other
// replica may never hear of it — design-settings says a change whose event is
// not delivered stays unseen until a later reload — but the next change,
// written once the bus is back, reaches it through its event, and the reload
// that event starts reads the whole table: the other replica then serves the
// change written during the outage as well. A module that reloaded the one key
// an event names, or a bus consumer that did not resume after the outage, would
// leave the second replica on the old currency.
func TestASettingWrittenWhileTheBusIsCutReachesTheOtherReplicaOnceTheBusIsBack(t *testing.T) {
	forEachLinkedStack(t, func(s stack) bool { return reachesBus(s) && sharedStack(s) }, func(t *testing.T, c *crmCase) {
		c.config.set("settings.refresh-interval", "0s")
		c.migrate()
		c.setGlobal("ratelimit.limit", liftedRateLimit)
		acme := c.createTenant("acme")
		ann := c.createUser("ann")
		c.grant(acme, ann, roleOwner)
		replicas := c.serveReplicas(2, firstThenRest)
		a, b := replicas[0], replicas[1]
		valueOn := func(h *crmHost, key string) any {
			return settingIn(t, settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, basic(ann)), key).Value
		}
		c.requireCarried(busLink)

		c.cut(busLink)
		expect(t, "ann writing acme's currency through the first replica while the bus is cut",
			writeTenantSettingAs(t, a, acme.Alias, basic(ann), "deals.default-currency", "CHF"), nethttp.StatusOK)
		if got := valueOn(a, "deals.default-currency"); got != "CHF" {
			t.Errorf("the first replica serves acme's currency as %#v after the write, want CHF", got)
		}

		c.restore(busLink)
		written := map[float64]bool{}
		size := 30
		var seen any
		if !pollEvery(recoveryLimit, time.Second, func() bool {
			expect(t, "ann writing acme's page size through the first replica",
				writeTenantSettingAs(t, a, acme.Alias, basic(ann), "accounts.page-size", size), nethttp.StatusOK)
			written[float64(size)] = true
			size++
			n, ok := valueOn(b, "accounts.page-size").(float64)
			seen = n
			return ok && written[n]
		}) {
			t.Fatalf("the second replica serves acme's page size as %v %s after the bus's link was restored, "+
				"want one written since", seen, recoveryLimit)
		}
		if got := valueOn(b, "deals.default-currency"); got != "CHF" {
			t.Errorf("the second replica serves acme's currency as %#v once a later change reached it, want CHF", got)
		}
	})
}
