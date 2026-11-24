package s3

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/vislake/speed/pkg/objectstore"
)

const (
	testBucket    = "objects"
	testAccessKey = "test-access-key-id"
	testSecretKey = "test-secret-key-9f3a2c"
	testKey       = "object-key-1"
)

type requestRecord struct {
	method string
	path   string
	host   string
	header http.Header
	body   []byte
}

const lastModified = "Wed, 21 Oct 2015 07:28:00 GMT"

func recorder(rec *requestRecord, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var read []byte
		if r.Method != http.MethodHead {
			read, _ = io.ReadAll(r.Body)
		}
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.host = r.Host
		rec.header = r.Header.Clone()
		rec.body = read

		switch r.Method {
		case http.MethodGet, http.MethodHead:
			if status == http.StatusOK {
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				w.Header().Set("Last-Modified", lastModified)
				w.Header().Set("Content-Type", "application/octet-stream")
			}
		default:
			if status == http.StatusOK {
				w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
			}
		}
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, body)
		}
	}
}

func chunkedPayload(t *testing.T, body []byte) []byte {
	t.Helper()
	var out []byte
	for rest := body; ; {
		line, after, ok := bytes.Cut(rest, []byte("\r\n"))
		if !ok {
			t.Fatalf("the chunked body is malformed: a chunk header has no terminator: %q", rest)
		}
		sizeHex, _, _ := strings.Cut(string(line), ";")
		n, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			t.Fatalf("the chunk size %q is not hex: %v", sizeHex, err)
		}
		if n == 0 {
			return out
		}
		if int64(len(after)) < n+2 {
			t.Fatalf("the chunked body is truncated: a chunk declares %d bytes but %d remain", n, len(after))
		}
		out = append(out, after[:n]...)
		rest = after[n+2:]
	}
}

