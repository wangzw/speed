package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The cases of the deals, the pipeline, the activities and their events: the
// claims under their heading in main.go's package comment. Each runs on every
// stack, because each rests on a backend — the engine a statement is scoped
// and a transaction is kept on, the key-value store a claim is made in, the bus
// an event crosses — with three exceptions. The currency case and the case of
// how a change is read rest on no backend and run on the stack that runs
// everywhere, and the case of an event every replica hears runs on the stacks
// whose backends several replicas share.

// dealsServeEnv is the environment every serving run of these cases starts
// with: the journal's event list names every deal event, so the journal's
// event rows show what each write announced. It is given to the serving
// command alone, because the command groups run no event backend and a
// configuration naming events stops them at startup.
//
// The variable and the event types are spelled out rather than built from the
// host's constants: a case that assembled them the way the host does would
// follow the host's mistake.
var dealsServeEnv = []string{
	"CRM_AUDIT__JOURNAL__EVENTS=deal.created,deal.changed,deal.deleted,deal.stage-changed,deal.won",
}

// liftedRateLimit is the per-identity allowance these cases run under. They
// send more requests as one identity in one tenant than the limiter's default
// lets through in a minute, and what they are about is not the limiter.
const liftedRateLimit = "100000"

// headers are request headers a case sets beyond the ones the shared client
// writes.
type headers map[string]string

