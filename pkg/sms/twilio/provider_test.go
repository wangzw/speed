package twilio

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// oneSubmission is a template the cases offer to the carrier.
func oneSubmission() sms.Submission {
	return sms.Submission{
		Name:    "login code",
		Content: "your code is ${code}, valid for ${ttl_minutes} minutes",
		Kind:    sms.KindVerification,
		Locale:  "zh-CN",
		Params:  []string{"code", "ttl_minutes"},
		Remark:  "the sign-in code",
	}
}

// contentReply answers a Content API call with one content resource.
func contentReply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, body)
	}
}

// TestOfferCreatesATextTemplate holds the Content API request shape: a
// twilio/text content under the submission's name, its body rewritten from the
// seam's named placeholders into double-brace positional ones, and the language
// derived from the locale.
func TestOfferCreatesATextTemplate(t *testing.T) {
	var got struct {
		method, path string
		request      struct {
			FriendlyName string `json:"friendly_name"`
			Language     string `json:"language"`
			Types        map[string]struct {
				Body string `json:"body"`
			} `json:"types"`
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got.request); err != nil {
			t.Errorf("the CreateContent body %q is not JSON: %v", body, err)
		}
		contentReply(`{"sid":"HX0000000000000000000000000000000a","language":"zh"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	code, err := s.Offer(t.Context(), oneSubmission())
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if got.method != http.MethodPost {
		t.Errorf("the carrier was called with %s, want POST", got.method)
	}
	if got.path != "/v1/Content" {
		t.Errorf("the path = %q, want /v1/Content", got.path)
	}
	if got.request.FriendlyName != "login code" {
		t.Errorf("friendly_name = %q, want the submission name", got.request.FriendlyName)
	}
	if got.request.Language != "zh" {
		t.Errorf("language = %q, want zh derived from zh-CN", got.request.Language)
	}
	text, ok := got.request.Types["twilio/text"]
	if !ok {
		t.Fatalf("types = %v, want a twilio/text entry", got.request.Types)
	}
	want := "your code is {{1}}, valid for {{2}} minutes"
	if text.Body != want {
		t.Errorf("the template body = %q, want %q", text.Body, want)
	}
	if code != "HX0000000000000000000000000000000a" {
		t.Errorf("Offer = %q, want the content SID", code)
	}
}

// TestTheLanguageIsTheLowercasedPrimaryTag holds the derivation, including the
// locales that cannot be expressed as a two-letter ISO 639-1 code.
func TestTheLanguageIsTheLowercasedPrimaryTag(t *testing.T) {
	for _, c := range []struct {
		locale string
		want   string
		ok     bool
	}{
		{"zh-CN", "zh", true},
		{"en-US", "en", true},
		{"EN", "en", true},
		{"zh-Hans-CN", "zh", true},
		{"pt_BR", "pt", true},
		{"fil", "", false},
		{"", "", false},
		{"e", "", false},
		{"english", "", false},
		{"1234", "", false},
	} {
		t.Run("locale "+c.locale, func(t *testing.T) {
			got, err := contentLanguage(c.locale)
			if c.ok {
				if err != nil {
					t.Fatalf("contentLanguage(%q): %v", c.locale, err)
				}
				if got != c.want {
					t.Errorf("contentLanguage(%q) = %q, want %q", c.locale, got, c.want)
				}
				return
			}
			if !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("contentLanguage(%q) answered %v, want the invalid-config sentinel", c.locale, err)
			}
		})
	}
}

// TestAnInexpressibleLocaleIsRefusedBeforeTheVendorCall holds that the language
// check runs before any vendor call.
func TestAnInexpressibleLocaleIsRefusedBeforeTheVendorCall(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		contentReply(`{"sid":"HX1"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	sub := oneSubmission()
	sub.Locale = "fil"
	if _, err := s.Offer(t.Context(), sub); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("Offer answered %v, want the invalid-config sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times, want 0 for an inexpressible locale", n)
	}
}

// TestTheBodyIsRewrittenIntoPositionalPlaceholders holds the rewriting itself and
// the defects it refuses.
func TestTheBodyIsRewrittenIntoPositionalPlaceholders(t *testing.T) {
	t.Run("rewritten", func(t *testing.T) {
		got, err := doubleBrace("code ${code}, ${n} minutes, ${code} again", []string{"code", "n"})
		if err != nil {
			t.Fatalf("doubleBrace: %v", err)
		}
		want := "code {{1}}, {{2}} minutes, {{1}} again"
		if got != want {
			t.Errorf("doubleBrace = %q, want %q", got, want)
		}
	})

	t.Run("a body with no placeholder", func(t *testing.T) {
		got, err := doubleBrace("a plain notice", nil)
		if err != nil {
			t.Fatalf("doubleBrace: %v", err)
		}
		if got != "a plain notice" {
			t.Errorf("doubleBrace = %q, want the body verbatim", got)
		}
	})

	for name, c := range map[string]struct {
		body   string
		params []string
	}{
		"a parameter declared twice": {"${code}", []string{"code", "code"}},
		"a placeholder not declared": {"${code}", nil},
		"an unclosed placeholder":    {"code ${code", []string{"code"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := doubleBrace(c.body, c.params); !errors.Is(err, sms.ErrRejected) {
				t.Fatalf("doubleBrace answered %v, want the rejected sentinel", err)
			}
		})
	}
}

// TestOfferFailuresFollowTheErrorTable holds that a limit, a refusal and a server
// failure are classified like a send, and that a response with no SID is
// uncertain: the offer interface is not idempotent, so offering the template
// again would be a second one.
func TestOfferFailuresFollowTheErrorTable(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		reply  string
		want   error
	}{
		"a limit":          {http.StatusTooManyRequests, `{"code":20429,"message":"no","status":429}`, sms.ErrThrottled},
		"a refusal":        {http.StatusBadRequest, `{"code":21610,"message":"no","status":400}`, sms.ErrRejected},
		"a server failure": {http.StatusInternalServerError, `{"code":20500,"message":"no","status":500}`, sms.ErrUncertain},
		"no content SID":   {http.StatusCreated, `{"language":"zh"}`, sms.ErrUncertain},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(func() http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(c.status)
					_, _ = io.WriteString(w, c.reply)
				}
			}())
			defer server.Close()

			s := senderOver(t, server.URL, server.URL)
			if _, err := s.Offer(t.Context(), oneSubmission()); !errors.Is(err, c.want) {
				t.Fatalf("Offer answered %v, want %v", err, c.want)
			}
		})
	}

	t.Run("a call that did not complete", func(t *testing.T) {
		s := senderOver(t, "http://127.0.0.1:1", "http://127.0.0.1:1")
		if _, err := s.Offer(t.Context(), oneSubmission()); !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("Offer answered %v, want the uncertain sentinel", err)
		}
	})
}

