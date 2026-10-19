package http_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// This file is the runnable half of this package's documentation: every
// example below goes through the public surface and stands on a real assembly,
// and what it prints is what that assembly did.
//
// It opens the external test package, and it brings its own stubs rather than
// the internal suite's fixtures. That is also what fixes the naming direction
// here: net/http keeps its own name and the parent package takes the alias,
// which is what the package documentation gives an external file.
//
// The entry point it assembles is this package's own module, speedhttp.Module,
// taken into the registry the way a host takes a module in. Importing the
// package registers the same descriptor with the process registry, and a host
// that builds its command tree from that registry names it by
// speedhttp.ModuleName.

// exampleDocument is the configuration every example runs on: one listening
// socket named backend, on an address the operating system picks, with one
// routing endpoint named public hanging on it.
const exampleDocument = `{
  "http": {
    "listeners": {
      "backend": {"address": "127.0.0.1:0"}
    },
    "endpoints": {
      "public": {"listener": "backend"}
    }
  }
}`

// exampleDeadline bounds every wait in this file. It is generous on purpose:
// what it guards against is a wait that never ends, not a slow machine.
const exampleDeadline = 10 * time.Second

// anExampleHost is one assembly running for an example: the registry going
// through the whole lifecycle on its own goroutine, and the handles the
// example needs to end the run.
type anExampleHost struct {
	cancel context.CancelFunc
	result chan error
	dir    string
}

// startExample assembles a host and runs it until the entry point has served.
//
// The assembly is the one a host with a config file builds: the JSON format
// and the file source, the host identity pointing at the document above, a
// logger writing to standard output, and the standard library entry point, all
// taken into a command tree whose single command drives the whole lifecycle.
//
// registrant runs in the Init stage of a module that declares its dependency
// on Router, so the assembly orders it after the entry point: the registration
// gate is open by the time it is called, which is what lets an example
// register the way a real module does. That module's Serve callback runs once
// the entry point has bound every address, which is what "the assembly is up"
// means here.
//
// extra modules are registered besides that registrant, for the examples that
// watch the assembly rather than take part in it. A non-nil error is a startup
// that failed; nothing is running then.
func startExample(registrant func(*core.Registry) error, extra ...core.Module) (*anExampleHost, error) {
	dir, err := os.MkdirTemp("", "http-example")
	if err != nil {
		return nil, err
	}
	document := filepath.Join(dir, "config.json")
	if err := os.WriteFile(document, []byte(exampleDocument), 0o600); err != nil {
		return nil, err
	}

	// A command tree takes its arguments and its program name from the
	// process, and the ones a test binary is started with are not this
	// program's. They stand in while the tree is built, which is when it
	// takes them.
	args := os.Args
	os.Args = []string{"example"}
	defer func() { os.Args = args }()

	served := make(chan struct{})
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "EXAMPLE", DefaultLocator: "file:" + document}},
	})
	reg.Register(exampleLoggerModule())
	reg.Register(speedhttp.Module())
	for _, module := range extra {
		reg.Register(module)
	}
	reg.Register(core.Module{
		Name:     "registrant",
		Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			return registrant(reg)
		},
		Serve: func(context.Context, *core.Registry, any) error {
			close(served)
			return nil
		},
	})

	// The host's command tree: one command, every available module in it,
	// driven to the end of the lifecycle. The reader every module reads its
	// configuration through is loaded here and supplied to the config module
	// registered first above. A registry made with core.New does not inherit that
	// registration from the process registry, and NewApp panics on one without it.
	app := config.NewApp(reg)
	app.Root().IncludeAll()
	app.Root().Do(core.StageServe, nil)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- app.Execute(ctx) }()

	select {
	case <-served:
		return &anExampleHost{cancel: cancel, result: result, dir: dir}, nil
	case err := <-result:
		cancel()
		_ = os.RemoveAll(dir)
		return nil, err
	case <-time.After(exampleDeadline):
		cancel()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("the assembly did not reach the Serve stage within %s", exampleDeadline)
	}
}

// stop ends the run and reports what Run came back with.
func (h *anExampleHost) stop() error {
	h.cancel()
	err := <-h.result
	_ = os.RemoveAll(h.dir)
	return err
}