// doWith makes one request carrying extra headers, reporting a request that
// could not be made rather than failing the case, so it can run off the case's
// goroutine.
func (h *crmHost) doWith(req request, extra headers) (response, error) {
	var body io.Reader
	if req.body != nil {
		encoded, err := json.Marshal(req.body)
		if err != nil {
			return response{}, fmt.Errorf("encoding the request body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	r, err := nethttp.NewRequestWithContext(context.Background(), req.method, h.url(req.endpoint)+req.path, body)
	if err != nil {
		return response{}, fmt.Errorf("building the request: %w", err)
	}
	if req.body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if req.tenant != "" {
		r.Header.Set(tenantHeader, req.tenant)
	}
	for name, value := range extra {
		r.Header.Set(name, value)
	}
	if req.cred != nil {
		req.cred.apply(r)
	}
	resp, err := client.Do(r)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	read, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, fmt.Errorf("reading the response: %w", err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: read}, nil
}

// sendWith makes one request carrying extra headers, failing the case when it
// cannot be made.
func (h *crmHost) sendWith(t *testing.T, req request, extra headers) response {
	t.Helper()
	resp, err := h.doWith(req, extra)
	if err != nil {
		t.Fatalf("%s %s on %s: %v", req.method, req.path, req.endpoint, err)
	}
	return resp
}

// api is a request on the API endpoint.
func api(method, path string, tenant tenantRef, cred credential, body any) request {
	return request{method: method, endpoint: apiEndpoint, path: path, tenant: tenant.Alias, cred: cred, body: body}
}

// dealsWorld is what the cases of this file start from: two tenants, an owner
// in each, and the serving replicas, h being the first of them.
//
// The owners sign in once and send every request of a case with the session
// token they get, as a client does. A Basic credential costs a password
// derivation on every request, which is the users module's doing and not
// what these cases are about, and under load that cost is what the gate's
// call budget runs out on.
type dealsWorld struct {
	c              *crmCase
	h              *crmHost
	hosts          []*crmHost
	acme, globex   tenantRef
	alice, bob     userRef
	asAlice, asBob credential
}

// startDeals migrates the case's database, registers acme with alice as its
// owner and globex with bob as its owner, lifts the rate limit, lets the case
// put whatever else it needs in place at the command line or in the settings
// table, serves one replica, and opens the accounts the cases' deals are with
// in both tenants.
func startDeals(t *testing.T, c *crmCase, before func(w *dealsWorld)) *dealsWorld {
	t.Helper()
	return startDealsOn(t, c, 1, before)
}

// startDealsOn is startDeals serving n replicas. The first is ready before the
// rest are launched: what these cases are about is not the rollout.
func startDealsOn(t *testing.T, c *crmCase, n int, before func(w *dealsWorld)) *dealsWorld {
	t.Helper()
	return startDealsWith(t, c, n, dealsServeEnv, before)
}

// startDealsWith is startDealsOn serving with the environment given in place of
// dealsServeEnv, for a case whose journal captures other events.
func startDealsWith(t *testing.T, c *crmCase, n int, env []string, before func(w *dealsWorld)) *dealsWorld {
	t.Helper()
	c.migrate()
	w := &dealsWorld{c: c, acme: c.createTenant("acme"), globex: c.createTenant("globex")}
	w.alice = c.createUser("alice")
	w.bob = c.createUser("bob")
	c.grant(w.acme, w.alice, roleOwner)
	c.grant(w.globex, w.bob, roleOwner)
	c.setGlobal("ratelimit.limit", liftedRateLimit)
	if before != nil {
		before(w)
	}
	w.hosts = c.serveReplicas(n, firstThenRest, env...)
	w.h = w.hosts[0]
	w.asAlice = bearer(w.h.login(t, w.alice))
	w.asBob = bearer(w.h.login(t, w.bob))
	for _, id := range dealAccounts {
		openAccount(t, w.h, w.acme, w.asAlice, id)
		openAccount(t, w.h, w.globex, w.asBob, id)
	}
	return w
}

// dealAccounts are the accounts a deals world opens in each of its tenants,
// under the same identifiers in both.
var dealAccounts = []string{"acc-1", "acc-2", "acc-9"}

// openAccount creates an account in a tenant through one replica, under the
// identifier the deals with it name it by.
func openAccount(t *testing.T, h *crmHost, ref tenantRef, cred credential, id string) {
	t.Helper()
	resp := h.send(t, api(nethttp.MethodPost, "/accounts", ref, cred, map[string]any{"id": id, "name": "Account " + id}))
	expect(t, "opening the account "+id+" in "+ref.ID, resp, nethttp.StatusCreated)
}

// sessionActor is how the journal names a user who sent a request with a
// session token.
func sessionActor(u userRef) string { return sessionsBackendName + "/" + u.ID }

// stageDef is a stage as a client defines it.
type stageDef struct {
	key, name   string
	position    int
	probability int
	kind        string
}

func (s stageDef) body() map[string]any {
	return map[string]any{
		"key": s.key, "name": s.name, "position": s.position, "probability": s.probability, "kind": s.kind,
	}
}

// pipelineOf is the pipeline every case defines, the stage names marked with
// the tenant's so that a stage read from the wrong tenant says so.
func pipelineOf(tenant string) []stageDef {
	return []stageDef{
		{"qualify", "Qualify at " + tenant, 1, 20, "open"},
		{"proposal", "Proposal at " + tenant, 2, 50, "open"},
		{"won", "Won at " + tenant, 3, 100, "won"},
		{"lost", "Lost at " + tenant, 4, 0, "lost"},
	}
}

// definePipeline defines a pipeline's stages in a tenant through one replica,
// each newly.
func definePipeline(t *testing.T, h *crmHost, ref tenantRef, cred credential, stages []stageDef) {
	t.Helper()
	for _, s := range stages {
		resp := h.send(t, api(nethttp.MethodPost, "/pipeline/stages", ref, cred, s.body()))
		expect(t, "defining the stage "+s.key+" in "+ref.ID, resp, nethttp.StatusCreated)
	}
}

// createDeal creates a deal through the world's first replica and returns it
// as the creation answered it.
func (w *dealsWorld) createDeal(t *testing.T, ref tenantRef, cred credential, body map[string]any) dealView {
	t.Helper()
	return createDealOn(t, w.h, ref, cred, body)
}

// createDealOn creates a deal through one replica and returns it as the
// creation answered it.
func createDealOn(t *testing.T, h *crmHost, ref tenantRef, cred credential, body map[string]any) dealView {
	t.Helper()
	resp := h.send(t, api(nethttp.MethodPost, "/deals", ref, cred, body))
	expect(t, fmt.Sprintf("creating the deal %v in %s", body["title"], ref.ID), resp, nethttp.StatusCreated)
	var got dealView
	resp.decode(t, &got)
	return got
}

// createDealIn creates a deal with acc-1 in one of a tenant's stages through
// one replica, owned by the member given.
func createDealIn(t *testing.T, h *crmHost, ref tenantRef, cred credential, id, stage string, amount int64,
	currency string, owner userRef,
) dealView {
	t.Helper()
	return createDealOn(t, h, ref, cred, map[string]any{
		"id": id, "account_id": "acc-1", "title": id + " of " + ref.ID, "amount": amount, "currency": currency,
		"stage": stage, "owner_id": owner.ID,
	})
}

// salesSide is one tenant of a deals world, with the member who makes the
// requests there and owns the deals.
type salesSide struct {
	name  string
	ref   tenantRef
	cred  credential
	owner userRef
}

// sidesOf is the two tenants of a deals world, each with its owner.
func sidesOf(w *dealsWorld) []salesSide {
	return []salesSide{{"acme", w.acme, w.asAlice, w.alice}, {"globex", w.globex, w.asBob, w.bob}}
}

// getDeal reads a deal, failing the case unless it is there.
func (w *dealsWorld) getDeal(t *testing.T, ref tenantRef, cred credential, id string) dealView {
	t.Helper()
	resp := w.h.get(t, apiEndpoint, "/deals/"+id, ref.Alias, cred)
	expect(t, "reading the deal "+id+" in "+ref.ID, resp, nethttp.StatusOK)
	var got dealView
	resp.decode(t, &got)
	return got
}

// timelineOf reads a deal's timeline.
func (w *dealsWorld) timelineOf(t *testing.T, ref tenantRef, cred credential, id string) []activityView {
	t.Helper()
	resp := w.h.get(t, apiEndpoint, "/deals/"+id+"/timeline", ref.Alias, cred)
	expect(t, "reading the timeline of "+id+" in "+ref.ID, resp, nethttp.StatusOK)
	var got timeline
	resp.decode(t, &got)
	return got.Activities
}

// move moves a deal to a stage at a version.
func (w *dealsWorld) move(t *testing.T, ref tenantRef, cred credential, id, stage string, version int64) response {
	t.Helper()
	return w.h.sendWith(t, api(nethttp.MethodPost, "/deals/"+id+"/stage", ref, cred, map[string]any{"stage": stage}),
		headers{"If-Match": strconv.Quote(strconv.FormatInt(version, 10))})
}

// dealRow is one row of the deals table as the harness reads it, whatever the
// filter would let a request see.
type dealRow struct {
	title   string
	stage   string
	owner   string
	version int64
	deleted bool
}

// dealRow reads one tenant's row of the deals table, reporting whether it is
// there.
func (c *crmCase) dealRow(ref tenantRef, id string) (dealRow, bool) {
	c.t.Helper()
	var row dealRow
	var deletedAt sql.NullString
	err := c.sql().QueryRow(c.rebind("SELECT title, stage_key, owner_id, version, deleted_at FROM deals WHERE tenant_id = ? AND id = ?"),
		ref.ID, id).Scan(&row.title, &row.stage, &row.owner, &row.version, &deletedAt)
	if err == sql.ErrNoRows {
		return dealRow{}, false
	}
	if err != nil {
		c.t.Fatalf("reading the deals row %s/%s: %v", ref.ID, id, err)
	}
	row.deleted = deletedAt.Valid
	return row, true
}

// count answers a counting statement on the case's database.
func (c *crmCase) count(statement string, args ...any) int {
	c.t.Helper()
	var n int
	if err := c.sql().QueryRow(c.rebind(statement), args...).Scan(&n); err != nil {
		c.t.Fatalf("running %q: %v", statement, err)
	}
	return n
}

// eventRow is one row the journal wrote from its event path, with its detail
// decoded.
type eventRow struct {
	action  string
	object  string
	outcome string
	tenant  string
	detail  map[string]string
}

// eventRows reads the journal's event rows for one event type, oldest first.
func (c *crmCase) eventRows(eventType string) []eventRow {
	c.t.Helper()
	rows, err := c.sql().Query(c.rebind("SELECT action, object, outcome, COALESCE(tenant_id, ''), detail "+
		"FROM audit_entries WHERE source = 'event' AND action = ? ORDER BY id"), eventType)
	if err != nil {
		c.t.Fatalf("reading the journal's event rows: %v", err)
	}
	defer rows.Close()
	var out []eventRow
	for rows.Next() {
		var r eventRow
		var detail string
		if err := rows.Scan(&r.action, &r.object, &r.outcome, &r.tenant, &detail); err != nil {
			c.t.Fatalf("scanning an event row: %v", err)
		}
		if err := json.Unmarshal([]byte(detail), &r.detail); err != nil {
			c.t.Fatalf("decoding the event row's detail %q: %v", detail, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading the journal's event rows: %v", err)
	}
	return out
}

// awaitEventRow waits for an event row matching a predicate. An event reaches
// the journal after the response on every bus: the outbox's relay publishes a
// deal event once the write that staged it has committed.
func (c *crmCase) awaitEventRow(what, eventType string, match func(eventRow) bool) eventRow {
	c.t.Helper()
	var found eventRow
	if !eventually(settleLimit, func() bool {
		for _, row := range c.eventRows(eventType) {
			if match(row) {
				found = row
				return true
			}
		}
		return false
	}) {
		c.t.Fatalf("the journal holds no %s event row for %s; it holds %+v", eventType, what, c.eventRows(eventType))
	}
	return found
}

// tableLock is a lock the harness holds on the case's database so that a write
// the host issues waits for it: how a case holds a request in flight at the
// point where it writes. On PostgreSQL it is a lock on one table, which lets
// reads through; on SQLite, which has one writer, it is the database's write
// lock.
type tableLock struct {
	c     *crmCase
	table string
	conn  *sql.Conn
	once  sync.Once
}

// sqliteReachPause is how long a case waits on SQLite for a request it sent to
// reach the write the lock holds up. SQLite has no view of the statements
// waiting on its lock, so the case waits a pause instead of watching; two
// pauses, which is the longest a request waits on the lock, stay well inside
// the five seconds the driver waits on a busy database before failing the
// statement.
const sqliteReachPause = 750 * time.Millisecond

// sqliteReach is the pause for this binary. A host built with the race detector
// takes longer to reach the write, and twice its doubled pause still stays
// inside the driver's wait.
func sqliteReach() time.Duration {
	if raceEnabled {
		return 2 * sqliteReachPause
	}
	return sqliteReachPause
}

// lockTable takes the lock and holds it until release, or the case's end.
func (c *crmCase) lockTable(table string) *tableLock {
	c.t.Helper()
	ctx := context.Background()
	conn, err := c.sql().Conn(ctx)
	if err != nil {
		c.t.Fatalf("taking a connection to lock %s: %v", table, err)
	}
	statements := []string{"BEGIN IMMEDIATE"}
	if c.dialect == "postgres" {
		statements = []string{"BEGIN", "LOCK TABLE " + table + " IN EXCLUSIVE MODE"}
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			_ = conn.Close()
			c.t.Fatalf("locking %s with %q: %v", table, statement, err)
		}
	}
	l := &tableLock{c: c, table: table, conn: conn}
	c.t.Cleanup(l.release)
	return l
}

// release gives the lock up. It runs once, whether a case calls it or the
// cleanup does.
func (l *tableLock) release() {
	l.once.Do(func() {
		if _, err := l.conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
			l.c.t.Errorf("releasing the lock on %s: %v", l.table, err)
		}
		_ = l.conn.Close()
	})
}

// awaitWaiters waits until n statements the host issued wait on the lock. On
// PostgreSQL the lock table says so; on SQLite the wait is a pause.
func (l *tableLock) awaitWaiters(n int) {
	l.c.t.Helper()
	if l.c.dialect != "postgres" {
		time.Sleep(sqliteReach())
		return
	}
	var waiting int
	if !eventually(startLimit, func() bool {
		waiting = l.c.count("SELECT count(*) FROM pg_locks WHERE relation = ?::regclass AND NOT granted", l.table)
		return waiting >= n
	}) {
		l.c.t.Fatalf("%d statement(s) wait on the lock on %s, want %d", waiting, l.table, n)
	}
}

// outcome is one concurrent request's answer or the reason it has none.
type outcome struct {
	resp response
	err  error
}

// sendAsync sends a request off the case's goroutine and hands its outcome
// over once it has one.
func (h *crmHost) sendAsync(req request, extra headers) <-chan outcome {
	done := make(chan outcome, 1)
	go func() {
		resp, err := h.doWith(req, extra)
		done <- outcome{resp: resp, err: err}
	}()
	return done
}

// receive takes a concurrent request's outcome, failing the case when it has
// none within the client's own timeout.
func receive(t *testing.T, what string, from <-chan outcome) response {
	t.Helper()
	select {
	case got := <-from:
		if got.err != nil {
			t.Fatalf("%s could not be sent: %v", what, got.err)
		}
		return got.resp
	case <-time.After(client.Timeout + time.Second):
		t.Fatalf("%s was not answered", what)
		return response{}
	}
}

// statuses lists the statuses of several answers in ascending order.
func statuses(answers ...response) []int {
	out := make([]int, 0, len(answers))
	for _, a := range answers {
		out = append(out, a.status)
	}
	slices.Sort(out)
	return out
}

// TestTheDealStatementsStayInTheirTenant is the case for every statement form
// the pipeline, deals and activities modules issue: each keeps to the tenant
// the request names.
//
// acme and globex define stages of the same keys and create a deal and
// activities under the same identifiers, which the composite keys allow. A
// statement missing its tenant condition is then observable: a read returns
// both tenants' rows, or the other tenant's where GORM orders by the key the
// tenant column leads; a write reaches both tenants' rows, or fails to reach
// the one it addresses. Each read is made from both tenants, since a form that
// takes the first row by key answers correctly for the tenant that sorts first
// and wrongly only for the other.
//
// The last subtest sends bursts of creations under one identifier: the look-up
// before the insert lets several through, and the primary key's refusal of
// each is answered as the conflict the look-up gives.
func TestTheDealStatementsStayInTheirTenant(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, globex := w.acme, w.globex
		alice, bob := w.asAlice, w.asBob
		sides := sidesOf(w)

		t.Run("a stage is found or created within its own tenant", func(t *testing.T) {
			sub := c.in(t)
			for _, side := range sides {
				definePipeline(t, w.h, side.ref, side.cred, pipelineOf(side.name))
			}
			// globex defines one stage acme does not have.
			definePipeline(t, w.h, globex, bob, []stageDef{{"negotiate", "Negotiate at globex", 5, 80, "open"}})

			qualify := pipelineOf("acme")[0]
			again := w.h.send(t, api(nethttp.MethodPost, "/pipeline/stages", acme, alice, qualify.body()))
			expect(t, "acme defining qualify again as it is", again, nethttp.StatusOK)
			renamed := qualify
			renamed.name = "Qualify, renamed"
			expect(t, "acme defining qualify again with another name",
				w.h.send(t, api(nethttp.MethodPost, "/pipeline/stages", acme, alice, renamed.body())), nethttp.StatusConflict)

			for _, side := range sides {
				var name string
				err := sub.sql().QueryRow(sub.rebind("SELECT name FROM pipeline_stages WHERE tenant_id = ? AND key = ?"),
					side.ref.ID, "qualify").Scan(&name)
				if err != nil || name != "Qualify at "+side.name {
					t.Errorf("%s's qualify stage reads %q (%v), want %q", side.name, name, err, "Qualify at "+side.name)
				}
			}
		})

		t.Run("a stage listing holds the tenant's own stages", func(t *testing.T) {
			for _, side := range sides {
				resp := w.h.get(t, apiEndpoint, "/pipeline/stages", side.ref.Alias, side.cred)
				expect(t, side.name+" listing its stages", resp, nethttp.StatusOK)
				var got stageList
				resp.decode(t, &got)
				want := pipelineOf(side.name)
				if side.name == "globex" {
					want = append(want, stageDef{"negotiate", "Negotiate at globex", 5, 80, "open"})
				}
				if len(got.Stages) != len(want) {
					t.Errorf("%s's listing holds %+v, want the %d stages it defined", side.name, got.Stages, len(want))
					continue
				}
				for i, s := range want {
					if got.Stages[i].Key != s.key || got.Stages[i].Name != s.name {
						t.Errorf("%s's stage %d reads %+v, want %s %q", side.name, i, got.Stages[i], s.key, s.name)
					}
				}
			}
		})

		t.Run("a probability change reaches the tenant's own stage", func(t *testing.T) {
			resp := w.h.send(t, api(nethttp.MethodPatch, "/pipeline/stages/qualify", acme, alice, map[string]any{"probability": 35}))
			expect(t, "acme changing qualify's probability", resp, nethttp.StatusOK)
			var got stageView
			resp.decode(t, &got)
			if got.Probability != 35 || got.Name != "Qualify at acme" {
				t.Errorf("the change answered %+v, want acme's qualify at 35", got)
			}
			for _, side := range []struct {
				ref  tenantRef
				want int
			}{{acme, 35}, {globex, 20}} {
				n := c.count("SELECT count(*) FROM pipeline_stages WHERE tenant_id = ? AND key = ? AND probability = ?",
					side.ref.ID, "qualify", side.want)
				if n != 1 {
					t.Errorf("%s's qualify stage is not at probability %d after acme's change", side.ref.ID, side.want)
				}
			}
			expect(t, "acme changing a stage only globex has",
				w.h.send(t, api(nethttp.MethodPatch, "/pipeline/stages/negotiate", acme, alice, map[string]any{"probability": 10})),
				nethttp.StatusNotFound)
		})

		t.Run("a deal created under one identifier in each tenant is each tenant's own", func(t *testing.T) {
			sub := c.in(t)
			for _, side := range sides {
				got := w.createDeal(t, side.ref, side.cred, map[string]any{
					"id": "d-1", "account_id": "acc-1", "title": side.name + "'s deal", "amount": 1000,
					"currency": "EUR", "stage": "qualify",
				})
				if got.ID != "d-1" || got.OwnerID != side.owner.ID || got.StageName != "Qualify at "+side.name {
					t.Errorf("%s's creation answered %+v, want d-1 owned by %s in %s's qualify", side.name, got, side.owner.ID, side.name)
				}
				if row, ok := sub.dealRow(side.ref, "d-1"); !ok || row.title != side.name+"'s deal" {
					t.Errorf("%s's deals row reads %+v (present %v)", side.name, row, ok)
				}
			}
			expect(t, "acme creating a second deal d-1",
				w.h.send(t, api(nethttp.MethodPost, "/deals", acme, alice, map[string]any{
					"id": "d-1", "account_id": "acc-1", "title": "again", "amount": 1, "stage": "qualify",
				})), nethttp.StatusConflict)
		})

		t.Run("a deal's stage, account and owner are its own tenant's", func(t *testing.T) {
			sub := c.in(t)
			for _, stage := range []string{"nope", "negotiate"} {
				expect(t, "acme creating a deal in the stage "+stage,
					w.h.send(t, api(nethttp.MethodPost, "/deals", acme, alice, map[string]any{
						"account_id": "acc-1", "title": "in " + stage, "amount": 1, "stage": stage,
					})), nethttp.StatusUnprocessableEntity)
				expect(t, "acme moving d-1 to the stage "+stage, w.move(t, acme, alice, "d-1", stage, 1),
					nethttp.StatusUnprocessableEntity)
			}
			// acc-g is globex's alone, acc-gone an account acme deleted, and
			// no tenant holds acc-none.
			openAccount(t, w.h, globex, bob, "acc-g")
			openAccount(t, w.h, acme, alice, "acc-gone")
			expect(t, "acme deleting acc-gone", w.h.send(t, api(nethttp.MethodDelete, "/accounts/acc-gone", acme, alice, nil)),
				nethttp.StatusNoContent)
			for _, account := range []string{"acc-g", "acc-gone", "acc-none"} {
				expect(t, "acme creating a deal at the account "+account,
					w.h.send(t, api(nethttp.MethodPost, "/deals", acme, alice, map[string]any{
						"account_id": account, "title": "at " + account, "amount": 1, "stage": "qualify",
					})), nethttp.StatusUnprocessableEntity)
				expect(t, "acme giving d-1 the account "+account,
					w.h.sendWith(t, api(nethttp.MethodPatch, "/deals/d-1", acme, alice, map[string]any{"account_id": account}),
						headers{"If-Match": `"1"`}), nethttp.StatusUnprocessableEntity)
			}
			// bob is globex's member and not acme's.
			expect(t, "acme creating a deal owned by bob",
				w.h.send(t, api(nethttp.MethodPost, "/deals", acme, alice, map[string]any{
					"account_id": "acc-1", "title": "bob's in acme", "amount": 1, "stage": "qualify", "owner_id": w.bob.ID,
				})), nethttp.StatusUnprocessableEntity)
			if n := c.count("SELECT count(*) FROM deals WHERE tenant_id = ?", acme.ID); n != 1 {
				t.Errorf("acme holds %d deals after the refused creations, want 1", n)
			}
			expect(t, "acme handing d-1 to bob",
				w.h.sendWith(t, api(nethttp.MethodPatch, "/deals/d-1", acme, alice, map[string]any{"owner_id": w.bob.ID}),
					headers{"If-Match": `"1"`}), nethttp.StatusUnprocessableEntity)
			if row, _ := sub.dealRow(acme, "d-1"); row.owner != w.alice.ID || row.version != 1 {
				t.Errorf("acme's d-1 reads %+v after it was refused to bob and to other accounts, want alice's at "+
					"version 1", row)
			}
		})

		t.Run("a listing counts and joins the tenant's own rows", func(t *testing.T) {
			for _, side := range sides {
				resp := w.h.get(t, apiEndpoint, "/deals", side.ref.Alias, side.cred)
				expect(t, side.name+" listing its deals", resp, nethttp.StatusOK)
				var got dealList
				resp.decode(t, &got)
				if got.Total != 1 || len(got.Deals) != 1 {
					t.Errorf("%s's listing counts %d and holds %+v, want its one deal", side.name, got.Total, got.Deals)
					continue
				}
				d := got.Deals[0]
				if d.Title != side.name+"'s deal" || d.StageName != "Qualify at "+side.name || d.OwnerName != ownerName(side.owner) {
					t.Errorf("%s's listing holds %+v, want its own deal, stage and owner", side.name, d)
				}
				filtered := w.h.get(t, apiEndpoint, "/deals?stage=qualify&owner="+side.owner.ID, side.ref.Alias, side.cred)
				expect(t, side.name+" listing its deals by stage and owner", filtered, nethttp.StatusOK)
				var only dealList
				filtered.decode(t, &only)
				if only.Total != 1 {
					t.Errorf("%s's filtered listing counts %d, want 1", side.name, only.Total)
				}
			}
		})

		t.Run("a deal read holds the tenant's own row and stage", func(t *testing.T) {
			for _, side := range sides {
				got := w.getDeal(t, side.ref, side.cred, "d-1")
				if got.Title != side.name+"'s deal" || got.StageName != "Qualify at "+side.name ||
					got.OwnerName != ownerName(side.owner) || got.Version != 1 {
					t.Errorf("%s reading d-1 got %+v, want its own deal at version 1", side.name, got)
				}
			}
		})

		t.Run("a change reaches the tenant's own deal", func(t *testing.T) {
			sub := c.in(t)
			// globex first: a read-back that took the first d-1 by key would
			// answer with acme's.
			for _, side := range []struct {
				ref   tenantRef
				cred  credential
				title string
			}{{globex, bob, "globex's renamed deal"}, {acme, alice, "acme's renamed deal"}} {
				resp := w.h.sendWith(t, api(nethttp.MethodPatch, "/deals/d-1", side.ref, side.cred,
					map[string]any{"title": side.title, "amount": 1500}), headers{"If-Match": `"1"`})
				expect(t, "changing d-1 in "+side.ref.ID, resp, nethttp.StatusOK)
				var got dealView
				resp.decode(t, &got)
				if got.Title != side.title || got.Version != 2 || got.Amount != 1500 {
					t.Errorf("the change in %s answered %+v, want %q at version 2", side.ref.ID, got, side.title)
				}
				if etag := resp.header.Get("ETag"); etag != `"2"` {
					t.Errorf("the change in %s answered the ETag %q, want \"2\"", side.ref.ID, etag)
				}
			}
			for _, side := range []struct {
				ref   tenantRef
				title string
			}{{acme, "acme's renamed deal"}, {globex, "globex's renamed deal"}} {
				if row, _ := sub.dealRow(side.ref, "d-1"); row.title != side.title || row.version != 2 {
					t.Errorf("%s's d-1 reads %+v, want %q at version 2", side.ref.ID, row, side.title)
				}
			}
		})

		t.Run("a stage move changes the tenant's own deal and is on its own timeline", func(t *testing.T) {
			sub := c.in(t)
			resp := w.move(t, globex, bob, "d-1", "won", 2)
			expect(t, "globex moving d-1 to won", resp, nethttp.StatusOK)
			var got moveView
			resp.decode(t, &got)
			if got.Deal.Title != "globex's renamed deal" || got.Deal.Stage != "won" || got.Deal.StageName != "Won at globex" ||
				got.Deal.Version != 3 || got.Deal.ClosedAt == nil {
				t.Errorf("globex's move answered %+v, want globex's d-1 closed in its won stage at version 3", got.Deal)
			}
			if got.Activity.Kind != activityStageChange || got.Activity.SubjectID != "d-1" || got.Activity.EventID == nil {
				t.Errorf("globex's move recorded %+v, want a stage change of d-1 carrying its event", got.Activity)
			}
			if row, _ := sub.dealRow(acme, "d-1"); row.stage != "qualify" || row.version != 2 {
				t.Errorf("acme's d-1 reads %+v after globex's move, want qualify at version 2", row)
			}
			if acmes := w.timelineOf(t, acme, alice, "d-1"); len(acmes) != 0 {
				t.Errorf("acme's timeline of d-1 holds %+v after globex's move, want nothing", acmes)
			}
			if globexes := w.timelineOf(t, globex, bob, "d-1"); len(globexes) != 1 || globexes[0].Kind != activityStageChange {
				t.Errorf("globex's timeline of d-1 holds %+v, want its stage change", globexes)
			}
		})

		t.Run("activities under one identifier in each tenant are each tenant's own, in order", func(t *testing.T) {
			record := func(ref tenantRef, cred credential, id, kind, body, at string) {
				t.Helper()
				resp := w.h.send(t, api(nethttp.MethodPost, "/activities", ref, cred, map[string]any{
					"id": id, "subject_kind": "deal", "subject_id": "d-1", "kind": kind, "body": body, "occurred_at": at,
				}))
				expect(t, "recording "+id+" in "+ref.ID, resp, nethttp.StatusCreated)
			}
			// acme's two are recorded out of the order they occurred in.
			record(acme, alice, "a-1", "note", "acme's note", "2026-01-02T09:00:00Z")
			record(acme, alice, "a-2", "call", "acme's call", "2026-01-01T09:00:00Z")
			record(globex, bob, "a-1", "email", "globex's email", "2026-01-03T09:00:00Z")

			acmes := w.timelineOf(t, acme, alice, "d-1")
			if got := activityIDs(acmes); !slices.Equal(got, []string{"a-2", "a-1"}) || acmes[1].Body != "acme's note" {
				t.Errorf("acme's timeline holds %+v, want its call then its note", acmes)
			}
			globexes := w.timelineOf(t, globex, bob, "d-1")
			if len(globexes) != 2 || globexes[0].ID != "a-1" || globexes[0].Body != "globex's email" ||
				globexes[1].Kind != activityStageChange {
				t.Errorf("globex's timeline holds %+v, want its email then its stage change", globexes)
			}
			// d-2 is globex's alone.
			w.createDeal(t, globex, bob, map[string]any{
				"id": "d-2", "account_id": "acc-2", "title": "globex's second", "amount": 1, "stage": "qualify",
			})
			expect(t, "acme recording an activity on globex's d-2",
				w.h.send(t, api(nethttp.MethodPost, "/activities", acme, alice, map[string]any{
					"subject_kind": "deal", "subject_id": "d-2", "kind": "note", "body": "not mine",
				})), nethttp.StatusUnprocessableEntity)
			expect(t, "acme reading the timeline of globex's d-2",
				w.h.get(t, apiEndpoint, "/deals/d-2/timeline", acme.Alias, alice), nethttp.StatusNotFound)
		})

		t.Run("a write to a deal only the other tenant holds finds no deal", func(t *testing.T) {
			sub := c.in(t)
			// At the version globex's d-2 is at: a write that reached it, or
			// a look-up that saw it, would answer 200 or 412 rather than 404.
			expect(t, "acme changing globex's d-2",
				w.h.sendWith(t, api(nethttp.MethodPatch, "/deals/d-2", acme, alice, map[string]any{"title": "not mine"}),
					headers{"If-Match": `"1"`}), nethttp.StatusNotFound)
			expect(t, "acme moving globex's d-2", w.move(t, acme, alice, "d-2", "proposal", 1), nethttp.StatusNotFound)
			expect(t, "acme deleting globex's d-2", w.h.send(t, api(nethttp.MethodDelete, "/deals/d-2", acme, alice, nil)),
				nethttp.StatusNotFound)
			if row, ok := sub.dealRow(globex, "d-2"); !ok || row.title != "globex's second" || row.stage != "qualify" ||
				row.version != 1 || row.deleted {
				t.Errorf("globex's d-2 reads %+v (present %v) after acme's writes, want it as globex created it", row, ok)
			}
			if _, ok := sub.dealRow(acme, "d-2"); ok {
				t.Errorf("acme holds a d-2 after writing to globex's")
			}
		})

		t.Run("an activity deleted in one tenant stays in the other", func(t *testing.T) {
			expect(t, "acme deleting a-1", w.h.send(t, api(nethttp.MethodDelete, "/activities/a-1", acme, alice, nil)),
				nethttp.StatusNoContent)
			if got := activityIDs(w.timelineOf(t, acme, alice, "d-1")); !slices.Equal(got, []string{"a-2"}) {
				t.Errorf("acme's timeline holds %v after the deletion, want a-2 alone", got)
			}
			if n := c.count("SELECT count(*) FROM activities WHERE tenant_id = ? AND id = ?", globex.ID, "a-1"); n != 1 {
				t.Errorf("globex's a-1 is gone after acme deleted its own")
			}
			globexes := w.timelineOf(t, globex, bob, "d-1")
			if len(globexes) != 2 || globexes[1].Kind != activityStageChange {
				t.Fatalf("globex's timeline of d-1 holds %+v, want its email then its stage change", globexes)
			}
			stageChange := globexes[1].ID
			expect(t, "globex deleting its stage change",
				w.h.send(t, api(nethttp.MethodDelete, "/activities/"+stageChange, globex, bob, nil)), nethttp.StatusConflict)
			expect(t, "acme deleting globex's stage change",
				w.h.send(t, api(nethttp.MethodDelete, "/activities/"+stageChange, acme, alice, nil)), nethttp.StatusNotFound)
		})

		t.Run("a deal deleted in one tenant stays in the other", func(t *testing.T) {
			sub := c.in(t)
			expect(t, "acme deleting d-1", w.h.send(t, api(nethttp.MethodDelete, "/deals/d-1", acme, alice, nil)),
				nethttp.StatusNoContent)
			expect(t, "acme reading its deleted d-1", w.h.get(t, apiEndpoint, "/deals/d-1", acme.Alias, alice),
				nethttp.StatusNotFound)
			expect(t, "acme deleting d-1 again", w.h.send(t, api(nethttp.MethodDelete, "/deals/d-1", acme, alice, nil)),
				nethttp.StatusNotFound)
			if got := w.getDeal(t, globex, bob, "d-1"); got.Title != "globex's renamed deal" {
				t.Errorf("globex reading d-1 after acme deleted its own got %+v", got)
			}
			if row, ok := sub.dealRow(acme, "d-1"); !ok || !row.deleted {
				t.Errorf("acme's d-1 row reads %+v (present %v), want it kept and marked deleted", row, ok)
			}
			if row, ok := sub.dealRow(globex, "d-1"); !ok || row.deleted {
				t.Errorf("globex's d-1 row reads %+v (present %v), want it kept and not deleted", row, ok)
			}
			var listed dealList
			resp := w.h.get(t, apiEndpoint, "/deals", acme.Alias, alice)
			expect(t, "acme listing after the deletion", resp, nethttp.StatusOK)
			resp.decode(t, &listed)
			if listed.Total != 0 || len(listed.Deals) != 0 {
				t.Errorf("acme's listing after the deletion reads %+v, want nothing", listed)
			}
		})

		t.Run("a deleted deal keeps its identifier in its own tenant alone", func(t *testing.T) {
			sub := c.in(t)
			again := w.h.send(t, api(nethttp.MethodPost, "/deals", acme, alice, map[string]any{
				"id": "d-1", "account_id": "acc-1", "title": "acme's second d-1", "amount": 1, "stage": "qualify",
			}))
			expect(t, "acme creating d-1 again after deleting it", again, nethttp.StatusConflict)
			if row, ok := sub.dealRow(acme, "d-1"); !ok || !row.deleted || row.title != "acme's renamed deal" {
				t.Errorf("acme's d-1 row reads %+v (present %v) after the refused creation, want the deleted deal", row, ok)
			}
			// The look-up that sees deleted deals is still the tenant's: d-2
			// is globex's alone, and acme creates its own.
			got := w.createDeal(t, acme, alice, map[string]any{
				"id": "d-2", "account_id": "acc-2", "title": "acme's d-2", "amount": 1, "stage": "qualify",
			})
			if got.ID != "d-2" || got.StageName != "Qualify at acme" {
				t.Errorf("acme's creation of d-2 answered %+v, want acme's d-2 in its qualify stage", got)
			}
			if row, _ := sub.dealRow(globex, "d-2"); row.title != "globex's second" || row.version != 1 {
				t.Errorf("globex's d-2 reads %+v after acme created its own, want it as globex created it", row)
			}
		})

		t.Run("every write is in the journal under the tenant it wrote in", func(t *testing.T) {
			sub := c.in(t)
			type written struct{ route, outcome string }
			for _, side := range []struct {
				ref    tenantRef
				user   userRef
				writes []written
			}{
				{acme, w.alice, []written{
					{createStageRoute, "201"},
					{patchStageRoute, "200"},
					{createDealRoute, "201"},
					{patchDealRoute, "200"},
					{moveDealRoute, "422"},
					{createActivityRoute, "201"},
					{deleteActivityRoute, "204"},
					{deleteDealRoute, "204"},
				}},
				{globex, w.bob, []written{
					{createStageRoute, "201"},
					{createDealRoute, "201"},
					{patchDealRoute, "200"},
					{moveDealRoute, "200"},
					{createActivityRoute, "201"},
					{deleteActivityRoute, "409"},
				}},
			} {
				actor := sessionActor(side.user)
				for _, write := range side.writes {
					sub.awaitJournal(write.route+" answered "+write.outcome+" in "+side.ref.ID, func(r auditRow) bool {
						return r.source == "request" && r.object == write.route && r.outcome == write.outcome &&
							r.tenant == side.ref.ID && r.actor == actor
					})
				}
			}
		})

		t.Run("a burst of creations under one identifier writes one and answers the rest as conflicts", func(t *testing.T) {
			sub := c.in(t)
			// Every creation of a burst is sent while the harness holds the
			// table's writes back, so none can commit before every one has
			// made its look-up: each finds the identifier free and meets the
			// primary key at the insert, and every conflict but none of the
			// look-up's is the key's refusal, answered as the look-up's is,
			// never as a server error.
			for _, form := range []struct {
				what, path, table string
				body              map[string]any
			}{
				{"a deal", "/deals", dealTable, map[string]any{
					"id": "d-burst", "account_id": "acc-1", "title": "sent at once", "amount": 1, "stage": "qualify",
				}},
				{"an activity", "/activities", activityTable, map[string]any{
					"id": "a-burst", "subject_kind": "deal", "subject_id": "d-2", "kind": "note", "body": "sent at once",
				}},
			} {
				release := sub.holdWrites(form.table)
				pending := make([]<-chan outcome, creationBurst)
				for i := range pending {
					pending[i] = w.h.sendAsync(api(nethttp.MethodPost, form.path, acme, alice, form.body), nil)
				}
				sub.awaitWritersHeld(form.table, creationBurst)
				release()
				answered := map[int]int{}
				for _, p := range pending {
					answered[receive(t, "a creation of "+form.what+" in the burst", p).status]++
				}
				if answered[nethttp.StatusCreated] != 1 || answered[nethttp.StatusConflict] != creationBurst-1 {
					t.Errorf("a burst of %d creations of %s under one identifier answered %v, "+
						"want one 201 and the rest 409", creationBurst, form.what, answered)
				}
				if n := sub.count("SELECT count(*) FROM "+form.table+" WHERE tenant_id = ? AND id = ?",
					acme.ID, form.body["id"]); n != 1 {
					t.Errorf("acme holds %d rows of %s %v after the burst, want 1", n, form.what, form.body["id"])
				}
			}
		})
	})
}

// creationBurst is how many creations under one identifier the burst case
// sends at once. Each holds one of the process's database connections while it
// waits at its insert, so it is no more than casePostgresConns.
const creationBurst = 8

// writersHeldPause is how long the harness holds a SQLite database's writes
// back once a burst is sent. Nothing there tells a writer waiting for the lock
// from one still reading, so the pause is set far past what a burst takes to
// reach its inserts and well within the driver's five-second busy timeout,
// which a waiting writer gives up at.
var writersHeldPause = scaledForRace(time.Second)

// holdWrites holds back every write to a table of the case's database, and
// lets every read of it through, until the function it returns is called: on
// PostgreSQL a SHARE lock on the table, which conflicts with the lock an
// insert takes and with none a read takes, and on SQLite the database's write
// lock, which the harness's locator has a transaction begin by taking, as the
// host's does. A request's transaction then waits at its insert until the
// release.
func (c *crmCase) holdWrites(table string) func() {
	c.t.Helper()
	ctx := c.t.Context()
	conn, err := c.sql().Conn(ctx)
	if err != nil {
		c.t.Fatalf("taking a connection to hold %s's writes: %v", table, err)
	}
	if c.dialect != "postgres" {
		// The host may be writing as the lock is asked for; a connection
		// with no busy timeout would be refused it at once.
		if _, err = conn.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
			c.t.Fatalf("setting the holding connection's busy timeout: %v", err)
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		c.t.Fatalf("beginning the transaction that holds %s's writes: %v", table, err)
	}
	if c.dialect == "postgres" {
		if _, err = tx.ExecContext(ctx, "LOCK TABLE "+table+" IN SHARE MODE"); err != nil {
			c.t.Fatalf("locking %s: %v", table, err)
		}
	}
	return func() {
		c.t.Helper()
		if err := tx.Rollback(); err != nil {
			c.t.Errorf("releasing %s's writes: %v", table, err)
		}
		_ = conn.Close()
	}
}

// awaitWritersHeld waits until the writes holdWrites holds back on a table
// number n. On PostgreSQL each is a statement of the case's database waiting
// for the table's lock, which the server lists, so the wait ends once every
// one has reached its write. SQLite lists nothing of the kind, so there the
// wait is writersHeldPause.
func (c *crmCase) awaitWritersHeld(table string, n int) {
	c.t.Helper()
	if c.dialect != "postgres" {
		time.Sleep(writersHeldPause)
		return
	}
	var waiting int
	if !eventually(settleLimit, func() bool {
		err := c.sql().QueryRowContext(c.t.Context(),
			"SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid "+
				"WHERE a.datname = current_database() AND l.locktype = 'relation' "+
				"AND l.relation = to_regclass($1) AND NOT l.granted", table).Scan(&waiting)
		if err != nil {
			c.t.Fatalf("counting the statements waiting for %s's lock: %v", table, err)
		}
		return waiting >= n
	}) {
		c.t.Errorf("within %s %d statements waited for %s's held writes, want %d", settleLimit, waiting, table, n)
	}
}

// TestAnAccountADealNamesKeepsItsRow is the case for the reference from a deal
// to its account. The account's delete is soft: it leaves the row the deal
// names and goes through, the deal keeps its account, and a new deal at the
// deleted account is refused. A purge removes the row, which the deals table's
// reference refuses: it is answered as a conflict, and the account and the deal
// stay as they were. So is the purge of an account whose one deal was deleted,
// since a deal's delete leaves its row. An account no deal names is purged,
// which shows the purge itself goes through.
func TestAnAccountADealNamesKeepsItsRow(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		deal := w.createDeal(t, acme, alice, map[string]any{
			"account_id": "acc-1", "title": "with acc-1", "amount": 10, "stage": "qualify",
		})

		expect(t, "acme deleting acc-1, which a deal names",
			w.h.send(t, api(nethttp.MethodDelete, "/accounts/acc-1", acme, alice, nil)), nethttp.StatusNoContent)
		if got := w.getDeal(t, acme, alice, deal.ID); got.AccountID != "acc-1" {
			t.Errorf("the deal reads %+v once its account was deleted, want it still with acc-1", got)
		}
		expect(t, "acme creating a deal at the deleted acc-1",
			w.h.send(t, api(nethttp.MethodPost, "/deals", acme, alice, map[string]any{
				"account_id": "acc-1", "title": "at a deleted account", "amount": 1, "stage": "qualify",
			})), nethttp.StatusUnprocessableEntity)

		purge := w.h.send(t, api(nethttp.MethodPost, "/accounts/acc-1/purge", acme, alice, nil))
		if purge.status != nethttp.StatusConflict {
			t.Errorf("purging acc-1, which a deal names, answered %d %s, want 409", purge.status, purge.body)
		}
		if n := c.count("SELECT count(*) FROM accounts WHERE tenant_id = ? AND id = ?", acme.ID, "acc-1"); n != 1 {
			t.Errorf("acme holds %d rows for acc-1 after the purge a deal refused, want 1", n)
		}
		if row, ok := c.dealRow(acme, deal.ID); !ok || row.deleted || row.version != 1 {
			t.Errorf("the deal's row reads %+v (present %v) after its account's purge was refused, want it as it was", row, ok)
		}

		// A deleted deal still names its account: its delete is soft, so its
		// row and its reference stay, and the purge is refused as for a live
		// one.
		gone := w.createDeal(t, acme, alice, map[string]any{
			"account_id": "acc-9", "title": "deleted at acc-9", "amount": 10, "stage": "qualify",
		})
		expect(t, "acme deleting the deal at acc-9",
			w.h.send(t, api(nethttp.MethodDelete, "/deals/"+gone.ID, acme, alice, nil)), nethttp.StatusNoContent)
		purge = w.h.send(t, api(nethttp.MethodPost, "/accounts/acc-9/purge", acme, alice, nil))
		if purge.status != nethttp.StatusConflict {
			t.Errorf("purging acc-9, which only a deleted deal names, answered %d %s, want 409", purge.status, purge.body)
		}
		if n := c.count("SELECT count(*) FROM accounts WHERE tenant_id = ? AND id = ?", acme.ID, "acc-9"); n != 1 {
			t.Errorf("acme holds %d rows for acc-9 after the purge a deleted deal refused, want 1", n)
		}
		if row, ok := c.dealRow(acme, gone.ID); !ok || !row.deleted {
			t.Errorf("the deleted deal's row reads %+v (present %v) after its account's purge was refused, "+
				"want it there and deleted", row, ok)
		}

		expect(t, "acme purging acc-2, which no deal names",
			w.h.send(t, api(nethttp.MethodPost, "/accounts/acc-2/purge", acme, alice, nil)), nethttp.StatusNoContent)
		if n := c.count("SELECT count(*) FROM accounts WHERE tenant_id = ? AND id = ?", acme.ID, "acc-2"); n != 0 {
			t.Errorf("acme holds %d rows for acc-2 after purging it, want none", n)
		}
	})
}

