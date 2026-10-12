package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	nethttp "net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The cases of the deal.won consumers: the revenue projection and the
// webhooks, on the serving process and beside it on the worker. The reports'
// cases are in reports_test.go.
//
// Every deal.won a case can make through the deals module it makes there: it
// defines a pipeline, creates a deal and moves it into the won stage, and the
// event is the one the move publishes and records on its activity. What the
// deals module never publishes — one identifier twice, one identifier in two
// tenants, a tenant the consumers do not serve — a case publishes through the
// deal feed (see dealfeed.go), as the platform operator.
//
// The event types, the setting keys, the payload members and the request
// headers are spelled out here rather than taken from the host's constants:
// they are what the deals module publishes, what an operator writes and what a
// receiver reads, and a case built from the host's own spelling would follow
// the host's rename.

// webhookCall is one request the receiver took.
type webhookCall struct {
	path   string
	header nethttp.Header
	body   []byte
}

// webhookReceiver stands in for the tenants' webhook endpoints: it records every
// request and answers each path with the statuses it was given, in order, the
// last one repeating. A request a case asked it to hold is recorded as it
// arrives and answered once the case lets it go or its client gives up on it.
type webhookReceiver struct {
	server *httptest.Server

	mu      sync.Mutex
	calls   []webhookCall
	answers map[string][]int
	taken   map[string]int
	held    map[heldKey]*heldCall
}

// heldKey names one request to one path, counting from 1.
type heldKey struct {
	path string
	call int
}

// heldCall is a request the receiver holds open.
type heldCall struct {
	arrived chan struct{} // closed once the request has arrived and been recorded
	ended   chan struct{} // closed once the receiver has answered it, or its client has gone
	release chan struct{}
	once    sync.Once
}

// let lets the held request be answered.
func (h *heldCall) let() { h.once.Do(func() { close(h.release) }) }

// hasEnded reports whether the held request is over.
func (h *heldCall) hasEnded() bool {
	select {
	case <-h.ended:
		return true
	default:
		return false
	}
}

// startWebhookReceiver starts a receiver for one case. A path it was given no
// statuses for answers 204.
func startWebhookReceiver(t *testing.T) *webhookReceiver {
	t.Helper()
	r := &webhookReceiver{answers: map[string][]int{}, taken: map[string]int{}, held: map[heldKey]*heldCall{}}
	r.server = httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, req *nethttp.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(nethttp.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.calls = append(r.calls, webhookCall{path: req.URL.Path, header: req.Header.Clone(), body: body})
		status := nethttp.StatusNoContent
		if statuses := r.answers[req.URL.Path]; len(statuses) > 0 {
			status = statuses[min(r.taken[req.URL.Path], len(statuses)-1)]
		}
		r.taken[req.URL.Path]++
		held := r.held[heldKey{req.URL.Path, r.taken[req.URL.Path]}]
		r.mu.Unlock()
		if held != nil {
			defer close(held.ended)
			close(held.arrived)
			select {
			case <-held.release:
			case <-req.Context().Done():
				return
			}
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(r.server.Close)
	return r
}

// hold makes the receiver hold the call-th request to one path, counting from 1,
// until the case lets it go. The case's end lets it go too, so the receiver can
// be closed.
func (r *webhookReceiver) hold(t *testing.T, path string, call int) *heldCall {
	t.Helper()
	h := &heldCall{arrived: make(chan struct{}), ended: make(chan struct{}), release: make(chan struct{})}
	r.mu.Lock()
	r.held[heldKey{path, call}] = h
	r.mu.Unlock()
	t.Cleanup(h.let)
	return h
}

// awaitArrival waits for a held request to arrive.
func (h *heldCall) awaitArrival(t *testing.T, what string) {
	t.Helper()
	select {
	case <-h.arrived:
	case <-time.After(eventLimit):
		t.Fatalf("%s did not reach the receiver within %s", what, eventLimit)
	}
}

// url is the receiver's URL for one path.
func (r *webhookReceiver) url(path string) string { return r.server.URL + path }

// answer sets the statuses one path answers with.
func (r *webhookReceiver) answer(path string, statuses ...int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answers[path] = statuses
}

// callsTo is every request one path took, in the order they arrived.
func (r *webhookReceiver) callsTo(path string) []webhookCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []webhookCall
	for _, call := range r.calls {
		if call.path == path {
			out = append(out, call)
		}
	}
	return out
}

// webhookPath is where a tenant's webhook points on the receiver.
func webhookPath(ref tenantRef) string { return "/hooks/" + ref.ID }

// configureWebhook writes a tenant's webhook URL and signing secret on its own
// layer before a run starts.
func (c *crmCase) configureWebhook(r *webhookReceiver, ref tenantRef, secret string) {
	c.t.Helper()
	c.setForTenant(ref, "webhooks.url", r.url(webhookPath(ref)))
	c.setForTenant(ref, "webhooks.secret", secret)
}

// receivedWebhook is a delivery's body, with the member names a receiver reads.
type receivedWebhook struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	TenantID string `json:"tenant_id"`
	Data     struct {
		Object     string `json:"object"`
		Outcome    string `json:"outcome"`
		DealID     string `json:"deal_id"`
		AccountID  string `json:"account_id"`
		Stage      string `json:"stage"`
		Amount     int64  `json:"amount"`
		Currency   string `json:"currency"`
		OwnerID    string `json:"owner_id"`
		WonAt      string `json:"won_at"`
		ActivityID string `json:"activity_id"`
		ActorID    string `json:"actor_id"`
	} `json:"data"`
}

// signedWith reports whether a call carries the HMAC-SHA256 of its body under a
// secret, written as a receiver checks it.
func (w webhookCall) signedWith(secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(w.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(w.header.Get("X-CRM-Signature")), []byte(want))
}

// byEventID groups a path's calls by the event they deliver.
func byEventID(calls []webhookCall) map[string][]webhookCall {
	out := map[string][]webhookCall{}
	for _, call := range calls {
		id := call.header.Get("X-CRM-Event-Id")
		out[id] = append(out[id], call)
	}
	return out
}

// eventKey names one event of one tenant.
type eventKey struct {
	tenant string
	id     string
}

// The records each consumer writes once it is done with one delivery of an
// event, whichever way it went.
var (
	revenueOutcomes = []string{wonCountedMsg, wonSkippedMsg, wonNotServedMsg}
	webhookOutcomes = []string{
		webhookDeliveredMsg, webhookFailedMsg, webhookAlreadyClaimedMsg, webhookAlreadyDeliveredMsg,
		webhookExhaustedMsg, webhookInterruptedMsg, webhookStoppingMsg, webhookNotServedMsg, webhookUnusableMsg,
	}
)

// outcomesIn counts, in one run's output, the records a consumer module wrote
// about each event, by the outcome each names.
func outcomesIn(out, module string, outcomes []string) map[eventKey]map[string]int {
	counts := map[eventKey]map[string]int{}
	for _, rec := range records(out) {
		if rec["module"] != module || !slices.Contains(outcomes, rec["msg"]) {
			continue
		}
		key := eventKey{tenant: rec["tenant_id"], id: rec["event_id"]}
		if counts[key] == nil {
			counts[key] = map[string]int{}
		}
		counts[key][rec["msg"]]++
	}
	return counts
}

