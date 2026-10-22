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
)

// linesMatching returns the log lines that carry every one of the substrings.
// A case reads the call line back from here rather than asking whether the
// output merely contains a field, so a second line for one call is visible.
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

// loggerInto is the module logger a case hands the store: a text handler writing
// into out, which is where the call lines land.
func loggerInto(out *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, nil))
}

// hashedTestKey is the key_hash field a line about testKey carries, computed
// here from the digest rather than from the package's own helper: the first
// twelve hex digits.
func hashedTestKey() string {
	sum := sha256.Sum256([]byte(testKey))
	return "key_hash=" + hex.EncodeToString(sum[:])[:12]
}

// opaqueReader hides a reader's Seeker, which is the SDK's cue to retry a
// failed put with backoff: wrapping the body holds a case to one attempt.
type opaqueReader struct{ io.Reader }

// calls drives the three byte operations, each under its own logger.
type call struct {
	// operation is the value the line carries in its operation field.
	operation string
	// sized says the operation reports the object's size.
	sized bool
	// drive runs the operation against s under ctx.
	drive func(ctx context.Context, s *store) error
}

// byteCalls is the three operations the seam promises, each driven with the
// case's context.
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

// workingServer answers each of the three operations with the success the SDK
// reads: a GET answers the object's bytes and its size, a DELETE answers 204,
// and a PUT answers an ETag.
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

// TestEachCallLogsExactlyOneSuccessLine holds the external-call rule on the
// success side: every call writes exactly one line carrying the operation, the
// bucket, the key's hash, the duration and the outcome, the size where the
// operation reports one, and the key itself nowhere. The line count is the
// assertion, not a substring: a call that wrote two lines fails the case.
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
			// A body that ended at EOF is the transfer completing, not a failure:
			// it adds no second line.
			if got := linesMatching(&out, "outcome=failed"); len(got) != 0 {
				t.Errorf("a successful %s wrote a failure line: %v", c.operation, got)
			}
		})
	}
}

// TestEachFailedCallLogsExactlyOneOutcomeLine holds the failure side: a call the
// service refuses writes exactly one line with outcome=failed, the service's code
// as the cause, its HTTP status and the duration — and no key, even when the
// service echoes the key in the error message.
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

// TestATransportFailureLogsACauseWithoutTheKey holds the failure that reaches no
// service answer: a refused connection still writes its line, exactly one of
// them, and the key is scrubbed out of the transport error's text, which renders
// the whole request URL.
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
	// The transport error carries the request URL, so the key was in the text
	// this line was rendered from: the marker is what took its place.
	if !strings.Contains(line, "[redacted]") {
		t.Errorf("the failure line carries no redaction marker: %s", line)
	}
}

// TestACallTheContextEndedBeforeItWasMadeLogsNoLine holds what the rule covers:
// a context already ended makes no request, so no external call is recorded and
// the operation answers the context's own error.
func TestACallTheContextEndedBeforeItWasMadeLogsNoLine(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The endpoint is never dialed: the context is checked before the call.
	s := storeOverWithLogger(t, "http://127.0.0.1:1", loggerInto(&out))
	err := s.Put(ctx, testKey, strings.NewReader("x"), 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put answered %v, want the context's own error", err)
	}
	if out.Len() != 0 {
		t.Errorf("the call that was never made wrote a line: %s", out.String())
	}
}

// TestABackgroundContextStillReachesTheModuleLogger holds the destination the
// factory's sweep relies on: Delete runs on a context the caller created, which
// carries no logger. The line still lands on the logger the module took up in
// New, named after the module, so a configured file or JSON destination sees it.
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

// truncatingServer answers the HEAD Stat with the declared size and the GET with
// fewer bytes than declared, then ends the response: the client's body read sees
// an unexpected end of stream, the failure Open cannot report itself.
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

// TestAnInterruptedBodyReadWritesOneFailureLine holds the gap between Open's
// success line and the body: Open reports the header it read, and a transfer
// that breaks after Open returned leaves its own failure line, with the same
// key_hash and no key. Exactly one such line is written, however often the
// broken reader answers the error.
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

// idleBeforeFirstRead is how long a case waits between Open returning and the
// first Read, so a read line that counted the wait cannot be mistaken for one
// that did not.
const idleBeforeFirstRead = 100 * time.Millisecond

// TestTheReadFailureDurationStartsAtTheFirstRead holds that the read line's
// duration is the body read's own call, not Open's: the wait between Open
// returning and the first Read is not part of it, and neither is the Open call,
// which wrote its own line before that. The stream is left idle for
// idleBeforeFirstRead and the recorded duration must stay below it; the body is
// five bytes over loopback, so the read itself answers in well under that.
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

// durationMillis reads the duration_ms field out of one line.
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

// TestScrubKeyReplacesEveryEscapedForm holds the scrub itself: a text that names
// a key with a space, a plus and a percent carries it in the three forms a URL
// can produce — as written, path-escaped and query-escaped — and every one is
// replaced, so a transport error that renders the whole request URL can be
// logged.
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
