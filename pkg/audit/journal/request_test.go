package journal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	nethttp "net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// cancelled is a context whose caller has gone: net/http cancels a request's
// context when the client closes its connection, and a publisher's context ends
// the same way when the request that published has.
func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// requestEndpoint is the endpoint the request-level cases register on and
// configure the journal for.
const requestEndpoint = "public"

// requestWait bounds every wait in the request-level cases. It is generous on
// purpose: what it guards against is a wait that never ends, not a slow machine.
const requestWait = 10 * time.Second

// served is what a request-level case is handed while the real entry point is
// listening: where it listens, the delivered database the rows are read from,
// and a client that opens a connection of its own for every request.
type served struct {
	t      *testing.T
	addr   string
	handle *gorm.DB
	client *nethttp.Client
}

// reply is what a client got back from a request.
type reply struct {
	status int
	header nethttp.Header
	body   string
}

// servedRequests assembles the real entry point beside the journal and runs
// action once the endpoint is listening.
//
// The journal is configured to record requests on the endpoint "public", and
// those are the request items the case gives. The routes callback runs in the
// Init stage of a module that depends on the entry point, which is when its
// registration gate is open, so a case registers its routes and its own layers
// the way a module does. What a case observes — the route a request was
// dispatched to, the status a client received, the connection a handler gave
// up — is the engine's own, and nothing here stands in for the engine.
func servedRequests(t *testing.T, requests []string, routes func(speedhttp.Endpoint),
	action func(ctx context.Context, s *served) error,
) {
	t.Helper()
	run := assembly{
		database: sqlite.Module(),
		engine:   "sqlite",
		dsn:      filepath.Join(t.TempDir(), "delivered.db"),
		extra: []core.Module{speedhttp.Module(), {
			Name:     "routes",
			Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
			Init: func(_ context.Context, reg *core.Registry, _ any) error {
				router, err := core.Resolve[speedhttp.Router](reg)
				if err != nil {
					return err
				}
				endpoint, err := router.Endpoint(requestEndpoint)
				if err != nil {
					return err
				}
				routes(endpoint)
				return nil
			},
		}},
		sections: map[string]any{
			"http": map[string]any{"endpoints": map[string]any{
				requestEndpoint: map[string]any{"address": "127.0.0.1:0", "drain-timeout": "5s"},
			}},
			"audit": map[string]any{"journal": map[string]any{
				"endpoints": []string{requestEndpoint},
				"requests":  requests,
			}},
		},
		dir:   t.TempDir(),
		depth: core.StageServe,
	}
	err := run.drive(t, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		addr, found := listeningAddress(t, run.logFile(), requestEndpoint)
		if !found {
			t.Errorf("the entry point did not report the address the endpoint %q is listening on", requestEndpoint)
			return errCaseFailed
		}
		return action(ctx, &served{
			t:      t,
			addr:   addr,
			handle: database.DB(),
			client: &nethttp.Client{
				Timeout:   requestWait,
				Transport: &nethttp.Transport{DisableKeepAlives: true},
			},
		})
	})
	if err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
}

// listeningAddress reads the address the entry point bound for an endpoint out
// of the record it writes once it is listening. The endpoint is configured on
// port 0, so the record is the one place that says which port the operating
// system gave it.
func listeningAddress(t *testing.T, logFile, endpoint string) (string, bool) {
	t.Helper()
	contents, err := os.ReadFile(logFile)
	if err != nil {
		t.Errorf("reading the log file: %v", err)
		return "", false
	}
	for _, line := range bytes.Split(contents, []byte("\n")) {
		var record map[string]any
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		if record["msg"] == "an HTTP endpoint is listening" && record["endpoint"] == endpoint {
			address, ok := record["address"].(string)
			return address, ok
		}
	}
	return "", false
}

