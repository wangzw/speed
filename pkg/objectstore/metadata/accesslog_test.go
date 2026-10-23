package metadata

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/tenant/catalog"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// TestTheAccessLogRecordsTheContentPathWithoutTheToken runs the real entry point
// with the real content route and reads the log it writes. The token is a
// credential, so the property that matters is that the access log's path field
// is the bare /content and the token appears nowhere in the log at all.
//
// The content endpoint carries no gateway, which is what a browser loading an
// image requires: the token is the credential.
func TestTheAccessLogRecordsTheContentPathWithoutTheToken(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "log.json")
		document := map[string]any{
			"db": map[string]any{e.section: map[string]any{"dsn": e.locate(t)}},
			"log": map[string]any{"outputs": []any{
				map[string]any{"to": "file", "format": "json", "path": logPath},
			}},
			"http": map[string]any{
				"listeners": map[string]any{
					"backend": map[string]any{"address": "127.0.0.1:0", "drain-timeout": "1s"},
				},
				"endpoints": map[string]any{
					"content": map[string]any{
						"listener":       "backend",
						"prefixes":       []string{contentPath},
						"max-body-bytes": 0,
					},
				},
			},
			"objectstore": map[string]any{"metadata": map[string]any{
				"content-endpoints": []string{"content"},
				"sweep-interval":    "0s",
			}},
		}
		body, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encoding the configuration: %v", err)
		}
		locator := filepath.Join(dir, "config.json")
		if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
			t.Fatalf("writing the primary config source: %v", writeErr)
		}

		// A probe that depends on both the entry point and the metadata seam runs
		// its Serve after both are served, so the channel is the point at which
		// the module is wired and the listener is bound.
		ready := make(chan struct{})
		probe := core.Module{
			Name: "probe.served",
			Requires: []core.Requirement{
				{Token: (*speedhttp.Router)(nil)},
				{Token: (*objectstore.Objects)(nil)},
			},
			Serve: func(context.Context, *core.Registry, any) error {
				close(ready)
				return nil
			},
		}
		modules := []core.Module{
			e.database,
			log.Module(),
			catalog.Module(),
			speedhttp.Module(),
			Module(),
			newFakeStore().module(),
			probe,
		}

		loading := core.New()
		loading.Register(jsonformat.Module())
		loading.Register(filesource.Module())
		loading.Register(core.Module{
			Name:      "host",
			Resources: []any{config.HostIdentity{Prefix: "SPEEDACCESSLOG", DefaultLocator: "file://" + locator}},
		})
		for _, m := range modules {
			loading.Register(m)
		}
		reader, loadErr := config.Load(t.Context(), loading, nil)
		if loadErr != nil {
			t.Fatalf("loading the configuration: %v", loadErr)
		}

		reg := core.New()
		reg.Register(config.Module())
		for _, m := range modules {
			reg.Register(m)
		}
		reg.Supply(reader, config.ModuleName)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- reg.Run(ctx) }()

		select {
		case <-ready:
		case runErr := <-done:
			t.Fatalf("the assembly ended before serving: %v", runErr)
		case <-time.After(10 * time.Second):
			t.Fatal("the assembly never reached the serving stage")
		}

		objects, err := core.Resolve[objectstore.Objects](reg)
		if err != nil {
			t.Fatalf("resolving the object store: %v", err)
		}
		tctx := tenantCtx(t, reg, "acme")
		const body1 = "body"
		obj, err := objects.Create(tctx, objectstore.Declaration{Size: int64(len(body1)), ContentType: "text/plain"})
		if err != nil {
			t.Fatalf("declaring: %v", err)
		}
		if upErr := objects.Upload(tctx, obj.ID, strings.NewReader(body1), int64(len(body1))); upErr != nil {
			t.Fatalf("uploading: %v", upErr)
		}
		if _, cErr := objects.Complete(tctx, obj.ID); cErr != nil {
			t.Fatalf("completing: %v", cErr)
		}
		issued, err := objects.URL(tctx, obj.ID, objectstore.URLRequest{Access: objectstore.URLAccessRead})
		if err != nil {
			t.Fatalf("minting a read URL: %v", err)
		}
		token := tokenOf(t, issued.URL)

		address := waitForListenerAddress(t, logPath)
		response, err := http.Get("http://" + address + issued.URL)
		if err != nil {
			t.Fatalf("requesting the content route: %v", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("the content route answered %d, want 200", response.StatusCode)
		}

		cancel()
		<-done

		waitForAccessLog(t, logPath, token)
	})
}

// waitForListenerAddress reads the bound address out of the log the entry point
// wrote. The configured address asked for port zero, so the log is the only
// place the bound one is known.
func waitForListenerAddress(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if !strings.Contains(line, "an HTTP listener is listening") {
					continue
				}
				const key = `"address":"`
				start := strings.Index(line, key)
				if start < 0 {
					continue
				}
				rest := line[start+len(key):]
				end := strings.Index(rest, `"`)
				if end > 0 {
					return rest[:end]
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the listener's address never appeared in %s", path)
	return ""
}

// waitForAccessLog waits until the access log carries the content path and then
// holds that the token appears nowhere in the file.
func waitForAccessLog(t *testing.T, path, token string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			text := string(raw)
			if strings.Contains(text, `"path":"`+contentPath+`"`) {
				if strings.Contains(text, token) {
					t.Fatalf("the token appears in the access log:\n%s", text)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	raw, _ := os.ReadFile(path)
	t.Fatalf("the access log never carried path %q:\n%s", contentPath, raw)
}
