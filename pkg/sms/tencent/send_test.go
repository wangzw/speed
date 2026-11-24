package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"

	"github.com/vislake/speed/pkg/sms"
)

func testConfig() resolved {
	return resolved{
		secretID:  "AKIDexample",
		secretKey: "secret-key-example",
		sdkAppID:  "1400006666",
		region:    "ap-guangzhou",
		signName:  "Speed",
	}
}

func resolvedMessage(id string) sms.Resolved {
	return sms.Resolved{
		To:       "+8613800138000",
		Template: "authn.code.login",
		Locale:   "zh-CN",
		Code:     "1001",
		Params:   map[string]string{"code": "123456", "ttl_minutes": "5"},
		Ordered:  []string{"123456", "5"},
		DedupeID: id,
	}
}

func fakeServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func newTestSender(t *testing.T, host string) *sender {
	t.Helper()
	s, err := newSender(testConfig(), options{endpoint: host, scheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func okBody() string {
	return `{"Response":{"SendStatusSet":[{"Code":"Ok","SerialNo":"sn-1",` +
		`"PhoneNumber":"+8613800138000"}],"RequestId":"req-1"}}`
}

func statusBody(code string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"SendStatusSet": []map[string]any{{"Code": code, "PhoneNumber": "+8613800138000"}},
		"RequestId":     "req-1",
	}})
	return string(encoded)
}

func errBody(code string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"Error":     map[string]any{"Code": code, "Message": "denied"},
		"RequestId": "req-1",
	}})
	return string(encoded)
}

func answerServer(t *testing.T, body string) string {
	t.Helper()
	return fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
}

type wireRequest struct {
	PhoneNumberSet   []string
	SmsSdkAppID      string
	TemplateID       string
	SignName         string
	TemplateParamSet []string
}

// TestSendBuildsTheRequestFromTheMessage checks that Send builds the carrier
// request from the resolved message, with the values in the approved order.
func TestSendBuildsTheRequestFromTheMessage(t *testing.T) {
	var got wireRequest
	var calls int32
	host := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("the request body is not the carrier's JSON: %v\n%s", err, data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)
	r := sms.Resolved{
		To:       "+8613800138000",
		Template: "authn.code.mfa",
		Locale:   "en-US",
		Code:     "1004",
		Params:   map[string]string{"code": "123456", "ttl_minutes": "5"},
		Ordered:  []string{"123456", "5"},
		DedupeID: "chal-shape",
	}
	receipt, err := s.Send(context.Background(), r)
	if err != nil {
		t.Fatalf("sending: %v", err)
	}
	if !reflect.DeepEqual(got.PhoneNumberSet, []string{"+8613800138000"}) {
		t.Errorf("PhoneNumberSet is %v, want the destination", got.PhoneNumberSet)
	}
	if got.SignName != "Speed" {
		t.Errorf("SignName is %q, want the configured signature", got.SignName)
	}
	if got.SmsSdkAppID != "1400006666" {
		t.Errorf("SmsSdkAppId is %q, want the configured application", got.SmsSdkAppID)
	}
	if got.TemplateID != "1004" {
		t.Errorf("TemplateId is %q, want the authn.code.mfa/en-US template", got.TemplateID)
	}
	if !reflect.DeepEqual(got.TemplateParamSet, []string{"123456", "5"}) {
		t.Errorf("TemplateParamSet is %v, want the code then the minutes", got.TemplateParamSet)
	}
	if receipt.ProviderID != "sn-1" {
		t.Errorf("the receipt names %q, want the send serial number", receipt.ProviderID)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("the carrier was called %d times, want 1", n)
	}
}

// TestSendClassifiesTheCarrierAnswer checks that rate-limit codes are throttles, a parameter
// mismatch is a rejection and every other code is uncertain, in both answer shapes.
func TestSendClassifiesTheCarrierAnswer(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{"a per-number rate limit is throttled", statusBody("LimitExceeded.PhoneNumberDailyLimit"), sms.ErrThrottled},
		{"a global rate limit is throttled", statusBody("LimitExceeded.GlobalSmsSendingRateLimit"), sms.ErrThrottled},
		{"a parameter mismatch is a rejection", statusBody("FailedOperation.TemplateParamSetNotMatchApprovedTemplate"), sms.ErrRejected},
		{"an unlisted per-number code is uncertain", statusBody("FailedOperation.JsonParseFail"), sms.ErrUncertain},
		{"a top-level rate limit is throttled", errBody("LimitExceeded.AppDailyLimit"), sms.ErrThrottled},
		{"a top-level mismatch is a rejection", errBody("FailedOperation.TemplateParamSetNotMatchApprovedTemplate"), sms.ErrRejected},
		{"a top-level unlisted code is uncertain", errBody("InternalError.UnknownError"), sms.ErrUncertain},
		{"an answer with no status is uncertain", `{"Response":{"RequestId":"req-1"}}`, sms.ErrUncertain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestSender(t, answerServer(t, c.body))
			_, err := s.Send(context.Background(), resolvedMessage("chal-"+c.name))
			if !errors.Is(err, c.want) {
				t.Fatalf("the answer gave %v, want %v", err, c.want)
			}
		})
	}
}