// ownerName is the display name a user created by the harness carries: the
// name it was created under, which starts its email.
func ownerName(u userRef) string {
	name, _, _ := strings.Cut(u.Email, "@")
	return name
}

// orNone renders an identifier an activity may not carry, for a failure
// message.
func orNone(id *string) string {
	if id == nil {
		return "none"
	}
	return *id
}

// activitiesText renders a timeline for a failure message, with the event
// identifiers an activity carries written out rather than as pointers.
func activitiesText(activities []activityView) string {
	parts := make([]string, 0, len(activities))
	for _, a := range activities {
		parts = append(parts, fmt.Sprintf("%s %s %q event=%s won=%s", a.ID, a.Kind, a.Body, orNone(a.EventID), orNone(a.WonEventID)))
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// activityIDs lists a timeline's activity identifiers in order.
func activityIDs(activities []activityView) []string {
	out := make([]string, 0, len(activities))
	for _, a := range activities {
		out = append(out, a.ID)
	}
	return out
}

// idempotencyLifetime is how long the idempotency case's short-lived tenant
// remembers a key: long enough for a creation and its replay to be answered
// inside it, on a binary built with the race detector as well.
var idempotencyLifetime = scaledForRace(2 * time.Second)

// TestADealCreationIsIdempotentByKey is the case for POST /deals under an
// Idempotency-Key: the claim in the key-value store the stack names, the
// answer stored in it, and the lifetime the setting gives it.
func TestADealCreationIsIdempotentByKey(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		var carol userRef
		w := startDeals(t, c, func(w *dealsWorld) {
			// alice belongs to globex as well, so one caller's key is tried
			// in two tenants; carol is a second caller in acme.
			c.grant(w.globex, w.alice, roleRep)
			carol = c.createUser("carol")
			c.grant(w.acme, carol, roleRep)
			c.setForTenant(w.globex, "deals.idempotency-ttl", idempotencyLifetime.String())
		})
		acme, globex := w.acme, w.globex
		alice := w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		definePipeline(t, w.h, globex, w.asBob, pipelineOf("globex"))

		create := func(ref tenantRef, cred credential, title string) request {
			return api(nethttp.MethodPost, "/deals", ref, cred, map[string]any{
				"account_id": "acc-1", "title": title, "amount": 5000, "stage": "qualify",
			})
		}
		keyed := func(key string) headers { return headers{"Idempotency-Key": key} }
		titled := func(ref tenantRef, title string) int {
			return c.count("SELECT count(*) FROM deals WHERE tenant_id = ? AND title = ?", ref.ID, title)
		}
		var firstID string

		t.Run("a replay answers the stored response", func(t *testing.T) {
			first := w.h.sendWith(t, create(acme, alice, "replayed"), keyed("key-replay"))
			expect(t, "the first creation under a key", first, nethttp.StatusCreated)
			var created dealView
			first.decode(t, &created)
			firstID = created.ID
			again := w.h.sendWith(t, create(acme, alice, "replayed"), keyed("key-replay"))
			expect(t, "the same creation again under the key", again, nethttp.StatusCreated)
			if !bytes.Equal(again.body, first.body) {
				t.Errorf("the replay answered %s, want the stored answer %s", again.body, first.body)
			}
			if again.header.Get("ETag") != first.header.Get("ETag") || again.header.Get("Idempotent-Replayed") != "true" ||
				first.header.Get("Idempotent-Replayed") != "" {
				t.Errorf("the first answer carries ETag %q, replayed %q; the replay ETag %q, replayed %q",
					first.header.Get("ETag"), first.header.Get("Idempotent-Replayed"),
					again.header.Get("ETag"), again.header.Get("Idempotent-Replayed"))
			}
			if n := titled(acme, "replayed"); n != 1 {
				t.Errorf("acme holds %d deals titled replayed after a creation and its replay, want 1", n)
			}
		})

		t.Run("the same key with another body is refused", func(t *testing.T) {
			other := w.h.sendWith(t, create(acme, alice, "another body"), keyed("key-replay"))
			expect(t, "another creation under a used key", other, nethttp.StatusUnprocessableEntity)
			if n := titled(acme, "another body"); n != 0 {
				t.Errorf("acme holds %d deals from the refused body, want none", n)
			}
		})

		t.Run("a key is its caller's and its tenant's own", func(t *testing.T) {
			for _, attempt := range []struct {
				what string
				ref  tenantRef
				cred credential
			}{
				{"carol, in acme", acme, bearer(w.h.login(t, carol))},
				{"alice, in globex", globex, alice},
			} {
				resp := w.h.sendWith(t, create(attempt.ref, attempt.cred, "replayed"), keyed("key-replay"))
				expect(t, attempt.what+" creating under alice's key in acme", resp, nethttp.StatusCreated)
				var got dealView
				resp.decode(t, &got)
				if got.ID == firstID || resp.header.Get("Idempotent-Replayed") != "" {
					t.Errorf("%s was answered with alice's stored creation %s", attempt.what, resp.body)
				}
			}
			if a, g := titled(acme, "replayed"), titled(globex, "replayed"); a != 2 || g != 1 {
				t.Errorf("acme holds %d and globex %d deals titled replayed, want 2 and 1", a, g)
			}
		})

		t.Run("a duplicate while the first is in flight answers 409", func(t *testing.T) {
			sub := c.in(t)
			req := create(acme, alice, "in flight")
			lock := sub.lockTable(dealTable)
			first := w.h.sendAsync(req, keyed("key-in-flight"))
			lock.awaitWaiters(1)
			second := w.h.sendAsync(req, keyed("key-in-flight"))
			var duplicate response
			if c.dialect == "postgres" {
				// Answered while the first still waits on its insert: the
				// duplicate's answer writes nothing the lock holds up.
				duplicate = receive(t, "the duplicate", second)
				lock.release()
			} else {
				// SQLite has one writer, and the journal's row for the
				// duplicate's answer waits on the same lock.
				time.Sleep(sqliteReach())
				lock.release()
				duplicate = receive(t, "the duplicate", second)
			}
			original := receive(t, "the first creation", first)
			if got := statuses(original, duplicate); !slices.Equal(got, []int{nethttp.StatusCreated, nethttp.StatusConflict}) {
				t.Fatalf("a creation and its duplicate in flight answered %d %s and %d %s, want one 201 and one 409",
					original.status, original.body, duplicate.status, duplicate.body)
			}
			if n := titled(acme, "in flight"); n != 1 {
				t.Errorf("acme holds %d deals from a creation and its duplicate, want 1", n)
			}
			created := original
			if duplicate.status == nethttp.StatusCreated {
				created = duplicate
			}
			replay := w.h.sendWith(t, req, keyed("key-in-flight"))
			if replay.status != nethttp.StatusCreated || !bytes.Equal(replay.body, created.body) {
				t.Errorf("the replay once the first was answered answered %d %s, want its 201 %s",
					replay.status, replay.body, created.body)
			}
		})

		t.Run("of a burst of duplicates exactly one creates", func(t *testing.T) {
			// Each round's duplicates are let go at once, so that as many as
			// the host takes together reach the claim together. A claim made
			// by reading the key and then writing it lets two duplicates
			// through only when both read before either writes. On a store
			// reached over a connection one burst makes that likely and the
			// rounds, each under a key of its own, make it all but certain; the
			// in-process store answers both halves too quickly for a burst to
			// land between them, so there the rounds show the property and do
			// not tell the two claims apart.
			const rounds, burst = 12, 32
			for round := range rounds {
				title := fmt.Sprintf("burst %d", round)
				key := fmt.Sprintf("key-burst-%d", round)
				req := create(acme, alice, title)
				answers := make([]outcome, burst)
				start := make(chan struct{})
				var wg sync.WaitGroup
				for i := range answers {
					wg.Go(func() {
						<-start
						answers[i].resp, answers[i].err = w.h.doWith(req, keyed(key))
					})
				}
				close(start)
				wg.Wait()
				var createdBody []byte
				for _, a := range answers {
					switch {
					case a.err != nil:
						t.Fatalf("a duplicate under %s could not be sent: %v", key, a.err)
					case a.resp.status == nethttp.StatusConflict:
					case a.resp.status == nethttp.StatusCreated && createdBody == nil:
						createdBody = a.resp.body
					case a.resp.status == nethttp.StatusCreated && !bytes.Equal(a.resp.body, createdBody):
						t.Errorf("two duplicates under %s were answered with two creations: %s and %s", key, createdBody, a.resp.body)
					case a.resp.status != nethttp.StatusCreated:
						t.Errorf("a duplicate under %s answered %d %s, want 201 or 409", key, a.resp.status, a.resp.body)
					}
				}
				if createdBody == nil {
					t.Errorf("no duplicate of %d under %s was answered with the creation", burst, key)
				}
				if n := titled(acme, title); n != 1 {
					t.Errorf("acme holds %d deals from %d duplicates under %s, want 1", n, burst, key)
				}
			}
		})

		t.Run("a key lapses after its tenant's lifetime", func(t *testing.T) {
			req := create(globex, alice, "short-lived")
			first := w.h.sendWith(t, req, keyed("key-lifetime"))
			expect(t, "a creation under a key in globex", first, nethttp.StatusCreated)
			again := w.h.sendWith(t, req, keyed("key-lifetime"))
			if !bytes.Equal(again.body, first.body) {
				t.Errorf("the replay within globex's lifetime answered %s, want %s", again.body, first.body)
			}
			time.Sleep(idempotencyLifetime + 2*lifetimeSlack)
			later := w.h.sendWith(t, req, keyed("key-lifetime"))
			expect(t, "the creation again once the key lapsed", later, nethttp.StatusCreated)
			if bytes.Equal(later.body, first.body) || later.header.Get("Idempotent-Replayed") != "" {
				t.Errorf("the creation %s after globex's lifetime of %s was answered from the lapsed key",
					later.body, idempotencyLifetime)
			}
			if n := titled(globex, "short-lived"); n != 2 {
				t.Errorf("globex holds %d deals titled short-lived, want 2", n)
			}
		})
	})
}