// answer is one response as an example prints it: the status, the media type and
// the body.
type answer struct {
	status    int
	mediaType string
	body      string
}

// get sends a GET to the endpoint the example's host bound and returns the
// response. The address is the one the endpoint's own record reported, since the
// examples listen on port 0.
func (h *anExampleHost) get(path string) (answer, error) {
	response, err := http.Get("http://" + boundAddress.read() + path)
	if err != nil {
		return answer{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return answer{}, err
	}
	return answer{status: response.StatusCode, mediaType: response.Header.Get("Content-Type"), body: string(body)}, nil
}

// boundAddress is the address the last endpoint to bind reported. The examples
// run one after another, each with one endpoint.
var boundAddress addressBox

type addressBox struct {
	mu    sync.Mutex
	value string
}

func (a *addressBox) write(value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.value = value
}

func (a *addressBox) read() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.value
}

// exampleLoggerModule delivers the Logger capability over a logger that writes
// every record to standard output.
//
// That is where an example's evidence comes from. What this module reports
// about the assembly it made — the chain it lists per endpoint — is a record,
// and the alternative to reading it is an example that asserts nothing about
// the run it just did.
func exampleLoggerModule() core.Module {
	return core.Module{
		Name:     "examplelogger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return exampleLogger{logger: slog.New(exampleHandler{})}, nil
		},
	}
}

// exampleLogger is the Logger capability over one logger. Named ignores the
// module name: the records this file compares carry the endpoint and the
// chain, neither of which depends on it.
type exampleLogger struct{ logger *slog.Logger }

func (l exampleLogger) Named(string) *slog.Logger { return l.logger }
func (l exampleLogger) Redaction() log.Redaction  { return exampleRedaction{} }

// exampleRedaction takes registrations and masks nothing: nothing these
// examples log carries a value a rule would redact.
type exampleRedaction struct{}

func (exampleRedaction) AddKeys(...string)              {}
func (exampleRedaction) AddPattern(string, log.Matcher) {}

var _ log.Logger = exampleLogger{}

// exampleHandler renders one record per line, level and message first and then
// the attributes in the order they were written.
//
// The port of a bound address is written as <port>. The examples listen on
// port 0, so the operating system picks the port and the record carries a
// different one on every run; what an example can show is where it appears.
type exampleHandler struct{}

func (exampleHandler) Enabled(context.Context, slog.Level) bool { return true }

func (exampleHandler) Handle(_ context.Context, r slog.Record) error {
	line := r.Level.String() + " " + r.Message
	r.Attrs(func(a slog.Attr) bool {
		value := fmt.Sprint(a.Value.Any())
		if a.Key == "address" {
			boundAddress.write(value)
			if host, _, err := net.SplitHostPort(value); err == nil {
				value = net.JoinHostPort(host, "<port>")
			}
		}
		line += fmt.Sprintf(" %s=%s", a.Key, value)
		return true
	})
	fmt.Println(line)
	return nil
}

func (exampleHandler) WithAttrs([]slog.Attr) slog.Handler { return exampleHandler{} }
func (exampleHandler) WithGroup(string) slog.Handler      { return exampleHandler{} }

// ExampleEndpoint_Route registers a route: what a module does in its Init
// callback. It takes the Router capability up, looks an endpoint up by the name
// configuration gave it, and binds a method and a path to a handler. The path is
// written the way gin writes it, and the handler is a gin handler, so
// everything gin offers a handler — binding a body, rendering a response — is
// there without this package in between.
func ExampleEndpoint_Route() {
	host, err := startExample(func(reg *core.Registry) error {
		router, err := core.Resolve[speedhttp.Router](reg)
		if err != nil {
			return err
		}
		endpoint, err := router.Endpoint("public")
		if err != nil {
			return err
		}
		endpoint.Route(http.MethodGet, "/things/:id", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"id": c.Param("id")})
		})
		return nil
	})
	if err != nil {
		fmt.Println("startup failed:", err)
		return
	}
	defer func() { _ = host.stop() }()

	response, err := host.get("/things/7")
	if err != nil {
		fmt.Println("the request failed:", err)
		return
	}
	fmt.Println(response.status, response.body)

	// Output:
	// INFO an HTTP endpoint's chain is assembled endpoint=public routes=1 prefixes=[/] chain=http:context -> holdAllow -> http:access-log -> http:recovery -> http:body-limit
	// INFO an HTTP listener is listening listener=backend address=127.0.0.1:<port> configured=127.0.0.1:0 tls=false endpoints=[public]
	// INFO an HTTP request was served endpoint=public method=GET route=/things/:id path=/things/7 status=200 size=10
	// 200 {"id":"7"}
}

