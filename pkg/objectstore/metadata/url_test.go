package metadata

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/objectstore"
)

func mintURL(t *testing.T, objects objectstore.Objects, ctx context.Context, id string, access objectstore.URLAccess, expiry time.Duration) objectstore.IssuedURL {
	t.Helper()
	issued, err := objects.URL(ctx, id, objectstore.URLRequest{Access: access, Expiry: expiry})
	if err != nil {
		t.Fatalf("minting a %s URL for %s: %v", access, id, err)
	}
	return issued
}

func tokenOf(t *testing.T, address string) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatalf("parsing the minted address %q: %v", address, err)
	}
	token := parsed.Query().Get(contentTokenParam)
	if token == "" {
		t.Fatalf("the minted address %q carries no token", address)
	}
	return token
}

// TestURLStateMatrix walks both directions against every state.
func TestURLStateMatrix(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		router := newRecordingRouter("public")
		h := e.on(t, harness{router: router})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")

			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			uploaded, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			if upErr := objects.Upload(tctx, uploaded.ID, bytes.NewReader([]byte("body")), 4); upErr != nil {
				t.Fatalf("uploading: %v", upErr)
			}
			completedID := declare(t, tctx, objects, "done", "body")
			deletedID := declare(t, tctx, objects, "gone", "body")
			if delErr := objects.Delete(tctx, deletedID); delErr != nil {
				t.Fatalf("deleting: %v", delErr)
			}

			read := objectstore.URLAccessRead
			write := objectstore.URLAccessWrite

			for _, id := range []string{pending.ID, uploaded.ID} {
				if _, uErr := objects.URL(tctx, id, objectstore.URLRequest{Access: read}); !errors.Is(uErr, objectstore.ErrStateConflict) {
					t.Errorf("a read URL for an uncompleted object gave %v, want ErrStateConflict", uErr)
				}
			}
			if got := mintURL(t, objects, tctx, completedID, read, 0); got.Method != "GET" {
				t.Errorf("a read URL's method is %q, want GET", got.Method)
			} else if !strings.HasPrefix(got.URL, contentPath+"?") {
				t.Errorf("the minted address is %q, want a root-relative %s when no public-url is set",
					got.URL, contentPath)
			}
			if _, uErr := objects.URL(tctx, deletedID, objectstore.URLRequest{Access: read}); !errors.Is(uErr, objectstore.ErrObjectNotFound) {
				t.Errorf("a read URL for a deleted object gave %v, want ErrObjectNotFound", uErr)
			}

			for _, id := range []string{pending.ID, uploaded.ID} {
				if got := mintURL(t, objects, tctx, id, write, 0); got.Method != "PUT" {
					t.Errorf("a write URL's method is %q, want PUT", got.Method)
				}
			}
			if _, uErr := objects.URL(tctx, completedID, objectstore.URLRequest{Access: write}); !errors.Is(uErr, objectstore.ErrStateConflict) {
				t.Errorf("a write URL for a completed object gave %v, want ErrStateConflict", uErr)
			}
			if _, uErr := objects.URL(tctx, deletedID, objectstore.URLRequest{Access: write}); !errors.Is(uErr, objectstore.ErrObjectNotFound) {
				t.Errorf("a write URL for a deleted object gave %v, want ErrObjectNotFound", uErr)
			}

			other := tenantCtx(t, reg, "globex")
			if _, uErr := objects.URL(other, completedID, objectstore.URLRequest{Access: read}); !errors.Is(uErr, objectstore.ErrObjectNotFound) {
				t.Errorf("another tenant's read URL gave %v, want ErrObjectNotFound", uErr)
			}
			return nil
		})
	})
}

// TestURLRefusesAnUnreadableRequest holds the two request shapes the method refuses before it
// reads a row.
func TestURLRefusesAnUnreadableRequest(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			if _, uErr := objects.URL(tctx, id, objectstore.URLRequest{Access: objectstore.URLAccess("delete")}); !errors.Is(uErr, objectstore.ErrInvalidRequest) {
				t.Errorf("an unknown access gave %v, want ErrInvalidRequest", uErr)
			}
			if _, uErr := objects.URL(tctx, id, objectstore.URLRequest{Access: objectstore.URLAccessRead, Expiry: -time.Second}); !errors.Is(uErr, objectstore.ErrInvalidRequest) {
				t.Errorf("a negative expiry gave %v, want ErrInvalidRequest", uErr)
			}
			return nil
		})
	})
}