// TestTwoWritersNamingOneVersionAreOneSuccessAndOnePreconditionFailure is the
// case for optimistic concurrency on a deal: a change and a stage move name the
// version they read in If-Match, and the update is conditioned on that version.
//
// Two concurrent changes, and then two concurrent moves, are each held at their
// write by a lock the case takes, so each has read what it read before either
// writes; a version checked by a read and not by the update would let both
// through. The move reads the deal before its transaction, so its version
// check before the write passes for both moves and the update's condition is
// what stops the second.
func TestTwoWritersNamingOneVersionAreOneSuccessAndOnePreconditionFailure(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		deal := w.createDeal(t, acme, alice, map[string]any{
			"account_id": "acc-1", "title": "contested", "amount": 900, "stage": "qualify",
		})
		patch := func(title string) request {
			return api(nethttp.MethodPatch, "/deals/"+deal.ID, acme, alice, map[string]any{"title": title})
		}

		t.Run("a write names the version it read", func(t *testing.T) {
			sub := c.in(t)
			expect(t, "a change without If-Match", w.h.send(t, patch("no version")), nethttp.StatusPreconditionRequired)
			for _, tag := range []string{`"7"`, `W/"1"`, `1`} {
				expect(t, "a change under If-Match "+tag, w.h.sendWith(t, patch("stale"), headers{"If-Match": tag}),
					nethttp.StatusPreconditionFailed)
			}
			if row, _ := sub.dealRow(acme, deal.ID); row.title != "contested" || row.version != 1 {
				t.Errorf("the refused changes left the deal at %+v", row)
			}
		})

		t.Run("of two concurrent writes naming one version one succeeds", func(t *testing.T) {
			sub := c.in(t)
			lock := sub.lockTable(dealTable)
			first := w.h.sendAsync(patch("first writer"), headers{"If-Match": `"1"`})
			lock.awaitWaiters(1)
			second := w.h.sendAsync(patch("second writer"), headers{"If-Match": `"1"`})
			lock.awaitWaiters(2)
			lock.release()
			a := receive(t, "the first change", first)
			b := receive(t, "the second change", second)
			if got := statuses(a, b); !slices.Equal(got, []int{nethttp.StatusOK, nethttp.StatusPreconditionFailed}) {
				t.Fatalf("two changes naming version 1 answered %d %s and %d %s, want one 200 and one 412",
					a.status, a.body, b.status, b.body)
			}
			winner := a
			if b.status == nethttp.StatusOK {
				winner = b
			}
			var won dealView
			winner.decode(t, &won)
			if row, _ := sub.dealRow(acme, deal.ID); row.title != won.Title || row.version != 2 {
				t.Errorf("the deal reads %+v, want the winner's %q at version 2", row, won.Title)
			}
		})

		t.Run("of two concurrent moves naming one version one succeeds", func(t *testing.T) {
			sub := c.in(t)
			contested := w.createDeal(t, acme, alice, map[string]any{
				"account_id": "acc-1", "title": "contested move", "amount": 900, "stage": "qualify",
			})
			moveTo := func(stage string) request {
				return api(nethttp.MethodPost, "/deals/"+contested.ID+"/stage", acme, alice, map[string]any{"stage": stage})
			}
			lock := sub.lockTable(dealTable)
			first := w.h.sendAsync(moveTo("proposal"), headers{"If-Match": `"1"`})
			lock.awaitWaiters(1)
			second := w.h.sendAsync(moveTo("lost"), headers{"If-Match": `"1"`})
			lock.awaitWaiters(2)
			lock.release()
			a := receive(t, "the first move", first)
			b := receive(t, "the second move", second)
			if got := statuses(a, b); !slices.Equal(got, []int{nethttp.StatusOK, nethttp.StatusPreconditionFailed}) {
				t.Fatalf("two moves naming version 1 answered %d %s and %d %s, want one 200 and one 412",
					a.status, a.body, b.status, b.body)
			}
			winner := a
			if b.status == nethttp.StatusOK {
				winner = b
			}
			var won moveView
			winner.decode(t, &won)
			if row, _ := sub.dealRow(acme, contested.ID); row.stage != won.Deal.Stage || row.version != 2 {
				t.Errorf("the deal reads %+v, want the winner's stage %q at version 2", row, won.Deal.Stage)
			}
			if got := w.timelineOf(t, acme, alice, contested.ID); len(got) != 1 || got[0].ID != won.Activity.ID {
				t.Errorf("the timeline holds %s after two moves at one version, want the winner's stage change %s alone",
					activitiesText(got), won.Activity.ID)
			}
		})

		t.Run("a stage move names the version it read", func(t *testing.T) {
			expect(t, "a move at the version the deal left", w.move(t, acme, alice, deal.ID, "proposal", 1),
				nethttp.StatusPreconditionFailed)
			expect(t, "a move without If-Match",
				w.h.send(t, api(nethttp.MethodPost, "/deals/"+deal.ID+"/stage", acme, alice, map[string]any{"stage": "proposal"})),
				nethttp.StatusPreconditionRequired)
			moved := w.move(t, acme, alice, deal.ID, "proposal", 2)
			expect(t, "a move at the deal's version", moved, nethttp.StatusOK)
			if etag := moved.header.Get("ETag"); etag != `"3"` {
				t.Errorf("the move answered the ETag %q, want \"3\"", etag)
			}
			expect(t, "a move to the stage the deal is in", w.move(t, acme, alice, deal.ID, "proposal", 3),
				nethttp.StatusConflict)
		})
	})
}