// ExampleEndpoint_Use registers two middleware layers on one endpoint.
//
// Provides and After are what make a constraint land: Provides says which
// capability a layer stands for on the chain, and After places this layer
// inside the middleware of that capability. The constraint reaches whichever
// layer declares the capability in its own Provides, so a capability no layer
// declares places nothing.
//
// A layer is a gin handler: it hands the request on with c.Next and ends the
// chain with c.Abort.
func ExampleEndpoint_Use() {
	// The capabilities the two layers name. A real layer points at a
	// capability some module delivers; what matters here is that the layer
	// standing for one says so itself.
	type (
		spans   interface{ spans() }
		tracing interface{ traces() }
	)

	host, err := startExample(func(reg *core.Registry) error {
		router, err := core.Resolve[speedhttp.Router](reg)
		if err != nil {
			return err
		}
		endpoint, err := router.Endpoint("public")
		if err != nil {
			return err
		}
		// trace stands for tracing and belongs inside the span layer. Its
		// Order asks for the front of its stage; the After constraint is
		// what overrides that.
		endpoint.Use(speedhttp.Middleware{
			Name:     "trace",
			Stage:    speedhttp.StageObserve,
			Order:    -100,
			Provides: []core.Token{(*tracing)(nil)},
			After:    []core.Token{(*spans)(nil)},
			Handler:  func(c *gin.Context) { c.Next() },
		})
		// span stands for spans itself, so trace's constraint lands on it
		// and nowhere else.
		endpoint.Use(speedhttp.Middleware{
			Name:     "span",
			Stage:    speedhttp.StageObserve,
			Provides: []core.Token{(*spans)(nil)},
			Handler:  func(c *gin.Context) { c.Next() },
		})
		return nil
	})
	if err != nil {
		fmt.Println("startup failed:", err)
		return
	}
	defer func() { _ = host.stop() }()

	// Output:
	// INFO an HTTP endpoint's chain is assembled endpoint=public routes=0 prefixes=[/] chain=http:context -> holdAllow -> http:access-log -> [observe] span(github.com/vislake/speed/pkg/http_test.spans) -> trace(github.com/vislake/speed/pkg/http_test.tracing) -> http:recovery -> http:body-limit
	// INFO an HTTP listener is listening listener=backend address=127.0.0.1:<port> configured=127.0.0.1:0 tls=false endpoints=[public]
}

// ExampleFail refuses a request from a handler. The response is the default
// error shape, a problem details object of RFC 9457, and the chain behind the
// handler does not run. A layer that refuses a request does the same, setting
// the response headers the refusal needs — WWW-Authenticate for a 401,
// Retry-After for a 429 — before it calls Fail.
//
// The same shape is what the endpoint answers for a path that matches no route,
// which goes through every layer first.
func ExampleFail() {
	host, err := startExample(func(reg *core.Registry) error {
		router, err := core.Resolve[speedhttp.Router](reg)
		if err != nil {
			return err
		}
		endpoint, err := router.Endpoint("public")
		if err != nil {
			return err
		}
		endpoint.Route(http.MethodGet, "/things/:id", func(c *gin.Context) {
			speedhttp.Fail(c, speedhttp.Problem{
				Status: http.StatusNotFound,
				Detail: "there is no thing " + c.Param("id"),
			})
		})
		return nil
	})
	if err != nil {
		fmt.Println("startup failed:", err)
		return
	}
	defer func() { _ = host.stop() }()

	for _, path := range []string{"/things/7", "/nowhere"} {
		response, err := host.get(path)
		if err != nil {
			fmt.Println("the request failed:", err)
			return
		}
		fmt.Println(response.status, response.mediaType)
		fmt.Println(response.body)
	}

	// Output:
	// INFO an HTTP endpoint's chain is assembled endpoint=public routes=1 prefixes=[/] chain=http:context -> holdAllow -> http:access-log -> http:recovery -> http:body-limit
	// INFO an HTTP listener is listening listener=backend address=127.0.0.1:<port> configured=127.0.0.1:0 tls=false endpoints=[public]
	// INFO an HTTP request was served endpoint=public method=GET route=/things/:id path=/things/7 status=404 size=86
	// 404 application/problem+json
	// {"type":"about:blank","title":"Not Found","status":404,"detail":"there is no thing 7"}
	// INFO an HTTP request was served endpoint=public method=GET route= path=/nowhere status=404 size=55
	// 404 application/problem+json
	// {"type":"about:blank","title":"Not Found","status":404}
}