// awaitConsumers waits until every run given has written, for each event, one
// outcome record of the module for each time the event was published. Every run
// receives every event, so until each has, a count taken of what the consumers
// did is not final; once each has, it is.
func awaitConsumers(t *testing.T, runs []*process, module string, outcomes []string, published map[eventKey]int) {
	t.Helper()
	settled := func() bool {
		for _, run := range runs {
			counts := outcomesIn(run.output(), module, outcomes)
			for key, times := range published {
				total := 0
				for _, n := range counts[key] {
					total += n
				}
				if total != times {
					return false
				}
			}
		}
		return true
	}
	if !eventually(eventLimit, settled) {
		var report strings.Builder
		for _, run := range runs {
			report.WriteString("\n" + run.name + ": ")
			for key, byOutcome := range outcomesIn(run.output(), module, outcomes) {
				report.WriteString(key.tenant + "/" + key.id + "=" + formatCounts(byOutcome) + " ")
			}
		}
		t.Fatalf("within %s not every run wrote one %s outcome per delivery of %v; they wrote:%s",
			eventLimit, module, published, report.String())
	}
}

// formatCounts writes a count per outcome for a failure message.
func formatCounts(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for msg, n := range counts {
		parts = append(parts, strconv.Quote(msg)+":"+strconv.Itoa(n))
	}
	slices.Sort(parts)
	return "{" + strings.Join(parts, ",") + "}"
}

// outcomeTotal adds up, over every run, the records of one outcome a module
// wrote about one event.
func outcomeTotal(runs []*process, module string, outcomes []string, key eventKey, outcome string) int {
	total := 0
	for _, run := range runs {
		total += outcomesIn(run.output(), module, outcomes)[key][outcome]
	}
	return total
}

// consumerRuns is the serving replica and, on a stack whose bus crosses
// processes, the workers beside it, each ready before anything is published: a
// subscription covers only the events published after it takes effect. On the
// in-process bus an event reaches the process that published it and no other,
// so the serving replica is the only consumer there.
func (c *crmCase) consumerRuns(h *crmHost, workers int) []*process {
	c.t.Helper()
	runs := []*process{h.process}
	if c.processLocal {
		return runs
	}
	for range workers {
		runs = append(runs, c.worker())
	}
	return runs
}

// revenueLine reads a tenant's won revenue in one currency; a currency the
// tenant has won nothing in reads as a line of nothing.
func revenueLine(t *testing.T, h *crmHost, ref tenantRef, cred credential, currency string) revenueView {
	t.Helper()
	got, _ := revenueOf(t, h, ref, cred)
	for _, line := range got.Revenue {
		if line.Currency == currency {
			return line
		}
	}
	return revenueView{Currency: currency}
}

// deliveriesOf reads a tenant's webhook deliveries through the API.
func (h *crmHost) deliveriesOf(t *testing.T, ref tenantRef, cred credential) []deliveryView {
	t.Helper()
	resp := h.get(t, apiEndpoint, "/reports/webhook-deliveries", ref.Alias, cred)
	expect(t, "GET /reports/webhook-deliveries in "+ref.Alias, resp, nethttp.StatusOK)
	var got deliveriesView
	resp.decode(t, &got)
	return got.Deliveries
}

// traceparentPattern is the W3C trace context header, version 00: the trace
// identifier, the identifier of the span the request was made in, and the flags.
var traceparentPattern = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)

// exportedSpan is a span as the stdout export backend writes it, one JSON
// object to a line.
type exportedSpan struct {
	Name        string `json:"Name"`
	SpanContext struct {
		TraceID string `json:"TraceID"`
		SpanID  string `json:"SpanID"`
	} `json:"SpanContext"`
}

