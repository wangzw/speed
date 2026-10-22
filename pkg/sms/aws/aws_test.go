package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/sms"
)

// testAccessKeySecret is the value the cases configure and then look for in
// everything the module produces.
const testAccessKeySecret = "test-secret-access-key-9f3a2c"

// testConfig is a configuration the cases run on: two identities, the primary one
// covering two countries and carrying the Indian entity identifier, and a
// default identity named.
func testConfig() resolved {
	identities := []sms.Identity{
		{
			Name:       "primary",
			Kind:       sms.IdentityPool,
			Value:      "pool-primary",
			Countries:  []string{"US", "IN"},
			Attributes: map[string]string{inEntityID: "entity-7"},
		},
		{
			Name:       "india",
			Kind:       sms.IdentitySenderID,
			Value:      "SPEEDIN",
			Countries:  []string{"IN"},
			Attributes: map[string]string{inEntityID: "entity-9"},
		},
	}
	byName := make(map[string]sms.Identity, len(identities))
	for _, identity := range identities {
		byName[identity.Name] = identity
	}
	return resolved{
		region:          "us-east-1",
		accessKeyID:     "test-access-key-id",
		accessKeySecret: testAccessKeySecret,
		defaultIdentity: "primary",
		identities:      identities,
		byName:          byName,
	}
}

// senderOver builds a sender whose client points at the given loopback endpoint,
// which is how a case reaches the vendor SDK without credentials or a network.
func senderOver(t *testing.T, endpoint string) *sender {
	t.Helper()
	cfg := testConfig()
	cfg.endpoint = endpoint
	client, err := newClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("building the carrier client against the loopback endpoint: %v", err)
	}
	return newSender(cfg, client)
}

// oneResolved is the send the cases drive: a message the registry has already
// rendered to a body.
func oneResolved() sms.Resolved {
	return sms.Resolved{
		To:         "+14155550100",
		Template:   "authn.code.login",
		Locale:     "en-US",
		Kind:       sms.KindVerification,
		Body:       "your code is 135790",
		Params:     map[string]string{"code": "135790"},
		Ordered:    []string{"135790"},
		Originator: "",
		DedupeID:   "challenge-1",
	}
}

// wireRequest is the part of the SendTextMessage body the cases read back.
type wireRequest struct {
	DestinationPhoneNumber       string
	MessageBody                  string
	OriginationIdentity          string
	MessageType                  string
	TimeToLive                   *int32
	ConfigurationSetName         string
	DestinationCountryParameters map[string]string
}

// accepted answers a SendTextMessage the vendor took.
func accepted(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	_, _ = io.WriteString(w, `{"MessageId":"message-42"}`)
}

// recordingServer answers every request with accepted and hands the decoded
// request to the case.
func recordingServer(t *testing.T, got *wireRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("the carrier was called with %s, want POST", r.Method)
		}
		if target := r.Header.Get("X-Amz-Target"); target != "PinpointSMSVoiceV2.SendTextMessage" {
			t.Errorf("X-Amz-Target = %q, want PinpointSMSVoiceV2.SendTextMessage", target)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		if err := json.Unmarshal(body, got); err != nil {
			t.Errorf("the request body %q is not JSON: %v", body, err)
		}
		accepted(w)
	}))
}

