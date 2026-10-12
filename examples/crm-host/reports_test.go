package main

import (
	"bytes"
	"database/sql"
	"fmt"
	nethttp "net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases of the reports module: the pipeline report and the leaderboard over
// the deals table, and the pipeline report's cache. The cases of the deal.won
// consumers, whose tables two of its routes read, are in webhooks_test.go. Each
// claim under "Reports, the revenue projection, webhooks and the worker" in
// main.go has its case in one of the two.

// pipelineRaw reads the pipeline report as the bytes it arrived as, so a case
// can tell a report served from the cache from one computed again.
func (h *crmHost) pipelineRaw(t *testing.T, alias string, cred credential) ([]byte, pipelineReport) {
	t.Helper()
	resp := h.get(t, apiEndpoint, "/reports/pipeline", alias, cred)
	expect(t, "GET /reports/pipeline in "+alias, resp, nethttp.StatusOK)
	var got pipelineReport
	resp.decode(t, &got)
	return resp.body, got
}

// requireStages fails the case unless a report's rows are these.
func requireStages(t *testing.T, what string, got pipelineReport, want ...stageTotal) {
	t.Helper()
	if !slices.Equal(got.Stages, want) {
		t.Errorf("%s holds %+v, want %+v", what, got.Stages, want)
	}
}

// dealsIn is how many deals a pipeline report counts.
func dealsIn(report pipelineReport) int64 {
	var n int64
	for _, s := range report.Stages {
		n += s.Deals
	}
	return n
}

// insertDeal writes a deal behind the deals module's back, through the
// harness's own connection: the one way to change what a report adds up without
// a write the deals module announces, which is what the cache cases need. The
// deal sits in one of the tenant's stages, is with acc-1, which a deals world
// opens in each tenant, and is owned by one of the tenant's members.
func (c *crmCase) insertDeal(ref tenantRef, id, stage string, amount int64, owner userRef) {
	c.t.Helper()
	now := time.Now().UTC()
	c.exec("INSERT INTO deals (tenant_id, id, account_id, title, amount, currency, stage_key, owner_id, version, "+
		"created_at, updated_at) VALUES (?, ?, 'acc-1', 'written behind the module''s back', ?, 'EUR', ?, ?, 1, ?, ?)",
		ref.ID, id, amount, stage, owner.ID, now, now)
}

// liveDeals counts one tenant's deals that are not deleted, through the
// harness's own connection.
func (c *crmCase) liveDeals(ref tenantRef) int64 {
	c.t.Helper()
	var n int64
	query := c.rebind("SELECT COUNT(*) FROM deals WHERE tenant_id = ? AND deleted_at IS NULL")
	if err := c.sql().QueryRow(query, ref.ID).Scan(&n); err != nil {
		c.t.Fatalf("counting the deals of %s: %v", ref.ID, err)
	}
	return n
}

// TestTheReportsStayInTheirTenant is the case for the reports' statements: the
// pipeline report and the leaderboard each group a tenant's deals and join them
// to other tables, and each is held to the request's tenant while the other
// tenant holds deals under the same identifiers, in stages of the same keys and
// owned by the same users.
//
// A statement missing its tenant condition is observable because of the twins:
// a scan without it sums both tenants' deals, and a join to the stages without
// it pairs a deal with both tenants' stage of its key, counting it twice. The
// cache is off, so each read computes the report from the table.
func TestTheReportsStayInTheirTenant(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, func(w *dealsWorld) {
			c.setGlobal("reports.cache-ttl", "0s")
			c.grant(w.acme, w.bob, roleRep)
			c.grant(w.globex, w.alice, roleRep)
		})
		for _, side := range sidesOf(w) {
			definePipeline(t, w.h, side.ref, side.cred, pipelineOf(side.name))
			createDealIn(t, w.h, side.ref, side.cred, "d1", "qualify", 10000, "EUR", w.alice)
			createDealIn(t, w.h, side.ref, side.cred, "d2", "won", 25000, "EUR", w.bob)
			createDealIn(t, w.h, side.ref, side.cred, "d3", "won", 5000, "EUR", w.alice)
			createDealIn(t, w.h, side.ref, side.cred, "d5", "won", 9900, "USD", w.bob)
		}
		createDealIn(t, w.h, w.globex, w.asBob, "d4", "proposal", 7000, "EUR", w.bob)

		t.Run("the pipeline report scans this tenant's sums, in the order of its stages", func(t *testing.T) {
			_, acme := w.h.pipelineRaw(t, w.acme.Alias, w.asAlice)
			if acme.Tenant != w.acme.ID {
				t.Errorf("acme's report names tenant %q", acme.Tenant)
			}
			requireStages(t, "acme's pipeline report", acme,
				stageTotal{"qualify", "EUR", 1, 10000},
				stageTotal{"won", "EUR", 2, 30000},
				stageTotal{"won", "USD", 1, 9900})
			_, globex := w.h.pipelineRaw(t, w.globex.Alias, w.asBob)
			requireStages(t, "globex's pipeline report", globex,
				stageTotal{"qualify", "EUR", 1, 10000},
				stageTotal{"proposal", "EUR", 1, 7000},
				stageTotal{"won", "EUR", 2, 30000},
				stageTotal{"won", "USD", 1, 9900})
		})

		t.Run("the leaderboard joins this tenant's won deals to the global users", func(t *testing.T) {
			for _, tc := range []struct {
				currency string
				want     []leader
			}{
				{"EUR", []leader{{w.bob.ID, "bob", 1, 25000}, {w.alice.ID, "alice", 1, 5000}}},
				{"USD", []leader{{w.bob.ID, "bob", 1, 9900}}},
			} {
				resp := w.h.get(t, apiEndpoint, "/reports/leaderboard?currency="+tc.currency, w.acme.Alias, w.asAlice)
				expect(t, "acme's "+tc.currency+" leaderboard", resp, nethttp.StatusOK)
				var got leaderboardView
				resp.decode(t, &got)
				if !slices.Equal(got.Leaders, tc.want) {
					t.Errorf("acme's %s leaderboard holds %+v, want %+v", tc.currency, got.Leaders, tc.want)
				}
			}
			expect(t, "a leaderboard in no currency",
				w.h.get(t, apiEndpoint, "/reports/leaderboard", w.acme.Alias, w.asAlice), nethttp.StatusBadRequest)
		})

		t.Run("a deleted deal leaves this tenant's reports and stays in the other's", func(t *testing.T) {
			expect(t, "acme deleting d3", w.h.send(t, api(nethttp.MethodDelete, "/deals/d3", w.acme, w.asAlice, nil)),
				nethttp.StatusNoContent)
			_, acme := w.h.pipelineRaw(t, w.acme.Alias, w.asAlice)
			requireStages(t, "acme's pipeline report after the deletion", acme,
				stageTotal{"qualify", "EUR", 1, 10000},
				stageTotal{"won", "EUR", 1, 25000},
				stageTotal{"won", "USD", 1, 9900})
			resp := w.h.get(t, apiEndpoint, "/reports/leaderboard?currency=EUR", w.acme.Alias, w.asAlice)
			expect(t, "acme's EUR leaderboard after the deletion", resp, nethttp.StatusOK)
			var board leaderboardView
			resp.decode(t, &board)
			if want := []leader{{w.bob.ID, "bob", 1, 25000}}; !slices.Equal(board.Leaders, want) {
				t.Errorf("acme's EUR leaderboard holds %+v after the deletion, want %+v", board.Leaders, want)
			}
			_, globex := w.h.pipelineRaw(t, w.globex.Alias, w.asBob)
			requireStages(t, "globex's pipeline report after acme's deletion", globex,
				stageTotal{"qualify", "EUR", 1, 10000},
				stageTotal{"proposal", "EUR", 1, 7000},
				stageTotal{"won", "EUR", 2, 30000},
				stageTotal{"won", "USD", 1, 9900})
		})
	})
}