// ask sends one request and reads the whole response.
func (s *served) ask(ctx context.Context, method, path string) (reply, error) {
	request, err := nethttp.NewRequestWithContext(ctx, method, "http://"+s.addr+path, nil)
	if err != nil {
		return reply{}, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	return reply{status: response.StatusCode, header: response.Header, body: string(body)}, err
}

// expect sends one request and reports it as the case's failure when it did not
// get the status it was asked for. The row a request leaves is written before
// its response is complete, so a case reads the rows after its requests have
// been answered.
func (s *served) expect(ctx context.Context, method, path string, status int) reply {
	s.t.Helper()
	got, err := s.ask(ctx, method, path)
	if err != nil {
		s.t.Errorf("%s %s: %v", method, path, err)
		return reply{}
	}
	if got.status != status {
		s.t.Errorf("%s %s answered %d, want %d", method, path, got.status, status)
	}
	return got
}

// rawStatus sends a request written out in full on a connection of its own and
// reads the status line of the answer, for the cases a client of net/http would
// not read as the answer: a connection a handler takes over, and a switch of
// protocols.
func (s *served) rawStatus(ctx context.Context, request string) (int, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	if deadlineErr := conn.SetDeadline(time.Now().Add(requestWait)); deadlineErr != nil {
		return 0, deadlineErr
	}
	if _, writeErr := conn.Write([]byte(request)); writeErr != nil {
		return 0, writeErr
	}
	response, err := nethttp.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	_ = response.Body.Close()
	return response.StatusCode, nil
}

// rows reads the rows the request path has written.
func (s *served) rows() []row {
	s.t.Helper()
	var rows []row
	if err := s.handle.Where("source = ?", string(audit.SourceRequest)).Order("id").Find(&rows).Error; err != nil {
		s.t.Errorf("reading the audit table: %v", err)
	}
	return rows
}

// awaitRows reads the rows once at least n of them are there. A case that holds
// a count to be exact compares it afterwards: the wait is for the rows that are
// expected, and a row that should not be there is one that is already.
func (s *served) awaitRows(n int) []row {
	s.t.Helper()
	deadline := time.Now().Add(requestWait)
	for {
		rows := s.rows()
		if len(rows) >= n || time.Now().After(deadline) {
			return rows
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// decodedDetail decodes the detail column of a row.
func decodedDetail(t *testing.T, r row) map[string]string {
	t.Helper()
	var detail map[string]string
	if err := json.Unmarshal([]byte(r.Detail), &detail); err != nil {
		t.Errorf("the detail %q is not a JSON object of strings: %v", r.Detail, err)
	}
	return detail
}

// describe renders rows as method, route path and outcome, which is what a case
// comparing the rows of several requests reads.
func describe(rows []row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Action+" "+r.Object+" "+r.Outcome)
	}
	return out
}

// A request whose method and route path equal a request item is recorded, with
// the method as its action, the route path as its object and the path the client
// asked for in the detail.
//
// The two paths are different things and the case keeps them apart: the route
// path is the one the route was registered under, "/contacts/:id", and the
// request path is the one that was asked, "/contacts/c1". An item is written in
// the first form, so a layer comparing the second would never see one match.
// The DELETE on the same route is served and not recorded, which is what the
// method is compared for.
func TestARequestIsRecordedWhenItsMethodAndRoutePathEqualARequestItem(t *testing.T) {
	servedRequests(t, []string{"GET /contacts/:id"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/contacts/:id", func(c *gin.Context) { c.String(nethttp.StatusOK, "a contact") })
		endpoint.Route("DELETE", "/contacts/:id", func(c *gin.Context) { c.Status(nethttp.StatusNoContent) })
	}, func(ctx context.Context, s *served) error {
		s.expect(ctx, "GET", "/contacts/c1", nethttp.StatusOK)
		s.expect(ctx, "DELETE", "/contacts/c1", nethttp.StatusNoContent)
		s.expect(ctx, "GET", "/contacts/c2", nethttp.StatusOK)

		rows := s.awaitRows(2)
		if len(rows) != 2 {
			t.Errorf("the two GETs were recorded and the DELETE was not, but the rows are %v", describe(rows))
			return nil
		}
		first := rows[0]
		if first.Source != string(audit.SourceRequest) || first.Action != "GET" ||
			first.Object != "/contacts/:id" || first.Outcome != "200" {
			t.Errorf("the first row is %+v, want a request row for GET with the object %q and the outcome 200",
				first, "/contacts/:id")
		}
		if detail := decodedDetail(t, first); detail["endpoint"] != requestEndpoint || detail["path"] != "/contacts/c1" {
			t.Errorf("the detail is %v, want the endpoint %q and the request path %q",
				detail, requestEndpoint, "/contacts/c1")
		}
		if detail := decodedDetail(t, rows[1]); detail["path"] != "/contacts/c2" {
			t.Errorf("the second row's request path is %q, want %q", detail["path"], "/contacts/c2")
		}
		return nil
	})
}

// A request item that differs from the registered spelling by one character
// never matches, and the startup does not fail on it.
//
// Nothing lists the routes an endpoint has, so there is nothing to check an item
// against: the host finds out by the rows that do not appear. The items here are
// off by a letter's case in the parameter name, by the parameter name, by a
// trailing slash and by the method's case, and one names a route that is not
// registered at all. The request to the real route is served and not recorded,
// which a layer that compared more loosely than character for character would
// get wrong; the control request is recorded, and it is the only row.
func TestARequestItemOffByACharacterNeverMatchesAndFailsNothing(t *testing.T) {
	servedRequests(t, []string{
		"GET /contacts/:ID",
		"GET /contacts/:name",
		"GET /contacts/:id/",
		"get /contacts/:id",
		"GET /nowhere/:id",
		"GET /ping",
	}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/contacts/:id", func(c *gin.Context) { c.String(nethttp.StatusOK, "a contact") })
		endpoint.Route("GET", "/ping", func(c *gin.Context) { c.String(nethttp.StatusOK, "pong") })
	}, func(ctx context.Context, s *served) error {
		s.expect(ctx, "GET", "/contacts/c1", nethttp.StatusOK)
		s.expect(ctx, "GET", "/ping", nethttp.StatusOK)

		rows := s.awaitRows(1)
		if got := describe(rows); len(got) != 1 || got[0] != "GET /ping 200" {
			t.Errorf("the rows are %v, want the control request's alone", got)
		}
		return nil
	})
}

// A request the engine matched no route for leaves no row, however it is
// spelled in the items.
//
// A path nothing is registered on is a 404 and a method the path does not allow
// is a 405, and both run through the layers before the engine answers them. The
// route path they carry is empty, so they equal no item: the items here name the
// very requests that were sent, which is what a layer comparing the request path
// instead of the route path would record. A HEAD request to a route registered
// for GET is among them, because the engine does not hand it to the GET route.
// The requests still reach the engine's answer, and the control at the end is
// recorded.
func TestARequestWithNoMatchedRouteLeavesNoRow(t *testing.T) {
	servedRequests(t, []string{
		"GET /missing",
		"POST /contacts",
		"HEAD /contacts",
		"GET /contacts",
	}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/contacts", func(c *gin.Context) { c.String(nethttp.StatusOK, "contacts") })
	}, func(ctx context.Context, s *served) error {
		for _, miss := range []struct {
			method, path string
			status       int
		}{
			{"GET", "/missing", nethttp.StatusNotFound},
			{"POST", "/contacts", nethttp.StatusMethodNotAllowed},
			{"HEAD", "/contacts", nethttp.StatusMethodNotAllowed},
		} {
			got := s.expect(ctx, miss.method, miss.path, miss.status)
			if got.status == miss.status && miss.method != "HEAD" &&
				!strings.HasPrefix(got.header.Get("Content-Type"), "application/problem+json") {
				t.Errorf("%s %s was answered with the media type %q, want the engine's problem details",
					miss.method, miss.path, got.header.Get("Content-Type"))
			}
		}
		s.expect(ctx, "GET", "/contacts", nethttp.StatusOK)

		rows := s.awaitRows(1)
		if got := describe(rows); len(got) != 1 || got[0] != "GET /contacts 200" {
			t.Errorf("the rows are %v, want the control request's alone", got)
		}
		return nil
	})
}