// TestADealChangeNamesItsMembersExactly is the case for how a change to a deal
// is read. A member a change leaves out leaves that attribute as it is, so a
// member read other than as the client wrote it would change the deal silently
// or not at all. A body that is null, one naming a member a change does not
// have, one spelling a member in another case and one repeating a member are
// refused as malformed; a change naming nothing, or a value the deal does not
// take, is refused as invalid; and none of them changes the deal.
//
// The property is the reading of the body, which no backend takes part in, so
// it runs on the stack that runs everywhere.
func TestADealChangeNamesItsMembersExactly(t *testing.T) {
	c := newCase(t, stackNamed(t, localStack))
	w := startDeals(t, c, nil)
	acme, alice := w.acme, w.asAlice
	definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
	deal := w.createDeal(t, acme, alice, map[string]any{
		"account_id": "acc-1", "title": "exact", "amount": 100, "stage": "qualify",
	})
	for _, attempt := range []struct {
		what   string
		body   any
		status int
	}{
		{"a null body", json.RawMessage(`null`), nethttp.StatusBadRequest},
		{"a member a change does not have", map[string]any{"stage": "won"}, nethttp.StatusBadRequest},
		{"a member spelled in another case", map[string]any{"Amount": 5}, nethttp.StatusBadRequest},
		{"a member given twice", json.RawMessage(`{"amount":5,"amount":6}`), nethttp.StatusBadRequest},
		{"a change naming nothing", map[string]any{}, nethttp.StatusUnprocessableEntity},
		{"a negative amount", map[string]any{"amount": -1}, nethttp.StatusUnprocessableEntity},
	} {
		resp := w.h.sendWith(t, api(nethttp.MethodPatch, "/deals/"+deal.ID, acme, alice, attempt.body),
			headers{"If-Match": `"1"`})
		expect(t, "a change with "+attempt.what, resp, attempt.status)
	}
	if got := w.getDeal(t, acme, alice, deal.ID); got.Amount != 100 || got.Title != "exact" || got.Version != 1 {
		t.Errorf("the refused changes left the deal at %+v, want it as created", got)
	}
}