// TestTheRequestCarriesTheBodyIdentityAndMessageType holds the SendTextMessage
// shape: the destination, the rendered body, the chosen identity and the vendor's
// message type for a verification message.
func TestTheRequestCarriesTheBodyIdentityAndMessageType(t *testing.T) {
	var got wireRequest
	server := recordingServer(t, &got)
	defer server.Close()

	s := senderOver(t, server.URL)
	receipt, err := s.Send(t.Context(), oneResolved())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.DestinationPhoneNumber != "+14155550100" {
		t.Errorf("DestinationPhoneNumber = %q, want the resolved destination", got.DestinationPhoneNumber)
	}
	if got.MessageBody != "your code is 135790" {
		t.Errorf("MessageBody = %q, want the body the registry rendered", got.MessageBody)
	}
	if got.OriginationIdentity != "pool-primary" {
		t.Errorf("OriginationIdentity = %q, want the default identity's value", got.OriginationIdentity)
	}
	if got.MessageType != "TRANSACTIONAL" {
		t.Errorf("MessageType = %q, want TRANSACTIONAL for a verification message", got.MessageType)
	}
	if got.TimeToLive != nil {
		t.Errorf("TimeToLive = %d, want it absent when no lifetime was asked for", *got.TimeToLive)
	}
	if len(got.DestinationCountryParameters) != 0 {
		t.Errorf("DestinationCountryParameters = %v, want none for a non-Indian destination",
			got.DestinationCountryParameters)
	}
	if receipt.ProviderID != "message-42" {
		t.Errorf("ProviderID = %q, want the vendor's message identifier", receipt.ProviderID)
	}
}

// TestTheMessageTypeFollowsTheKind holds the kind mapping: verification and
// notification are transactional, marketing is promotional.
func TestTheMessageTypeFollowsTheKind(t *testing.T) {
	for _, c := range []struct {
		kind sms.Kind
		want string
	}{
		{sms.KindVerification, "TRANSACTIONAL"},
		{sms.KindNotification, "TRANSACTIONAL"},
		{sms.KindMarketing, "PROMOTIONAL"},
	} {
		t.Run(string(c.kind), func(t *testing.T) {
			var got wireRequest
			server := recordingServer(t, &got)
			defer server.Close()

			resolvedMessage := oneResolved()
			resolvedMessage.Kind = c.kind
			if _, err := senderOver(t, server.URL).Send(t.Context(), resolvedMessage); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if got.MessageType != c.want {
				t.Errorf("MessageType = %q, want %q for %s", got.MessageType, c.want, c.kind)
			}
		})
	}
}

// TestTheLifetimeBecomesTimeToLive holds that a requested lifetime is carried in
// whole seconds, and that no lifetime leaves the field to the vendor's default.
func TestTheLifetimeBecomesTimeToLive(t *testing.T) {
	t.Run("a lifetime", func(t *testing.T) {
		var got wireRequest
		server := recordingServer(t, &got)
		defer server.Close()

		resolvedMessage := oneResolved()
		resolvedMessage.ValidFor = 5 * time.Minute
		if _, err := senderOver(t, server.URL).Send(t.Context(), resolvedMessage); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if got.TimeToLive == nil || *got.TimeToLive != 300 {
			t.Errorf("TimeToLive = %v, want 300 seconds", got.TimeToLive)
		}
	})

	t.Run("no lifetime", func(t *testing.T) {
		var got wireRequest
		server := recordingServer(t, &got)
		defer server.Close()

		if _, err := senderOver(t, server.URL).Send(t.Context(), oneResolved()); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if got.TimeToLive != nil {
			t.Errorf("TimeToLive = %d, want the field absent", *got.TimeToLive)
		}
	})

	t.Run("a lifetime below a second", func(t *testing.T) {
		var got wireRequest
		server := recordingServer(t, &got)
		defer server.Close()

		resolvedMessage := oneResolved()
		resolvedMessage.ValidFor = 500 * time.Millisecond
		if _, err := senderOver(t, server.URL).Send(t.Context(), resolvedMessage); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if got.TimeToLive == nil || *got.TimeToLive != 1 {
			t.Errorf("TimeToLive = %v, want one second rather than the zero that would mean the vendor default",
				got.TimeToLive)
		}
	})
}

