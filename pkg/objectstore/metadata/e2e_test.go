package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/objectstore"
)

// ginEndpoint stands in for one listening endpoint: it binds the handlers it is
// given onto a real gin engine, which the case then serves over HTTP. The
// middleware surface is inert, because this case exercises the content route
// itself rather than the layers a host chooses for it.
type ginEndpoint struct{ engine *gin.Engine }

func (e *ginEndpoint) Route(method, path string, handlers ...gin.HandlerFunc) {
	e.engine.Handle(method, path, handlers...)
}

func (e *ginEndpoint) Use(speedhttp.Middleware) {}

func (e *ginEndpoint) Accepting() bool { return true }

// ginRouter is the entry point the module mounts the content route on.
type ginRouter struct {
	engine   *gin.Engine
	endpoint *ginEndpoint
}

func newGinRouter() *ginRouter {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	return &ginRouter{engine: engine, endpoint: &ginEndpoint{engine: engine}}
}

func (r *ginRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	if name != "content" {
		return nil, fmt.Errorf("%w: %q", speedhttp.ErrUnknownEndpoint, name)
	}
	return r.endpoint, nil
}

func (r *ginRouter) Endpoints() []string { return []string{"content"} }

// TestTheLocalBackendServesAMintedAddressOverHTTP drives one object through the
// whole direct path on the real local-directory backend: a business backend
// declares it, asks for a write address, a client PUTs the bytes to that
// address, the backend completes it, and a read address serves them back
// byte-for-byte with the declared media type and file name. The case also
// holds the lifecycle edges the addresses inherit: the bytes must arrive before
// completion, another tenant cannot mint an address for the object, and a
// deleted object's address stops serving.
func TestTheLocalBackendServesAMintedAddressOverHTTP(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		router := newGinRouter()
		h := e.on(t, harness{
			router: router,
			fsRoot: t.TempDir(),
			metadata: map[string]any{
				"content-endpoints": []string{"content"},
			},
		})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			acmeCtx := tenantCtx(t, reg, "acme")
			payload := []byte("the avatar's bytes")

			server := httptest.NewServer(router.engine)
			defer server.Close()

			obj, err := objects.Create(acmeCtx, objectstore.Declaration{
				Name:           "avatar.png",
				Size:           int64(len(payload)),
				ContentType:    "image/png",
				ChecksumSHA256: "",
			})
			if err != nil {
				t.Fatalf("declaring the object: %v", err)
			}

			write, err := objects.URL(acmeCtx, obj.ID, objectstore.URLRequest{Access: objectstore.URLAccessWrite})
			if err != nil {
				t.Fatalf("minting the write address: %v", err)
			}
			if !strings.HasPrefix(write.URL, "/content?t=") {
				t.Fatalf("the write address is %q, not a root-relative content address", write.URL)
			}
			if write.Method != http.MethodPut {
				t.Fatalf("the write address names %q, want PUT", write.Method)
			}

			put, err := http.NewRequest(http.MethodPut, server.URL+write.URL, bytes.NewReader(payload))
			if err != nil {
				t.Fatalf("building the upload: %v", err)
			}
			putResp, err := http.DefaultClient.Do(put)
			if err != nil {
				t.Fatalf("uploading the bytes: %v", err)
			}
			_ = putResp.Body.Close()
			if putResp.StatusCode != http.StatusNoContent {
				t.Fatalf("the upload answered %d, want 204", putResp.StatusCode)
			}

			if _, completeErr := objects.Complete(acmeCtx, obj.ID); completeErr != nil {
				t.Fatalf("completing the object: %v", completeErr)
			}

			read, err := objects.URL(acmeCtx, obj.ID, objectstore.URLRequest{Access: objectstore.URLAccessRead})
			if err != nil {
				t.Fatalf("minting the read address: %v", err)
			}
			getResp, err := http.Get(server.URL + read.URL)
			if err != nil {
				t.Fatalf("reading the bytes: %v", err)
			}
			body, readErr := io.ReadAll(getResp.Body)
			_ = getResp.Body.Close()
			if readErr != nil {
				t.Fatalf("reading the answer's body: %v", readErr)
			}
			if getResp.StatusCode != http.StatusOK {
				t.Fatalf("the read answered %d, want 200", getResp.StatusCode)
			}
			if !bytes.Equal(body, payload) {
				t.Fatalf("the read answered %q, want %q", body, payload)
			}
			if got := getResp.Header.Get("Content-Type"); got != "image/png" {
				t.Fatalf("the read answered the media type %q, want image/png", got)
			}
			if got := getResp.Header.Get("Content-Disposition"); !strings.Contains(got, "avatar.png") {
				t.Fatalf("the read answered the disposition %q, want it to name avatar.png", got)
			}

			other := tenantCtx(t, reg, "other")
			if _, mintErr := objects.URL(other, obj.ID, objectstore.URLRequest{Access: objectstore.URLAccessRead}); !errors.Is(mintErr, objectstore.ErrObjectNotFound) {
				t.Fatalf("another tenant minted an address for the object: %v", mintErr)
			}

			if deleteErr := objects.Delete(acmeCtx, obj.ID); deleteErr != nil {
				t.Fatalf("deleting the object: %v", deleteErr)
			}
			gone, err := http.Get(server.URL + read.URL)
			if err != nil {
				t.Fatalf("reading after the delete: %v", err)
			}
			_ = gone.Body.Close()
			if gone.StatusCode != http.StatusNotFound {
				t.Fatalf("the deleted object's address answered %d, want 404", gone.StatusCode)
			}

			unuploaded, err := objects.Create(acmeCtx, objectstore.Declaration{
				Size:        1,
				ContentType: "text/plain",
			})
			if err != nil {
				t.Fatalf("declaring an object whose bytes never arrive: %v", err)
			}
			if _, err := objects.Complete(acmeCtx, unuploaded.ID); !errors.Is(err, objectstore.ErrContentMissing) {
				t.Fatalf("completing an object with no bytes answered %v, want ErrContentMissing", err)
			}
			return nil
		})
	})
}
