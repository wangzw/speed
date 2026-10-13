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

// mustDeclaration freezes the case templates into the declaration a sender runs
// on.
func mustDeclaration() sms.Declaration {
	decl, err := sms.NewDeclaration(validTemplates())
	if err != nil {
		panic(err)
	}
	return decl
}

// testConfig is the configuration the fake-endpoint cases run on: credentials
// that sign a request, and the full declared template set.
func testConfig() resolved {
	return resolved{
		secretID:  "AKIDexample",
		secretKey: "secret-key-example",
		sdkAppID:  "1400006666",
		region:    "ap-guangzhou",
		signName:  "Speed",
		decl:      mustDeclaration(),
	}
}

// message is one SendSms the cases hand the sender.
func message(id string) sms.Message {
	return sms.Message{
		To:       "+8613800138000",
		Template: "authn.code.login",
		Params:   map[string]string{"code": "123456", "ttl_minutes": "5"},
		Locale:   "zh-CN",
		DedupeID: id,
	}
}

// fakeServer runs an HTTP endpoint the carrier client is pointed at, and returns
// the host it listens on.
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

// newTestSender builds the product against a fake endpoint.
func newTestSender(t *testing.T, host string) *sender {
	t.Helper()
	s, err := newSender(testConfig(), options{endpoint: host, scheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// okBody is a SendSms answer for one accepted number.
func okBody() string {
	return `{"Response":{"SendStatusSet":[{"Code":"Ok","SerialNo":"sn-1",` +
		`"PhoneNumber":"+8613800138000"}],"RequestId":"req-1"}}`
}

// statusBody is an answer whose per-number status carries code.
func statusBody(code string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"SendStatusSet": []map[string]any{{"Code": code, "PhoneNumber": "+8613800138000"}},
		"RequestId":     "req-1",
	}})
	return string(encoded)
}

// errBody is an answer whose top level carries a carrier error with code.
func errBody(code string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"Error":     map[string]any{"Code": code, "Message": "denied"},
		"RequestId": "req-1",
	}})
	return string(encoded)
}

// answerServer serves one fixed body to every request.
func answerServer(t *testing.T, body string) string {
	t.Helper()
	return fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
}

// wireRequest is the request the carrier client posts, read back by its JSON
// field names.
type wireRequest struct {
	PhoneNumberSet   []string
	SmsSdkAppID      string
	TemplateID       string
	SignName         string
	TemplateParamSet []string
}

// Send builds the carrier request out of the message: the destination, the
// signature and application, the template the (template, locale) pair selects,
// and the parameters in the order the approved template expects.
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
	m := sms.Message{
		To:       "+8613800138000",
		Template: "authn.code.mfa",
		Params:   map[string]string{"code": "123456", "ttl_minutes": "5"},
		Locale:   "en-US",
		DedupeID: "chal-shape",
	}
	receipt, err := s.Send(context.Background(), m)
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

// An empty language is the caller's defect: this package refuses it before the
// carrier is reached rather than reading it as a default.
func TestAnEmptyLocaleIsRefused(t *testing.T) {
	var calls int32
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)
	m := message("chal-locale")
	m.Locale = ""
	_, err := s.Send(context.Background(), m)
	if !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("an empty language gave %v, want ErrRejected", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times for a message with no language, want 0", n)
	}
}

// The carrier's answer is sorted into the sentinels: its rate-limit codes are
// throttles, a parameter mismatch is a rejection, and every other code is
// uncertain. The codes arrive both as a per-number status and as a top-level
// error, and both shapes are classified the same way.
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
			_, err := s.Send(context.Background(), message("chal-"+c.name))
			if !errors.Is(err, c.want) {
				t.Fatalf("the answer gave %v, want %v", err, c.want)
			}
		})
	}
}

// Every rate-limit code the vendor's list names is throttled, and the one code it
// names for a parameter mismatch is a rejection. The codes are asserted one by
// one against the SDK's own list: a code the adapter sorted elsewhere would fail
// here, which is what holds the classification table to the design's assumption.
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
				if _, err := s.Send(context.Background(), message("chal-"+code)); !errors.Is(err, sms.ErrThrottled) {
					t.Fatalf("the code %s gave %v, want ErrThrottled", code, err)
				}
			}
		})
	}
}

// The parameter-mismatch code is a rejection whether it arrives as a per-number
// status or as a top-level error.
func TestTheParameterMismatchCodeIsARejection(t *testing.T) {
	code := tc.FAILEDOPERATION_TEMPLATEPARAMSETNOTMATCHAPPROVEDTEMPLATE
	for _, body := range []string{statusBody(code), errBody(code)} {
		s := newTestSender(t, answerServer(t, body))
		if _, err := s.Send(context.Background(), message("chal-mismatch")); !errors.Is(err, sms.ErrRejected) {
			t.Fatalf("the mismatch code gave %v, want ErrRejected", err)
		}
	}
}

// A (template, locale) pair the declaration does not cover never reaches the
// carrier: nothing is composed, so the send is a definite rejection and not an
// uncertain one, and the endpoint is never called.
func TestAnUncoveredTemplateIsRefusedWithoutCallingTheCarrier(t *testing.T) {
	var calls int32
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)
	m := message("chal-uncovered")
	m.Locale = "fr-FR" // a language no declared template has a code for
	_, err := s.Send(context.Background(), m)
	if !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("an uncovered template gave %v, want ErrRejected", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the carrier was called %d times for a message with no template, want 0", n)
	}
}

// A send whose server never answers before the context ends is uncertain: the
// carrier's call is not idempotent, so the adapter must not treat the timeout as
// either a definite success or a definite failure.
func TestATimedOutSendIsUncertain(t *testing.T) {
	release := make(chan struct{})
	host := fakeServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	})
	s := newTestSender(t, host)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.Send(ctx, message("chal-timeout"))
	// Let the server handler return, so its server can close, before asserting.
	close(release)
	if !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("a timed-out send gave %v, want ErrUncertain", err)
	}
}

// Neither the verification code nor the credential reaches the error text. The
// code does reach the carrier, which is the whole point of the message, so its
// absence from the error is a choice about what is written and not about what is
// sent.
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

	m := message("chal-leak")
	m.Params = map[string]string{"code": code, "ttl_minutes": "5"}
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

// One identifier reaches the carrier once, and a second call is answered with the
// first call's outcome rather than sending the message again.
func TestOneIdentifierReachesTheCarrierOnce(t *testing.T) {
	var calls int32
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)
	m := message("chal-same")

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

// Distinct identifiers each reach the carrier: a resend builds a new challenge
// and so a new identifier, which is not held back by the one before it.
func TestDistinctIdentifiersEachReachTheCarrier(t *testing.T) {
	var calls int32
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})
	s := newTestSender(t, host)
	for _, id := range []string{"chal-a", "chal-b", "chal-c"} {
		if _, err := s.Send(context.Background(), message(id)); err != nil {
			t.Fatalf("sending %s: %v", id, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("the carrier was called %d times for three identifiers, want 3", n)
	}
}

// Calls for one identifier that arrive together still reach the carrier once: the
// first reserves it and the rest wait for that call's outcome.
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
			if _, err := s.Send(context.Background(), message("chal-race")); err != nil {
				t.Errorf("sending: %v", err)
			}
		}()
	}
	wg.Wait()

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("the carrier was called %d times for one identifier under concurrency, want 1", n)
	}
}