// eventLimit bounds the wait for a consumer's effect. A transport delivers
// asynchronously, the publishing replica's own subscribers included, and the
// PostgreSQL bus can take a poll interval to notice an event.
var eventLimit = scaledForRace(20 * time.Second)

// reportReplicas serves the replicas a cache case reads through, with the
// deals world set up. On a stack whose key-value store several processes share
// there are two, and a case caches a report through the first and reads it
// through the second: a report the second serves byte for byte as the first
// cached it, while the table has moved, came out of the store and not out of
// either process. On a stack whose store lives in one process, one replica
// plays both parts.
func reportReplicas(t *testing.T, c *crmCase, before func(w *dealsWorld)) (w *dealsWorld, writer, reader *crmHost) {
	t.Helper()
	n := 2
	if c.processLocal {
		n = 1
	}
	w = startDealsOn(t, c, n, before)
	return w, w.hosts[0], w.hosts[n-1]
}

// distinct is the replicas given, each once: the one replica of a process-local
// stack plays both parts in a cache case.
func distinct(hosts ...*crmHost) []*process {
	var out []*process
	for _, h := range hosts {
		if !slices.Contains(out, h.process) {
			out = append(out, h.process)
		}
	}
	return out
}

// invalidations counts, in one run's output, the invalidations of one tenant's
// report the reports module recorded.
func invalidations(run *process, ref tenantRef) int { return len(invalidatingTypes(run, ref)) }