// refusalName is the constraint the atomicity case adds to the activities
// table.
const refusalName = "crm_case_refuses_stage_changes"

// refuseStageChanges makes every insert of a stage-change activity fail, with
// a check constraint on PostgreSQL and with a trigger on SQLite, which adds no
// constraint to an existing table.
func (c *crmCase) refuseStageChanges() {
	c.t.Helper()
	if c.dialect == "postgres" {
		c.exec("ALTER TABLE activities ADD CONSTRAINT " + refusalName + " CHECK (kind <> 'stage-change')")
		return
	}
	c.exec("CREATE TRIGGER " + refusalName + " BEFORE INSERT ON activities WHEN NEW.kind = 'stage-change' " +
		"BEGIN SELECT RAISE(ABORT, 'the case refuses a stage change'); END")
}

// allowStageChanges removes what refuseStageChanges added.
func (c *crmCase) allowStageChanges() {
	c.t.Helper()
	if c.dialect == "postgres" {
		c.exec("ALTER TABLE activities DROP CONSTRAINT " + refusalName)
		return
	}
	c.exec("DROP TRIGGER " + refusalName)
}

// statementCount counts the journal's statement rows of one action on one
// table in one tenant.
func (c *crmCase) statementCount(action, table string, ref tenantRef) int {
	c.t.Helper()
	return c.count("SELECT count(*) FROM audit_entries WHERE source = 'statement' AND action = ? AND object = ? AND tenant_id = ?",
		action, table, ref.ID)
}

// TestAStageMoveThatCannotRecordItsActivityChangesNothing is the case for the
// stage move's transaction. The activities table is made to refuse the
// stage-change insert, which comes after the deal's update in the move: the
// move fails, and the deal is as it was — stage, version and closing time —
// with no activity, no statement row for the update and no event. With the
// refusal lifted the same move, at the same version, goes through once.
func TestAStageMoveThatCannotRecordItsActivityChangesNothing(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		deal := w.createDeal(t, acme, alice, map[string]any{
			"account_id": "acc-1", "title": "unmovable", "amount": 700, "stage": "qualify",
		})

		c.refuseStageChanges()
		expect(t, "a move whose activity the table refuses", w.move(t, acme, alice, deal.ID, "won", 1),
			nethttp.StatusInternalServerError)
		after := w.getDeal(t, acme, alice, deal.ID)
		if after.Stage != "qualify" || after.Version != 1 || after.ClosedAt != nil {
			t.Errorf("the failed move left the deal at %+v, want it in qualify at version 1 and open", after)
		}
		if row, _ := c.dealRow(acme, deal.ID); row.stage != "qualify" || row.version != 1 {
			t.Errorf("the deals row reads %+v after the failed move, want qualify at version 1", row)
		}
		if got := w.timelineOf(t, acme, alice, deal.ID); len(got) != 0 {
			t.Errorf("the timeline holds %+v after the failed move, want nothing", got)
		}
		if n := c.statementCount("update", dealTable, acme); n != 0 {
			t.Errorf("the journal holds %d statement rows of an update to deals after the failed move, want none: "+
				"the row rides the transaction the move rolled back", n)
		}

		c.allowStageChanges()
		expect(t, "the same move once the refusal is lifted", w.move(t, acme, alice, deal.ID, "won", 1), nethttp.StatusOK)
		moved := w.getDeal(t, acme, alice, deal.ID)
		if moved.Stage != "won" || moved.Version != 2 || moved.ClosedAt == nil {
			t.Errorf("the move left the deal at %+v, want it won at version 2 and closed", moved)
		}
		if got := w.timelineOf(t, acme, alice, deal.ID); len(got) != 1 || got[0].Kind != activityStageChange {
			t.Errorf("the timeline holds %+v after the move, want its one stage change", got)
		}
		ofThisDeal := func(r eventRow) bool { return r.detail["deal_id"] == deal.ID }
		c.awaitEventRow("the move that went through", "deal.stage-changed", ofThisDeal)
		c.awaitEventRow("the move that went through", "deal.won", ofThisDeal)
		for _, eventType := range []string{"deal.stage-changed", "deal.won"} {
			if rows := slices.DeleteFunc(c.eventRows(eventType), func(r eventRow) bool { return !ofThisDeal(r) }); len(rows) != 1 {
				t.Errorf("the journal holds %d %s rows for the deal, want the one the move that went through published: %+v",
					len(rows), eventType, rows)
			}
		}
		if u, a := c.statementCount("update", dealTable, acme), c.statementCount("create", activityTable, acme); u != 1 || a != 1 {
			t.Errorf("the journal holds %d statement rows of an update to deals and %d of a create in activities, want 1 each", u, a)
		}
	})
}

// commitRefusalName names what refuseCommitsOfDealUpdates adds to the case's
// database.
const commitRefusalName = "crm_case_refuses_commits"

// refuseCommitsOfDealUpdates makes every transaction that updates a deal fail
// at its commit, after every statement in it has succeeded. On PostgreSQL a
// deferred constraint trigger on deals raises at the commit; on SQLite, whose
// one commit-time check is a deferred foreign key, a trigger on deals inserts
// a row whose deferred reference names nothing.
func (c *crmCase) refuseCommitsOfDealUpdates() {
	c.t.Helper()
	if c.dialect == "postgres" {
		c.exec("CREATE FUNCTION " + commitRefusalName + "() RETURNS trigger LANGUAGE plpgsql AS " +
			"$$ BEGIN RAISE EXCEPTION 'the case refuses the commit'; END $$")
		c.exec("CREATE CONSTRAINT TRIGGER " + commitRefusalName + " AFTER UPDATE ON deals " +
			"DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION " + commitRefusalName + "()")
		return
	}
	c.exec("CREATE TABLE " + commitRefusalName + "_anchor (id TEXT PRIMARY KEY)")
	c.exec("CREATE TABLE " + commitRefusalName + "_dangling (ref TEXT REFERENCES " + commitRefusalName +
		"_anchor (id) DEFERRABLE INITIALLY DEFERRED)")
	c.exec("CREATE TRIGGER " + commitRefusalName + " AFTER UPDATE ON deals BEGIN " +
		"INSERT INTO " + commitRefusalName + "_dangling (ref) VALUES ('nothing'); END")
}

// allowCommitsOfDealUpdates removes what refuseCommitsOfDealUpdates added.
func (c *crmCase) allowCommitsOfDealUpdates() {
	c.t.Helper()
	if c.dialect == "postgres" {
		c.exec("DROP TRIGGER " + commitRefusalName + " ON deals")
		c.exec("DROP FUNCTION " + commitRefusalName + "()")
		return
	}
	c.exec("DROP TRIGGER " + commitRefusalName)
	c.exec("DROP TABLE " + commitRefusalName + "_dangling")
	c.exec("DROP TABLE " + commitRefusalName + "_anchor")
}

// TestAStageMoveWhoseCommitFailsLeavesNoActivity is the case for the other
// direction of the stage move's transaction: the activity is written and then
// the transaction fails, at its commit. The move fails, and neither its update
// nor its activity is there afterwards, and nothing is published — which holds
// only when the activity was written inside the move's transaction. With the
// refusal lifted the same move, at the same version, goes through once.
func TestAStageMoveWhoseCommitFailsLeavesNoActivity(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		deal := w.createDeal(t, acme, alice, map[string]any{
			"account_id": "acc-1", "title": "refused at commit", "amount": 700, "stage": "qualify",
		})

		c.refuseCommitsOfDealUpdates()
		expect(t, "a move whose commit fails", w.move(t, acme, alice, deal.ID, "won", 1), nethttp.StatusInternalServerError)
		if row, _ := c.dealRow(acme, deal.ID); row.stage != "qualify" || row.version != 1 {
			t.Errorf("the deals row reads %+v after the move whose commit failed, want qualify at version 1", row)
		}
		if n := c.count("SELECT count(*) FROM activities WHERE tenant_id = ? AND subject_id = ?", acme.ID, deal.ID); n != 0 {
			t.Errorf("the activities table holds %d rows of the deal after the move whose commit failed, want none", n)
		}
		if got := w.timelineOf(t, acme, alice, deal.ID); len(got) != 0 {
			t.Errorf("the timeline holds %s after the move whose commit failed, want nothing", activitiesText(got))
		}
		c.allowCommitsOfDealUpdates()

		moved := w.move(t, acme, alice, deal.ID, "won", 1)
		expect(t, "the same move once the refusal is lifted", moved, nethttp.StatusOK)
		var got moveView
		moved.decode(t, &got)
		if tl := w.timelineOf(t, acme, alice, deal.ID); len(tl) != 1 || tl[0].ID != got.Activity.ID {
			t.Errorf("the timeline holds %s after the move, want its one stage change %s", activitiesText(tl), got.Activity.ID)
		}
		ofThisDeal := func(r eventRow) bool { return r.detail["deal_id"] == deal.ID }
		c.awaitEventRow("the move that went through", "deal.stage-changed", ofThisDeal)
		c.awaitEventRow("the move that went through", "deal.won", ofThisDeal)
		for _, eventType := range []string{"deal.stage-changed", "deal.won"} {
			if rows := slices.DeleteFunc(c.eventRows(eventType), func(r eventRow) bool { return !ofThisDeal(r) }); len(rows) != 1 {
				t.Errorf("the journal holds %d %s rows for the deal, want the one the move that went through published: %+v",
					len(rows), eventType, rows)
			}
		}
	})
}

// stagedRefusalName is what refuseStagedCreations adds to the outbox's table.
const stagedRefusalName = "crm_case_refuses_deal_created"

// refuseStagedCreations makes every insert of a deal.created into the outbox's
// table fail, with a check constraint on PostgreSQL and with a trigger on
// SQLite, which adds no constraint to an existing table.
func (c *crmCase) refuseStagedCreations() {
	c.t.Helper()
	if c.dialect == "postgres" {
		c.exec("ALTER TABLE event_staged ADD CONSTRAINT " + stagedRefusalName +
			" CHECK (type <> '" + dealCreatedEvent + "') NOT VALID")
		return
	}
	c.exec("CREATE TRIGGER " + stagedRefusalName + " BEFORE INSERT ON event_staged WHEN NEW.type = '" +
		dealCreatedEvent + "' BEGIN SELECT RAISE(ABORT, 'the case refuses a deal.created row'); END")
}

// allowStagedCreations removes what refuseStagedCreations added.
func (c *crmCase) allowStagedCreations() {
	c.t.Helper()
	if c.dialect == "postgres" {
		c.exec("ALTER TABLE event_staged DROP CONSTRAINT " + stagedRefusalName)
		return
	}
	c.exec("DROP TRIGGER " + stagedRefusalName)
}

// TestADealWhoseAnnouncementCannotBeStagedIsNotWritten is the case for the
// creation's transaction: the deal's row and its announcement are one unit of
// work. The outbox's table is made to refuse the deal.created the creation
// stages, which comes after the deal's own insert: the creation fails, and the
// deals row is not there afterwards, with the journal holding no statement row
// of it — which holds only when the announcement was staged inside the
// transaction that wrote the deal. A host that staged after the commit would
// answer the creation and leave the deals row behind, the announcement lost to
// the write it announces. With the refusal lifted the same creation, under the
// same identifier, goes through once and its announcement reaches its
// consumer.
func TestADealWhoseAnnouncementCannotBeStagedIsNotWritten(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		body := map[string]any{
			"id": "unstaged", "account_id": "acc-1", "title": "unstaged announcement", "amount": 700,
			"currency": "EUR", "stage": "qualify",
		}

		c.refuseStagedCreations()
		expect(t, "a creation whose announcement the outbox's table refuses",
			w.h.send(t, api(nethttp.MethodPost, "/deals", acme, alice, body)), nethttp.StatusInternalServerError)
		if row, ok := c.dealRow(acme, "unstaged"); ok {
			t.Errorf("the deals row %+v is there after the creation whose announcement could not be staged, want none", row)
		}
		if n := c.statementCount("create", dealTable, acme); n != 0 {
			t.Errorf("the journal holds %d statement rows of a create in deals after the failed creation, want none: "+
				"the row rides the transaction the creation rolled back", n)
		}
		c.allowStagedCreations()

		created := w.createDeal(t, acme, alice, body)
		if row, ok := c.dealRow(acme, created.ID); !ok || row.stage != "qualify" {
			t.Errorf("the deals row reads %+v (present %v) after the same creation once the refusal is lifted, "+
				"want the deal in qualify", row, ok)
		}
		if n := c.statementCount("create", dealTable, acme); n != 1 {
			t.Errorf("the journal holds %d statement rows of a create in deals after the same creation went "+
				"through, want 1", n)
		}
		c.awaitEventRow("the creation that went through", dealCreatedEvent,
			func(r eventRow) bool { return r.detail["deal_id"] == "unstaged" })
	})
}