// TestAnIndianDestinationCarriesTheDLTParameters holds the target-country
// parameters: the template's IN_TEMPLATE_ID and the identity's IN_ENTITY_ID, and
// nothing for a destination outside India.
func TestAnIndianDestinationCarriesTheDLTParameters(t *testing.T) {
	var got wireRequest
	server := recordingServer(t, &got)
	defer server.Close()

	resolvedMessage := oneResolved()
	resolvedMessage.To = "+919876543210"
	resolvedMessage.Attributes = map[string]string{inTemplateID: "template-9"}
	if _, err := senderOver(t, server.URL).Send(t.Context(), resolvedMessage); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.DestinationCountryParameters[inTemplateID] != "template-9" {
		t.Errorf("DestinationCountryParameters = %v, want IN_TEMPLATE_ID from the template's attributes",
			got.DestinationCountryParameters)
	}
	if got.DestinationCountryParameters[inEntityID] != "entity-7" {
		t.Errorf("DestinationCountryParameters = %v, want IN_ENTITY_ID from the identity's attributes",
			got.DestinationCountryParameters)
	}
}

// TestTheChosenIdentityCarriesItsOwnParameters holds that naming an identity sends
// from that identity's value and its attributes, not the default's.
func TestTheChosenIdentityCarriesItsOwnParameters(t *testing.T) {
	var got wireRequest
	server := recordingServer(t, &got)
	defer server.Close()

	resolvedMessage := oneResolved()
	resolvedMessage.To = "+919876543210"
	resolvedMessage.Originator = "india"
	resolvedMessage.Attributes = map[string]string{inTemplateID: "template-9"}
	if _, err := senderOver(t, server.URL).Send(t.Context(), resolvedMessage); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.OriginationIdentity != "SPEEDIN" {
		t.Errorf("OriginationIdentity = %q, want the named identity's value", got.OriginationIdentity)
	}
	if got.DestinationCountryParameters[inEntityID] != "entity-9" {
		t.Errorf("DestinationCountryParameters = %v, want the named identity's entity identifier",
			got.DestinationCountryParameters)
	}
}

// TestTheConfigurationSetIsCarriedWhenConfigured holds that a configured
// configuration set is filed under with every send.
func TestTheConfigurationSetIsCarriedWhenConfigured(t *testing.T) {
	var got wireRequest
	server := recordingServer(t, &got)
	defer server.Close()

	cfg := testConfig()
	cfg.endpoint = server.URL
	cfg.configurationSetName = "speed-sends"
	client, err := newClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("building the carrier client: %v", err)
	}
	if _, err := newSender(cfg, client).Send(t.Context(), oneResolved()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.ConfigurationSetName != "speed-sends" {
		t.Errorf("ConfigurationSetName = %q, want the configured set", got.ConfigurationSetName)
	}
}

// TestAnUnknownOriginatorIsRejectedWithoutAVendorCall holds that a message naming
// an identity this deployment does not have is a caller defect, refused before
// the carrier is reached.
func TestAnUnknownOriginatorIsRejectedWithoutAVendorCall(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		accepted(w)
	}))
	defer server.Close()

	resolvedMessage := oneResolved()
	resolvedMessage.Originator = "nowhere"
	_, err := senderOver(t, server.URL).Send(t.Context(), resolvedMessage)
	if !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("Send answered %v, want the rejected sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times for an unknown originator, want 0", n)
	}
}

// TestAnIdentityThatDoesNotCoverTheDestinationIsUnroutable holds the coverage
// check on the one destination this carrier derives a country for: an identity
// that lists countries but not India is a deployment gap, refused before the
// carrier is reached.
func TestAnIdentityThatDoesNotCoverTheDestinationIsUnroutable(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		accepted(w)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.endpoint = server.URL
	cfg.identities = append(cfg.identities, sms.Identity{
		Name:      "us-only",
		Kind:      sms.IdentityTollFree,
		Value:     "+18005550100",
		Countries: []string{"US"},
	})
	cfg.byName["us-only"] = cfg.identities[len(cfg.identities)-1]
	client, err := newClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("building the carrier client: %v", err)
	}

	resolvedMessage := oneResolved()
	resolvedMessage.To = "+919876543210"
	resolvedMessage.Originator = "us-only"
	_, err = newSender(cfg, client).Send(t.Context(), resolvedMessage)
	if !errors.Is(err, sms.ErrUnroutable) {
		t.Fatalf("Send answered %v, want the unroutable sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times for an identity that cannot serve the destination, want 0", n)
	}
}

