package twilio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/sms"
)

const (
	testAccountSID = "AC00000000000000000000000000000000"
	testAuthToken  = "testauthtoken9f3a2c"
)

func completeConfig(apiBase, contentBase string) moduleConfig {
	return moduleConfig{
		AccountSID:      testAccountSID,
		AuthToken:       testAuthToken,
		APIBaseURL:      apiBase,
		ContentBaseURL:  contentBase,
		DefaultIdentity: "service",
		Identities: []identitySpec{
			{
				Name:       "service",
				Kind:       sms.IdentityMessagingService,
				Value:      "MG00000000000000000000000000000000",
				Countries:  []string{"US"},
				Attributes: map[string]string{fromAttribute: "+15550000000"},
			},
			{Name: "number", Kind: sms.IdentityLongCode, Value: "+15551111111"},
		},
	}
}

func senderOver(t *testing.T, apiBase, contentBase string) *sender {
	t.Helper()
	conf, err := completeConfig(apiBase, contentBase).resolve()
	if err != nil {
		t.Fatalf("resolving the complete configuration: %v", err)
	}
	return newSender(conf)
}

func messageReply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, body)
	}
}

func oneResolved() sms.Resolved {
	return sms.Resolved{
		To:       "+15551234567",
		Template: "authn.code.login",
		Locale:   "en-US",
		Kind:     sms.KindVerification,
		Code:     "HX00000000000000000000000000000001",
		Params:   map[string]string{"code": "123456", "ttl_minutes": "5"},
		Ordered:  []string{"123456", "5"},
		DedupeID: "challenge-1",
	}
}

// TestTheSenderReportsTheCarrierName checks that the sender reports the carrier name.
func TestTheSenderReportsTheCarrierName(t *testing.T) {
	s := senderOver(t, "http://127.0.0.1:1", "http://127.0.0.1:1")
	if got := s.Name(); got != "twilio" {
		t.Errorf("Name() = %q, want twilio", got)
	}
	if ModuleName != "sms.twilio" {
		t.Errorf("ModuleName = %q, want sms.twilio", ModuleName)
	}
}

// TestTheCarrierDeclaresItsModel checks the declaration: the content template is the sending shape,
// a send may pick its identity, there is no per-template review and delivery is asynchronous.
func TestTheCarrierDeclaresItsModel(t *testing.T) {
	model := senderOver(t, "http://127.0.0.1:1", "http://127.0.0.1:1").Model()
	if !model.Content.TemplateCode || model.Content.TextBody {
		t.Errorf("Content = %+v, want the vendor template code and no text body", model.Content)
	}
	if model.Review.PerTemplate {
		t.Error("Review.PerTemplate is true, yet the content resources this carrier creates carry no review")
	}
	if !model.Review.Originator {
		t.Error("Review.Originator is false, yet a send may name one of the deployment's identities")
	}
	if !model.AsyncReceipt {
		t.Error("AsyncReceipt is false, yet the vendor's final delivery status arrives out of band")
	}
	if model.MaxBodyRunes != 0 || len(model.AttributeKeys) != 0 || len(model.Countries) != 0 {
		t.Errorf("Model = %+v, want no body bound, no attribute keys and no country restriction", model)
	}
	if len(model.Identities) != 2 {
		t.Fatalf("Identities = %+v, want the deployment's two identities", model.Identities)
	}
	if model.Identities[0].Name != "number" || model.Identities[1].Name != "service" {
		t.Errorf("Identities = %+v, want a stable order by name", model.Identities)
	}
	service := model.Identities[1]
	if service.Kind != sms.IdentityMessagingService || service.Value != "MG00000000000000000000000000000000" {
		t.Errorf("the service identity is %+v, want the configured messaging service", service)
	}
	if len(service.Countries) != 1 || service.Countries[0] != "US" {
		t.Errorf("the service identity's Countries = %v, want the declared US", service.Countries)
	}
	if service.Attributes[fromAttribute] != "+15550000000" {
		t.Errorf("the service identity's From attribute = %q, want +15550000000",
			service.Attributes[fromAttribute])
	}
}

