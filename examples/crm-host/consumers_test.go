package main

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	eventnats "github.com/vislake/speed/pkg/event/nats"
	eventredis "github.com/vislake/speed/pkg/event/redis"
)

// The cases for consumers that share the events of a transport bus rather than
// each hearing all of them.

// sharedConsumer is the name the competing workers' consumer takes, on the
// buses that let a deployment name one: NATS by its durable consumer, Redis by
// its consumer group. The name is the case's own on both, since each case has
// a namespace of its own on NATS and a database of its own on Redis.
const sharedConsumer = "crm-workers"

// sharedConsumerEnv names the workers' shared consumer on a stack's bus, and
// reports false for a bus that has no such name.
func sharedConsumerEnv(s stack) (string, bool) {
	switch s.event {
	case eventnats.ModuleName:
		return "CRM_EVENT__NATS__CONSUMER_NAME=" + sharedConsumer, true
	case eventredis.ModuleName:
		return "CRM_EVENT__REDIS__CONSUMER_GROUP=" + sharedConsumer, true
	}
	return "", false
}

// natsAckWait is how long the NATS server waits for a delivered entry's
// acknowledgement before it hands the entry to a consumer of the same name
// again: the server's default, which the NATS bus does not change.
const natsAckWait = 30 * time.Second