// errorServer answers every request with one vendor exception.
func errorServer(kind, message string, status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(status)
		body, _ := json.Marshal(map[string]string{"__type": kind, "Message": message})
		_, _ = w.Write(body)
	}))
}

// TestTheExceptionTableIsTheDesigns holds the per-exception classification: the
// vendor's exception type, and nothing about its text, decides the sentinel.
func TestTheExceptionTableIsTheDesigns(t *testing.T) {
	for _, c := range []struct {
		kind     string
		sentinel error
	}{
		{"ThrottlingException", sms.ErrThrottled},
		{"ValidationException", sms.ErrRejected},
		{"AccessDeniedException", sms.ErrUnroutable},
		{"ResourceNotFoundException", sms.ErrUnroutable},
		{"ConflictException", sms.ErrUnroutable},
		{"ServiceQuotaExceededException", sms.ErrUnroutable},
	} {
		t.Run(c.kind, func(t *testing.T) {
			server := errorServer(c.kind, "the vendor said no", http.StatusBadRequest)
			defer server.Close()

			_, err := senderOver(t, server.URL).Send(t.Context(), oneResolved())
			if !errors.Is(err, c.sentinel) {
				t.Fatalf("Send answered %v, want %v for %s", err, c.sentinel, c.kind)
			}
			if !strings.Contains(err.Error(), c.kind) {
				t.Errorf("the error text %q does not name the exception %s", err.Error(), c.kind)
			}
		})
	}
}

// TestAnUnlistedExceptionIsUncertain holds the fail-closed direction: an exception
// type the table does not name, a server failure and a transport failure are all
// uncertain, never a definite throttle or refusal.
func TestAnUnlistedExceptionIsUncertain(t *testing.T) {
	for name, server := range map[string]*httptest.Server{
		"an unlisted exception": errorServer("SomethingNobodyListed", "no", http.StatusBadRequest),
		"a server failure":      errorServer("InternalServerException", "no", http.StatusInternalServerError),
	} {
		t.Run(name, func(t *testing.T) {
			s := senderOver(t, server.URL)
			defer server.Close()
			_, err := s.Send(t.Context(), oneResolved())
			if !errors.Is(err, sms.ErrUncertain) {
				t.Fatalf("Send answered %v, want the uncertain sentinel", err)
			}
			if errors.Is(err, sms.ErrThrottled) || errors.Is(err, sms.ErrRejected) || errors.Is(err, sms.ErrUnroutable) {
				t.Errorf("Send classified %v as a definite outcome; it is not one the table names", err)
			}
		})
	}
}

// TestATransportFailureIsUncertain holds that a call that did not complete — a
// network error and a timeout alike — falls to the uncertain sentinel, the
// direction which never resends a message the carrier may already hold.
func TestATransportFailureIsUncertain(t *testing.T) {
	t.Run("a network error", func(t *testing.T) {
		// A port nothing listens on: the dial fails inside the SDK.
		_, err := senderOver(t, "http://127.0.0.1:1").Send(t.Context(), oneResolved())
		if !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("Send answered %v, want the uncertain sentinel", err)
		}
	})

	t.Run("a timeout", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-release
			accepted(w)
		}))
		defer server.Close()
		defer close(release)

		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		_, err := senderOver(t, server.URL).Send(ctx, oneResolved())
		if !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("Send answered %v, want the uncertain sentinel for a call the context ended", err)
		}
	})
}

// TestACancelledContextDoesNotReachTheCarrier holds that the adapter passes the
// caller's context to the vendor call: a send whose context is already ended is
// refused without the endpoint being reached.
func TestACancelledContextDoesNotReachTheCarrier(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		accepted(w)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := senderOver(t, server.URL).Send(ctx, oneResolved())
	if !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("a cancelled send answered %v, want the uncertain sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times for a cancelled send, want 0", n)
	}
}