// invalidatingTypes is, in the order they were recorded in one run's output, the
// type of each deal event that invalidated one tenant's report.
func invalidatingTypes(run *process, ref tenantRef) []string {
	var types []string
	for _, rec := range records(run.output()) {
		if rec["module"] == reportsModuleName && rec["msg"] == pipelineInvalidatedMsg && rec["tenant_id"] == ref.ID {
			types = append(types, rec["event_type"])
		}
	}
	return types
}

// awaitInvalidations waits until every replica given has recorded want
// invalidations of a tenant's report. On a transport bus each replica receives
// every deal event and moves the generation on when its own subscriber runs,
// so until every one has, a report read may still be invalidated under the
// case's feet.
func awaitInvalidations(t *testing.T, replicas []*process, ref tenantRef, want int) {
	t.Helper()
	if !eventually(eventLimit, func() bool {
		for _, run := range replicas {
			if invalidations(run, ref) < want {
				return false
			}
		}
		return true
	}) {
		var got []string
		for _, run := range replicas {
			got = append(got, fmt.Sprintf("%s: %d", run.name, invalidations(run, ref)))
		}
		t.Fatalf("within %s not every replica recorded %d invalidations of %s's report: %v", eventLimit, want, ref.ID, got)
	}
}

// TestThePipelineReportIsServedFromTheCacheUntilInvalidated is the case for the
// report cache: a report is served from the key-value store under a key that
// carries its tenant, every deal write the deals module announces invalidates
// it on every replica, and none reaches another tenant's cached report.
//
// The two tenants hold different deals, so a key missing the tenant would
// answer one tenant with the other's report. What a cache hit looks like is a
// report that does not move when the table does: a row is written behind the
// module's back, and the report stays byte for byte what it was until an
// invalidation. On a stack whose store several processes share, the report is
// cached through one replica and read through the other, so a cache that lived
// in the process rather than in the store would answer the second replica with
// the table as it now is; each write goes through the first and its
// invalidation is read on the second.
//
// Each write is checked on its own: the report is cached just before it, and
// read again once every replica has handled the write's events. A write whose
// event did not invalidate would leave that read on the report cached before
// it.
func TestThePipelineReportIsServedFromTheCacheUntilInvalidated(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w, writer, reader := reportReplicas(t, c, nil)
		replicas := distinct(writer, reader)
		acme, globex := w.acme, w.globex
		definePipeline(t, writer, acme, w.asAlice, pipelineOf("acme"))
		definePipeline(t, writer, globex, w.asBob, pipelineOf("globex"))
		createDealIn(t, writer, acme, w.asAlice, "e1", "qualify", 10000, "EUR", w.alice)
		createDealIn(t, writer, globex, w.asBob, "e1", "qualify", 10000, "EUR", w.bob)
		createDealIn(t, writer, globex, w.asBob, "g2", "won", 5000, "EUR", w.bob)
		awaitInvalidations(t, replicas, acme, 1)
		awaitInvalidations(t, replicas, globex, 2)

		acmeRaw, acmeReport := writer.pipelineRaw(t, acme.Alias, w.asAlice)
		globexRaw, globexReport := writer.pipelineRaw(t, globex.Alias, w.asBob)
		if acmeReport.Tenant != acme.ID || globexReport.Tenant != globex.ID {
			t.Fatalf("the reports name the tenants %q and %q, want %q and %q",
				acmeReport.Tenant, globexReport.Tenant, acme.ID, globex.ID)
		}
		requireStages(t, "acme's first report", acmeReport, stageTotal{"qualify", "EUR", 1, 10000})
		requireStages(t, "globex's first report", globexReport,
			stageTotal{"qualify", "EUR", 1, 10000}, stageTotal{"won", "EUR", 1, 5000})

		c.insertDeal(acme, "d1", "won", 7000, w.alice)
		c.insertDeal(globex, "d1", "won", 7000, w.bob)
		if again, _ := reader.pipelineRaw(t, acme.Alias, w.asAlice); !bytes.Equal(again, acmeRaw) {
			t.Fatalf("%s answered acme's report as the table now is rather than as %s cached it before any "+
				"invalidation\ncached: %s\nserved: %s", reader.name, writer.name, acmeRaw, again)
		}
		if again, _ := reader.pipelineRaw(t, globex.Alias, w.asBob); !bytes.Equal(again, globexRaw) {
			t.Fatalf("%s answered globex's report as the table now is rather than as %s cached it\ncached: %s\nserved: %s",
				reader.name, writer.name, globexRaw, again)
		}

		t.Run("each deal write invalidates its tenant's report on every replica and no other tenant's", func(t *testing.T) {
			sub := c.in(t)
			path := "/deals/w1"
			ifMatch := func(version int) headers { return headers{"If-Match": strconv.Quote(strconv.Itoa(version))} }
			seen := invalidations(replicas[0], acme)
			// Each write's events, by type and by the outcome the payload
			// carries: the invalidations name the types, and the journal's
			// event rows carry the payloads.
			type announced struct{ eventType, outcome string }
			for i, write := range []struct {
				what   string
				send   func() response
				status int
				events []announced
			}{
				{"a creation", func() response {
					return writer.send(t, api(nethttp.MethodPost, "/deals", acme, w.asAlice, map[string]any{
						"id": "w1", "account_id": "acc-1", "title": "Written", "amount": 100, "currency": "EUR", "stage": "qualify",
					}))
				}, nethttp.StatusCreated, []announced{{"deal.created", "created"}}},
				{"a change", func() response {
					return writer.sendWith(t, api(nethttp.MethodPatch, path, acme, w.asAlice, map[string]any{"amount": 200}), ifMatch(1))
				}, nethttp.StatusOK, []announced{{"deal.changed", "changed"}}},
				{"a move to an open stage", func() response {
					return writer.sendWith(t, api(nethttp.MethodPost, path+"/stage", acme, w.asAlice, map[string]any{"stage": "proposal"}),
						ifMatch(2))
				}, nethttp.StatusOK, []announced{{"deal.stage-changed", "open"}}},
				{"a move to a won stage", func() response {
					return writer.sendWith(t, api(nethttp.MethodPost, path+"/stage", acme, w.asAlice, map[string]any{"stage": "won"}),
						ifMatch(3))
				}, nethttp.StatusOK, []announced{{"deal.stage-changed", "won"}, {"deal.won", "won"}}},
				{"a deletion", func() response {
					return writer.send(t, api(nethttp.MethodDelete, path, acme, w.asAlice, nil))
				}, nethttp.StatusNoContent, []announced{{"deal.deleted", "deleted"}}},
			} {
				before, _ := writer.pipelineRaw(t, acme.Alias, w.asAlice)
				sub.insertDeal(acme, "behind-"+strconv.Itoa(i), "proposal", int64(1000+i), w.alice)
				if cached, _ := reader.pipelineRaw(t, acme.Alias, w.asAlice); !bytes.Equal(cached, before) {
					t.Fatalf("before %s, %s answered acme's report as the table now is rather than as %s cached it\n"+
						"cached: %s\nserved: %s", write.what, reader.name, writer.name, before, cached)
				}
				expect(t, write.what+" of acme's w1", write.send(), write.status)
				from := seen
				seen += len(write.events)
				awaitInvalidations(t, replicas, acme, seen)
				var want []string
				for _, e := range write.events {
					want = append(want, e.eventType)
				}
				for _, run := range replicas {
					got := slices.Clone(invalidatingTypes(run, acme)[from:seen])
					slices.Sort(got)
					slices.Sort(want)
					if !slices.Equal(got, want) {
						t.Errorf("%s invalidated acme's report on %s through the events %v, want %v", write.what, run.name, got, want)
					}
				}
				for _, e := range write.events {
					sub.awaitEventRow(write.what+" of w1", e.eventType, func(r eventRow) bool {
						return r.detail["deal_id"] == "w1" && r.outcome == e.outcome && r.object == "deal/w1" &&
							r.tenant == acme.ID && r.detail["actor_id"] == w.alice.ID
					})
				}
				after, report := reader.pipelineRaw(t, acme.Alias, w.asAlice)
				if bytes.Equal(after, before) {
					t.Errorf("once every replica had handled %s, %s still answered acme's report cached before it: %s",
						write.what, reader.name, after)
					continue
				}
				if got, want := dealsIn(report), sub.liveDeals(acme); got != want {
					t.Errorf("after %s, acme's report counts %d deals and the table holds %d: %s", write.what, got, want, after)
				}
			}
			if again, _ := reader.pipelineRaw(t, globex.Alias, w.asBob); !bytes.Equal(again, globexRaw) {
				t.Errorf("globex's report was computed again after writes to acme's deals alone\nfirst: %s\nthen:  %s",
					globexRaw, again)
			}
		})
	})
}