// TestURLClampsTheLifetime checks that a read URL's lifetime is clamped to the ceiling, to the
// object's remaining life and to the upload window, refusing an expired grant or window.
func TestURLClampsTheLifetime(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{router: newRecordingRouter("public"), withSettings: true})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			store := settingsStoreOf(t, reg)

			writeSetting(t, store, urlTTLSetting, 5*time.Minute)
			id := declare(t, tctx, objects, "done", "body")
			before := time.Now()
			ceil := mintURL(t, objects, tctx, id, objectstore.URLAccessRead, time.Hour)
			after := time.Now()
			if life := ceil.Expires.Sub(before); life > 5*time.Minute+time.Second {
				t.Errorf("a one-hour request was granted %s against a five-minute ceiling", life)
			}
			if ceil.Expires.Before(before) || ceil.Expires.After(after.Add(5*time.Minute+time.Second)) {
				t.Errorf("the granted expiry %v is not about now + 5m", ceil.Expires)
			}

			short := mintURL(t, objects, tctx, id, objectstore.URLAccessRead, 10*time.Second)
			if life := short.Expires.Sub(before); life > 10*time.Second+time.Second {
				t.Errorf("a ten-second request was granted %s", life)
			}

			setColumn(t, reg, "expires_at", time.Now().Add(2*time.Second).UnixMilli(), "acme", id)
			clamped := mintURL(t, objects, tctx, id, objectstore.URLAccessRead, 0)
			if life := time.Until(clamped.Expires); life > 3*time.Second {
				t.Errorf("a read URL outlived the object by %s", life)
			}
			setColumn(t, reg, "expires_at", time.Now().Add(500*time.Millisecond).UnixMilli(), "acme", id)
			if _, uErr := objects.URL(tctx, id, objectstore.URLRequest{Access: objectstore.URLAccessRead}); !errors.Is(uErr, objectstore.ErrObjectNotFound) {
				t.Errorf("a read URL for an object with under a second left gave %v, want ErrObjectNotFound", uErr)
			}

			fresh, err := objects.Create(tctx, objectstore.Declaration{Size: 5, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			setColumn(t, reg, "upload_expires_at", time.Now().Add(2*time.Second).UnixMilli(), "acme", fresh.ID)
			writeIssued := mintURL(t, objects, tctx, fresh.ID, objectstore.URLAccessWrite, 0)
			if life := time.Until(writeIssued.Expires); life > 3*time.Second {
				t.Errorf("a write URL outlived its upload window by %s", life)
			}
			setColumn(t, reg, "upload_expires_at", time.Now().Add(-time.Second).UnixMilli(), "acme", fresh.ID)
			if _, uErr := objects.URL(tctx, fresh.ID, objectstore.URLRequest{Access: objectstore.URLAccessWrite}); !errors.Is(uErr, objectstore.ErrStateConflict) {
				t.Errorf("a write URL for a closed window gave %v, want ErrStateConflict", uErr)
			}
			return nil
		})
	})
}

// TestThePublicURLPrefixesTheMintedAddress checks that the public URL prefixes the minted address
// without a double slash.
func TestThePublicURLPrefixesTheMintedAddress(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{
			router: newRecordingRouter("public"),
			metadata: map[string]any{
				"content-endpoints": []string{"public"},
				"public-url":        "https://api.example.com/",
			},
		})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			issued := mintURL(t, objects, tctx, id, objectstore.URLAccessRead, 0)
			if !strings.HasPrefix(issued.URL, "https://api.example.com/content?t=") {
				t.Errorf("the minted address is %q, want it prefixed with the public URL and no double slash",
					issued.URL)
			}
			return nil
		})
	})
}