// The outcome is the status the chain ended with, read after the inner chain has
// returned.
//
// The requests end in different ways: a handler that sets a status, one that
// writes nothing at all, an inner layer that refuses the request, a handler that
// sets an informational status before the final one, one that sets only an
// informational status and then writes a body, and one that sets a status after
// its response has begun to go out. In every case the outcome is the status the
// client received. A row written on the way in could hold a constant at best,
// and one that read the status before the chain ran would hold 200 for all of
// them.
//
// The informational status alone is the one the writer still reports after the
// client has been answered with something else: net/http sends 103 ahead of the
// response and supplies the 200 that follows, and a row holding 103 would name a
// status no caller was answered with. 101 Switching Protocols is final, and the
// last request here is answered with it.
func TestTheOutcomeIsTheStatusTheChainEndedWith(t *testing.T) {
	servedRequests(t, []string{
		"GET /created", "GET /silent", "GET /refused", "GET /informational", "GET /hinted", "GET /late",
		"GET /switching",
	}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/created", func(c *gin.Context) { c.String(nethttp.StatusCreated, "made") })
		endpoint.Route("GET", "/silent", func(*gin.Context) {})
		endpoint.Route("GET", "/refused", func(c *gin.Context) { c.String(nethttp.StatusOK, "never reached") })
		endpoint.Route("GET", "/informational", func(c *gin.Context) {
			c.Status(nethttp.StatusEarlyHints)
			c.String(nethttp.StatusCreated, "made")
		})
		endpoint.Route("GET", "/hinted", func(c *gin.Context) {
			c.Status(nethttp.StatusEarlyHints)
			_, _ = c.Writer.WriteString("made")
		})
		endpoint.Route("GET", "/late", func(c *gin.Context) {
			c.String(nethttp.StatusOK, "answered")
			c.Status(nethttp.StatusInternalServerError)
		})
		endpoint.Route("GET", "/switching", func(c *gin.Context) {
			c.Header("Connection", "Upgrade")
			c.Header("Upgrade", "test")
			c.Status(nethttp.StatusSwitchingProtocols)
		})
		endpoint.Use(speedhttp.Middleware{
			Name:  "refuse",
			Stage: speedhttp.StageRecord,
			After: []core.Token{(*audit.Audit)(nil)},
			Handler: func(c *gin.Context) {
				if c.Request.URL.Path == "/refused" {
					c.Writer.Header().Set("Retry-After", "1")
					speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusTooManyRequests})
					return
				}
				c.Next()
			},
		})
	}, func(ctx context.Context, s *served) error {
		for _, call := range []struct {
			path   string
			status int
		}{
			{"/created", nethttp.StatusCreated},
			{"/silent", nethttp.StatusOK},
			{"/refused", nethttp.StatusTooManyRequests},
			{"/informational", nethttp.StatusCreated},
			{"/hinted", nethttp.StatusOK},
			{"/late", nethttp.StatusOK},
		} {
			s.expect(ctx, "GET", call.path, call.status)
		}
		if status, err := s.rawStatus(ctx, "GET /switching HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil ||
			status != nethttp.StatusSwitchingProtocols {
			t.Errorf("GET /switching answered %d (%v), want 101", status, err)
		}

		want := []string{
			"GET /created 201",
			"GET /silent 200",
			"GET /refused 429",
			"GET /informational 201",
			"GET /hinted 200",
			"GET /late 200",
			"GET /switching 101",
		}
		rows := s.awaitRows(len(want))
		got := describe(rows)
		if len(got) != len(want) {
			t.Errorf("the rows are %v, want %v", got, want)
			return nil
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("row %d is %q, want %q", i, got[i], want[i])
			}
		}
		return nil
	})
}