// problemShape is an error shape of an API's own, delivered as the
// ErrorResponder capability.
type problemShape struct{}

// RespondError writes every error response of the process in this module's one
// shape: a JSON object with a code and a message, under the status the Problem
// asks for.
func (problemShape) RespondError(w http.ResponseWriter, _ *http.Request, p speedhttp.Problem) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(p.Status)
	_, _ = fmt.Fprintf(w, `{"code":%d,"message":%q}`, p.Status, http.StatusText(p.Status))
}

// ExampleErrorResponder replaces the shape of every error response. A module
// that owns an API's error shape delivers the ErrorResponder capability, and
// from then on the responses this module writes — what Fail writes, the answer
// to a path that matches no route, the answer to a panic — take that shape. The
// default shape is not written beside it.
func ExampleErrorResponder() {
	host, err := startExample(func(*core.Registry) error { return nil }, core.Module{
		Name:     "errorshape",
		Provides: []core.Provision{{Token: (*speedhttp.ErrorResponder)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return problemShape{}, nil
		},
	})
	if err != nil {
		fmt.Println("startup failed:", err)
		return
	}
	defer func() { _ = host.stop() }()

	response, err := host.get("/nowhere")
	if err != nil {
		fmt.Println("the request failed:", err)
		return
	}
	fmt.Println(response.status, response.mediaType)
	fmt.Println(response.body)

	// Output:
	// INFO an HTTP endpoint's chain is assembled endpoint=public routes=0 prefixes=[/] chain=http:context -> holdAllow -> http:access-log -> http:recovery -> http:body-limit
	// INFO an HTTP listener is listening listener=backend address=127.0.0.1:<port> configured=127.0.0.1:0 tls=false endpoints=[public]
	// INFO an HTTP request was served endpoint=public method=GET route= path=/nowhere status=404 size=34
	// 404 application/json
	// {"code":404,"message":"Not Found"}
}

// ExampleSpec declares an OpenAPI fragment as a resource.
//
// The fragment is a static declaration: this module stores it and checks its
// shape at startup, and a reader takes the declarations up with
// core.Resources, each carrying the name of the module that owns it. Prefix,
// Version are bare segments the reader joins into the root the fragment's
// paths are relative to; a management surface adds a third, Scope.
func ExampleSpec() {
	reg := core.New()
	reg.Register(core.Module{
		Name: "catalog",
		Resources: []any{speedhttp.Spec{
			Endpoint: "public",
			Prefix:   "api",
			Version:  "v1",
			Document: []byte(`{"openapi":"3.1.0","info":{"title":"Catalog"},"paths":{"/things":{}}}`),
		}},
	})

	for _, declared := range core.Resources[speedhttp.Spec](reg) {
		fmt.Println(declared.Module, declared.Value.Endpoint, declared.Value.APIRoot())
	}

	// Output:
	// catalog public /api/v1
}

// ExampleSpec_invalidShape declares a fragment whose shape is not usable. The
// shape is checked at startup, and the failure names the module that declared
// it, so the fragment is fixed at its source.
func ExampleSpec_invalidShape() {
	_, err := startExample(func(*core.Registry) error { return nil }, core.Module{
		Name:      "catalog",
		Resources: []any{speedhttp.Spec{Endpoint: "public", Document: []byte(`{"swagger":"2.0"}`)}},
	})
	fmt.Println(errors.Is(err, speedhttp.ErrInvalidSpec))

	// Output:
	// true
}