// TestTheCarrierIsCalledExactlyOnceEvenWhenItFails holds the no-retry rule: the
// vendor interface is not idempotent, so a failing call is never made again,
// neither by this module nor by the SDK's own retry, which is forced to one
// attempt.
func TestTheCarrierIsCalledExactlyOnceEvenWhenItFails(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		// A server error, the shape the SDK's retry would have picked up.
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"__type":"InternalServerException","Message":"no"}`)
	}))
	defer server.Close()

	if _, err := senderOver(t, server.URL).Send(t.Context(), oneResolved()); !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("Send answered %v, want the uncertain sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("the carrier was called %d times, want exactly one: the interface is not idempotent", n)
	}
}

// TestTheBodyAndParametersReachNoErrorText holds that neither the message body nor
// a parameter value reaches the text of a failure, even when the vendor echoes
// them back in its own message. The access key is never carried into the request
// body; the secret is checked too, since the signing header is the only place it
// appears.
func TestTheBodyAndParametersReachNoErrorText(t *testing.T) {
	const code = "135790"
	echoed := "the body your code is " + code + " and the key " + testAccessKeySecret + " were rejected"
	server := errorServer("ValidationException", echoed, http.StatusBadRequest)
	defer server.Close()

	_, err := senderOver(t, server.URL).Send(t.Context(), oneResolved())
	if err == nil {
		t.Fatal("Send answered no error")
	}
	for _, secret := range []string{code, "your code is", testAccessKeySecret} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error text %q carries %q", err.Error(), secret)
		}
	}
}

// TestOneIdentifierReachesTheCarrierOnce holds the dedupe: one identifier reaches
// the carrier once, and a second call is answered with the first call's outcome.
func TestOneIdentifierReachesTheCarrierOnce(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		accepted(w)
	}))
	defer server.Close()

	s := senderOver(t, server.URL)
	message := oneResolved()
	first, firstErr := s.Send(t.Context(), message)
	second, secondErr := s.Send(t.Context(), message)
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("the carrier was called %d times for one identifier, want 1", n)
	}
	if firstErr != nil || secondErr != nil {
		t.Fatalf("the sends failed: %v and %v", firstErr, secondErr)
	}
	if first != second {
		t.Errorf("the second call was answered %+v, want the first call's %+v", second, first)
	}
}

// TestDistinctIdentifiersEachReachTheCarrier holds that a resend, which builds a
// new challenge and so a new identifier, is not held back by the one before it.
func TestDistinctIdentifiersEachReachTheCarrier(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		accepted(w)
	}))
	defer server.Close()

	s := senderOver(t, server.URL)
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

// TestConcurrentCallsForOneIdentifierReachTheCarrierOnce holds that calls for one
// identifier that arrive together still reach the carrier once.
func TestConcurrentCallsForOneIdentifierReachTheCarrierOnce(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		accepted(w)
	}))
	defer server.Close()

	s := senderOver(t, server.URL)
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

// TestADeclarationRendersItsBodyAndSends holds the whole path a body carrier
// takes: a declaration rendered by the registry becomes the MessageBody, and the
// parameter values appear nowhere but there.
func TestADeclarationRendersItsBodyAndSends(t *testing.T) {
	declaration, err := sms.NewDeclaration(map[string]sms.TemplateSpec{
		"authn.code.login": {
			Kind:   sms.KindVerification,
			Params: []string{"code", "ttl_minutes"},
			Bodies: map[string]string{"en-US": "your code is ${code}, valid for ${ttl_minutes} minutes"},
		},
	})
	if err != nil {
		t.Fatalf("NewDeclaration: %v", err)
	}
	resolvedMessage, err := declaration.Resolve(sms.Message{
		To:       "+14155550100",
		Template: "authn.code.login",
		Locale:   "en-US",
		Params:   map[string]string{"code": "135790", "ttl_minutes": "5"},
		DedupeID: "chal-rendered",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var got wireRequest
	server := recordingServer(t, &got)
	defer server.Close()
	if _, err := senderOver(t, server.URL).Send(t.Context(), resolvedMessage); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.MessageBody != "your code is 135790, valid for 5 minutes" {
		t.Errorf("MessageBody = %q, want the rendered body", got.MessageBody)
	}
}