// TestApprovalAnswersNotRequiredWithoutAVendorCall holds that this carrier has no
// per-template review to read, so Approval makes no call.
func TestApprovalAnswersNotRequiredWithoutAVendorCall(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		contentReply(`{"sid":"HX1"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	approval, err := s.Approval(t.Context(), "HX1")
	if err != nil {
		t.Fatalf("Approval: %v", err)
	}
	if approval.State != sms.ApprovalNotRequired {
		t.Errorf("State = %q, want %q", approval.State, sms.ApprovalNotRequired)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times, want 0: there is no review to read", n)
	}
}

// TestRetireDeletesTheContentBySID holds the DeleteContent request shape.
func TestRetireDeletesTheContentBySID(t *testing.T) {
	var got struct{ method, path string }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	if err := s.Retire(t.Context(), "HX42"); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if got.method != http.MethodDelete {
		t.Errorf("the carrier was called with %s, want DELETE", got.method)
	}
	if got.path != "/v1/Content/HX42" {
		t.Errorf("the path = %q, want /v1/Content/HX42", got.path)
	}
}

// TestRetireTreatsAGoneContentAsRemoved holds that a 404 is success: the content
// is not there either way.
func TestRetireTreatsAGoneContentAsRemoved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":20404,"message":"not found","status":404}`)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	if err := s.Retire(t.Context(), "HX42"); err != nil {
		t.Fatalf("Retire answered %v, want success for a content the vendor no longer holds", err)
	}
}

// TestRetireFailuresFollowTheErrorTable holds the classification on the removal
// call.
func TestRetireFailuresFollowTheErrorTable(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		want   error
	}{
		"a limit":          {http.StatusTooManyRequests, sms.ErrThrottled},
		"a refusal":        {http.StatusBadRequest, sms.ErrRejected},
		"a server failure": {http.StatusInternalServerError, sms.ErrUncertain},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, `{"code":1,"message":"no","status":`+itoa(c.status)+`}`)
			}))
			defer server.Close()

			s := senderOver(t, server.URL, server.URL)
			if err := s.Retire(t.Context(), "HX42"); !errors.Is(err, c.want) {
				t.Fatalf("Retire answered %v, want %v", err, c.want)
			}
		})
	}
}

// TestAManagementCallIsNotRetried holds the no-retry rule on the management seam.
func TestAManagementCallIsNotRetried(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":20500,"message":"no","status":500}`)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	if _, err := s.Offer(t.Context(), oneSubmission()); !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("Offer answered %v, want the uncertain sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("the carrier was called %d times, want exactly one", n)
	}
}
