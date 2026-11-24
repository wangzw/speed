package ops

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func object(t *testing.T, body string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("the body %q is not a JSON object: %v", body, err)
	}
	return parsed
}

func at(t *testing.T, doc map[string]any, keys ...string) any {
	t.Helper()
	var value any = doc
	for _, key := range keys {
		obj, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("the path %v does not name an object at %q", keys, key)
		}
		value = obj[key]
	}
	return value
}

// TestOpsServesProbesAndTheMergedDocument checks that the health, readiness and
// document routes answer and that the document carries the configured metadata.
func TestOpsServesProbesAndTheMergedDocument(t *testing.T) {
	host := startHost(t, hostOptions{config: `{
		"http": {"listeners": {"ops": {"address": "127.0.0.1:0"}}, "endpoints": {"ops": {"listener": "ops"}}},
		"ops": {"title": "the platform", "version": "1.2.3"}
	}`})

	t.Run("health answers the process is alive", func(t *testing.T) {
		got := host.get(t, healthPath)
		if got.status != 200 {
			t.Fatalf("GET %s answered %d, want 200 (body %q)", healthPath, got.status, got.body)
		}
		if obj := object(t, got.body); obj["status"] != "ok" {
			t.Errorf("the health body is %v, want status ok", obj)
		}
	})

	t.Run("ready answers the process can serve", func(t *testing.T) {
		got := host.get(t, readyPath)
		if got.status != 200 {
			t.Fatalf("GET %s answered %d, want 200 (body %q)", readyPath, got.status, got.body)
		}
		obj := object(t, got.body)
		if obj["status"] != "ready" {
			t.Errorf("the readiness body is %v, want status ready", obj)
		}
	})

	t.Run("openapi.json answers the merged document", func(t *testing.T) {
		got := host.get(t, documentPath)
		if got.status != 200 {
			t.Fatalf("GET %s answered %d, want 200 (body %q)", documentPath, got.status, got.body)
		}
		if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("the document's media type is %q, want application/json", ct)
		}
		doc := object(t, got.body)

		if v := doc["openapi"]; v != "3.1.0" {
			t.Errorf("the document declares openapi %v, want 3.1.0", v)
		}
		if v := at(t, doc, "info", "title"); v != "the platform" {
			t.Errorf("info.title is %v, want the configured title", v)
		}
		if v := at(t, doc, "info", "version"); v != "1.2.3" {
			t.Errorf("info.version is %v, want the configured version", v)
		}
		for _, path := range []string{healthPath, readyPath, documentPath} {
			if v := at(t, doc, "paths", path, "get", "x-endpoint"); v != "ops" {
				t.Errorf("the %s operation is tagged %v, want the endpoint it is served on", path, v)
			}
		}
		fragments, ok := doc["x-fragments"].([]any)
		if !ok || len(fragments) == 0 {
			t.Fatalf("x-fragments is %v, want the module's own fragment listed", doc["x-fragments"])
		}
		first := fragments[0].(map[string]any)
		if first["module"] != "ops" || first["endpoint"] != "ops" {
			t.Errorf("the module's fragment is recorded as %v, want ops/ops", first)
		}
	})
}

// TestOpsReadinessNamesADatabaseFailure checks that readiness answers 503 naming
// the database when its handle does not answer.
func TestOpsReadinessNamesADatabaseFailure(t *testing.T) {
	host := startHost(t, hostOptions{
		config: `{
			"http": {"listeners": {"ops": {"address": "127.0.0.1:0"}}, "endpoints": {"ops": {"listener": "ops"}}},
			"ops": {"title": "t"}
		}`,
		database: brokenDatabase(t),
	})

	got := host.get(t, readyPath)
	if got.status != 503 {
		t.Fatalf("GET %s with a database that does not answer returned %d, want 503 (body %q)",
			readyPath, got.status, got.body)
	}
	obj := object(t, got.body)
	failures, ok := obj["failures"].([]any)
	if !ok || len(failures) == 0 {
		t.Fatalf("the readiness body %v names no failure", got.body)
	}
	joined := got.body
	if !strings.Contains(joined, "database") {
		t.Errorf("the readiness body %q does not name the database", joined)
	}
}

