package s3

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/minio/minio-go/v7"

	"github.com/vislake/speed/pkg/objectstore"
)

// The values a case drives the store with. The two credentials are the ones the
// module is configured with and then kept out of everything it produces.
const (
	testBucket    = "objects"
	testAccessKey = "test-access-key-id"
	testSecretKey = "test-secret-key-9f3a2c"
	testKey       = "object-key-1"
)

// requestRecord is what a case reads back about the one request the SDK produced.
type requestRecord struct {
	method string
	path   string
	host   string
	header http.Header
	body   []byte
}

// lastModified is the fixed timestamp a GET and a HEAD answer carries, which is
// what the SDK's Stat reads the object's age from.
const lastModified = "Wed, 21 Oct 2015 07:28:00 GMT"

// recorder builds a handler that records the request and answers it with status
// and body, so a case can hold the request shape the real SDK produced.
//
// A GET or HEAD answer carries an explicit Content-Length and Last-Modified,
// which is what the SDK's Stat reads the object's size and age from; a HEAD
// answer carries no body, as the method requires. A 200 PUT or DELETE answer
// carries the ETag an S3 service returns, so the SDK reads the call as the
// success it is.
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

// chunkedPayload extracts the object's bytes from an aws-chunked body, the
// streaming SigV4 framing the SDK sends a PUT in by default: a size in hex and a
// chunk signature, the chunk's bytes, and a final zero-sized chunk. It is what a
// case reads to hold that the bytes that reached the service are what Put was
// handed.
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

// noSuchKey is the error body an S3-compatible service answers a GET for an
// object it does not hold with. It carries no object key, so a case can hold that
// the key is not in what this backend reports.
const noSuchKey = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`

// storeOver builds the module's product against the given endpoint, the way the
// module's own Open does: the endpoint carries the scheme the transport is
// derived from, and the bucket and credentials are the configured ones. It
// discards the call lines, which the logging cases drive through
// storeOverWithLogger instead.
func storeOver(t *testing.T, endpoint string) *store {
	t.Helper()
	return storeOverWithLogger(t, endpoint, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// storeOverWithLogger builds the product over endpoint with logger as the module
// logger the factory would have named.
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

// endpointOf is the endpoint a case points the store at for a loopback server,
// carrying the http scheme the store derives the transport from.
func endpointOf(server *httptest.Server) string {
	return "http://" + server.Listener.Addr().String()
}

func TestTheStoreReportsTheS3Name(t *testing.T) {
	if got := (&store{}).Name(); got != "s3" {
		t.Errorf("Name() = %q, want s3", got)
	}
	if ModuleName != "objectstore.s3" {
		t.Errorf("ModuleName = %q, want objectstore.s3", ModuleName)
	}
}

// TestPutSendsTheBytesWithTheMethodPathHostAndSignature holds the request shape a
// Put produces: a PUT of the object key under the bucket, to the configured host,
// signed with SigV4. The SDK sends the payload in its streaming SigV4 framing by
// default, so the body is aws-chunked; the case decodes it and holds that the
// bytes that reached the service are what Put was handed, and that the
// Content-Length is the framed body's length.
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

// TestOpenReadsTheBytesBack holds that Open returns a stream over the object's
// bytes: the request is a GET of the key under the bucket and the caller reads
// back exactly what the service holds.
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

// TestOpenOnAMissingObjectAnswersAnError holds that an object the bucket does not
// hold is reported from Open, not left to surface on the first read, and that the
// service's own not-found error is what is handed back — wrapped so errors.As
// still reaches it, and carrying no object key.
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

// TestDeleteIsIdempotentOnAMissingObject holds the idempotence the seam promises:
// an S3-compatible service answers 204 for a delete whether or not the object
// exists, so the backend reports success, and a repeat delete does too.
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

// TestAServiceFailureIsReported holds that a call the service refuses is reported
// rather than swallowed, on each of the three byte operations, with the service's
// own error kept reachable.
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
			// Open forces a Stat before it hands back a stream, and the SDK's
			// Stat is a HEAD, so this is the one request Open makes when it
			// cannot open the object.
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

// TestAMissingBucketErrorIsPassedThrough holds the premise the module takes on
// trust — the bucket is there — and what a byte operation does when it is not:
// the service's own NoSuchBucket error is handed back untouched, and the backend
// invents none of the capability's sentinels, which the metadata module is what
// raises. The bucket is not probed at startup, so a missing one surfaces at the
// first operation rather than failing construction.
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

// TestTheStoreServesCallersConcurrently holds that one store is safe to use from
// many goroutines: it holds no caller's state, so a Put, an Open and a Delete on
// distinct keys run together. Run under -race it is the gate on that promise.
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

// TestTheSchemeOfTheEndpointChoosesTheTransport holds the endpoint parsing the
// module runs on when it builds the client: an explicit http or https scheme
// chooses the transport and is stripped, a scheme-less endpoint is https, and a
// scheme the backend does not know is a configuration it cannot run on.
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
