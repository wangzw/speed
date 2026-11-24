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

func startExample(registrant func(*core.Registry) error, extra ...core.Module) (*anExampleHost, error) {
	dir, err := os.MkdirTemp("", "http-example")
	if err != nil {
		return nil, err
	}
	document := filepath.Join(dir, "config.json")
	if err := os.WriteFile(document, []byte(exampleDocument), 0o600); err != nil {
		return nil, err
	}

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

func (h *anExampleHost) stop() error {
	h.cancel()
	err := <-h.result
	_ = os.RemoveAll(h.dir)
	return err
}

type answer struct {
	status    int
	mediaType string
	body      string
}

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

func exampleLoggerModule() core.Module {
	return core.Module{
		Name:     "examplelogger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return exampleLogger{logger: slog.New(exampleHandler{})}, nil
		},
	}
}

type exampleLogger struct{ logger *slog.Logger }

func (l exampleLogger) Named(string) *slog.Logger { return l.logger }
func (l exampleLogger) Redaction() log.Redaction  { return exampleRedaction{} }

type exampleRedaction struct{}

func (exampleRedaction) AddKeys(...string)              {}
func (exampleRedaction) AddPattern(string, log.Matcher) {}

var _ log.Logger = exampleLogger{}

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

// ExampleEndpoint_Route registers a route: what a module does in its Init callback.
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
func ExampleEndpoint_Use() {
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
		endpoint.Use(speedhttp.Middleware{
			Name:     "trace",
			Stage:    speedhttp.StageObserve,
			Order:    -100,
			Provides: []core.Token{(*tracing)(nil)},
			After:    []core.Token{(*spans)(nil)},
			Handler:  func(c *gin.Context) { c.Next() },
		})
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

// ExampleFail refuses a request from a handler.
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

type problemShape struct{}

// RespondError writes every error response of the process in this module's one shape: a
// JSON object with a code and a message, under the status the Problem asks for.
func (problemShape) RespondError(w http.ResponseWriter, _ *http.Request, p speedhttp.Problem) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(p.Status)
	_, _ = fmt.Fprintf(w, `{"code":%d,"message":%q}`, p.Status, http.StatusText(p.Status))
}

// ExampleErrorResponder replaces the shape of every error response.
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

// ExampleSpec_invalidShape declares a fragment whose shape is not usable.
func ExampleSpec_invalidShape() {
	_, err := startExample(func(*core.Registry) error { return nil }, core.Module{
		Name:      "catalog",
		Resources: []any{speedhttp.Spec{Endpoint: "public", Document: []byte(`{"swagger":"2.0"}`)}},
	})
	fmt.Println(errors.Is(err, speedhttp.ErrInvalidSpec))

	// Output:
	// true
}