// The report lifetime the lapse case writes, and how far either side of it the
// lapse may land: the slack covers the clocks a backend judges an expiry by and
// a second-granular expiry.
const (
	reportLifetime = 2 * time.Second
	reportSlack    = time.Second
)

// TestAPipelineReportLapsesWithItsTenantsLifetime is the case for the cache
// lifetime: the reports.cache-ttl setting, read for the request's tenant, and
// honoured by the key-value backend the stack names.
//
// acme's lifetime is written on its own layer and globex keeps the default.
// A row written behind the module's back reaches acme's report once its
// lifetime has passed and not well before, and globex's report, cached at the
// same moment, is still the cached one then. On a stack whose store several
// processes share, the reports are cached through one replica and read through
// the other, so what lapses is the entry the store holds, on the store's
// judgement: a cache kept in the process would answer the second replica with
// the table at once.
func TestAPipelineReportLapsesWithItsTenantsLifetime(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w, writer, reader := reportReplicas(t, c, func(w *dealsWorld) {
			c.setForTenant(w.acme, "reports.cache-ttl", reportLifetime.String())
		})
		sides := sidesOf(w)
		for _, side := range sides {
			definePipeline(t, writer, side.ref, side.cred, pipelineOf(side.name))
			createDealIn(t, writer, side.ref, side.cred, "e1", "qualify", 10000, "EUR", side.owner)
			awaitInvalidations(t, distinct(writer, reader), side.ref, 1)
		}

		cachedAt := time.Now()
		acmeRaw, _ := writer.pipelineRaw(t, w.acme.Alias, w.asAlice)
		globexRaw, _ := writer.pipelineRaw(t, w.globex.Alias, w.asBob)
		for _, side := range sides {
			c.insertDeal(side.ref, "d1", "won", 7000, side.owner)
		}

		const every = 100 * time.Millisecond
		var lapsedAt time.Time
		for time.Since(cachedAt) < reportLifetime+5*reportSlack {
			if raw, _ := reader.pipelineRaw(t, w.acme.Alias, w.asAlice); !bytes.Equal(raw, acmeRaw) {
				lapsedAt = time.Now()
				break
			}
			time.Sleep(every)
		}
		switch lived := lapsedAt.Sub(cachedAt); {
		case lapsedAt.IsZero():
			t.Fatalf("acme's report was still the cached one %s after it was cached with a lifetime of %s",
				time.Since(cachedAt).Round(time.Millisecond), reportLifetime)
		case lived < reportLifetime-reportSlack:
			t.Errorf("%s answered acme's report computed again %s after %s cached it, before its lifetime of %s "+
				"had passed", reader.name, lived.Round(time.Millisecond), writer.name, reportLifetime)
		}
		if raw, _ := reader.pipelineRaw(t, w.globex.Alias, w.asBob); !bytes.Equal(raw, globexRaw) {
			t.Errorf("globex's report, whose lifetime is the default, was computed again when acme's lapsed\n"+
				"first: %s\nthen:  %s", globexRaw, raw)
		}
	})
}