// TestURLNeverOutlivesTheSevenDayBackendMaximum holds the upper bound both backends share: even at
// the setting's own maximum, an address lives no longer than seven days.
func TestURLNeverOutlivesTheSevenDayBackendMaximum(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{router: newRecordingRouter("public"), withSettings: true})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			writeSetting(t, settingsStoreOf(t, reg), urlTTLSetting, 168*time.Hour)
			id := declare(t, tctx, objects, "done", "body")
			setColumn(t, reg, "expires_at", time.Now().Add(30*24*time.Hour).UnixMilli(), "acme", id)

			before := time.Now()
			issued := mintURL(t, objects, tctx, id, objectstore.URLAccessRead, 0)
			if life := issued.Expires.Sub(before); life > maxURLTTL+time.Second {
				t.Errorf("the address lives %s, above the %s maximum", life, maxURLTTL)
			}
			return nil
		})
	})
}

// TestAContentGrantStoresOnlyTheTokenHash holds that the table keeps the SHA-256 of the token and
// never the token itself, and that the token has the minted shape.
func TestAContentGrantStoresOnlyTheTokenHash(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			issued := mintURL(t, objects, tctx, id, objectstore.URLAccessRead, 0)
			token := tokenOf(t, issued.URL)

			if len(token) != tokenChars {
				t.Errorf("the token is %d characters, want %d", len(token), tokenChars)
			}
			if !validTokenShape(token) {
				t.Errorf("the token %q does not have the minted shape", token)
			}

			var stored struct {
				TenantID  string `gorm:"column:tenant_id"`
				TokenHash string `gorm:"column:token_hash"`
				ObjectID  string `gorm:"column:object_id"`
				Use       string `gorm:"column:use"`
			}
			if err := dbHandle(t, reg).Raw(
				"SELECT tenant_id, token_hash, object_id, use FROM object_grants WHERE object_id = ?", id).
				Scan(&stored).Error; err != nil {
				t.Fatalf("reading the grant row: %v", err)
			}
			if stored.TokenHash != hashToken(token) {
				t.Errorf("the stored hash is %q, want the token's SHA-256", stored.TokenHash)
			}
			if stored.TokenHash == token {
				t.Error("the grant row stores the token itself")
			}
			if len(stored.TokenHash) != 64 {
				t.Errorf("the stored hash is %d characters, want 64", len(stored.TokenHash))
			}
			if _, err := hex.DecodeString(stored.TokenHash); err != nil {
				t.Errorf("the stored hash is not hexadecimal: %v", err)
			}
			if stored.TenantID != "acme" || stored.ObjectID != id || stored.Use != "read" {
				t.Errorf("the grant row reads %+v, want acme/%s/read", stored, id)
			}
			return nil
		})
	})
}

type fakeDirect struct {
	readCalls  int
	writeCalls int
	lastParams objectstore.PresignParams
	err        error
}

func (d *fakeDirect) PresignRead(_ context.Context, _ string, p objectstore.PresignParams) (objectstore.IssuedURL, error) {
	d.readCalls++
	d.lastParams = p
	if d.err != nil {
		return objectstore.IssuedURL{}, d.err
	}
	return objectstore.IssuedURL{URL: "https://bucket.example/presigned-read", Method: "GET", Expires: time.Now().Add(p.TTL)}, nil
}

func (d *fakeDirect) PresignWrite(_ context.Context, _ string, p objectstore.PresignParams) (objectstore.IssuedURL, error) {
	d.writeCalls++
	d.lastParams = p
	if d.err != nil {
		return objectstore.IssuedURL{}, d.err
	}
	return objectstore.IssuedURL{URL: "https://bucket.example/presigned-write", Method: "PUT", Expires: time.Now().Add(p.TTL)}, nil
}