// TestWorkersSharingAConsumerHandleEachWinOnce is the case for competing
// consumers on the buses that let a deployment name the consumer — NATS's
// durable consumer and Redis's consumer group — and for a worker killed while
// it holds an event, on the bus that hands such an event to a consumer of the
// same name again.
//
// The serving replica hears every event under a consumer of its own, and two
// workers share one: each deal.won reaches the serving replica and exactly one
// of the two workers, which is what sharing the name means on both buses. On
// the broadcast every process gets by default, both workers would handle each
// event. Six deals are won, each is counted once in the revenue line and
// posted once to the tenant's webhook, and each is handled by one worker.
//
// On NATS a worker then holds a deal.won unacknowledged: a tenant registered
// after the workers read the roster has its win made while the roster's table
// is locked against reads, so the worker the event reaches waits for the table
// to enter the tenant, with nothing of the event's work done. The serving
// replica counts the win from its own snapshot. Both workers are killed, as a
// crash would end them, the lock is released and a worker is started again
// under the same name: the server hands it the event the killed worker never
// acknowledged, once its acknowledgement wait has passed, and the restarted
// worker finds the win counted and counts nothing. Redis and PostgreSQL do not
// read an entry a killed process took again, as design-event has it, so that
// half runs on NATS alone.
func TestWorkersSharingAConsumerHandleEachWinOnce(t *testing.T) {
	ran := false
	for _, s := range caseStacks() {
		env, ok := sharedConsumerEnv(s)
		if !ok {
			continue
		}
		ran = true
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			c := newCase(t, s)
			c.migrate()
			acme := c.createTenant("acme")
			alice := c.createUser("alice")
			c.grant(acme, alice, roleOwner)
			c.setGlobal("ratelimit.limit", liftedRateLimit)
			receiver := startWebhookReceiver(t)
			c.configureWebhook(receiver, acme, "the secret acme signs with")
			h := c.serve()
			workers := []*process{c.worker(env), c.worker(env)}
			token := bearer(h.login(t, alice))
			openAccount(t, h, acme, token, "acc-1")
			definePipeline(t, h, acme, token, pipelineOf("acme"))

			const wins = 6
			published := map[eventKey]int{}
			for i := range wins {
				won := winDeal(t, h, acme, token, fmt.Sprintf("shared-%d", i), "EUR", 100)
				published[eventKey{acme.ID, *won.WonEventID}] = 1
			}
			awaitRevenue(t, h, acme, token, revenueView{Currency: "EUR", Deals: wins, Amount: 100 * wins})
			awaitConsumers(t, []*process{h.process}, revenueModuleName, revenueOutcomes, published)
			byWorkers := func(key eventKey) int {
				total := 0
				for _, wk := range workers {
					for _, n := range outcomesIn(wk.output(), revenueModuleName, revenueOutcomes)[key] {
						total += n
					}
				}
				return total
			}
			if !eventually(eventLimit, func() bool {
				for key := range published {
					if byWorkers(key) < 1 {
						return false
					}
				}
				return true
			}) {
				t.Fatalf("not every deal.won reached a worker within %s", eventLimit)
			}
			time.Sleep(duplicateWindow)
			for key := range published {
				if n := byWorkers(key); n != 1 {
					t.Errorf("the deal.won %s was handled %d times by the two workers sharing a consumer, want once",
						key.id, n)
				}
			}
			calls := byEventID(receiver.awaitCalls(t, webhookPath(acme), wins))
			for key := range published {
				if n := len(calls[key.id]); n != 1 {
					t.Errorf("the deal.won %s was posted %d times, want once", key.id, n)
				}
			}
			awaitRevenue(t, h, acme, token, revenueView{Currency: "EUR", Deals: wins, Amount: 100 * wins})

			if s.event != eventnats.ModuleName {
				return
			}
			t.Run("a killed worker's unacknowledged win reaches the worker started under its name", func(t *testing.T) {
				late := c.tenantNamed("late")
				h.adminCreateTenant(t, late)
				h.adminGrant(t, late, alice, roleOwner)
				openAccount(t, h, late, token, "acc-1")
				definePipeline(t, h, late, token, pipelineOf("late"))

				release := c.in(t).lockReads("tenants")
				won := winDeal(t, h, late, token, "held", "EUR", 700)
				key := eventKey{late.ID, *won.WonEventID}
				awaitRevenue(t, h, late, token, revenueView{Currency: "EUR", Deals: 1, Amount: 700})
				if !eventually(eventLimit, func() bool { return c.in(t).readersWaitingOn("tenants") > 0 }) {
					t.Fatalf("no worker waited on the roster's table for the deal.won %s within %s", key.id, eventLimit)
				}
				for _, wk := range workers {
					wk.kill(t)
				}
				release()

				restarted := c.in(t).worker(env)
				if !eventually(natsAckWait+recoveryLimit, func() bool {
					return outcomesIn(restarted.output(), revenueModuleName, revenueOutcomes)[key][wonSkippedMsg] == 1
				}) {
					t.Fatalf("the worker started under the shared name recorded no %q for the deal.won %s within %s; "+
						"it recorded %v", wonSkippedMsg, key.id, natsAckWait+recoveryLimit,
						outcomesIn(restarted.output(), revenueModuleName, revenueOutcomes)[key])
				}
				awaitRevenue(t, h, late, token, revenueView{Currency: "EUR", Deals: 1, Amount: 700})
			})
		})
	}
	if !ran {
		t.Fatal("no stack's bus lets a deployment name a shared consumer, so this case checks nothing")
	}
}

// readersWaitingOn counts the sessions of the case's database waiting for a
// lock on a statement that names a table.
func (c *crmCase) readersWaitingOn(table string) int {
	c.t.Helper()
	var n int
	if err := c.sql().QueryRow("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() "+
		"AND pid <> pg_backend_pid() AND wait_event_type = 'Lock' AND query LIKE $1", "%"+table+"%").Scan(&n); err != nil {
		c.t.Fatalf("reading the sessions waiting on %s: %v", table, err)
	}
	return n
}

// lockReads takes the lock on a PostgreSQL table that holds its reads back as
// well as its writes, until the function it returns is called or the case
// ends.
func (c *crmCase) lockReads(table string) func() {
	c.t.Helper()
	tx, err := c.sql().Begin()
	if err != nil {
		c.t.Fatalf("beginning the lock on %s: %v", table, err)
	}
	if _, err := tx.Exec("LOCK TABLE " + table + " IN ACCESS EXCLUSIVE MODE"); err != nil {
		_ = tx.Rollback()
		c.t.Fatalf("locking %s: %v", table, err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			c.t.Errorf("releasing the lock on %s: %v", table, err)
		}
	}
	c.t.Cleanup(release)
	return release
}