// The row is written after the handler returns, and not before it.
//
// The handler sends the first part of a streamed response and holds the rest
// back until the client has read it: while it is held, the request is still
// being served and nothing is recorded. The layer puts nothing in the writer's
// place, so the flush reaches the client straight away, and once the handler has
// finished the row is there with the status it answered with.
func TestTheRowIsWrittenOnlyOnceTheHandlerHasReturned(t *testing.T) {
	release := make(chan struct{})
	servedRequests(t, []string{"GET /stream"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/stream", func(c *gin.Context) {
			_, _ = c.Writer.WriteString("first\n")
			c.Writer.Flush()
			select {
			case <-release:
			case <-time.After(requestWait):
			}
			_, _ = c.Writer.WriteString("second\n")
		})
	}, func(ctx context.Context, s *served) error {
		request, err := nethttp.NewRequestWithContext(ctx, "GET", "http://"+s.addr+"/stream", nil)
		if err != nil {
			t.Errorf("building the request: %v", err)
			return errCaseFailed
		}
		response, err := s.client.Do(request)
		if err != nil {
			t.Errorf("the streamed response did not begin while the handler was still running: %v", err)
			close(release)
			return errCaseFailed
		}
		defer func() { _ = response.Body.Close() }()
		stream := bufio.NewReader(response.Body)
		if line, err := stream.ReadString('\n'); err != nil || line != "first\n" {
			t.Errorf("the first part of the stream is %q (%v), want it flushed to the client", line, err)
		}
		if rows := s.rows(); len(rows) != 0 {
			t.Errorf("the handler is still running, but %v are recorded", describe(rows))
		}

		close(release)
		if rest, err := io.ReadAll(stream); err != nil || string(rest) != "second\n" {
			t.Errorf("the rest of the stream is %q (%v), want %q", rest, err, "second\n")
		}
		rows := s.awaitRows(1)
		if got := describe(rows); len(got) != 1 || got[0] != "GET /stream 200" {
			t.Errorf("the rows are %v, want the one request recorded after it was served", got)
		}
		return nil
	})
}