// TestTheRequestCarriesTheContentTemplateAndOrderedVariables holds the Messages API request: the
// destination, the content SID, the ordered values, the messaging service and the From value.
func TestTheRequestCarriesTheContentTemplateAndOrderedVariables(t *testing.T) {
	var got struct {
		method, path, to, contentSid, contentVariables, messagingServiceSid, from, body string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.method = r.Method
		got.path = r.URL.Path
		got.to = r.PostFormValue("To")
		got.contentSid = r.PostFormValue("ContentSid")
		got.contentVariables = r.PostFormValue("ContentVariables")
		got.messagingServiceSid = r.PostFormValue("MessagingServiceSid")
		got.from = r.PostFormValue("From")
		got.body = r.PostFormValue("Body")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"sid":"SM00000000000000000000000000000001","status":"queued"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	receipt, err := s.Send(t.Context(), oneResolved())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.method != http.MethodPost {
		t.Errorf("the carrier was called with %s, want POST", got.method)
	}
	wantPath := "/2010-04-01/Accounts/" + testAccountSID + "/Messages.json"
	if got.path != wantPath {
		t.Errorf("the path = %q, want %q", got.path, wantPath)
	}
	if got.to != "+15551234567" {
		t.Errorf("To = %q, want the resolved destination", got.to)
	}
	if got.contentSid != "HX00000000000000000000000000000001" {
		t.Errorf("ContentSid = %q, want the resolved template code", got.contentSid)
	}
	if got.messagingServiceSid != "MG00000000000000000000000000000000" {
		t.Errorf("MessagingServiceSid = %q, want the default identity's service", got.messagingServiceSid)
	}
	if got.from != "+15550000000" {
		t.Errorf("From = %q, want the identity attribute's sender", got.from)
	}
	if got.body != "" {
		t.Errorf("Body = %q, want no body beside the content template", got.body)
	}
	var variables map[string]string
	if err := json.Unmarshal([]byte(got.contentVariables), &variables); err != nil {
		t.Fatalf("ContentVariables = %q, which is not JSON: %v", got.contentVariables, err)
	}
	want := map[string]string{"1": "123456", "2": "5"}
	if len(variables) != len(want) || variables["1"] != want["1"] || variables["2"] != want["2"] {
		t.Errorf("ContentVariables = %v, want the ordered values keyed %v", variables, want)
	}
	if receipt.ProviderID != "SM00000000000000000000000000000001" {
		t.Errorf("ProviderID = %q, want the message SID", receipt.ProviderID)
	}
}

// TestARequestedLifetimeBecomesTheValidityPeriod checks the lifetime mapping: whole
// seconds, one second minimum, clamped to the bound, and no parameter when none is named.
func TestARequestedLifetimeBecomesTheValidityPeriod(t *testing.T) {
	cases := []struct {
		name     string
		validFor time.Duration
		want     string
	}{
		{"no lifetime", 0, ""},
		{"in range", 90 * time.Second, "90"},
		{"under a second", 500 * time.Millisecond, "1"},
		{"past the vendor's bound", 48 * time.Hour, "36000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				got = r.PostFormValue("ValidityPeriod")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"sid":"SM00000000000000000000000000000002","status":"queued"}`)
			}))
			defer server.Close()

			s := senderOver(t, server.URL, server.URL)
			resolved := oneResolved()
			resolved.ValidFor = c.validFor
			if _, err := s.Send(t.Context(), resolved); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if got != c.want {
				t.Errorf("ValidityPeriod = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAnIdentityWithoutAttributesSendsFromItsValue checks that a non-service
// identity sends From its value and names no messaging service.
func TestAnIdentityWithoutAttributesSendsFromItsValue(t *testing.T) {
	var got struct{ from, messagingServiceSid string }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.from = r.PostFormValue("From")
		got.messagingServiceSid = r.PostFormValue("MessagingServiceSid")
		messageReply(`{"sid":"SM1"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	message := oneResolved()
	message.Originator = "number"
	if _, err := s.Send(t.Context(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.from != "+15551111111" {
		t.Errorf("From = %q, want the named identity's value", got.from)
	}
	if got.messagingServiceSid != "" {
		t.Errorf("MessagingServiceSid = %q, want none for a long-code identity", got.messagingServiceSid)
	}
}

// TestAnUnknownOriginatorIsRejectedWithoutAVendorCall checks that an undeclared identity is refused before the carrier is reached.
func TestAnUnknownOriginatorIsRejectedWithoutAVendorCall(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		messageReply(`{"sid":"SM1"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	message := oneResolved()
	message.Originator = "nobody"
	if _, err := s.Send(t.Context(), message); !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("Send answered %v, want the rejected sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times, want 0 for an unknown originator", n)
	}
}

// TestAMessageWithoutATemplateCodeIsRejected checks that a resolved message
// carrying no content code is refused before the vendor is reached.
func TestAMessageWithoutATemplateCodeIsRejected(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		messageReply(`{"sid":"SM1"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	message := oneResolved()
	message.Code = ""
	if _, err := s.Send(t.Context(), message); !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("Send answered %v, want the rejected sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times, want 0 for a message with no template code", n)
	}
}

// TestTheErrorTableFollowsTheHTTPStatus checks the per-class vector: 429 a limit, another
// 4xx a refusal, and a 5xx, transport failure or ended context uncertain.
func TestTheErrorTableFollowsTheHTTPStatus(t *testing.T) {
	for _, c := range []struct {
		status int
		code   int
		want   error
	}{
		{http.StatusTooManyRequests, 20429, sms.ErrThrottled},
		{http.StatusBadRequest, 21610, sms.ErrRejected},
		{http.StatusForbidden, 20003, sms.ErrRejected},
		{http.StatusNotFound, 20404, sms.ErrRejected},
		{http.StatusUnprocessableEntity, 21614, sms.ErrRejected},
		{http.StatusInternalServerError, 20500, sms.ErrUncertain},
		{http.StatusServiceUnavailable, 20503, sms.ErrUncertain},
	} {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, `{"code":`+itoa(c.code)+`,"message":"no","status":`+itoa(c.status)+`}`)
			}))
			defer server.Close()

			s := senderOver(t, server.URL, server.URL)
			_, err := s.Send(t.Context(), oneResolved())
			if !errors.Is(err, c.want) {
				t.Fatalf("Send answered %v, want %v for HTTP %d", err, c.want, c.status)
			}
		})
	}
}

// TestATransportFailureAndAnEndedContextAreUncertain checks that a network error
// and an ended context fall to the uncertain sentinel.
func TestATransportFailureAndAnEndedContextAreUncertain(t *testing.T) {
	t.Run("a network error", func(t *testing.T) {
		s := senderOver(t, "http://127.0.0.1:1", "http://127.0.0.1:1")
		if _, err := s.Send(t.Context(), oneResolved()); !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("Send answered %v, want the uncertain sentinel", err)
		}
	})

	t.Run("an ended context", func(t *testing.T) {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			messageReply(`{"sid":"SM1"}`)(w, r)
		}))
		defer server.Close()

		s := senderOver(t, server.URL, server.URL)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := s.Send(ctx, oneResolved()); !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("Send answered %v, want the uncertain sentinel", err)
		}
		if n := atomic.LoadInt32(&calls); n != 0 {
			t.Errorf("the carrier was called %d times for an ended context, want 0", n)
		}
	})
}

// TestADeliveryStatusErrorCodeIsNotASynchronousRefusal checks that a 2xx answer has taken
// the message and an error code it carries describes delivery, not the call.
func TestADeliveryStatusErrorCodeIsNotASynchronousRefusal(t *testing.T) {
	server := httptest.NewServer(messageReply(
		`{"sid":"SM42","status":"failed","error_code":21610,"error_message":"unsubscribed recipient"}`))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	receipt, err := s.Send(t.Context(), oneResolved())
	if err != nil {
		t.Fatalf("Send answered %v, want success: the message was accepted", err)
	}
	if receipt.ProviderID != "SM42" {
		t.Errorf("ProviderID = %q, want the accepted message SID", receipt.ProviderID)
	}
}

// TestAMessageAnswerWithNoIdentifierIsUncertain checks that an accepted call
// naming no message identifier leaves the outcome unknown.
func TestAMessageAnswerWithNoIdentifierIsUncertain(t *testing.T) {
	server := httptest.NewServer(messageReply(`{"status":"queued"}`))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	if _, err := s.Send(t.Context(), oneResolved()); !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("Send answered %v, want the uncertain sentinel", err)
	}
}

// TestTheCarrierIsCalledExactlyOnceEvenWhenItFails checks that a failing call is never made again.
func TestTheCarrierIsCalledExactlyOnceEvenWhenItFails(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":20500,"message":"boom","status":500}`)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	if _, err := s.Send(t.Context(), oneResolved()); !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("Send answered %v, want the uncertain sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("the carrier was called %d times, want exactly one", n)
	}
}

// TestOneIdentifierReachesTheCarrierOnce checks that one identifier reaches the
// carrier once and a second call is answered with the first call's outcome.
func TestOneIdentifierReachesTheCarrierOnce(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		messageReply(`{"sid":"SM7"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	message := oneResolved()
	firstReceipt, firstErr := s.Send(t.Context(), message)
	secondReceipt, secondErr := s.Send(t.Context(), message)

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("the carrier was called %d times for one identifier, want 1", n)
	}
	if firstErr != nil || secondErr != nil {
		t.Fatalf("the sends failed: %v and %v", firstErr, secondErr)
	}
	if firstReceipt != secondReceipt {
		t.Errorf("the second call was answered %+v, want the first call's %+v", secondReceipt, firstReceipt)
	}
}

// TestDistinctIdentifiersEachReachTheCarrier checks that distinct identifiers each reach the carrier.
func TestDistinctIdentifiersEachReachTheCarrier(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		messageReply(`{"sid":"SM7"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	for _, id := range []string{"chal-a", "chal-b", "chal-c"} {
		message := oneResolved()
		message.DedupeID = id
		if _, err := s.Send(t.Context(), message); err != nil {
			t.Fatalf("sending %s: %v", id, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("the carrier was called %d times for three identifiers, want 3", n)
	}
}

// TestConcurrentCallsForOneIdentifierReachTheCarrierOnce checks that calls for
// one identifier arriving together reach the carrier once.
func TestConcurrentCallsForOneIdentifierReachTheCarrierOnce(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		messageReply(`{"sid":"SM7"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Send(t.Context(), oneResolved()); err != nil {
				t.Errorf("sending: %v", err)
			}
		}()
	}
	wg.Wait()

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("the carrier was called %d times for one identifier under concurrency, want 1", n)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