// dropStoreKey removes one key from the kv.postgres table behind the module's
// back, as a store that evicts keys would lose it. The key is spelled as the
// backend spells a key this short: its bytes as they are.
func (c *crmCase) dropStoreKey(key string) {
	c.t.Helper()
	c.exec("DELETE FROM kv_entries WHERE key = ?", []byte(key))
}

// storeExpiry reads when the kv.postgres table says one key lapses; the second
// result is false when the key is stored without an expiry.
func (c *crmCase) storeExpiry(key string) (time.Time, bool) {
	c.t.Helper()
	var expiry sql.NullTime
	if err := c.sql().QueryRow(c.rebind("SELECT expires_at FROM kv_entries WHERE key = ?"), []byte(key)).Scan(&expiry); err != nil {
		c.t.Fatalf("reading the expiry of %s: %v", key, err)
	}
	return expiry.Time, expiry.Valid
}

// TestLosingTheReportGenerationBringsNoSupersededReportBack is the case for the
// generation a tenant's cached report is filed under going missing, as it does
// when its lifetime passes or a store evicts it: the generation reads as 0
// again, and a report an invalidation superseded must not be what 0 finds.
//
// acme's first report is cached under generation 0, since nothing has
// invalidated it yet: its one deal was written behind the deals module's back.
// A creation invalidates it, and then the generation is lost. A module that
// only moved the generation on would answer the next read with that first
// report, which knows nothing of the creation.
//
// The generation keys carry a lifetime of their own, so a tenant whose reports
// nobody reads again, and an identifier a deal event names and no tenant has,
// leave no key behind for ever. The deals module publishes for tenants a
// request resolved alone, so the event for a tenant no roster carries comes
// from the deal feed.
//
// It runs on the stack whose key-value store is a table the harness reads, which
// is how a case loses a key the way a store would.
func TestLosingTheReportGenerationBringsNoSupersededReportBack(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, "postgres"))
	w := startDeals(t, c, func(*dealsWorld) { c.config.set("dealfeed.enabled", true) })
	acme, h, nobody := w.acme, w.h, c.tenantNamed("nobody")
	definePipeline(t, h, acme, w.asAlice, pipelineOf("acme"))
	c.insertDeal(acme, "e1", "qualify", 10000, w.alice)

	superseded, report := h.pipelineRaw(t, acme.Alias, w.asAlice)
	requireStages(t, "acme's first report", report, stageTotal{"qualify", "EUR", 1, 10000})
	createDealIn(t, h, acme, w.asAlice, "e2", "won", 5000, "EUR", w.alice)
	awaitInvalidations(t, []*process{h.process}, acme, 1)
	_, report = h.pipelineRaw(t, acme.Alias, w.asAlice)
	want := []stageTotal{{"qualify", "EUR", 1, 10000}, {"won", "EUR", 1, 5000}}
	requireStages(t, "acme's report after the creation", report, want...)
	generation := pipelineGenerationKey(tenant.ID(acme.ID))
	if expiry, ok := c.storeExpiry(generation); !ok || time.Until(expiry) <= 0 {
		t.Errorf("acme's report generation is stored with the expiry %v (set: %v), want one still ahead", expiry, ok)
	}

	c.dropStoreKey(generation)
	raw, report := h.pipelineRaw(t, acme.Alias, w.asAlice)
	if bytes.Equal(raw, superseded) {
		t.Errorf("once acme's report generation was lost, acme was answered with the report the creation had "+
			"superseded: %s", raw)
	}
	requireStages(t, "acme's report once its generation was lost", report, want...)

	h.publishWon(t, wonEvent(nobody, "", "d_1", 1, w.alice))
	awaitInvalidations(t, []*process{h.process}, nobody, 1)
	if expiry, ok := c.storeExpiry(pipelineGenerationKey(tenant.ID(nobody.ID))); !ok || time.Until(expiry) <= 0 {
		t.Errorf("the report generation a deal event for %s left is stored with the expiry %v (set: %v), "+
			"want one still ahead", nobody.ID, expiry, ok)
	}
}

