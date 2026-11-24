package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/objectstore"
)

func linesMatching(out *bytes.Buffer, substrings ...string) []string {
	var matched []string
	for _, line := range strings.Split(out.String(), "\n") {
		ok := line != ""
		for _, s := range substrings {
			ok = ok && strings.Contains(line, s)
		}
		if ok {
			matched = append(matched, line)
		}
	}
	return matched
}

func loggerInto(out *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, nil))
}

func hashedTestKey() string {
	sum := sha256.Sum256([]byte(testKey))
	return "key_hash=" + hex.EncodeToString(sum[:])[:12]
}

type opaqueReader struct{ io.Reader }

type call struct {
	operation string
	sized     bool
	drive     func(ctx context.Context, s *store) error
}

func byteCalls() []call {
	return []call{
		{
			operation: "put",
			sized:     true,
			drive: func(ctx context.Context, s *store) error {
				return s.Put(ctx, testKey, strings.NewReader("bytes"), 5)
			},
		},
		{
			operation: "stat",
			drive: func(ctx context.Context, s *store) error {
				_, err := s.Stat(ctx, testKey)
				return err
			},
		},
		{
			operation: "open",
			sized:     true,
			drive: func(ctx context.Context, s *store) error {
				rc, err := s.Open(ctx, testKey)
				if err != nil {
					return err
				}
				_, _ = io.Copy(io.Discard, rc)
				return rc.Close()
			},
		},
		{
			operation: "delete",
			drive: func(ctx context.Context, s *store) error {
				return s.Delete(ctx, testKey)
			},
		},
	}
}

func workingServer(payload string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Header().Set("Last-Modified", lastModified)
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, payload)
			}
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
			w.WriteHeader(http.StatusOK)
		}
	}))
}

// TestEachCallLogsExactlyOneSuccessLine checks that every byte call writes one success line with the
// bucket, hashed key and duration, carrying a size only where it has one and never the raw key.
func TestEachCallLogsExactlyOneSuccessLine(t *testing.T) {
	server := workingServer("bytes")
	defer server.Close()

	for _, c := range byteCalls() {
		t.Run(c.operation, func(t *testing.T) {
			var out bytes.Buffer
			s := storeOverWithLogger(t, endpointOf(server), loggerInto(&out))
			if err := c.drive(context.Background(), s); err != nil {
				t.Fatalf("%s: %v", c.operation, err)
			}
			lines := linesMatching(&out, "operation="+c.operation)
			if len(lines) != 1 {
				t.Fatalf("%s wrote %d lines, want exactly 1:\n%s", c.operation, len(lines), out.String())
			}
			line := lines[0]
			for _, field := range []string{
				"bucket=" + testBucket,
				hashedTestKey(),
				"duration_ms=",
				"outcome=ok",
			} {
				if !strings.Contains(line, field) {
					t.Errorf("the success line lacks %q: %s", field, line)
				}
			}
			if got := strings.Contains(line, "size="); got != c.sized {
				t.Errorf("the success line carries a size field = %v, want %v: %s", got, c.sized, line)
			}
			if strings.Contains(line, testKey) {
				t.Errorf("the success line carries the internal key: %s", line)
			}
			if got := linesMatching(&out, "outcome=failed"); len(got) != 0 {
				t.Errorf("a successful %s wrote a failure line: %v", c.operation, got)
			}
		})
	}
}