const noSuchKey = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`

func storeOver(t *testing.T, endpoint string) *store {
	t.Helper()
	return storeOverWithLogger(t, endpoint, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func storeOverWithLogger(t *testing.T, endpoint string, logger *slog.Logger) *store {
	t.Helper()
	s, err := open(t.Context(), &moduleConfig{
		Endpoint:     endpoint,
		Region:       "us-east-1",
		Bucket:       testBucket,
		AccessKey:    testAccessKey,
		SecretKey:    testSecretKey,
		UsePathStyle: true,
	}, logger)
	if err != nil {
		t.Fatalf("building the store over %s: %v", endpoint, err)
	}
	return s.(*store)
}

func endpointOf(server *httptest.Server) string {
	return "http://" + server.Listener.Addr().String()
}

// TestTheStoreReportsTheS3Name checks that the store names itself s3 and the module is
// objectstore.s3.
func TestTheStoreReportsTheS3Name(t *testing.T) {
	if got := (&store{}).Name(); got != "s3" {
		t.Errorf("Name() = %q, want s3", got)
	}
	if ModuleName != "objectstore.s3" {
		t.Errorf("ModuleName = %q, want objectstore.s3", ModuleName)
	}
}

// TestPutSendsTheBytesWithTheMethodPathHostAndSignature holds the request shape a Put produces: a
// PUT of the object key under the bucket, to the configured host, signed with SigV4.
func TestPutSendsTheBytesWithTheMethodPathHostAndSignature(t *testing.T) {
	var rec requestRecord
	server := httptest.NewServer(recorder(&rec, http.StatusOK, ""))
	defer server.Close()

	s := storeOver(t, endpointOf(server))
	const payload = "the object's bytes"
	if err := s.Put(t.Context(), testKey, strings.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if rec.method != http.MethodPut {
		t.Errorf("the service was called with %s, want PUT", rec.method)
	}
	if want := "/" + testBucket + "/" + testKey; rec.path != want {
		t.Errorf("the request path = %q, want %q", rec.path, want)
	}
	if want := server.Listener.Addr().String(); rec.host != want {
		t.Errorf("the request host = %q, want %q", rec.host, want)
	}
	if got := rec.header.Get("Authorization"); !strings.HasPrefix(got, "AWS4-HMAC-SHA256") {
		t.Errorf("Authorization = %q, want a SigV4 signature", got)
	}
	if got := rec.header.Get("Content-Length"); got != strconv.Itoa(len(rec.body)) {
		t.Errorf("Content-Length = %q, want the framed body's length %d", got, len(rec.body))
	}
	if got := string(chunkedPayload(t, rec.body)); got != payload {
		t.Errorf("the uploaded bytes = %q, want %q", got, payload)
	}
}

// TestOpenReadsTheBytesBack holds that Open returns a stream over the object's bytes: the request
// is a GET of the key under the bucket and the caller reads back exactly what the service holds.
func TestOpenReadsTheBytesBack(t *testing.T) {
	var rec requestRecord
	const payload = "bytes the caller reads back"
	server := httptest.NewServer(recorder(&rec, http.StatusOK, payload))
	defer server.Close()

	s := storeOver(t, endpointOf(server))
	rc, err := s.Open(t.Context(), testKey)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading the object's bytes: %v", err)
	}
	if string(got) != payload {
		t.Errorf("the read bytes = %q, want %q", got, payload)
	}
	if rec.method != http.MethodGet {
		t.Errorf("the service was called with %s, want GET", rec.method)
	}
	if want := "/" + testBucket + "/" + testKey; rec.path != want {
		t.Errorf("the request path = %q, want %q", rec.path, want)
	}
}

// TestOpenOnAMissingObjectAnswersAnError checks that Open surfaces the service's NoSuchKey error and
// does not carry the internal key in its text.
func TestOpenOnAMissingObjectAnswersAnError(t *testing.T) {
	server := httptest.NewServer(recorder(&requestRecord{}, http.StatusNotFound, noSuchKey))
	defer server.Close()

	s := storeOver(t, endpointOf(server))
	rc, err := s.Open(t.Context(), testKey)
	if err == nil {
		_ = rc.Close()
		t.Fatal("Open answered no error for an object the bucket does not hold")
	}
	var resp minio.ErrorResponse
	if !errors.As(err, &resp) {
		t.Fatalf("Open answered %v, want the service's own error to stay reachable", err)
	}
	if resp.Code != "NoSuchKey" {
		t.Errorf("the service error code = %q, want NoSuchKey", resp.Code)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("the error text %q carries the internal key", err.Error())
	}
}

// TestDeleteIsIdempotentOnAMissingObject checks that Delete succeeds twice with no error and issues
// a DELETE to the bucket path.
func TestDeleteIsIdempotentOnAMissingObject(t *testing.T) {
	var rec requestRecord
	server := httptest.NewServer(recorder(&rec, http.StatusNoContent, ""))
	defer server.Close()

	s := storeOver(t, endpointOf(server))
	for i := range 2 {
		if err := s.Delete(t.Context(), testKey); err != nil {
			t.Fatalf("Delete call %d: %v", i+1, err)
		}
	}
	if rec.method != http.MethodDelete {
		t.Errorf("the service was called with %s, want DELETE", rec.method)
	}
	if want := "/" + testBucket + "/" + testKey; rec.path != want {
		t.Errorf("the request path = %q, want %q", rec.path, want)
	}
}

// TestAServiceFailureIsReported holds that a call the service refuses is reported rather than
// swallowed, on each of the three byte operations, with the service's own error kept reachable.
func TestAServiceFailureIsReported(t *testing.T) {
	const denied = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<Error><Code>AccessDenied</Code><Message>Access Denied.</Message></Error>`
	cases := []struct {
		name   string
		method string
		drive  func(t *testing.T, s *store) error
	}{
		{
			name:   "Put",
			method: http.MethodPut,
			drive: func(_ *testing.T, s *store) error {
				return s.Put(t.Context(), testKey, strings.NewReader("x"), 1)
			},
		},
		{
			name:   "Open",
			method: http.MethodHead,
			drive: func(_ *testing.T, s *store) error {
				_, err := s.Open(t.Context(), testKey)
				return err
			},
		},
		{
			name:   "Delete",
			method: http.MethodDelete,
			drive: func(_ *testing.T, s *store) error {
				return s.Delete(t.Context(), testKey)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != c.method {
					t.Errorf("the service was called with %s, want %s", r.Method, c.method)
				}
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, denied)
			}))
			defer server.Close()

			err := c.drive(t, storeOver(t, endpointOf(server)))
			if err == nil {
				t.Fatalf("%s answered no error for a call the service refused", c.name)
			}
			var resp minio.ErrorResponse
			if !errors.As(err, &resp) {
				t.Fatalf("%s answered %v, want the service's own error to stay reachable", c.name, err)
			}
			if resp.Code != "AccessDenied" {
				t.Errorf("the service error code = %q, want AccessDenied", resp.Code)
			}
		})
	}
}