// outboxRefusalName is the constraint the announcement case adds to the
// PostgreSQL bus's outbox.
const outboxRefusalName = "crm_case_refuses_wins"

// TestAWinAndItsAnnouncementStandOrFallTogether is the case for a move into a
// won stage whose deal.won the bus refuses. The revenue projection and the
// webhooks hear of a win from that event alone, so a move that stands and an
// announcement that is lost is a won deal no revenue line counts and no
// webhook posts, with nothing that could repair it. The move and its
// announcement are to stand or fall together: either the move is refused and
// the deal is not won, or the win is counted once the bus takes events again.
//
// It runs on the stack whose bus is a table in the case's database, which is
// how a case makes the bus refuse one event type: a constraint on the bus's
// own table refuses deal.won while the move is made, and is dropped once the
// outbox's relay has recorded that the bus refused the staged deal.won. The
// move stages its events in its transaction, so the move stands and the event
// stays staged; the relay publishes it again after the drop, and the win is
// counted. A host that published after the commit instead would have its
// publish refused, the event lost and no refusal of the relay's to wait for.
func TestAWinAndItsAnnouncementStandOrFallTogether(t *testing.T) {
	c := newCase(t, stackNamed(t, "postgres"))
	w := startDeals(t, c, nil)
	acme, alice := w.acme, w.asAlice
	definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
	deal := w.createDeal(t, acme, alice, map[string]any{
		"account_id": "acc-1", "title": "won while the bus refuses", "amount": 700, "currency": "EUR", "stage": "qualify",
	})

	c.exec("ALTER TABLE event_outbox ADD CONSTRAINT " + outboxRefusalName + " CHECK (type <> 'deal.won')")
	moved := w.move(t, acme, alice, deal.ID, "won", 1)
	// The relay publishes after the commit, so the refusal is met after the
	// answer; the constraint stays until it has been, or the relay would
	// publish the event after the drop and the refusal would never be met.
	refused := eventually(settleLimit, func() bool {
		_, ok := findRecord(w.h.output(), relayRefusedMsg, "type", "deal.won")
		return ok
	})
	c.exec("ALTER TABLE event_outbox DROP CONSTRAINT " + outboxRefusalName)

	row, _ := c.dealRow(acme, deal.ID)
	if row.stage != "won" {
		// The move fell with its announcement: nothing won, nothing to count.
		if moved.status == nethttp.StatusOK {
			t.Errorf("the move answered 200 and left the deal in %q", row.stage)
		}
		if got, _ := revenueOf(t, w.h, acme, alice); len(got.Revenue) != 0 {
			t.Errorf("acme's won revenue reads %+v after a move that did not stand, want nothing", got.Revenue)
		}
		return
	}
	// The move stands, so the refusal has to have been met for the case to
	// say anything, and the win has to be counted.
	if !refused {
		t.Fatalf("the deal was won and the outbox's relay recorded no refused deal.won within %s, "+
			"so the refusal was never met through the outbox", settleLimit)
	}
	awaitRevenue(t, w.h, acme, alice, revenueView{Currency: "EUR", Deals: 1, Amount: 700})
}

// relayRefusedMsg is the record the outbox's relay writes when the bus refuses
// a staged event it then keeps staged.
const relayRefusedMsg = "outbox: the bus refused a staged event; it stays staged and is published again"

// pastFloat is an amount no float64 holds: 2^53 + 1, which a float rounds to
// 2^53.
const pastFloat int64 = 1<<53 + 1

// awaitRecord waits for a record a replica writes, failing the case when none
// arrives. A subscriber writes its records after the response on every
// transport bus.
func awaitRecord(t *testing.T, h *crmHost, what, msg string, attrs ...string) {
	t.Helper()
	if !eventually(settleLimit, func() bool {
		_, ok := findRecord(h.output(), msg, attrs...)
		return ok
	}) {
		t.Errorf("no %q record %s carries %v", msg, what, attrs)
	}
}

// TestAMovesEventsReachTheirConsumersAsTheMoveRecordedThem is the case for the
// events a stage move publishes, on every bus.
//
// A move to an open stage publishes deal.stage-changed, and a move to a won one
// publishes deal.won as well. The identifier each event's subscriber is handed
// — the flow module's for deal.stage-changed, the revenue module's for
// deal.won — is the one the move wrote on its activity, so a publish that let
// the bus mint an identifier of its own is told apart. The journal records both
// events from its event path, and the amount, above what a float64 holds,
// arrives exact in its rows and in the pipeline report.
func TestAMovesEventsReachTheirConsumersAsTheMoveRecordedThem(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		deal := w.createDeal(t, acme, alice, map[string]any{
			"account_id": "acc-9", "title": "past a float", "amount": pastFloat, "currency": "JPY", "stage": "qualify",
		})
		exact := strconv.FormatInt(pastFloat, 10)
		if deal.Amount != pastFloat {
			t.Errorf("the creation answered the amount %d, want %d", deal.Amount, pastFloat)
		}
		if read := w.h.get(t, apiEndpoint, "/deals/"+deal.ID, acme.Alias, alice); !bytes.Contains(read.body, []byte(`"amount":`+exact)) {
			t.Errorf("the deal reads %s, want the amount written %s", read.body, exact)
		}

		var moves []moveView
		for i, stage := range []string{"proposal", "won"} {
			resp := w.move(t, acme, alice, deal.ID, stage, int64(i+1))
			expect(t, "moving the deal to "+stage, resp, nethttp.StatusOK)
			var got moveView
			resp.decode(t, &got)
			if got.Activity.EventID == nil || *got.Activity.EventID == "" {
				t.Fatalf("the move to %s recorded no event identifier on %+v", stage, got.Activity)
			}
			moves = append(moves, got)
		}
		if moves[0].Activity.WonEventID != nil || moves[1].Activity.WonEventID == nil {
			t.Fatalf("the moves recorded the won event identifiers %s and %s, want none and one",
				orNone(moves[0].Activity.WonEventID), orNone(moves[1].Activity.WonEventID))
		}

		from := "qualify"
		for i, m := range moves {
			awaitRecord(t, w.h, "for the deal.stage-changed event the move to "+m.Deal.Stage+" recorded", moveCountedMsg,
				"module", flowModuleName, "event_id", *m.Activity.EventID, "tenant_id", acme.ID,
				"deal_id", deal.ID, "activity_id", m.Activity.ID)
			changed := c.awaitEventRow("the move to "+m.Deal.Stage, "deal.stage-changed", func(r eventRow) bool {
				return r.detail["deal_id"] == deal.ID && r.detail["to_stage"] == m.Deal.Stage
			})
			want := map[string]string{
				"from_stage": from, "amount": exact, "currency": "JPY", "activity_id": m.Activity.ID,
				"version": strconv.Itoa(i + 2), "actor_id": w.alice.ID,
			}
			for key, value := range want {
				if changed.detail[key] != value {
					t.Errorf("the journal's deal.stage-changed row carries %s=%q, want %q; the row is %+v",
						key, changed.detail[key], value, changed)
				}
			}
			if changed.tenant != acme.ID || changed.object != "deal/"+deal.ID {
				t.Errorf("the journal's deal.stage-changed row is %+v, want acme's and about the deal", changed)
			}
			from = m.Deal.Stage
		}
		awaitRecord(t, w.h, "for the deal.won event the won move recorded", wonCountedMsg,
			"module", revenueModuleName, "event_id", *moves[1].Activity.WonEventID, "tenant_id", acme.ID, "deal_id", deal.ID)
		won := c.awaitEventRow("the won move", "deal.won", func(r eventRow) bool { return r.detail["deal_id"] == deal.ID })
		if won.detail["amount"] != exact || won.detail["activity_id"] != moves[1].Activity.ID ||
			won.outcome != "won" || won.object != "deal/"+deal.ID || won.tenant != acme.ID {
			t.Errorf("the journal's deal.won row is %+v, want acme's won deal %s with the amount %s", won, deal.ID, exact)
		}
		if rows := c.eventRows("deal.won"); len(rows) != 1 {
			t.Errorf("the journal holds %d deal.won rows, want the one the move to won published: %+v", len(rows), rows)
		}
		if got := w.timelineOf(t, acme, alice, deal.ID); len(got) != 2 || got[1].WonEventID == nil ||
			*got[1].WonEventID != *moves[1].Activity.WonEventID {
			t.Errorf("the timeline holds %+v, want the two moves with the won event on the second", got)
		}
		awaitFlow(t, w.h, acme, alice,
			flowView{FromStage: "proposal", ToStage: "won", Moves: 1}, flowView{FromStage: "qualify", ToStage: "proposal", Moves: 1})

		report := w.h.get(t, apiEndpoint, "/reports/pipeline", acme.Alias, alice)
		expect(t, "reading the pipeline report", report, nethttp.StatusOK)
		if !bytes.Contains(report.body, []byte(`"amount":`+exact)) {
			t.Errorf("the pipeline report reads %s, want the amount %s", report.body, exact)
		}

		for _, route := range []struct{ pattern, outcome string }{
			{createDealRoute, "201"},
			{moveDealRoute, "200"},
		} {
			c.awaitJournal(route.pattern, func(r auditRow) bool {
				return r.source == "request" && r.object == route.pattern && r.outcome == route.outcome &&
					r.tenant == acme.ID && r.actor == sessionActor(w.alice)
			})
		}
	})
}

// TestADealTakesItsCurrencyFromItsTenantsSetting is the case for the currency a
// deal created without one gets: the deals.default-currency setting, read for
// the request's tenant, so a tenant's own row wins over the global one, and
// held to the shape of a currency code when it is read.
func TestADealTakesItsCurrencyFromItsTenantsSetting(t *testing.T) {
	c := newCase(t, stackNamed(t, localStack))
	w := startDeals(t, c, func(w *dealsWorld) {
		c.setGlobal("deals.default-currency", "GBP")
		c.setForTenant(w.acme, "deals.default-currency", "EUR")
	})
	for _, side := range []struct {
		ref  tenantRef
		cred credential
		name string
		want string
	}{
		{w.acme, w.asAlice, "acme", "EUR"},
		{w.globex, w.asBob, "globex", "GBP"},
	} {
		definePipeline(t, w.h, side.ref, side.cred, pipelineOf(side.name))
		got := w.createDeal(t, side.ref, side.cred, map[string]any{
			"account_id": "acc-1", "title": "no currency", "amount": 100, "stage": "qualify",
		})
		if got.Currency != side.want {
			t.Errorf("a deal created in %s without a currency is in %q, want %q", side.name, got.Currency, side.want)
		}
		named := w.createDeal(t, side.ref, side.cred, map[string]any{
			"account_id": "acc-1", "title": "in yen", "amount": 100, "currency": "JPY", "stage": "qualify",
		})
		if named.Currency != "JPY" {
			t.Errorf("a deal created in %s in JPY is in %q", side.name, named.Currency)
		}
	}

	t.Run("a default currency that is no ISO 4217 code writes no deal", func(t *testing.T) {
		// The settings module holds any text under a text setting, so the
		// owner's write goes through; what the code is judged by is the deals
		// module, when it reads it. A deal written in it would be announced
		// and then dropped by every consumer of its deal.won.
		expect(t, "acme's owner writing euro as the default currency",
			writeTenantSettingAs(t, w.h, w.acme.Alias, w.asAlice, "deals.default-currency", "euro"), nethttp.StatusOK)
		before := c.count("SELECT count(*) FROM deals WHERE tenant_id = ?", w.acme.ID)
		resp := w.h.send(t, api(nethttp.MethodPost, "/deals", w.acme, w.asAlice, map[string]any{
			"account_id": "acc-1", "title": "in the misconfigured default", "amount": 700, "stage": "qualify",
		}))
		expect(t, "a creation without a currency under the default euro", resp, nethttp.StatusInternalServerError)
		if n := c.count("SELECT count(*) FROM deals WHERE tenant_id = ?", w.acme.ID); n != before {
			t.Errorf("acme holds %d deals after a creation under the default euro, want the %d it held", n, before)
		}
		if _, ok := findRecord(w.h.output(), "the default currency is not a three-letter ISO 4217 code, so no deal is written",
			"setting", "deals.default-currency", "value", "euro"); !ok {
			t.Errorf("the refusal of a creation under the default euro wrote no record naming the setting")
		}
		named := w.createDeal(t, w.acme, w.asAlice, map[string]any{
			"account_id": "acc-1", "title": "in euros, named", "amount": 700, "currency": "EUR", "stage": "qualify",
		})
		if named.Currency != "EUR" {
			t.Errorf("a deal naming EUR under the default euro is in %q", named.Currency)
		}
	})
}