// exportedSpans reads the spans of one name out of a run's standard output.
func exportedSpans(out, name string) []exportedSpan {
	var found []exportedSpan
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"Name":"`+name+`"`) {
			continue
		}
		var span exportedSpan
		if json.Unmarshal([]byte(line), &span) == nil && span.Name == name {
			found = append(found, span)
		}
	}
	return found
}

// wonEvent is the body of a deal.won published through the feed, written with
// the member names a client writes. An empty identifier leaves the feed to
// mint one.
func wonEvent(ref tenantRef, id, dealID string, amount int64, owner userRef) map[string]any {
	body := map[string]any{
		"tenant_id": ref.ID, "deal_id": dealID, "account_id": "acc-1", "stage": "won",
		"amount": amount, "currency": "EUR", "owner_id": owner.ID,
	}
	if id != "" {
		body["id"] = id
	}
	return body
}

// publishWon publishes a deal.won through the feed as the operator, and
// returns the envelope the feed handed the bus.
func (h *crmHost) publishWon(t *testing.T, body map[string]any) dealFeedView {
	t.Helper()
	resp := h.post(t, adminEndpoint, "/deal-events", "", operator(), body)
	expect(t, "publishing a deal.won for "+body["tenant_id"].(string), resp, nethttp.StatusAccepted)
	var got dealFeedView
	resp.decode(t, &got)
	if got.ID == "" || got.Type != "deal.won" || got.TenantID != body["tenant_id"] {
		t.Fatalf("the feed answered %+v for %v", got, body)
	}
	if id, ok := body["id"]; ok && got.ID != id {
		t.Fatalf("the feed answered the identifier %q for a publish under %q", got.ID, id)
	}
	return got
}

// wonDeal is one deal a case wins.
type wonDeal struct {
	id     string
	amount int64
}

// TestEachWonDealIsDeliveredAndCountedOnce is the case for the two consumers of
// deal.won running in several processes: the serving replica and two workers on
// a stack whose bus crosses processes, each receiving every event, and the
// serving replica alone on the in-process bus.
//
// Both tenants win deals under the same deal identifiers through the deals
// module, and one move goes to an open stage, which publishes no deal.won. A
// consumer that did not claim each event would act once per process; a counter
// or a delivery row that did not carry its tenant would show the other
// tenant's deals.
//
// Then one of acme's events is published a second time, as a publisher does
// after a failure it could not read, and globex gets an event under the same
// identifier. A consumer that did not claim would act on the second delivery
// again, and one that claimed by the identifier alone would take globex's event
// for acme's second delivery and drop it.
func TestEachWonDealIsDeliveredAndCountedOnce(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		receiver := startWebhookReceiver(t)
		secrets := map[string]string{}
		w := startDeals(t, c, func(w *dealsWorld) {
			c.config.set("dealfeed.enabled", true)
			secrets[w.acme.ID] = "acme signs with this " + c.suffix
			secrets[w.globex.ID] = "globex signs with that " + c.suffix
			for _, ref := range []tenantRef{w.acme, w.globex} {
				c.configureWebhook(receiver, ref, secrets[ref.ID])
			}
		})
		runs := c.consumerRuns(w.h, 2)
		sides := sidesOf(w)

		// One amount is past what a float64 holds, so a delivery that carried
		// it through a float would post another number.
		won := []wonDeal{{"won-1", 12500}, {"won-2", 40000}, {"won-3", pastFloat}}
		var wonTotal int64
		for _, deal := range won {
			wonTotal += deal.amount
		}
		published := map[eventKey]int{}
		wonIDs := map[string]map[string]string{}
		wonActivities := map[string]map[string]string{}
		for _, side := range sides {
			definePipeline(t, w.h, side.ref, side.cred, pipelineOf(side.name))
			wonIDs[side.ref.ID] = map[string]string{}
			wonActivities[side.ref.ID] = map[string]string{}
			for _, deal := range won {
				activity := winDeal(t, w.h, side.ref, side.cred, deal.id, "EUR", deal.amount)
				wonIDs[side.ref.ID][deal.id] = *activity.WonEventID
				wonActivities[side.ref.ID][deal.id] = activity.ID
				published[eventKey{side.ref.ID, *activity.WonEventID}]++
			}
		}
		createDealIn(t, w.h, w.acme, w.asAlice, "moved-1", "qualify", 999, "EUR", w.alice)
		expect(t, "moving acme's moved-1 to proposal", w.move(t, w.acme, w.asAlice, "moved-1", "proposal", 1),
			nethttp.StatusOK)
		awaitConsumers(t, runs, revenueModuleName, revenueOutcomes, published)
		awaitConsumers(t, runs, webhooksModuleName, webhookOutcomes, published)

		t.Run("one process acts on each event, whichever received it", func(t *testing.T) {
			for key := range published {
				if n := outcomeTotal(runs, revenueModuleName, revenueOutcomes, key, wonCountedMsg); n != 1 {
					t.Errorf("%s of %s was counted by %d processes, want 1", key.id, key.tenant, n)
				}
				if n := outcomeTotal(runs, webhooksModuleName, webhookOutcomes, key, webhookDeliveredMsg); n != 1 {
					t.Errorf("%s of %s was delivered by %d processes, want 1", key.id, key.tenant, n)
				}
			}
		})

		t.Run("each won deal reaches its tenant's webhook once, signed with its tenant's secret", func(t *testing.T) {
			for _, side := range sides {
				calls := byEventID(receiver.callsTo(webhookPath(side.ref)))
				if got, want := slices.Sorted(maps.Keys(calls)), slices.Sorted(maps.Values(wonIDs[side.ref.ID])); !slices.Equal(got, want) {
					t.Errorf("%s's webhook received the events %v, want its won deals' %v", side.ref.ID, got, want)
				}
				for _, deal := range won {
					eventID := wonIDs[side.ref.ID][deal.id]
					got := calls[eventID]
					if len(got) != 1 {
						t.Errorf("%s's webhook received %s's event %d times, want once", side.ref.ID, deal.id, len(got))
						continue
					}
					call := got[0]
					if !call.signedWith(secrets[side.ref.ID]) {
						t.Errorf("%s's delivery of %s carries the signature %q, which is not its body's under %s's secret",
							side.ref.ID, deal.id, call.header.Get("X-CRM-Signature"), side.ref.ID)
					}
					var body receivedWebhook
					if err := json.Unmarshal(call.body, &body); err != nil {
						t.Errorf("%s's delivery of %s is not JSON: %v\n%s", side.ref.ID, deal.id, err, call.body)
						continue
					}
					if body.ID != eventID || body.Type != "deal.won" || body.TenantID != side.ref.ID ||
						body.Data.DealID != deal.id || body.Data.AccountID != "acc-1" || body.Data.Stage != "won" ||
						body.Data.Amount != deal.amount || body.Data.Currency != "EUR" || body.Data.OwnerID != side.owner.ID ||
						body.Data.Object != "deal/"+deal.id || body.Data.Outcome != "won" ||
						body.Data.ActivityID != wonActivities[side.ref.ID][deal.id] || body.Data.ActorID != side.owner.ID {
						t.Errorf("%s's delivery of %s carries %+v, want the move's activity %s", side.ref.ID, deal.id, body,
							wonActivities[side.ref.ID][deal.id])
					}
					if _, err := time.Parse(time.RFC3339Nano, body.Data.WonAt); err != nil {
						t.Errorf("%s's delivery of %s says it was won at %q, which is no RFC 3339 time", side.ref.ID, deal.id,
							body.Data.WonAt)
					}
					// The amount travels as the JSON integer it is: the body is
					// read as text, since decoding it would say nothing about a
					// number a float rounded on the way out.
					if exact := `"amount":` + strconv.FormatInt(deal.amount, 10); !strings.Contains(string(call.body), exact) {
						t.Errorf("%s's delivery of %s does not carry %s: %s", side.ref.ID, deal.id, exact, call.body)
					}
					if got := call.header.Get("Content-Type"); got != "application/json" {
						t.Errorf("%s's delivery of %s is sent as %q", side.ref.ID, deal.id, got)
					}
					if got := call.header.Get("X-CRM-Delivery-Attempt"); got != "1" {
						t.Errorf("%s's delivery of %s is attempt %q, want 1", side.ref.ID, deal.id, got)
					}
				}
			}
			for _, call := range receiver.callsTo(webhookPath(w.acme)) {
				if call.signedWith(secrets[w.globex.ID]) {
					t.Errorf("acme's delivery of %s is signed with globex's secret", call.header.Get("X-CRM-Event-Id"))
				}
			}
		})

		t.Run("the projection counts each tenant's won deals once", func(t *testing.T) {
			for _, side := range sides {
				got, _ := revenueOf(t, w.h, side.ref, side.cred)
				if want := []revenueView{{Currency: "EUR", Deals: 3, Amount: wonTotal}}; !slices.Equal(got.Revenue, want) {
					t.Errorf("%s's won revenue reads %+v, want %+v", side.ref.ID, got.Revenue, want)
				}
			}
		})

		t.Run("each tenant's delivery rows are its own", func(t *testing.T) {
			for _, side := range sides {
				rows := w.h.deliveriesOf(t, side.ref, side.cred)
				ids := make([]string, 0, len(rows))
				for _, d := range rows {
					ids = append(ids, d.EventID)
					if d.TenantID != side.ref.ID || d.EventType != "deal.won" || d.Status != "delivered" ||
						d.Attempts != 1 || d.ResponseCode != nethttp.StatusNoContent {
						t.Errorf("%s's delivery of %s reads %+v", side.ref.ID, d.EventID, d)
					}
				}
				slices.Sort(ids)
				if want := slices.Sorted(maps.Values(wonIDs[side.ref.ID])); !slices.Equal(ids, want) {
					t.Errorf("%s's deliveries list %v, want %v", side.ref.ID, ids, want)
				}
			}
		})

		t.Run("a webhook request carries the trace context of the span it was made in", func(t *testing.T) {
			var calls []webhookCall
			for _, side := range sides {
				calls = append(calls, receiver.callsTo(webhookPath(side.ref))...)
			}
			for _, call := range calls {
				header := call.header.Get("Traceparent")
				m := traceparentPattern.FindStringSubmatch(header)
				if m == nil || m[1] == strings.Repeat("0", 32) {
					t.Errorf("the delivery of %s carries the traceparent %q", call.header.Get("X-CRM-Event-Id"), header)
					continue
				}
				traceID, spanID := m[1], m[2]
				if !eventually(settleLimit, func() bool {
					// On the OTLP stack the spans leave through the case's
					// receiver, which decodes them; on the others through
					// each run's standard output.
					if c.receiver != nil {
						return spanNamedIn(c.receiver, "webhooks POST", traceID, spanID)
					}
					for _, run := range runs {
						for _, span := range exportedSpans(run.output(), "webhooks POST") {
							if span.SpanContext.TraceID == traceID && span.SpanContext.SpanID == spanID {
								return true
							}
						}
					}
					return false
				}) {
					t.Errorf("the traceparent %q of the delivery of %s names no exported span called %q",
						header, call.header.Get("X-CRM-Event-Id"), "webhooks POST")
				}
			}
		})

		t.Run("a second delivery of an event is recognised, and one identifier in two tenants is two events", func(t *testing.T) {
			again := wonIDs[w.acme.ID]["won-2"]
			w.h.publishWon(t, wonEvent(w.acme, again, "won-2", 40000, w.alice))
			w.h.publishWon(t, wonEvent(w.globex, again, "twin", 11, w.bob))
			acmeKey, globexKey := eventKey{w.acme.ID, again}, eventKey{w.globex.ID, again}
			published[acmeKey]++
			published[globexKey]++
			awaitConsumers(t, runs, revenueModuleName, revenueOutcomes, published)
			awaitConsumers(t, runs, webhooksModuleName, webhookOutcomes, published)
			for _, key := range []eventKey{acmeKey, globexKey} {
				if n := outcomeTotal(runs, revenueModuleName, revenueOutcomes, key, wonCountedMsg); n != 1 {
					t.Errorf("%s of %s was counted %d times, want once", key.id, key.tenant, n)
				}
				if n := outcomeTotal(runs, webhooksModuleName, webhookOutcomes, key, webhookDeliveredMsg); n != 1 {
					t.Errorf("%s of %s was delivered %d times, want once", key.id, key.tenant, n)
				}
			}
			if calls := byEventID(receiver.callsTo(webhookPath(w.acme)))[again]; len(calls) != 1 {
				t.Errorf("acme's webhook received its won-2 event %d times once it was published again, want once", len(calls))
			}
			if calls := byEventID(receiver.callsTo(webhookPath(w.globex)))[again]; len(calls) != 1 {
				t.Errorf("globex's webhook received the event under acme's won-2 identifier %d times, want once", len(calls))
			}
			if got := revenueLine(t, w.h, w.acme, w.asAlice, "EUR"); got.Deals != 3 || got.Amount != wonTotal {
				t.Errorf("acme's won revenue reads %+v once its won-2 event was published again, want it as it was", got)
			}
			if got := revenueLine(t, w.h, w.globex, w.asBob, "EUR"); got.Deals != 4 || got.Amount != wonTotal+11 {
				t.Errorf("globex's won revenue reads %+v once it got an event under acme's won-2 identifier, "+
					"want that deal counted", got)
			}
		})
	})
}

// TestAWebhookIsRetriedAndEachAttemptIsRecorded is the case for a delivery the
// receiver does not take at once: each attempt is recorded on the tenant's
// delivery row as it ends, and the row says how the delivery ended.
//
// acme wins a deal through the deals module, and globex gets a deal.won under
// the same event identifier through the feed. acme's receiver refuses the first
// attempt and holds the second until the case has read the row, which then
// says one attempt was made and refused: a module that wrote the row once, at
// the end, would show none. globex's receiver refuses every attempt. The
// attempts are recorded by an update naming the event alone, so an update the
// filter did not scope would write globex's failures over acme's delivered row.
//
// globex's claim lasts claimLifetime, and once it has lapsed the event is
// published again: the row has used every attempt, and a second delivery sends
// nothing and records that rather than another failure.
func TestAWebhookIsRetriedAndEachAttemptIsRecorded(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		receiver := startWebhookReceiver(t)
		secrets := map[string]string{}
		var second *heldCall
		w := startDeals(t, c, func(w *dealsWorld) {
			c.config.set("dealfeed.enabled", true)
			c.setGlobal("webhooks.retry-delay", "50ms")
			receiver.answer(webhookPath(w.acme), nethttp.StatusServiceUnavailable, nethttp.StatusOK)
			second = receiver.hold(t, webhookPath(w.acme), 2)
			receiver.answer(webhookPath(w.globex), nethttp.StatusInternalServerError)
			secrets[w.acme.ID] = "acme's secret " + c.suffix
			secrets[w.globex.ID] = "globex's secret " + c.suffix
			for _, ref := range []tenantRef{w.acme, w.globex} {
				c.configureWebhook(receiver, ref, secrets[ref.ID])
			}
			c.setForTenant(w.globex, "webhooks.claim-ttl", claimLifetime.String())
		})
		acme, globex := w.acme, w.globex
		definePipeline(t, w.h, acme, w.asAlice, pipelineOf("acme"))
		eventID := *winDeal(t, w.h, acme, w.asAlice, "won-1", "EUR", 30000).WonEventID
		w.h.publishWon(t, wonEvent(globex, eventID, "won-1", 30000, w.bob))
		published := map[eventKey]int{{acme.ID, eventID}: 1, {globex.ID, eventID}: 1}

		second.awaitArrival(t, "the second attempt of acme's delivery")
		pending := deliveryView{
			TenantID: acme.ID, EventID: eventID, EventType: "deal.won", Status: "pending",
			Attempts: 1, ResponseCode: nethttp.StatusServiceUnavailable,
		}
		if rows := w.h.deliveriesOf(t, acme, w.asAlice); len(rows) != 1 {
			t.Errorf("while its second attempt was in flight, acme's deliveries list %+v, want its one delivery", rows)
		} else {
			got := rows[0]
			got.UpdatedAt = time.Time{}
			if got != pending {
				t.Errorf("while its second attempt was in flight, acme's delivery reads %+v, want %+v", got, pending)
			}
		}
		second.let()
		runs := []*process{w.h.process}
		awaitConsumers(t, runs, webhooksModuleName, webhookOutcomes, published)
		settled := time.Now()

		for _, tc := range []struct {
			ref      tenantRef
			cred     credential
			attempts []string
			want     deliveryView
		}{
			{acme, w.asAlice, []string{"1", "2"}, deliveryView{
				TenantID: acme.ID, EventID: eventID, EventType: "deal.won", Status: "delivered",
				Attempts: 2, ResponseCode: nethttp.StatusOK,
			}},
			{globex, w.asBob, []string{"1", "2", "3"}, deliveryView{
				TenantID: globex.ID, EventID: eventID, EventType: "deal.won", Status: "failed",
				Attempts: 3, ResponseCode: nethttp.StatusInternalServerError,
			}},
		} {
			var attempts []string
			for _, call := range receiver.callsTo(webhookPath(tc.ref)) {
				attempts = append(attempts, call.header.Get("X-CRM-Delivery-Attempt"))
				if !call.signedWith(secrets[tc.ref.ID]) || call.header.Get("X-CRM-Event-Id") != eventID {
					t.Errorf("attempt %s of %s's delivery is event %q signed %q", call.header.Get("X-CRM-Delivery-Attempt"),
						tc.ref.ID, call.header.Get("X-CRM-Event-Id"), call.header.Get("X-CRM-Signature"))
				}
			}
			if !slices.Equal(attempts, tc.attempts) {
				t.Errorf("%s's receiver took the attempts %v, want %v", tc.ref.ID, attempts, tc.attempts)
			}
			rows := w.h.deliveriesOf(t, tc.ref, tc.cred)
			if len(rows) != 1 {
				t.Errorf("%s's deliveries list %+v, want its one delivery", tc.ref.ID, rows)
				continue
			}
			got := rows[0]
			got.UpdatedAt = time.Time{}
			if got != tc.want {
				t.Errorf("%s's delivery reads %+v, want %+v", tc.ref.ID, got, tc.want)
			}
		}

		t.Run("a delivery that used every attempt is not attempted again when its event returns", func(t *testing.T) {
			key := eventKey{globex.ID, eventID}
			time.Sleep(time.Until(settled.Add(claimLifetime + claimSlack)))
			w.h.publishWon(t, wonEvent(globex, eventID, "won-1", 30000, w.bob))
			published[key]++
			awaitConsumers(t, runs, webhooksModuleName, webhookOutcomes, published)
			if n := outcomeTotal(runs, webhooksModuleName, webhookOutcomes, key, webhookExhaustedMsg); n != 1 {
				t.Errorf("the second delivery of globex's failed event was recorded as having no attempts left %d "+
					"times, want once", n)
			}
			if n := outcomeTotal(runs, webhooksModuleName, webhookOutcomes, key, webhookFailedMsg); n != 1 {
				t.Errorf("globex's event was recorded as failing %d times, want once: the second delivery made no attempt", n)
			}
			if calls := receiver.callsTo(webhookPath(globex)); len(calls) != 3 {
				t.Errorf("globex's receiver took %d requests once the event returned, want the first delivery's 3",
					len(calls))
			}
			rows := w.h.deliveriesOf(t, globex, w.asBob)
			if len(rows) != 1 || rows[0].Status != "failed" || rows[0].Attempts != 3 {
				t.Errorf("globex's deliveries read %+v once the event returned, want its one failed after 3 attempts", rows)
			}
		})
	})
}

// TestADisabledOrUnknownTenantGetsNoDelivery is the case for a deal.won naming
// a tenant the roster does not serve: each consumer enters the event's tenant,
// the roster refuses a disabled one and one it does not carry, and the consumer
// records the refusal and does nothing more.
//
// dormant is disabled before any process starts, and nobody is a tenant the
// roster never carried; neither can have a request resolved, so their events
// come from the feed. Both have a webhook configured: the settings surface
// holds a tenant's layer whether or not the roster carries the tenant, so a
// consumer that crossed tenants to act on the event, rather than entering its
// tenant, would count either deal and find a webhook to post it to. acme's
// deal, won through the deals module, is the one that shows the consumers were
// running.
func TestADisabledOrUnknownTenantGetsNoDelivery(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		receiver := startWebhookReceiver(t)
		nobody := c.tenantNamed("nobody")
		var dormant tenantRef
		w := startDeals(t, c, func(w *dealsWorld) {
			c.config.set("dealfeed.enabled", true)
			dormant = c.createTenant("dormant")
			for _, ref := range []tenantRef{w.acme, dormant, nobody} {
				c.configureWebhook(receiver, ref, "secret of "+ref.ID)
			}
			c.disableTenant(dormant)
		})
		runs := c.consumerRuns(w.h, 1)

		published := map[eventKey]int{}
		refused := []tenantRef{dormant, nobody}
		for _, ref := range refused {
			view := w.h.publishWon(t, wonEvent(ref, "won-1", "d_1", 5000, w.alice))
			published[eventKey{ref.ID, view.ID}]++
		}
		definePipeline(t, w.h, w.acme, w.asAlice, pipelineOf("acme"))
		published[eventKey{w.acme.ID, *winDeal(t, w.h, w.acme, w.asAlice, "won-1", "EUR", 5000).WonEventID}]++
		awaitConsumers(t, runs, revenueModuleName, revenueOutcomes, published)
		awaitConsumers(t, runs, webhooksModuleName, webhookOutcomes, published)

		for _, ref := range refused {
			key := eventKey{ref.ID, "won-1"}
			if n := outcomeTotal(runs, revenueModuleName, revenueOutcomes, key, wonNotServedMsg); n != len(runs) {
				t.Errorf("%d of %d processes recorded that %s is not served when counting its deal", n, len(runs), ref.ID)
			}
			if n := outcomeTotal(runs, webhooksModuleName, webhookOutcomes, key, webhookNotServedMsg); n != len(runs) {
				t.Errorf("%d of %d processes recorded that %s is not served when delivering its deal", n, len(runs), ref.ID)
			}
			if calls := receiver.callsTo(webhookPath(ref)); len(calls) != 0 {
				t.Errorf("%s's webhook received %d requests, want none", ref.ID, len(calls))
			}
		}
		for _, table := range []string{"webhook_deliveries", "won_revenue"} {
			var rows int
			if err := c.sql().QueryRow(c.rebind("SELECT COUNT(*) FROM "+table+" WHERE tenant_id <> ?"), w.acme.ID).Scan(&rows); err != nil {
				t.Fatalf("counting the rows of %s: %v", table, err)
			}
			if rows != 0 {
				t.Errorf("%s holds %d rows of tenants other than acme, want none", table, rows)
			}
		}
		if calls := receiver.callsTo(webhookPath(w.acme)); len(calls) != 1 {
			t.Errorf("acme's webhook received %d requests, want 1", len(calls))
		}
		if got := revenueLine(t, w.h, w.acme, w.asAlice, "EUR"); got.Deals != 1 || got.Amount != 5000 {
			t.Errorf("acme's won revenue reads %+v, want its one won deal", got)
		}
	})
}

// The claim lifetime the lapse case writes, and how far either side of it the
// lapse may land. The lifetime has to outlast a won move and a publish as the
// operator, which derives the operator's password, and a binary built with the
// race detector derives it several times slower.
var (
	claimLifetime = scaledForRace(3 * time.Second)
	claimSlack    = time.Second
)

// TestAClaimLastsAsLongAsItsSetting is the case for the claims' lifetimes: the
// revenue.claim-ttl and webhooks.claim-ttl settings, read for the event's
// tenant and honoured by the key-value backend the stack names.
//
// acme wins a deal through the deals module, and its event is published again
// through the feed, under the identifier the move recorded. A second delivery
// within the lifetime is recognised by both consumers through their claims;
// one after it has passed is counted again, because the projection's claim is
// all it remembers an event by, and is not delivered again, because a delivery
// also leaves its row, and a row already delivered sends nothing. The two ways
// the webhooks module recognises an event write records of their own, so the
// third delivery shows its claim was taken afresh and the row is what stopped
// it. A claim stored without its lifetime would never lapse: the projection
// would never count the event again, and the webhooks module would find its
// claim a second time.
func TestAClaimLastsAsLongAsItsSetting(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		receiver := startWebhookReceiver(t)
		w := startDeals(t, c, func(w *dealsWorld) {
			c.config.set("dealfeed.enabled", true)
			c.configureWebhook(receiver, w.acme, "acme's secret")
			c.setForTenant(w.acme, "revenue.claim-ttl", claimLifetime.String())
			c.setForTenant(w.acme, "webhooks.claim-ttl", claimLifetime.String())
		})
		acme, h := w.acme, w.h
		definePipeline(t, h, acme, w.asAlice, pipelineOf("acme"))
		runs := []*process{h.process}
		published := map[eventKey]int{}
		settle := func() {
			awaitConsumers(t, runs, revenueModuleName, revenueOutcomes, published)
			awaitConsumers(t, runs, webhooksModuleName, webhookOutcomes, published)
		}

		// The claims are stored after the move starts and before its outcomes
		// are all written, so the one bounds how old they are when the second
		// delivery is handled and the other when they have lapsed.
		firstPublished := time.Now()
		key := eventKey{acme.ID, *winDeal(t, h, acme, w.asAlice, "won-1", "EUR", 2500).WonEventID}
		published[key]++
		settle()
		claimed := time.Now()
		deliverAgain := func() {
			h.publishWon(t, wonEvent(acme, key.id, "won-1", 2500, w.alice))
			published[key]++
			settle()
		}
		deliverAgain()
		if age := time.Since(firstPublished); age >= claimLifetime-claimSlack {
			t.Fatalf("the second delivery was handled %s after the move, too close to the claim lifetime of %s "+
				"to say anything", age.Round(time.Millisecond), claimLifetime)
		}
		if got := revenueLine(t, h, acme, w.asAlice, "EUR"); got.Deals != 1 {
			t.Errorf("a second delivery within the claim lifetime left the won revenue at %+v, want the deal counted once", got)
		}

		time.Sleep(time.Until(claimed.Add(claimLifetime + claimSlack)))
		deliverAgain()
		if got := revenueLine(t, h, acme, w.asAlice, "EUR"); got.Deals != 2 || got.Amount != 5000 {
			t.Errorf("a delivery after the claim lifetime left the won revenue at %+v, want the deal counted again", got)
		}
		if n := outcomeTotal(runs, revenueModuleName, revenueOutcomes, key, wonCountedMsg); n != 2 {
			t.Errorf("the projection counted the event %d times, want twice", n)
		}
		if calls := receiver.callsTo(webhookPath(acme)); len(calls) != 1 {
			t.Errorf("acme's webhook received the event %d times, want once", len(calls))
		}
		if n := outcomeTotal(runs, webhooksModuleName, webhookOutcomes, key, webhookAlreadyClaimedMsg); n != 1 {
			t.Errorf("the webhooks module found its claim on the event %d times, want once: on the second delivery, "+
				"and not on the third, after the claim had lapsed", n)
		}
		if n := outcomeTotal(runs, webhooksModuleName, webhookOutcomes, key, webhookAlreadyDeliveredMsg); n != 1 {
			t.Errorf("the webhooks module found the event's row already delivered %d times, want once: on the "+
				"third delivery, whose claim was its own because the first one had lapsed", n)
		}
	})
}

// deliveryRow reads one tenant's delivery row through the harness's own
// connection: its status, its attempts and the error it last recorded.
func (c *crmCase) deliveryRow(ref tenantRef, eventID string) (status string, attempts int, lastError string) {
	c.t.Helper()
	query := c.rebind("SELECT status, attempts, last_error FROM webhook_deliveries WHERE tenant_id = ? AND event_id = ?")
	if err := c.sql().QueryRow(query, ref.ID, eventID).Scan(&status, &attempts, &lastError); err != nil {
		c.t.Fatalf("reading the delivery of %s in %s: %v", eventID, ref.ID, err)
	}
	return status, attempts, lastError
}

// TestASlowReceiverHoldsUpNoOtherTenant is the case for where a delivery runs:
// beside the bus, not on it. The bus hands each deal.won to its subscribers one
// after another on one goroutine, so a delivery made there would hold every
// later event up — every tenant's, the revenue projection's and the report
// cache's alike — for as long as a receiver takes, and a stop would find it
// still running at the end of the bus's grace.
//
// acme's receiver holds its delivery's first request, and acme's timeout is
// longer than the case waits. globex's won deal, won after acme's, is delivered
// and counted while acme's request is still held, on the serving process alone
// so no other process can take globex's event instead. Stopping the process
// then cuts acme's delivery short: the process leaves cleanly, and the row says
// one attempt was made and the delivery is pending.
func TestASlowReceiverHoldsUpNoOtherTenant(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		receiver := startWebhookReceiver(t)
		var held *heldCall
		w := startDeals(t, c, func(w *dealsWorld) {
			held = receiver.hold(t, webhookPath(w.acme), 1)
			for _, ref := range []tenantRef{w.acme, w.globex} {
				c.configureWebhook(receiver, ref, "secret of "+ref.ID)
			}
			c.setForTenant(w.acme, "webhooks.timeout", "25s")
		})
		for _, side := range sidesOf(w) {
			definePipeline(t, w.h, side.ref, side.cred, pipelineOf(side.name))
		}

		acmeWon := winDeal(t, w.h, w.acme, w.asAlice, "won-1", "EUR", 1000)
		held.awaitArrival(t, "acme's delivery")
		winDeal(t, w.h, w.globex, w.asBob, "won-1", "EUR", 2000)
		done := eventually(eventLimit, func() bool {
			return len(receiver.callsTo(webhookPath(w.globex))) == 1 && revenueLine(t, w.h, w.globex, w.asBob, "EUR").Deals == 1
		})
		switch {
		case !done:
			t.Errorf("globex's won deal was not both delivered and counted within %s while acme's receiver held "+
				"acme's delivery", eventLimit)
		case held.hasEnded():
			t.Errorf("globex's won deal was delivered and counted only after acme's held request had ended")
		}

		w.h.stop(t)
		key := eventKey{w.acme.ID, *acmeWon.WonEventID}
		if n := outcomeTotal([]*process{w.h.process}, webhooksModuleName, webhookOutcomes, key, webhookInterruptedMsg); n != 1 {
			t.Errorf("the stop was recorded as cutting acme's delivery short %d times, want once", n)
		}
		status, attempts, lastError := c.deliveryRow(w.acme, key.id)
		if status != "pending" || attempts != 1 || lastError != errInterrupted.Error() {
			t.Errorf("once the stop cut it short, acme's delivery reads %s after %d attempts with %q, "+
				"want pending after 1 with %q", status, attempts, lastError, errInterrupted.Error())
		}
	})
}

// TestAWonDealOfATenantRegisteredWhileServingIsCounted is the case for a
// tenant registered after the consumers last read the roster. None of them
// rereads it: the case leaves the roster's periodic reread off, as every case
// does that sets no refresh period, so each process holds the roster it read
// when it started. A consumer enters an event's tenant
// through the roster, and the roster reads its table for an identifier its
// snapshot does not hold, so the tenant is entered and its won deal counted
// once, however long the consumers' snapshots stay as they were.
//
// The tenant is registered at the command line, a process of its own, after
// the consumers started, and its won deal is published at once. A request
// cannot win that deal through the deals module, since a request's tenant is
// resolved from the snapshot alone, so the event comes from the feed.
func TestAWonDealOfATenantRegisteredWhileServingIsCounted(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		c.config.set("dealfeed.enabled", true)
		h := c.serve()
		runs := c.consumerRuns(h, 1)
		owner := userRef{ID: "u_late_owner"}
		ref := c.createTenant("late")
		key := eventKey{ref.ID, h.publishWon(t, wonEvent(ref, "", "d_1", 1000, owner)).ID}
		// Every run receives the event and records an outcome for it, so the
		// count is final once each has; a count taken at its first sight of
		// one would miss a second process counting it too.
		awaitConsumers(t, runs, revenueModuleName, revenueOutcomes, map[eventKey]int{key: 1})
		if outcomeTotal(runs, revenueModuleName, revenueOutcomes, key, wonCountedMsg) == 1 {
			return
		}
		var report strings.Builder
		for _, run := range runs {
			report.WriteString("\n" + run.name + ": " + formatCounts(outcomesIn(run.output(), revenueModuleName, revenueOutcomes)[key]))
			if rec, ok := findRecord(run.output(), wonNotServedMsg, "tenant_id", ref.ID); ok {
				report.WriteString(" cause " + strconv.Quote(rec["cause"]))
			}
		}
		t.Fatalf("%s was registered while the consumers ran, and once every process recorded its outcome its won "+
			"deal was counted %d times, want once, though a consumer's entry reads the roster's table for a tenant "+
			"its snapshot lacks; they recorded:%s",
			ref.ID, outcomeTotal(runs, revenueModuleName, revenueOutcomes, key, wonCountedMsg), report.String())
	})
}

// TestAWebhookSettingOutsideItsBoundSendsNothing is the case for the bounds a
// tenant's delivery settings are held to. Each tenant but the last writes one
// setting just past its bound, and gets nothing sent and a record naming the
// setting; the last writes all three at their bounds and is delivered to.
func TestAWebhookSettingOutsideItsBoundSendsNothing(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	receiver := startWebhookReceiver(t)
	olga := c.createUser("olga")
	past := []struct {
		name, key, value string
	}{
		{"slow", "webhooks.timeout", "31s"},
		{"eager", "webhooks.max-attempts", "11"},
		{"patient", "webhooks.retry-delay", "1m1s"},
	}
	refused := map[string]tenantRef{}
	open := func(name string) tenantRef {
		ref := c.createTenant(name)
		c.grant(ref, olga, roleOwner)
		c.configureWebhook(receiver, ref, "secret of "+ref.ID)
		return ref
	}
	for _, p := range past {
		ref := open(p.name)
		c.setForTenant(ref, p.key, p.value)
		refused[p.key] = ref
	}
	bounded := open("bounded")
	c.setForTenant(bounded, "webhooks.timeout", "30s")
	c.setForTenant(bounded, "webhooks.max-attempts", "10")
	c.setForTenant(bounded, "webhooks.retry-delay", "1m0s")
	h := c.serve()
	cred := bearer(h.login(t, olga))
	runs := []*process{h.process}

	published := map[eventKey]int{}
	wonIDs := map[string]string{}
	for _, ref := range append(slices.Collect(maps.Values(refused)), bounded) {
		definePipeline(t, h, ref, cred, pipelineOf(ref.Alias))
		openAccount(t, h, ref, cred, "acc-1")
		wonIDs[ref.ID] = *winDeal(t, h, ref, cred, "won-1", "EUR", 1000).WonEventID
		published[eventKey{ref.ID, wonIDs[ref.ID]}]++
	}
	awaitConsumers(t, runs, webhooksModuleName, webhookOutcomes, published)
	for setting, ref := range refused {
		if calls := receiver.callsTo(webhookPath(ref)); len(calls) != 0 {
			t.Errorf("%s, whose %s is past its bound, had %d requests sent", ref.ID, setting, len(calls))
		}
		rec, ok := findRecord(h.output(), webhookUnusableMsg, "tenant_id", ref.ID)
		if !ok || !strings.Contains(rec["error"], setting) {
			t.Errorf("%s, whose %s is past its bound, has no record naming the setting; found %v", ref.ID, setting, rec)
		}
	}
	key := eventKey{bounded.ID, wonIDs[bounded.ID]}
	if n := outcomeTotal(runs, webhooksModuleName, webhookOutcomes, key, webhookDeliveredMsg); n != 1 {
		t.Errorf("%s, whose settings are at their bounds, was delivered to %d times, want once", bounded.ID, n)
	}
}

// TestTheDealFeedIsOffUnlessTurnedOn is the case for the deal feed's switch. The
// feed publishes a deal.won for any tenant under any identifier, which reaches
// the revenue projection and the webhooks, so it is served only where a
// deployment turns it on, and there to an operator alone.
//
// Served with the switch left alone, the operator's publish finds no route and
// nothing is counted. Served again with it on, a CRM user is refused at the
// admin endpoint's operator check and nothing is counted, and the operator's
// publish is counted: the route is the switch's, and the refusal the check's.
func TestTheDealFeedIsOffUnlessTurnedOn(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	w := startDeals(t, c, nil)
	fed := wonEvent(w.acme, "", "fed-1", 500, w.alice)
	expect(t, "the operator publishing through a feed left off",
		w.h.post(t, adminEndpoint, "/deal-events", "", operator(), fed), nethttp.StatusNotFound)
	if got, _ := revenueOf(t, w.h, w.acme, signedIn(w.alice)); len(got.Revenue) != 0 {
		t.Errorf("acme's won revenue reads %+v after a publish through a feed left off, want nothing", got.Revenue)
	}
	w.h.stop(t)

	c.config.set("dealfeed.enabled", true)
	h := c.serve(dealsServeEnv...)
	for _, cred := range []credential{signedIn(w.alice), bearer(h.login(t, w.alice))} {
		expect(t, "acme's owner publishing through the feed",
			h.post(t, adminEndpoint, "/deal-events", "", cred, fed), nethttp.StatusForbidden)
	}
	if got, _ := revenueOf(t, h, w.acme, signedIn(w.alice)); len(got.Revenue) != 0 {
		t.Errorf("acme's won revenue reads %+v after a CRM user's publish was refused, want nothing", got.Revenue)
	}
	h.publishWon(t, fed)
	awaitRevenue(t, h, w.acme, signedIn(w.alice), revenueView{Currency: "EUR", Deals: 1, Amount: 500})
}

// awaitCalls waits until one path of the receiver has taken n requests, and
// returns them.
func (r *webhookReceiver) awaitCalls(t *testing.T, path string, n int) []webhookCall {
	t.Helper()
	var calls []webhookCall
	if !eventually(eventLimit, func() bool {
		calls = r.callsTo(path)
		return len(calls) >= n
	}) {
		t.Fatalf("%s took %d requests within %s, want %d", path, len(calls), eventLimit, n)
	}
	return calls
}

// TestATenantConfiguresItsWebhookThroughTheSettings is the case for a tenant's
// webhook set up from the API, on every stack: the owner writes the URL and
// the signing secret into the tenant's own layer, a won deal is posted there
// signed with that secret, and after the owner rotates the secret the next one
// is signed with the new secret and not the old.
//
// The secret is declared sensitive. The settings module keeps a sensitive value
// out of its change events and stores it as text, handing it back to any read;
// what the host serves and records is the host's to keep it out of. So neither
// secret is in any answer the settings routes give, the tenant's own or the
// operator's, in any row the journal holds — the change events it captured from
// the bus, the seam rows, the request rows — or in any log record the process
// wrote, and the settings table holds the one that is set.
func TestATenantConfiguresItsWebhookThroughTheSettings(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		receiver := startWebhookReceiver(t)
		w := startDealsWith(t, c, 1, []string{"CRM_AUDIT__JOURNAL__EVENTS=deal.won,settings.changed"}, nil)
		acme, alice := w.acme, w.asAlice
		first, second := "acme's first secret "+c.suffix, "acme's second secret "+c.suffix
		path := webhookPath(acme)
		answers := []response{
			writeTenantSettingAs(t, w.h, acme.Alias, alice, "webhooks.url", receiver.url(path)),
			writeTenantSettingAs(t, w.h, acme.Alias, alice, "webhooks.secret", first),
		}
		for _, resp := range answers {
			expect(t, "acme's owner configuring its webhook", resp, nethttp.StatusOK)
		}
		var written settingView
		answers[1].decode(t, &written)
		if !written.Sensitive || writtenText(written) != "********" || written.Value != "********" {
			t.Errorf("the write of the secret answered %+v, want it marked sensitive and hidden", written)
		}
		if got, ok := c.settingRow(acme.ID, "webhooks.secret"); !ok || got != first {
			t.Errorf("acme's layer holds the secret as %q (present %v), want the text written", got, ok)
		}
		hidden := settingIn(t, settingsListing(t, w.h, apiEndpoint, "/settings", acme.Alias, alice), "webhooks.secret")
		requireSetting(t, "acme's listing", hidden, "********", "********")
		if !hidden.Sensitive {
			t.Errorf("acme's listing shows the secret as %+v, want it marked sensitive", hidden)
		}
		requireSetting(t, "acme's listing", settingIn(t,
			settingsListing(t, w.h, apiEndpoint, "/settings", acme.Alias, alice), "webhooks.url"),
			receiver.url(path), receiver.url(path))
		requireSetting(t, "the operator's listing of acme's layer", settingIn(t,
			settingsListing(t, w.h, adminEndpoint, "/tenants/"+acme.ID+"/settings", "", operator()), "webhooks.secret"),
			"********", "********")
		requireSetting(t, "globex's listing", settingIn(t,
			settingsListing(t, w.h, apiEndpoint, "/settings", w.globex.Alias, w.asBob), "webhooks.secret"),
			"<none>", "")

		// The claim's lifetime, and the other consumers' and the report
		// cache's, are the platform's: the operator writes them in acme's
		// layer, and acme's owner is not found them.
		for _, key := range []string{"webhooks.claim-ttl", "revenue.claim-ttl", "reports.cache-ttl"} {
			expect(t, "acme's owner writing "+key, writeTenantSettingAs(t, w.h, acme.Alias, alice, key, "36h"),
				nethttp.StatusNotFound)
			expect(t, "the operator writing "+key+" in acme's layer",
				settingsMutation(t, w.h, adminEndpoint, "/tenants/"+acme.ID+"/settings/"+key, "", operator(), "36h", false),
				nethttp.StatusOK)
			if got, ok := c.settingRow(acme.ID, key); !ok || got != "36h0m0s" {
				t.Errorf("acme's layer holds %s as %q (present %v), want what the operator wrote", key, got, ok)
			}
		}

		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		winDeal(t, w.h, acme, alice, "hooked-1", "EUR", 100)
		if call := receiver.awaitCalls(t, path, 1)[0]; !call.signedWith(first) {
			t.Errorf("the delivery of hooked-1 carries the signature %q, which is not its body's under the secret written",
				call.header.Get("X-CRM-Signature"))
		}

		expect(t, "acme's owner rotating the secret",
			writeTenantSettingAs(t, w.h, acme.Alias, alice, "webhooks.secret", second), nethttp.StatusOK)
		winDeal(t, w.h, acme, alice, "hooked-2", "EUR", 200)
		call := receiver.awaitCalls(t, path, 2)[1]
		if !call.signedWith(second) || call.signedWith(first) {
			t.Errorf("the delivery of hooked-2 carries the signature %q, want its body's under the rotated secret alone",
				call.header.Get("X-CRM-Signature"))
		}

		// The change events of both writes reached the journal from the bus,
		// each naming the key and carrying no value.
		changes := 0
		if !eventually(settleLimit, func() bool {
			changes = c.count("SELECT count(*) FROM audit_entries WHERE source = 'event' AND action = 'settings.changed' "+
				"AND detail LIKE ?", `%"Key":"webhooks.secret"%`)
			return changes >= 2
		}) {
			t.Errorf("the journal holds %d change events of acme's secret, want one for each of the two writes", changes)
		}
		rows, err := c.sql().Query("SELECT source, action, object, COALESCE(detail, '') FROM audit_entries")
		if err != nil {
			t.Fatalf("reading the journal: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var source, action, object, detail string
			if err := rows.Scan(&source, &action, &object, &detail); err != nil {
				t.Fatalf("scanning a journal row: %v", err)
			}
			for _, secret := range []string{first, second} {
				if strings.Contains(detail, secret) || strings.Contains(object, secret) {
					t.Errorf("the journal's %s row %s %s carries a secret: %s", source, action, object, detail)
				}
			}
			if source == "event" && action == "settings.changed" && strings.Contains(detail, `"Key":"webhooks.secret"`) {
				var change map[string]string
				if err := json.Unmarshal([]byte(detail), &change); err != nil {
					t.Fatalf("decoding the change event's row %q: %v", detail, err)
				}
				if change["Sensitive"] != "true" || change["Old"] != "" || change["New"] != "" {
					t.Errorf("the journal's change event of the secret reads %v, want it marked sensitive with no value", change)
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("reading the journal: %v", err)
		}
		// Every record the serving process wrote is read, the database
		// module's statement records among them: its logger writes no value a
		// statement binds (design-db.md, 日志).
		for _, rec := range records(w.h.output()) {
			for key, value := range rec {
				for _, secret := range []string{first, second} {
					if strings.Contains(value, secret) {
						t.Errorf("the serving process wrote a secret under %s in the record %q", key, rec["msg"])
					}
				}
			}
		}
	})
}