// A request whose client went away before the handler returned is still
// recorded.
//
// The work the handler did has happened by then — a delete committed, an export
// was read — and the row is the only trace of it this path leaves. Writing it
// under the request's own context loses it to the cancellation, and the failure
// signal then reports a healthy table as failing. The handler here waits for the
// server to cancel the request's context, which it does when the client closes
// the connection.
func TestARequestWhoseClientHasGoneIsStillRecorded(t *testing.T) {
	entered := make(chan struct{})
	servedRequests(t, []string{"DELETE /things/:id"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("DELETE", "/things/:id", func(c *gin.Context) {
			close(entered)
			select {
			case <-c.Request.Context().Done():
			case <-time.After(requestWait):
			}
			c.Status(nethttp.StatusNoContent)
		})
	}, func(ctx context.Context, s *served) error {
		hangUp, goAway := context.WithCancel(ctx)
		defer goAway()
		ended := make(chan error, 1)
		go func() {
			_, err := s.ask(hangUp, "DELETE", "/things/t1")
			ended <- err
		}()
		select {
		case <-entered:
		case <-time.After(requestWait):
			t.Error("the request never reached its handler")
			return errCaseFailed
		}
		goAway()
		if err := <-ended; !errors.Is(err, context.Canceled) {
			t.Errorf("the client's request ended with %v, want its own cancellation", err)
		}

		rows := s.awaitRows(1)
		if got := describe(rows); len(got) != 1 || got[0] != "DELETE /things/:id 204" {
			t.Errorf("the rows are %v, want the request recorded after its client went away", got)
		}
		return nil
	})
}

// A request the configuration does not name is passed through untouched, and
// records nothing, whether its handler returns or panics.
//
// The contrast with the panic case below is the point: a panic is recorded
// because the request matched, not because it panicked. A layer that recorded
// every panic it saw would be a different thing, and one that decided after the
// fact could not know which requests the host asked for. The panic still ends in
// the entry point's answer, because the layer neither swallows it nor adds to it.
func TestARequestTheConfigurationDoesNotNameIsPassedThroughUnrecorded(t *testing.T) {
	servedRequests(t, []string{"GET /named"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/named", func(c *gin.Context) { c.String(nethttp.StatusOK, "named") })
		endpoint.Route("GET", "/other", func(c *gin.Context) { c.String(nethttp.StatusAccepted, "other") })
		endpoint.Route("GET", "/boom", func(*gin.Context) { panic("the handler fell over") })
	}, func(ctx context.Context, s *served) error {
		if got := s.expect(ctx, "GET", "/other", nethttp.StatusAccepted); got.body != "other" {
			t.Errorf("the handler's answer is %q, want it passed through", got.body)
		}
		s.expect(ctx, "GET", "/boom", nethttp.StatusInternalServerError)
		s.expect(ctx, "GET", "/named", nethttp.StatusOK)

		rows := s.awaitRows(1)
		if got := describe(rows); len(got) != 1 || got[0] != "GET /named 200" {
			t.Errorf("the rows are %v, want the named request's alone", got)
		}
		return nil
	})
}