// revenueOf reads a tenant's won revenue through one replica, with the body as
// it was written.
func revenueOf(t *testing.T, h *crmHost, ref tenantRef, cred credential) (revenueList, []byte) {
	t.Helper()
	resp := h.get(t, apiEndpoint, "/revenue", ref.Alias, cred)
	expect(t, "reading the won revenue of "+ref.ID, resp, nethttp.StatusOK)
	var got revenueList
	resp.decode(t, &got)
	return got, resp.body
}

// awaitRevenue waits until a tenant's won revenue reads as wanted. The
// subscriber that keeps it runs after the response on every transport bus.
func awaitRevenue(t *testing.T, h *crmHost, ref tenantRef, cred credential, want ...revenueView) []byte {
	t.Helper()
	var got revenueList
	var body []byte
	if !eventually(settleLimit, func() bool {
		got, body = revenueOf(t, h, ref, cred)
		return slices.Equal(got.Revenue, want)
	}) {
		t.Fatalf("%s's won revenue reads %+v, want %+v", ref.ID, got.Revenue, want)
	}
	return body
}

// awaitFlow waits until a tenant's stage flow, read through one replica, reads
// as wanted. The subscriber that keeps it runs after the response on every
// transport bus.
func awaitFlow(t *testing.T, h *crmHost, ref tenantRef, cred credential, want ...flowView) {
	t.Helper()
	var got flowList
	if !eventually(settleLimit, func() bool {
		resp := h.get(t, apiEndpoint, "/pipeline/flow", ref.Alias, cred)
		expect(t, "reading the stage flow of "+ref.ID, resp, nethttp.StatusOK)
		got = flowList{}
		resp.decode(t, &got)
		return slices.Equal(got.Flow, want)
	}) {
		t.Fatalf("%s's stage flow reads %+v, want %+v", ref.ID, got.Flow, want)
	}
}

// winDeal creates a deal in a tenant's qualify stage and moves it to won,
// returning the move's activity, which records the identifiers of the two
// events the move published.
func winDeal(t *testing.T, h *crmHost, ref tenantRef, cred credential, id, currency string, amount int64) activityView {
	t.Helper()
	createDealOn(t, h, ref, cred, map[string]any{
		"id": id, "account_id": "acc-1", "title": id + " to win", "amount": amount, "currency": currency, "stage": "qualify",
	})
	moved := h.sendWith(t, api(nethttp.MethodPost, "/deals/"+id+"/stage", ref, cred, map[string]any{"stage": "won"}),
		headers{"If-Match": `"1"`})
	expect(t, "moving "+id+" to won in "+ref.ID, moved, nethttp.StatusOK)
	var got moveView
	moved.decode(t, &got)
	if got.Activity.EventID == nil || got.Activity.WonEventID == nil || *got.Activity.WonEventID == "" {
		t.Fatalf("the move of %s to won in %s recorded the event identifiers %s and %s, want two",
			id, ref.ID, orNone(got.Activity.EventID), orNone(got.Activity.WonEventID))
	}
	return got.Activity
}

// TestEachSubscriberCountsInTheEventsOwnTenant is the case for the two
// subscribers of the events a move publishes, on every bus: the revenue
// module's, which adds a won deal to its tenant's line for its currency, and
// the flow module's, which adds a move to its tenant's line for the two stages.
// Each enters the tenant the event names, so its records carry the tenant
// Enter bound, and the amount, above what a float64 holds, is added exactly.
//
// acme and globex win deals under the same identifiers in the same currency,
// and globex moves one deal more, so a count that landed in the wrong tenant,
// or an upsert that met the other tenant's line, reads as a line in the wrong
// place.
func TestEachSubscriberCountsInTheEventsOwnTenant(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		acme, globex := w.acme, w.globex
		alice, bob := w.asAlice, w.asBob
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		definePipeline(t, w.h, globex, bob, pipelineOf("globex"))

		acmeWon := winDeal(t, w.h, acme, alice, "d-1", "JPY", pastFloat)
		globexWon := winDeal(t, w.h, globex, bob, "d-1", "JPY", 250)
		body := awaitRevenue(t, w.h, acme, alice, revenueView{Currency: "JPY", Deals: 1, Amount: pastFloat})
		if exact := `"amount":` + strconv.FormatInt(pastFloat, 10); !bytes.Contains(body, []byte(exact)) {
			t.Errorf("acme's won revenue reads %s, want %s", body, exact)
		}
		awaitRevenue(t, w.h, globex, bob, revenueView{Currency: "JPY", Deals: 1, Amount: 250})
		for _, won := range []struct {
			ref      tenantRef
			activity activityView
		}{{acme, acmeWon}, {globex, globexWon}} {
			awaitRecord(t, w.h, "for the deal.won event of "+won.ref.ID+", in its tenant", wonCountedMsg,
				"module", revenueModuleName, "event_id", *won.activity.WonEventID, "tenant_id", won.ref.ID, "deal_id", "d-1")
			awaitRecord(t, w.h, "for the deal.stage-changed event of "+won.ref.ID+", in its tenant", moveCountedMsg,
				"module", flowModuleName, "event_id", *won.activity.EventID, "tenant_id", won.ref.ID, "deal_id", "d-1")
		}

		// A second win in the currency adds to the tenant's line, and one in
		// another currency starts a line of its own; globex's third deal
		// moves to proposal, which acme's flow has no line for.
		winDeal(t, w.h, acme, alice, "d-2", "JPY", 5)
		winDeal(t, w.h, globex, bob, "d-2", "EUR", 70)
		w.createDeal(t, globex, bob, map[string]any{
			"id": "d-3", "account_id": "acc-1", "title": "globex's third", "amount": 1, "stage": "qualify",
		})
		expect(t, "globex moving d-3 to proposal", w.move(t, globex, bob, "d-3", "proposal", 1), nethttp.StatusOK)
		awaitRevenue(t, w.h, acme, alice, revenueView{Currency: "JPY", Deals: 2, Amount: pastFloat + 5})
		awaitRevenue(t, w.h, globex, bob,
			revenueView{Currency: "EUR", Deals: 1, Amount: 70}, revenueView{Currency: "JPY", Deals: 1, Amount: 250})
		awaitFlow(t, w.h, acme, alice, flowView{FromStage: "qualify", ToStage: "won", Moves: 2})
		awaitFlow(t, w.h, globex, bob,
			flowView{FromStage: "qualify", ToStage: "proposal", Moves: 1}, flowView{FromStage: "qualify", ToStage: "won", Moves: 2})
	})
}

// TestAWonDealHeardByEveryReplicaIsCountedOnce is the case for de-duplicating
// by the event's identifier. With two replicas on a transport bus each is handed
// every event; the identifier is the same on both, so for each of the two
// events a won move publishes one replica claims it in the key-value store and
// counts it, and the other finds it claimed.
//
// The journal's event path hears the same event on both replicas, and a row
// per replica is a second record of one event.
func TestAWonDealHeardByEveryReplicaIsCountedOnce(t *testing.T) {
	forEachSharedStack(t, func(t *testing.T, c *crmCase) {
		w := startDealsOn(t, c, 2, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		activity := winDeal(t, w.h, acme, alice, "d-1", "JPY", pastFloat)

		for _, subscriber := range []struct {
			name, counted, skipped, eventID string
		}{
			{revenueModuleName, wonCountedMsg, wonSkippedMsg, *activity.WonEventID},
			{flowModuleName, moveCountedMsg, moveSkippedMsg, *activity.EventID},
		} {
			// Each replica writes one record for the event once it has heard it.
			heard := func(h *crmHost) (counted, skipped bool) {
				_, counted = findRecord(h.output(), subscriber.counted, "module", subscriber.name, "event_id", subscriber.eventID)
				_, skipped = findRecord(h.output(), subscriber.skipped, "module", subscriber.name, "event_id", subscriber.eventID)
				return counted, skipped
			}
			if !eventually(settleLimit, func() bool {
				for _, h := range w.hosts {
					if counted, skipped := heard(h); !counted && !skipped {
						return false
					}
				}
				return true
			}) {
				t.Fatalf("not every replica's %s subscriber wrote a record for the event %s", subscriber.name, subscriber.eventID)
			}
			var counts, skips int
			for _, h := range w.hosts {
				counted, skipped := heard(h)
				if counted {
					counts++
				}
				if skipped {
					skips++
				}
			}
			if counts != 1 || skips != 1 {
				t.Errorf("of two replicas' %s subscribers %d counted the event %s and %d found it claimed, want one of each",
					subscriber.name, counts, subscriber.eventID, skips)
			}
		}
		awaitRevenue(t, w.hosts[1], acme, alice, revenueView{Currency: "JPY", Deals: 1, Amount: pastFloat})
		awaitFlow(t, w.hosts[1], acme, alice, flowView{FromStage: "qualify", ToStage: "won", Moves: 1})

		t.Run("the journal records the event once", func(t *testing.T) {
			sub := c.in(t)
			ofDeal := func() []eventRow {
				return slices.DeleteFunc(sub.eventRows("deal.won"), func(r eventRow) bool { return r.detail["deal_id"] != "d-1" })
			}
			sub.awaitEventRow("the won move", "deal.won", func(r eventRow) bool { return r.detail["deal_id"] == "d-1" })
			// A second row arrives from the other replica if it arrives at
			// all, so the case waits for one before it concludes there is none.
			eventually(settleLimit, func() bool { return len(ofDeal()) > 1 })
			if rows := ofDeal(); len(rows) != 1 {
				t.Errorf("the journal holds %d deal.won rows for the one event %s two replicas heard, want 1: %+v",
					len(rows), *activity.WonEventID, rows)
			}
		})
	})
}

// TestADealListingJoinsEachScopedRelationInItsTenant is the case for a listing
// that joins two scoped relations in one statement: each deal's stage and its
// account, besides its owner, whose table is global. Both tenants hold a stage
// and an account under the same keys, each named after its tenant, and a deal
// under the same identifier at them. A join the filter left without its tenant
// condition, the second as much as the first, would pair each deal with the
// other tenant's row as well: the page would hold the one deal the count found
// twice, or name the other tenant's account.
func TestADealListingJoinsEachScopedRelationInItsTenant(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		sides := sidesOf(w)
		for _, side := range sides {
			definePipeline(t, w.h, side.ref, side.cred, pipelineOf(side.name))
			expect(t, "opening the twin account in "+side.ref.ID,
				w.h.send(t, api(nethttp.MethodPost, "/accounts", side.ref, side.cred,
					map[string]any{"id": "acc-twin", "name": "Twin at " + side.name})), nethttp.StatusCreated)
			createDealOn(t, w.h, side.ref, side.cred, map[string]any{
				"id": "twin", "account_id": "acc-twin", "title": "twin of " + side.name, "amount": 10,
				"currency": "EUR", "stage": "qualify",
			})
			createDealIn(t, w.h, side.ref, side.cred, "other", "proposal", 20, "EUR", side.owner)
		}
		for _, side := range sides {
			resp := w.h.get(t, apiEndpoint, "/deals", side.ref.Alias, side.cred)
			expect(t, "listing "+side.ref.ID+"'s deals", resp, nethttp.StatusOK)
			var list dealList
			resp.decode(t, &list)
			ids := make([]string, 0, len(list.Deals))
			for _, d := range list.Deals {
				ids = append(ids, d.ID)
			}
			slices.Sort(ids)
			if list.Total != 2 || !slices.Equal(ids, []string{"other", "twin"}) {
				t.Errorf("%s's listing counts %d deals and holds %v, want its two deals once each", side.ref.ID, list.Total, ids)
			}
			for _, d := range list.Deals {
				wantAccount := "Account acc-1"
				if d.ID == "twin" {
					wantAccount = "Twin at " + side.name
				}
				if d.AccountName != wantAccount || !strings.HasSuffix(d.StageName, " at "+side.name) ||
					d.OwnerName != ownerName(side.owner) {
					t.Errorf("%s's listing shows %s at the account %q in the stage %q owned by %q, want its own tenant's rows",
						side.ref.ID, d.ID, d.AccountName, d.StageName, d.OwnerName)
				}
			}
			if got := w.getDeal(t, side.ref, side.cred, "twin"); got.AccountName != "Twin at "+side.name {
				t.Errorf("%s's twin reads the account name %q, want its own account's", side.ref.ID, got.AccountName)
			}
		}
	})
}