// TestEveryRateLimitCodeIsThrottled checks that every rate-limit code the vendor's list
// names is throttled and the parameter-mismatch code is a rejection.
func TestEveryRateLimitCodeIsThrottled(t *testing.T) {
	codes := []string{
		tc.LIMITEXCEEDED_APPCOUNTRYORREGIONDAILYLIMIT,
		tc.LIMITEXCEEDED_APPCOUNTRYORREGIONINBLACKLIST,
		tc.LIMITEXCEEDED_APPDAILYLIMIT,
		tc.LIMITEXCEEDED_APPGLOBALDAILYLIMIT,
		tc.LIMITEXCEEDED_APPMAINLANDCHINADAILYLIMIT,
		tc.LIMITEXCEEDED_DAILYLIMIT,
		tc.LIMITEXCEEDED_DELIVERYFREQUENCYLIMIT,
		tc.LIMITEXCEEDED_GLOBALSMSSENDINGRATELIMIT,
		tc.LIMITEXCEEDED_PHONENUMBERCOUNTLIMIT,
		tc.LIMITEXCEEDED_PHONENUMBERDAILYLIMIT,
		tc.LIMITEXCEEDED_PHONENUMBERONEHOURLIMIT,
		tc.LIMITEXCEEDED_PHONENUMBERSAMECONTENTDAILYLIMIT,
		tc.LIMITEXCEEDED_PHONENUMBERTHIRTYSECONDLIMIT,
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			for _, body := range []string{statusBody(code), errBody(code)} {
				s := newTestSender(t, answerServer(t, body))
				if _, err := s.Send(context.Background(), resolvedMessage("chal-"+code)); !errors.Is(err, sms.ErrThrottled) {
					t.Fatalf("the code %s gave %v, want ErrThrottled", code, err)
				}
			}
		})
	}
}

// TestTheParameterMismatchCodeIsARejection checks that the mismatch code is a rejection in both answer shapes.
func TestTheParameterMismatchCodeIsARejection(t *testing.T) {
	code := tc.FAILEDOPERATION_TEMPLATEPARAMSETNOTMATCHAPPROVEDTEMPLATE
	for _, body := range []string{statusBody(code), errBody(code)} {
		s := newTestSender(t, answerServer(t, body))
		if _, err := s.Send(context.Background(), resolvedMessage("chal-mismatch")); !errors.Is(err, sms.ErrRejected) {
			t.Fatalf("the mismatch code gave %v, want ErrRejected", err)
		}
	}
}

// TestATimedOutSendIsUncertain checks that a send whose server never answers before the context ends is uncertain.
func TestATimedOutSendIsUncertain(t *testing.T) {
	release := make(chan struct{})
	host := fakeServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	})
	s := newTestSender(t, host)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.Send(ctx, resolvedMessage("chal-timeout"))
	close(release)
	if !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("a timed-out send gave %v, want ErrUncertain", err)
	}
}

// TestNeitherTheCodeNorTheCredentialReachesTheErrorText checks that neither the
// verification code nor the credential reaches the error text.
func TestNeitherTheCodeNorTheCredentialReachesTheErrorText(t *testing.T) {
	const code = "424242"
	const secret = "secret-key-example"

	var sent wireRequest
	s := newTestSender(t, fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, errBody("InternalError.UnknownError"))
	}))

	m := resolvedMessage("chal-leak")
	m.Params = map[string]string{"code": code, "ttl_minutes": "5"}
	m.Ordered = []string{code, "5"}
	_, err := s.Send(context.Background(), m)
	if err == nil {
		t.Fatal("an answer carrying an error code gave no error")
	}
	if !reflect.DeepEqual(sent.TemplateParamSet, []string{code, "5"}) {
		t.Fatalf("the code did not reach the carrier: %v", sent.TemplateParamSet)
	}
	text := err.Error()
	if strings.Contains(text, code) {
		t.Errorf("the verification code is in the error text: %q", text)
	}
	if strings.Contains(text, secret) {
		t.Errorf("the credential is in the error text: %q", text)
	}
}

// TestOneIdentifierReachesTheCarrierOnce checks that one identifier reaches the
// carrier once and a second call is answered with the first call's outcome.
func TestOneIdentifierReachesTheCarrierOnce(t *testing.T) {
	var calls int32
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)
	m := resolvedMessage("chal-same")

	firstReceipt, firstErr := s.Send(context.Background(), m)
	secondReceipt, secondErr := s.Send(context.Background(), m)

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

// TestALifetimeTheCarrierCannotHonourIsRefused checks that a message asking for a lifetime is refused before the vendor is called.
func TestALifetimeTheCarrierCannotHonourIsRefused(t *testing.T) {
	var calls int32
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)
	m := resolvedMessage("chal-lifetime")
	m.ValidFor = 5 * time.Minute

	if _, err := s.Send(context.Background(), m); !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("send answered %v, want the rejected sentinel", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times, want none", n)
	}
}

// TestDistinctIdentifiersEachReachTheCarrier checks that distinct identifiers each reach the carrier.
func TestDistinctIdentifiersEachReachTheCarrier(t *testing.T) {
	var calls int32
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)
	for _, id := range []string{"chal-a", "chal-b", "chal-c"} {
		if _, err := s.Send(context.Background(), resolvedMessage(id)); err != nil {
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
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Send(context.Background(), resolvedMessage("chal-race")); err != nil {
				t.Errorf("sending: %v", err)
			}
		}()
	}
	wg.Wait()

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("the carrier was called %d times for one identifier under concurrency, want 1", n)
	}
}