// A handler that panics is recorded as a panic, and the panic goes on to the
// entry point's fallback.
//
// The client gets the fallback's answer, a 500 in problem details whose body
// leaves the panic's value out; the row says "panic" where a status code would
// be a guess, because no status ever arrived. A layer that swallowed the panic
// would leave the client an empty 200, and one that wrote the status the engine
// held at that moment would record that instead of the panic.
func TestAPanicIsRecordedAndReachesTheEntryPointFallback(t *testing.T) {
	servedRequests(t, []string{"GET /boom"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/boom", func(*gin.Context) { panic(errors.New("the handler fell over")) })
	}, func(ctx context.Context, s *served) error {
		got := s.expect(ctx, "GET", "/boom", nethttp.StatusInternalServerError)
		if media := got.header.Get("Content-Type"); !strings.HasPrefix(media, "application/problem+json") {
			t.Errorf("the answer's media type is %q, want the fallback's problem details", media)
		}
		var problem struct {
			Status int `json:"status"`
		}
		if err := json.Unmarshal([]byte(got.body), &problem); err != nil || problem.Status != nethttp.StatusInternalServerError {
			t.Errorf("the answer's body is %q (%v), want a problem details object with the status 500", got.body, err)
		}
		if strings.Contains(got.body, "fell over") {
			t.Errorf("the panic's value reached the client: %s", got.body)
		}

		rows := s.awaitRows(1)
		if got := describe(rows); len(got) != 1 || got[0] != "GET /boom panic" {
			t.Errorf("the rows are %v, want the panicking request recorded with the outcome %q", got, "panic")
		}
		return nil
	})
}

// A handler that abandons its connection is recorded as a panic too, and the
// client sees the connection dropped.
//
// The layer cannot tell net/http's sentinel from a defect, so the row says
// "panic" for a request whose caller was answered with nothing; the entry point's
// fallback recognises the sentinel and writes no response. A layer that
// recovered the value and let the request return would leave the client a 200.
func TestAnAbandonedConnectionIsRecordedAsAPanic(t *testing.T) {
	servedRequests(t, []string{"GET /abandon"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/abandon", func(*gin.Context) { panic(nethttp.ErrAbortHandler) })
	}, func(ctx context.Context, s *served) error {
		if got, err := s.ask(ctx, "GET", "/abandon"); err == nil {
			t.Errorf("the client was answered with %d, want the connection abandoned", got.status)
		}

		rows := s.awaitRows(1)
		if got := describe(rows); len(got) != 1 || got[0] != "GET /abandon panic" {
			t.Errorf("the rows are %v, want the abandoned request recorded with the outcome %q", got, "panic")
		}
		return nil
	})
}

// The result column of a panicking request holds the word "panic".
//
// The word is part of what a reader of the table matches on, so the case spells
// it out rather than reading it from the constant the layer writes: a constant
// compared with itself would stay green if someone changed its value.
func TestAPanicRowHoldsTheLiteralOutcomePanic(t *testing.T) {
	servedRequests(t, []string{"GET /boom"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/boom", func(*gin.Context) { panic("the handler fell over") })
	}, func(ctx context.Context, s *served) error {
		s.expect(ctx, "GET", "/boom", nethttp.StatusInternalServerError)
		rows := s.awaitRows(1)
		if len(rows) != 1 || rows[0].Outcome != "panic" {
			t.Errorf("the rows are %v, want one with the outcome panic", describe(rows))
		}
		return nil
	})
}