// TestADirectBackendMintsAddressesAndMountsNoContentRoute checks that a direct backend's signed
// addresses carry the right method and parameters, with no content route mounted.
func TestADirectBackendMintsAddressesAndMountsNoContentRoute(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		direct := &fakeDirect{}
		router := newRecordingRouter("public")
		h := e.on(t, harness{router: router, direct: direct, metadata: map[string]any{
			"content-endpoints": []string{"*"},
		}})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")

			issued := mintURL(t, objects, tctx, id, objectstore.URLAccessRead, 0)
			if issued.URL != "https://bucket.example/presigned-read" || issued.Method != "GET" {
				t.Errorf("the read URL reads %+v, want the backend's address", issued)
			}
			if direct.lastParams.ContentType != "text/plain" || direct.lastParams.Filename != "done" {
				t.Errorf("the signing parameters read %+v, want the object's media type and name", direct.lastParams)
			}
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			writeIssued := mintURL(t, objects, tctx, pending.ID, objectstore.URLAccessWrite, 0)
			if writeIssued.URL != "https://bucket.example/presigned-write" || writeIssued.Method != "PUT" {
				t.Errorf("the write URL reads %+v, want the backend's address", writeIssued)
			}

			if routes := router.endpoint("public").registered(); len(routes) != 0 {
				t.Errorf("the content route was mounted beside a direct backend: %v", routes)
			}
			return nil
		})
	})
}

// TestADirectBackendSigningFailureIsURLUnavailable holds that a backend that cannot mint an
// address is ErrURLUnavailable, not its own error escaping.
func TestADirectBackendSigningFailureIsURLUnavailable(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		direct := &fakeDirect{err: errors.New("the signer refused")}
		h := e.on(t, harness{direct: direct})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			if _, uErr := objects.URL(tctx, id, objectstore.URLRequest{Access: objectstore.URLAccessRead}); !errors.Is(uErr, objectstore.ErrURLUnavailable) {
				t.Errorf("a failed signing gave %v, want ErrURLUnavailable", uErr)
			}
			return nil
		})
	})
}

// TestURLWithoutAContentRouteIsUnavailable holds the assembly that can mint nothing: no direct
// backend and no entry point.
func TestURLWithoutAContentRouteIsUnavailable(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			if _, uErr := objects.URL(tctx, id, objectstore.URLRequest{Access: objectstore.URLAccessRead}); !errors.Is(uErr, objectstore.ErrURLUnavailable) {
				t.Errorf("a URL with no content route gave %v, want ErrURLUnavailable", uErr)
			}
			return nil
		})
	})
}

// TestTheContentRouteMountsOnePathThreeWays holds the mounted set and its path.
func TestTheContentRouteMountsOnePathThreeWays(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		router := newRecordingRouter("public")
		h := e.on(t, harness{router: router})
		h.drive(t, func(_ context.Context, _ *core.Registry) error {
			got := router.endpoint("public").registered()
			want := map[mountedRoute]bool{
				{method: "GET", path: contentPath}:  true,
				{method: "HEAD", path: contentPath}: true,
				{method: "PUT", path: contentPath}:  true,
			}
			if len(got) != len(want) {
				t.Fatalf("mounted %v, want the three methods on %s", got, contentPath)
			}
			for _, r := range got {
				if !want[r] {
					t.Errorf("an unexpected route was mounted: %s %s", r.method, r.path)
				}
			}
			return nil
		})
	})
}

// TestTheContentEndpointListIsRequiredOnlyWhereTheRouteMounts checks that an empty, unknown or
// repeated endpoint list fails as ErrInvalidConfig, while no entry point accepts every endpoint.
func TestTheContentEndpointListIsRequiredOnlyWhereTheRouteMounts(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		for name, endpoints := range map[string][]string{
			"empty":          {},
			"every endpoint": {allEndpoints},
			"a repeat":       {"public", "public"},
		} {
			t.Run(name, func(t *testing.T) {
				h := e.on(t, harness{router: newRecordingRouter("public"), metadata: map[string]any{
					"content-endpoints": endpoints,
				}})
				err := h.driveTo(t, core.StageInit, nil)
				if !errors.Is(err, objectstore.ErrInvalidConfig) {
					t.Fatalf("the endpoint list %v ended with %v, want ErrInvalidConfig", endpoints, err)
				}
				if !strings.Contains(err.Error(), contentEndpointsKey) {
					t.Errorf("the failure does not name %s: %v", contentEndpointsKey, err)
				}
			})
		}

		h := e.on(t, harness{metadata: map[string]any{"content-endpoints": []string{allEndpoints}}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			if _, err := core.Resolve[objectstore.Objects](reg); err != nil {
				t.Fatalf("resolving the object store with no entry point: %v", err)
			}
			return nil
		})
	})
}