// TestAMissingBucketErrorIsPassedThrough checks that Put passes the service's NoSuchBucket error
// through without translating it to a capability sentinel.
func TestAMissingBucketErrorIsPassedThrough(t *testing.T) {
	const noSuchBucket = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist.</Message></Error>`
	server := httptest.NewServer(recorder(&requestRecord{}, http.StatusNotFound, noSuchBucket))
	defer server.Close()

	err := storeOver(t, endpointOf(server)).Put(t.Context(), testKey, strings.NewReader("x"), 1)
	var resp minio.ErrorResponse
	if !errors.As(err, &resp) || resp.Code != "NoSuchBucket" {
		t.Fatalf("Put answered %v, want the service's NoSuchBucket error passed through", err)
	}
	for _, sentinel := range []error{
		objectstore.ErrObjectNotFound,
		objectstore.ErrStateConflict,
		objectstore.ErrContentMissing,
		objectstore.ErrStoreUnavailable,
		objectstore.ErrInvalidConfig,
		objectstore.ErrInvalidRequest,
	} {
		if errors.Is(err, sentinel) {
			t.Errorf("the backend answered the capability sentinel %v, which the metadata module raises", sentinel)
		}
	}
}

// TestTheStoreServesCallersConcurrently holds that one store is safe to use from many goroutines:
// it holds no caller's state, so a Put, an Open and a Delete on distinct keys run together.
func TestTheStoreServesCallersConcurrently(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			w.Header().Set("Content-Length", "1")
			w.Header().Set("Last-Modified", lastModified)
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, "x")
			}
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("ETag", `"x"`)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	s := storeOver(t, endpointOf(server))
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			if err := s.Put(t.Context(), key, strings.NewReader("x"), 1); err != nil {
				t.Errorf("Put %s: %v", key, err)
				return
			}
			rc, err := s.Open(t.Context(), key)
			if err != nil {
				t.Errorf("Open %s: %v", key, err)
				return
			}
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
			if err := s.Delete(t.Context(), key); err != nil {
				t.Errorf("Delete %s: %v", key, err)
			}
		}()
	}
	wg.Wait()
}

// TestStatSendsAHeadAndMapsTheSize holds the shape Stat produces: a HEAD of the object key under
// the bucket, whose answer's length is what the capability reports.
func TestStatSendsAHeadAndMapsTheSize(t *testing.T) {
	var rec requestRecord
	const payload = "twelve bytes"
	server := httptest.NewServer(recorder(&rec, http.StatusOK, payload))
	defer server.Close()

	s := storeOver(t, endpointOf(server))
	blob, err := s.Stat(t.Context(), testKey)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if rec.method != http.MethodHead {
		t.Errorf("the service was called with %s, want HEAD", rec.method)
	}
	if want := "/" + testBucket + "/" + testKey; rec.path != want {
		t.Errorf("the request path = %q, want %q", rec.path, want)
	}
	if blob.Size != int64(len(payload)) {
		t.Errorf("Stat answered %d bytes, want %d", blob.Size, len(payload))
	}
}

// TestStatOnAMissingObjectWrapsContentMissing checks that Stat of a missing object wraps
// ErrContentMissing and does not carry the internal key.
func TestStatOnAMissingObjectWrapsContentMissing(t *testing.T) {
	server := httptest.NewServer(recorder(&requestRecord{}, http.StatusNotFound, noSuchKey))
	defer server.Close()

	s := storeOver(t, endpointOf(server))
	_, err := s.Stat(t.Context(), testKey)
	if !errors.Is(err, objectstore.ErrContentMissing) {
		t.Fatalf("Stat answered %v, want one wrapping ErrContentMissing", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("the error text %q carries the internal key", err.Error())
	}
}

// TestEveryNotFoundAnswerMapsToContentMissing holds the classification from the service's side:
// either not-found code and a plain 404 report the bytes as missing, while a refusal does not.
func TestEveryNotFoundAnswerMapsToContentMissing(t *testing.T) {
	for _, resp := range []minio.ErrorResponse{
		{Code: "NoSuchKey"},
		{Code: "NoSuchObject"},
		{StatusCode: http.StatusNotFound},
	} {
		if !missingFromService(resp) {
			t.Errorf("the answer %+v is not read as missing bytes", resp)
		}
	}
	if missingFromService(minio.ErrorResponse{Code: "AccessDenied", StatusCode: http.StatusForbidden}) {
		t.Error("a refused call is read as missing bytes")
	}
}

func presignStore(t *testing.T) *store {
	t.Helper()
	return storeOver(t, "http://127.0.0.1:1")
}

// TestPresignReadSignsTheMediaTypeAndFileNameOverrides checks that a presigned read carries a GET,
// the TTL, the signature, the response media type and an inline disposition with the file name.
func TestPresignReadSignsTheMediaTypeAndFileNameOverrides(t *testing.T) {
	const ttl = 5 * time.Minute
	s := presignStore(t)
	issued, err := s.PresignRead(t.Context(), testKey, objectstore.PresignParams{
		TTL:         ttl,
		ContentType: "image/png",
		Filename:    "a picture.png",
	})
	if err != nil {
		t.Fatalf("PresignRead: %v", err)
	}
	if issued.Method != http.MethodGet {
		t.Errorf("the read address's method = %q, want GET", issued.Method)
	}
	if !issued.Expires.After(time.Now()) {
		t.Errorf("the read address expires at %v, which is not in the future", issued.Expires)
	}

	u, err := url.Parse(issued.URL)
	if err != nil {
		t.Fatalf("parsing the read address: %v", err)
	}
	query := u.Query()
	if got, want := query.Get("X-Amz-Expires"), strconv.Itoa(int(ttl/time.Second)); got != want {
		t.Errorf("X-Amz-Expires = %q, want %q", got, want)
	}
	if query.Get("X-Amz-Signature") == "" {
		t.Errorf("the read address carries no signature: %s", issued.URL)
	}
	if got := query.Get("response-content-type"); got != "image/png" {
		t.Errorf("response-content-type = %q, want image/png", got)
	}
	if got := query.Get("response-content-disposition"); !strings.HasPrefix(got, "inline") ||
		!strings.Contains(got, "a picture.png") {
		t.Errorf("response-content-disposition = %q, want an inline disposition carrying the file name", got)
	}
}

// TestPresignWriteSignsAPutAndNoResponseOverride holds the write address: a PUT carrying a
// signature and an expiry, and none of the read side's response overrides.
func TestPresignWriteSignsAPutAndNoResponseOverride(t *testing.T) {
	const ttl = 5 * time.Minute
	s := presignStore(t)
	issued, err := s.PresignWrite(t.Context(), testKey, objectstore.PresignParams{TTL: ttl})
	if err != nil {
		t.Fatalf("PresignWrite: %v", err)
	}
	if issued.Method != http.MethodPut {
		t.Errorf("the write address's method = %q, want PUT", issued.Method)
	}

	u, err := url.Parse(issued.URL)
	if err != nil {
		t.Fatalf("parsing the write address: %v", err)
	}
	query := u.Query()
	if got, want := query.Get("X-Amz-Expires"), strconv.Itoa(int(ttl/time.Second)); got != want {
		t.Errorf("X-Amz-Expires = %q, want %q", got, want)
	}
	if query.Get("X-Amz-Signature") == "" {
		t.Errorf("the write address carries no signature: %s", issued.URL)
	}
	for _, name := range []string{"response-content-type", "response-content-disposition"} {
		if query.Has(name) {
			t.Errorf("the write address carries the read-side override %q: %s", name, issued.URL)
		}
	}
}

// TestPresignRefusesALifetimeOutsideTheBounds checks that a lifetime below one second or above seven
// days fails as ErrURLUnavailable at both ends of both presign calls, while the boundaries succeed.
func TestPresignRefusesALifetimeOutsideTheBounds(t *testing.T) {
	s := presignStore(t)
	for _, ttl := range []time.Duration{0, 500 * time.Millisecond, 8 * 24 * time.Hour} {
		if _, err := s.PresignRead(t.Context(), testKey, objectstore.PresignParams{TTL: ttl}); !errors.Is(err, objectstore.ErrURLUnavailable) {
			t.Errorf("PresignRead with a lifetime of %s answered %v, want ErrURLUnavailable", ttl, err)
		}
		if _, err := s.PresignWrite(t.Context(), testKey, objectstore.PresignParams{TTL: ttl}); !errors.Is(err, objectstore.ErrURLUnavailable) {
			t.Errorf("PresignWrite with a lifetime of %s answered %v, want ErrURLUnavailable", ttl, err)
		}
	}
	for _, ttl := range []time.Duration{time.Second, 7 * 24 * time.Hour} {
		if _, err := s.PresignRead(t.Context(), testKey, objectstore.PresignParams{TTL: ttl}); err != nil {
			t.Errorf("PresignRead with the boundary lifetime %s answered %v, want success", ttl, err)
		}
		if _, err := s.PresignWrite(t.Context(), testKey, objectstore.PresignParams{TTL: ttl}); err != nil {
			t.Errorf("PresignWrite with the boundary lifetime %s answered %v, want success", ttl, err)
		}
	}
}

// TestTheSchemeOfTheEndpointChoosesTheTransport checks that the endpoint's scheme picks secure or
// plain transport, a bare host is secure, and an unknown scheme fails as ErrInvalidConfig.
func TestTheSchemeOfTheEndpointChoosesTheTransport(t *testing.T) {
	cases := []struct {
		endpoint string
		host     string
		secure   bool
	}{
		{"https://s3.example.com", "s3.example.com", true},
		{"http://minio.local:9000", "minio.local:9000", false},
		{"minio.local:9000", "minio.local:9000", true},
		{"s3.amazonaws.com", "s3.amazonaws.com", true},
	}
	for _, c := range cases {
		t.Run(c.endpoint, func(t *testing.T) {
			host, secure, err := transportOf(c.endpoint)
			if err != nil {
				t.Fatalf("transportOf(%q): %v", c.endpoint, err)
			}
			if host != c.host || secure != c.secure {
				t.Errorf("transportOf(%q) = (%q, %v), want (%q, %v)", c.endpoint, host, secure, c.host, c.secure)
			}
		})
	}

	t.Run("an unknown scheme", func(t *testing.T) {
		_, _, err := transportOf("ftp://s3.example.com")
		if !errors.Is(err, objectstore.ErrInvalidConfig) {
			t.Fatalf("transportOf answered %v, want the invalid-configuration sentinel", err)
		}
	})
}