// TestEachFailedCallLogsExactlyOneOutcomeLine checks that every refused byte call writes one failed
// line naming the service's cause and status, and never the raw key.
func TestEachFailedCallLogsExactlyOneOutcomeLine(t *testing.T) {
	const denied = `<?xml version="1.0" encoding="UTF-8"?><Error>` +
		`<Code>AccessDenied</Code><Message>Access to key ` + testKey + ` denied.</Message></Error>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, denied)
	}))
	defer server.Close()

	for _, c := range byteCalls() {
		t.Run(c.operation, func(t *testing.T) {
			var out bytes.Buffer
			s := storeOverWithLogger(t, endpointOf(server), loggerInto(&out))
			if err := c.drive(context.Background(), s); err == nil {
				t.Fatalf("%s answered no error for a call the service refused", c.operation)
			}
			lines := linesMatching(&out, "operation="+c.operation)
			if len(lines) != 1 {
				t.Fatalf("%s wrote %d lines for one refused call, want exactly 1:\n%s",
					c.operation, len(lines), out.String())
			}
			line := lines[0]
			for _, field := range []string{
				"bucket=" + testBucket,
				hashedTestKey(),
				"duration_ms=",
				"outcome=failed",
				"cause=AccessDenied",
				"http_status=403",
			} {
				if !strings.Contains(line, field) {
					t.Errorf("the failure line lacks %q: %s", field, line)
				}
			}
			if strings.Contains(line, testKey) {
				t.Errorf("the failure line carries the internal key: %s", line)
			}
		})
	}
}

// TestATransportFailureLogsACauseWithoutTheKey checks that a transport failure writes one failed
// line with a cause and a redaction marker, and not the key.
func TestATransportFailureLogsACauseWithoutTheKey(t *testing.T) {
	server := workingServer("bytes")
	endpoint := endpointOf(server)
	server.Close()

	var out bytes.Buffer
	err := storeOverWithLogger(t, endpoint, loggerInto(&out)).
		Put(context.Background(), testKey, opaqueReader{strings.NewReader("x")}, 1)
	if err == nil {
		t.Fatal("Put answered no error against a closed endpoint")
	}
	lines := linesMatching(&out, "operation=put")
	if len(lines) != 1 {
		t.Fatalf("one refused put wrote %d lines, want exactly 1:\n%s", len(lines), out.String())
	}
	line := lines[0]
	for _, field := range []string{
		"bucket=" + testBucket,
		hashedTestKey(),
		"duration_ms=",
		"outcome=failed",
		"cause=",
	} {
		if !strings.Contains(line, field) {
			t.Errorf("the failure line lacks %q: %s", field, line)
		}
	}
	if strings.Contains(line, testKey) {
		t.Errorf("the failure line carries the internal key: %s", line)
	}
	if !strings.Contains(line, "[redacted]") {
		t.Errorf("the failure line carries no redaction marker: %s", line)
	}
}

// TestPresigningLogsOneLineWithoutTheAddress checks that each presign call writes one success line
// without the signed address or the access-key identifier.
func TestPresigningLogsOneLineWithoutTheAddress(t *testing.T) {
	cases := []struct {
		operation string
		drive     func(ctx context.Context, s *store) (objectstore.IssuedURL, error)
	}{
		{
			operation: "presign-read",
			drive: func(ctx context.Context, s *store) (objectstore.IssuedURL, error) {
				return s.PresignRead(ctx, testKey, objectstore.PresignParams{
					TTL: time.Minute, ContentType: "text/plain", Filename: "note.txt",
				})
			},
		},
		{
			operation: "presign-write",
			drive: func(ctx context.Context, s *store) (objectstore.IssuedURL, error) {
				return s.PresignWrite(ctx, testKey, objectstore.PresignParams{TTL: time.Minute})
			},
		},
	}
	for _, c := range cases {
		t.Run(c.operation, func(t *testing.T) {
			var out bytes.Buffer
			s := storeOverWithLogger(t, "http://127.0.0.1:1", loggerInto(&out))
			issued, err := c.drive(context.Background(), s)
			if err != nil {
				t.Fatalf("%s: %v", c.operation, err)
			}

			lines := linesMatching(&out, "operation="+c.operation)
			if len(lines) != 1 {
				t.Fatalf("%s wrote %d lines, want exactly 1:\n%s", c.operation, len(lines), out.String())
			}
			line := lines[0]
			for _, field := range []string{
				"bucket=" + testBucket,
				hashedTestKey(),
				"duration_ms=",
				"outcome=ok",
			} {
				if !strings.Contains(line, field) {
					t.Errorf("the %s line lacks %q: %s", c.operation, field, line)
				}
			}
			if strings.Contains(line, testKey) {
				t.Errorf("the %s line carries the internal key: %s", c.operation, line)
			}
			if strings.Contains(line, issued.URL) {
				t.Errorf("the %s line carries the signed address: %s", c.operation, line)
			}
			if strings.Contains(line, testAccessKey) {
				t.Errorf("the %s line carries the access-key identifier: %s", c.operation, line)
			}
		})
	}
}

// TestARefusedLifetimeLogsNoLine holds that a lifetime the bounds refuse signs nothing and
// therefore writes no call line: no address was produced.
func TestARefusedLifetimeLogsNoLine(t *testing.T) {
	var out bytes.Buffer
	s := storeOverWithLogger(t, "http://127.0.0.1:1", loggerInto(&out))
	_, err := s.PresignRead(context.Background(), testKey, objectstore.PresignParams{TTL: 0})
	if !errors.Is(err, objectstore.ErrURLUnavailable) {
		t.Fatalf("PresignRead answered %v, want ErrURLUnavailable", err)
	}
	if out.Len() != 0 {
		t.Errorf("a refused signing call wrote a line: %s", out.String())
	}
}

// TestACallTheContextEndedBeforeItWasMadeLogsNoLine checks that a call whose context was already
// cancelled returns the context error and writes no line.
func TestACallTheContextEndedBeforeItWasMadeLogsNoLine(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := storeOverWithLogger(t, "http://127.0.0.1:1", loggerInto(&out))
	err := s.Put(ctx, testKey, strings.NewReader("x"), 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put answered %v, want the context's own error", err)
	}
	if out.Len() != 0 {
		t.Errorf("the call that was never made wrote a line: %s", out.String())
	}
}

// TestABackgroundContextStillReachesTheModuleLogger holds the destination the factory's sweep
// relies on: Delete runs on a context the caller created, which carries no logger.
func TestABackgroundContextStillReachesTheModuleLogger(t *testing.T) {
	var out bytes.Buffer
	server := workingServer("bytes")
	defer server.Close()
	s := storeOverWithLogger(t, endpointOf(server), loggerInto(&out))

	if err := s.Delete(context.Background(), testKey); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	lines := linesMatching(&out, "operation=delete")
	if len(lines) != 1 {
		t.Fatalf("a delete on a background context wrote %d lines, want exactly 1:\n%s",
			len(lines), out.String())
	}
	line := lines[0]
	for _, field := range []string{"outcome=ok", hashedTestKey()} {
		if !strings.Contains(line, field) {
			t.Errorf("the background-context line lacks %q: %s", field, line)
		}
	}
}

func truncatingServer(declared int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(declared))
		w.Header().Set("Last-Modified", lastModified)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, body)
		}
	}))
}

// TestAnInterruptedBodyReadWritesOneFailureLine checks that Open writes its success line and a body
// cut short writes exactly one failed read line with a cause, never the key.
func TestAnInterruptedBodyReadWritesOneFailureLine(t *testing.T) {
	var out bytes.Buffer
	server := truncatingServer(64, "short")
	defer server.Close()
	s := storeOverWithLogger(t, endpointOf(server), loggerInto(&out))

	rc, err := s.Open(context.Background(), testKey)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, readErr := io.Copy(io.Discard, rc)
	_ = rc.Close()
	if readErr == nil {
		t.Fatal("the body read answered no error for a stream cut short of its declared length")
	}
	if errors.Is(readErr, io.EOF) {
		t.Fatal("the body read answered EOF, which is the transfer completing, not a failure")
	}

	if got := linesMatching(&out, "operation=open"); len(got) != 1 {
		t.Errorf("the open wrote %d lines, want its one success line:\n%s", len(got), out.String())
	}
	failed := linesMatching(&out, "operation=read")
	if len(failed) != 1 {
		t.Fatalf("a broken body read wrote %d failure lines, want exactly 1:\n%s",
			len(failed), out.String())
	}
	line := failed[0]
	for _, field := range []string{"outcome=failed", hashedTestKey(), "duration_ms=", "cause="} {
		if !strings.Contains(line, field) {
			t.Errorf("the interrupted-read line lacks %q: %s", field, line)
		}
	}
	if strings.Contains(line, testKey) {
		t.Errorf("the interrupted-read line carries the internal key: %s", line)
	}
}

const idleBeforeFirstRead = 100 * time.Millisecond

// TestTheReadFailureDurationStartsAtTheFirstRead checks that an idle period before the first read is
// not counted in a failed read's duration.
func TestTheReadFailureDurationStartsAtTheFirstRead(t *testing.T) {
	var out bytes.Buffer
	server := truncatingServer(64, "short")
	defer server.Close()
	s := storeOverWithLogger(t, endpointOf(server), loggerInto(&out))

	rc, err := s.Open(context.Background(), testKey)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	time.Sleep(idleBeforeFirstRead)
	_, readErr := io.Copy(io.Discard, rc)
	_ = rc.Close()
	if readErr == nil {
		t.Fatal("the body read answered no error for a stream cut short of its declared length")
	}

	failed := linesMatching(&out, "operation=read")
	if len(failed) != 1 {
		t.Fatalf("a broken body read wrote %d failure lines, want exactly 1:\n%s",
			len(failed), out.String())
	}
	got := durationMillis(t, failed[0])
	if got >= idleBeforeFirstRead.Milliseconds() {
		t.Errorf("the read line's duration_ms = %d counts the %v the stream sat idle before the first read: %s",
			got, idleBeforeFirstRead, failed[0])
	}
}

func durationMillis(t *testing.T, line string) int64 {
	t.Helper()
	const field = "duration_ms="
	i := strings.Index(line, field)
	if i < 0 {
		t.Fatalf("the line carries no duration_ms: %s", line)
	}
	value := line[i+len(field):]
	if end := strings.IndexByte(value, ' '); end >= 0 {
		value = value[:end]
	}
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("duration_ms is not a number: %s", line)
	}
	return ms
}

// TestScrubKeyReplacesEveryEscapedForm checks that scrubKey replaces the key in plain,
// path-escaped and query-escaped form.
func TestScrubKeyReplacesEveryEscapedForm(t *testing.T) {
	const key = "tenant 1/a+b%c.bin"
	forms := []string{key, url.PathEscape(key), url.QueryEscape(key)}
	text := "plain=" + forms[0] + " path=" + forms[1] + " query=" + forms[2]

	got := scrubKey(text, key)
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("scrubKey replaced nothing: %q", got)
	}
	for _, form := range forms {
		if form == key && strings.Contains(got, key) {
			t.Errorf("scrubKey left the key as written: %q", got)
		}
		if form != key && strings.Contains(got, form) {
			t.Errorf("scrubKey left the escaped form %q: %q", form, got)
		}
	}
}