// The row is built from the request the layer received, and not from the one a
// layer inside handed the chain in its place.
//
// The layer inside replaces the request with a clone whose path is rewritten. The
// identity and the tenant the row carries come from the context of the request
// the layers outside settled, and the path is the one the client asked for.
func TestTheRowIsBuiltFromTheRequestTheLayerReceived(t *testing.T) {
	servedRequests(t, []string{"GET /things/:id"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Use(speedhttp.Middleware{
			Name:  "rewrite",
			Stage: speedhttp.StageRecord,
			After: []core.Token{(*audit.Audit)(nil)},
			Handler: func(c *gin.Context) {
				inner := c.Request.Clone(c.Request.Context())
				inner.URL.Path = "/rewritten"
				c.Request = inner
			},
		})
		endpoint.Route("GET", "/things/:id", func(c *gin.Context) { c.Status(nethttp.StatusNoContent) })
	}, func(ctx context.Context, s *served) error {
		s.expect(ctx, "GET", "/things/7", nethttp.StatusNoContent)
		rows := s.awaitRows(1)
		if len(rows) != 1 {
			t.Errorf("the rows are %v, want the one request", describe(rows))
			return nil
		}
		if got := decodedDetail(t, rows[0])["path"]; got != "/things/7" {
			t.Errorf("the row records the path %q, want the one the layer received, /things/7", got)
		}
		return nil
	})
}

// A handler that ends its goroutine with runtime.Goexit is recorded as a panic
// as well, and the unwinding goes on to the connection.
//
// Such a handler neither returns nor throws a value, so there is no status to
// read and nothing to catch: the row says "panic", as it does for a handler that
// panicked, and the connection is closed on the client without an answer. A layer
// that wrote its row only for a value it had caught would leave this request
// without one, and one that swallowed the unwinding would leave the client an
// answer.
func TestAGoexitIsRecordedAsAPanicAndTheConnectionIsClosed(t *testing.T) {
	servedRequests(t, []string{"GET /exit"}, func(endpoint speedhttp.Endpoint) {
		endpoint.Route("GET", "/exit", func(*gin.Context) { runtime.Goexit() })
	}, func(ctx context.Context, s *served) error {
		if got, err := s.ask(ctx, "GET", "/exit"); err == nil {
			t.Errorf("the client was answered with %d, want the connection closed", got.status)
		}

		rows := s.awaitRows(1)
		if got := describe(rows); len(got) != 1 || got[0] != "GET /exit panic" {
			t.Errorf("the rows are %v, want the request recorded with the outcome %q", got, "panic")
		}
		return nil
	})
}

// A request whose connection the handler took over is recorded with the status
// the writer holds, which is not the answer the handler wrote on the connection.
//
// After the takeover the status line is the handler's own, written on the raw
// connection, where the layer cannot read it: here the client is answered 101
// and the row holds 200, or the status the handler had set before the takeover.
// The second half is what tells the writer's status from a constant.
func TestAHijackedRequestRecordsTheStatusTheWriterHolds(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  int
		want string
	}{
		{name: "no status set before the takeover", set: 0, want: "200"},
		{name: "a status set before the takeover", set: nethttp.StatusAccepted, want: "202"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			servedRequests(t, []string{"GET /takeover"}, func(endpoint speedhttp.Endpoint) {
				endpoint.Route("GET", "/takeover", func(c *gin.Context) {
					if tc.set != 0 {
						c.Status(tc.set)
					}
					conn, _, err := nethttp.NewResponseController(c.Writer).Hijack()
					if err != nil {
						c.String(nethttp.StatusInternalServerError, err.Error())
						return
					}
					_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n"))
					_ = conn.Close()
				})
			}, func(ctx context.Context, s *served) error {
				status, err := s.rawStatus(ctx, "GET /takeover HTTP/1.1\r\nHost: test\r\n\r\n")
				if err != nil {
					t.Errorf("reading the handler's own answer: %v", err)
					return errCaseFailed
				}
				if status != nethttp.StatusSwitchingProtocols {
					t.Errorf("the client read %d, want the handler's own 101 written on the taken connection", status)
				}

				rows := s.awaitRows(1)
				want := "GET /takeover " + tc.want
				if got := describe(rows); len(got) != 1 || got[0] != want {
					t.Errorf("the rows are %v, want %q", got, want)
				}
				return nil
			})
		})
	}
}