// leadersOf reads a tenant's leaderboard in one currency.
func leadersOf(t *testing.T, h *crmHost, ref tenantRef, cred credential, currency string) []leader {
	t.Helper()
	resp := h.get(t, apiEndpoint, "/reports/leaderboard?currency="+currency, ref.Alias, cred)
	expect(t, ref.ID+"'s "+currency+" leaderboard", resp, nethttp.StatusOK)
	var got leaderboardView
	resp.decode(t, &got)
	return got.Leaders
}

// TestTheRevenueLineCountsWinsAndTheReportsCountDeals is the case for the two
// answers to what a tenant has won. The revenue line is the projection of the
// deal.won events: it counts each win once, when it is announced, with the
// amount the deal had then. The leaderboard and the pipeline report's won rows
// read the deals table as it now is. A deal won, moved back and won again is
// two wins and one won deal; a change of its amount, and its deletion, move
// the reports and leave the revenue line as it was.
func TestTheRevenueLineCountsWinsAndTheReportsCountDeals(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	w := startDeals(t, c, nil)
	acme, alice := w.acme, w.asAlice
	definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
	winDeal(t, w.h, acme, alice, "rw-1", "EUR", 1000)
	awaitRevenue(t, w.h, acme, alice, revenueView{Currency: "EUR", Deals: 1, Amount: 1000})

	expect(t, "moving rw-1 back to proposal", w.move(t, acme, alice, "rw-1", "proposal", 2), nethttp.StatusOK)
	expect(t, "winning rw-1 again", w.move(t, acme, alice, "rw-1", "won", 3), nethttp.StatusOK)
	twice := revenueView{Currency: "EUR", Deals: 2, Amount: 2000}
	awaitRevenue(t, w.h, acme, alice, twice)
	if got := leadersOf(t, w.h, acme, alice, "EUR"); !slices.Equal(got, []leader{{w.alice.ID, "alice", 1, 1000}}) {
		t.Errorf("acme's leaderboard holds %+v once rw-1 was won twice, want the one deal", got)
	}

	// Each report read waits for the write's own invalidation, so a report
	// cached before the write cannot answer for it.
	seen := invalidations(w.h.process, acme)
	expect(t, "changing the amount of the won rw-1",
		w.h.sendWith(t, api(nethttp.MethodPatch, "/deals/rw-1", acme, alice, map[string]any{"amount": 5000}),
			headers{"If-Match": `"4"`}), nethttp.StatusOK)
	awaitInvalidations(t, []*process{w.h.process}, acme, seen+1)
	if got := leadersOf(t, w.h, acme, alice, "EUR"); !slices.Equal(got, []leader{{w.alice.ID, "alice", 1, 5000}}) {
		t.Errorf("acme's leaderboard holds %+v once rw-1's amount changed, want the deal at its new amount", got)
	}
	_, report := w.h.pipelineRaw(t, acme.Alias, alice)
	requireStages(t, "acme's pipeline report once rw-1's amount changed", report, stageTotal{"won", "EUR", 1, 5000})
	awaitRevenue(t, w.h, acme, alice, twice)

	seen = invalidations(w.h.process, acme)
	expect(t, "deleting rw-1", w.h.send(t, api(nethttp.MethodDelete, "/deals/rw-1", acme, alice, nil)),
		nethttp.StatusNoContent)
	awaitInvalidations(t, []*process{w.h.process}, acme, seen+1)
	if got := leadersOf(t, w.h, acme, alice, "EUR"); len(got) != 0 {
		t.Errorf("acme's leaderboard holds %+v once rw-1 was deleted, want nothing", got)
	}
	_, report = w.h.pipelineRaw(t, acme.Alias, alice)
	requireStages(t, "acme's pipeline report once rw-1 was deleted", report)
	awaitRevenue(t, w.h, acme, alice, twice)
}

// TestReportAndFlowRoutesTakeTheirLeastRole is the case for the report and flow
// routes now that the enforcement middleware decides them. Each is a read of what
// other modules wrote, so each declares the report or flow read permission, which
// the matrix grants to every tenant role: a viewer, the least member, reaches
// every one. A member with no binding is not part of this case; the route
// declarations' exactness is the case of the assembly above.
func TestReportAndFlowRoutesTakeTheirLeastRole(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.sealWithCaseKey()
		c.migrate()
		h := c.serve()
		cr := setUpCrew(t, c, h)
		vera := cr.as(h, cr.vera, cr.acme) // viewer

		for _, path := range []string{
			"/reports/pipeline",
			"/reports/leaderboard?currency=EUR",
			"/revenue",
			"/reports/webhook-deliveries",
			"/pipeline/flow",
		} {
			vera.must(t, nethttp.MethodGet, path, nil, nethttp.StatusOK)
		}
	})
}