// TestOpsRoutesAndFragmentFollowTheConfiguredEndpoint checks that the routes and
// the module's own operations move to the configured endpoint.
func TestOpsRoutesAndFragmentFollowTheConfiguredEndpoint(t *testing.T) {
	host := startHost(t, hostOptions{config: `{
		"http": {"listeners": {"internal": {"address": "127.0.0.1:0"}}, "endpoints": {"internal": {"listener": "internal"}}},
		"ops": {"endpoint": "internal", "title": "t"}
	}`})

	if got := host.get(t, healthPath); got.status != 200 {
		t.Fatalf("GET %s on the configured endpoint returned %d, want 200 (body %q)",
			healthPath, got.status, got.body)
	}
	doc := object(t, host.get(t, documentPath).body)
	if v := at(t, doc, "paths", healthPath, "get", "x-endpoint"); v != "internal" {
		t.Errorf("the %s operation is tagged %v, want the configured endpoint internal", healthPath, v)
	}
	first := doc["x-fragments"].([]any)[0].(map[string]any)
	if first["endpoint"] != "internal" {
		t.Errorf("the module's fragment is recorded for endpoint %v, want internal", first["endpoint"])
	}
}

// TestReadinessNamesEachFailingPart checks that the readiness answer names each
// failing endpoint and the database, and no healthy one.
func TestReadinessNamesEachFailingPart(t *testing.T) {
	p := &product{
		router: fakeRouter{
			names: []string{"api", "ghost", "admin"},
			endpoints: map[string]speedhttp.Endpoint{
				"api":   fakeEndpoint{accepting: false},
				"admin": fakeEndpoint{accepting: true},
			},
		},
		database: brokenDatabase(t),
	}

	got := serve(t, p.reportReady)
	if got.status != 503 {
		t.Fatalf("the readiness answer is %d, want 503 (body %q)", got.status, got.body)
	}
	failures := object(t, got.body)["failures"].([]any)
	rendered := make([]string, 0, len(failures))
	for _, f := range failures {
		rendered = append(rendered, f.(string))
	}
	for _, want := range []string{
		`endpoint "api" is not accepting`,
		`endpoint "ghost" is not accepting`,
		"the database does not answer",
	} {
		found := false
		for _, f := range rendered {
			if strings.Contains(f, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("the failures %v do not name %q", rendered, want)
		}
	}
	for _, f := range rendered {
		if strings.Contains(f, `"admin"`) {
			t.Errorf("the failures %v name the healthy endpoint admin", rendered)
		}
	}
}

// TestInitRejectsAnInstanceItDidNotConstruct checks that Init refuses an instance
// its New did not build.
func TestInitRejectsAnInstanceItDidNotConstruct(t *testing.T) {
	err := initProduct(context.Background(), core.New(), "not a product")
	if !errors.Is(err, ErrForeignInstance) {
		t.Fatalf("Init handed a foreign instance reported %v, want ErrForeignInstance", err)
	}
}

// TestStopAndCloseTolerateAnAbsentInstance checks that Stop and Close tolerate an
// absent or foreign instance and write one record on a built product.
func TestStopAndCloseTolerateAnAbsentInstance(t *testing.T) {
	for _, cb := range []struct {
		name string
		call func(context.Context, *core.Registry, any) error
	}{{"Stop", stopProduct}, {"Close", closeProduct}} {
		for _, instance := range []any{nil, "not a product"} {
			if err := cb.call(context.Background(), core.New(), instance); err != nil {
				t.Errorf("%s(%T) returned %v, want nil", cb.name, instance, err)
			}
		}
	}

	logs := newRecorder()
	p := &product{logger: slog.New(logs)}
	if err := stopProduct(context.Background(), core.New(), p); err != nil {
		t.Fatalf("Stop on a built product returned %v", err)
	}
	if err := closeProduct(context.Background(), core.New(), p); err != nil {
		t.Fatalf("Close on a built product returned %v", err)
	}
	if got := len(logs.withMessage("stop")); got != 1 {
		t.Errorf("Stop wrote %d records, want one", got)
	}
	if got := len(logs.withMessage("close")); got != 1 {
		t.Errorf("Close wrote %d records, want one", got)
	}
}

func serve(t *testing.T, handler func(context.Context, struct{}, *speedhttp.Meta) (speedhttp.Reply, error)) response {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", readyPath, nil)
	readinessOperation.Handle(handler)(c)
	return response{status: w.Code, header: w.Header(), body: w.Body.String()}
}