// An entry of the requests input item that is not a method, a space and a path
// beginning with a slash fails the startup, and the failure names the item, the
// entry and the way to write one.
//
// The control is the same assembly with a well-formed entry, which starts.
func TestARequestItemThatIsNotAMethodAndAPathFailsTheStartup(t *testing.T) {
	drive := func(t *testing.T, entry string) error {
		t.Helper()
		run := assembly{
			database: sqlite.Module(),
			engine:   "sqlite",
			dsn:      filepath.Join(t.TempDir(), "delivered.db"),
			sections: map[string]any{
				"audit": map[string]any{"journal": map[string]any{"requests": []string{entry}}},
			},
			dir:   t.TempDir(),
			depth: core.StageStart,
		}
		return run.drive(t, nil)
	}

	if err := drive(t, "GET /contacts/:id"); err != nil {
		t.Fatalf("a request written as a method and a path failed the startup: %v", err)
	}
	for _, tc := range []struct{ name, entry string }{
		{"no space", "GET/contacts"},
		{"a method alone", "GET"},
		{"an empty path", "GET "},
		{"a path with no leading slash", "GET contacts"},
		{"two spaces before the path", "GET  /contacts"},
		{"an empty method", " /contacts"},
		{"a path alone", "/contacts"},
		{"an empty entry", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := drive(t, tc.entry)
			if err == nil {
				t.Fatalf("the entry %q started", tc.entry)
			}
			for _, part := range []string{ConfigNamespace + "." + requestsItem, strconv.Quote(tc.entry), `"GET /contacts/:id"`} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("the failure does not carry %s: %v", part, err)
				}
			}
		})
	}
}

// The entries a request is read from, and the ones it is not.
func TestParseRequestItem(t *testing.T) {
	for entry, want := range map[string]requestItem{
		"GET /contacts/:id":  {method: "GET", path: "/contacts/:id"},
		"DELETE /files/*key": {method: "DELETE", path: "/files/*key"},
		"POST /":             {method: "POST", path: "/"},
	} {
		got, ok := parseRequestItem(entry)
		if !ok || got != want {
			t.Errorf("the entry %q read as (%+v, %v), want %+v", entry, got, ok, want)
		}
	}
	for _, entry := range []string{
		"", "GET", "GET ", " /contacts", "/contacts", "GET/contacts", "GET contacts", "GET  /contacts",
		" GET /contacts",
	} {
		if got, ok := parseRequestItem(entry); ok {
			t.Errorf("the entry %q read as %+v, want it refused", entry, got)
		}
	}
	// An entry is read as it is written: a space after the path belongs to the
	// path, and no entry is trimmed into one that would be accepted.
	if got, ok := parseRequestItem("GET /contacts "); !ok || got.path != "/contacts " {
		t.Errorf("an entry with a trailing space read as (%+v, %v), want the path kept as written", got, ok)
	}
}

// The event path is the same shape: a process-local bus runs the subscriber on
// the publisher's context, and the event was published whether or not that
// context is still live.
func TestAnEventPublishedOnAFinishedContextIsStillRecorded(t *testing.T) {
	handle := open(t)
	var reported bytes.Buffer
	bus := newRecordingBus()
	j := newTestJournal(handle, settings{events: []string{"order.updated"}})
	j.signal = &signal{out: &reported}
	j.subscribe(bus)

	if err := bus.Publish(cancelled(), event.Event{
		Type:    "order.updated",
		Payload: map[string]any{"object": "order/o1"},
	}); err != nil {
		t.Fatalf("publishing: %v", err)
	}

	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want the event recorded after its publisher's context ended, got %d row(s); signal: %s",
			len(rows), reported.String())
	}
	if rows[0].Source != string(audit.SourceEvent) {
		t.Errorf("source is %q, want %q", rows[0].Source, audit.SourceEvent)
	}
	if reported.Len() != 0 {
		t.Errorf("the failure signal reported a healthy table: %s", reported.String())
	}
}
