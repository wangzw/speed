package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// offerRequest is the AddSmsTemplate body the carrier client posts, read back by
// its JSON field names.
type offerRequest struct {
	TemplateName    string
	TemplateContent string
	SmsType         uint64
	Remark          string
}

// approvalRequest is the DescribeSmsTemplateList body, read back by its field
// names.
type approvalRequest struct {
	TemplateIDSet []uint64
}

// retireRequest is the DeleteSmsTemplate body, read back by its field name.
type retireRequest struct {
	TemplateID uint64
}

// offerBody is an AddSmsTemplate answer that carries a template number.
func offerBody(id string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"AddTemplateStatus": map[string]any{"TemplateId": id},
		"RequestId":         "req-1",
	}})
	return string(encoded)
}

// describeBody is a DescribeSmsTemplateList answer holding one entry for id with
// the given review status.
func describeBody(id uint64, status *int64, reply string) string {
	entry := map[string]any{"TemplateId": id}
	if status != nil {
		entry["StatusCode"] = *status
	}
	if reply != "" {
		entry["ReviewReply"] = reply
	}
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"DescribeTemplateStatusSet": []map[string]any{entry},
		"RequestId":                 "req-1",
	}})
	return string(encoded)
}

// describeEmptyBody is a DescribeSmsTemplateList answer holding no entry.
func describeEmptyBody() string {
	return `{"Response":{"DescribeTemplateStatusSet":[],"RequestId":"req-1"}}`
}

// Offer renders the submission body positionally, maps the kind to the vendor's
// template type, and answers the number the vendor filed the template under.
func TestOfferBuildsTheRequestAndAnswersTheNumber(t *testing.T) {
	var got offerRequest
	host := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, offerBody("40001"))
	})
	s := newTestSender(t, host)
	code, err := s.Offer(context.Background(), sms.Submission{
		Name:    "authn.code.login",
		Content: "your code is ${code}, valid for ${ttl_minutes} minutes",
		Kind:    sms.KindVerification,
		Locale:  "zh-CN",
		Params:  []string{"code", "ttl_minutes"},
		Remark:  "login code",
	})
	if err != nil {
		t.Fatalf("offering: %v", err)
	}
	if code != "40001" {
		t.Errorf("Offer answered %q, want the vendor number", code)
	}
	if got.TemplateName != "authn.code.login" {
		t.Errorf("TemplateName is %q, want the submission name", got.TemplateName)
	}
	if got.TemplateContent != "your code is {1}, valid for {2} minutes" {
		t.Errorf("TemplateContent is %q, want the named placeholders rewritten to positions", got.TemplateContent)
	}
	if got.SmsType != smsTypeVerification {
		t.Errorf("SmsType is %d, want the verification type %d", got.SmsType, smsTypeVerification)
	}
	if got.Remark != "login code" {
		t.Errorf("Remark is %q, want the submission remark", got.Remark)
	}
}

// An answer without a template number is uncertain: the submission may have
// landed and this process cannot know the number to read its state under.
func TestOfferWithoutANumberIsUncertain(t *testing.T) {
	s := newTestSender(t, answerServer(t, `{"Response":{"RequestId":"req-1"}}`))
	_, err := s.Offer(context.Background(), sms.Submission{
		Name: "authn.code.login", Content: "${code}", Kind: sms.KindVerification,
		Locale: "zh-CN", Params: []string{"code"},
	})
	if !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("an answer with no number gave %v, want ErrUncertain", err)
	}
}

// The kind without a vendor template type is a caller defect, refused before the
// carrier is reached.
func TestOfferWithAnUnknownKindIsRefused(t *testing.T) {
	var calls int
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, offerBody("40001"))
	})
	s := newTestSender(t, host)
	_, err := s.Offer(context.Background(), sms.Submission{
		Name: "authn.code.login", Content: "${code}", Kind: sms.Kind("advert"),
		Locale: "zh-CN", Params: []string{"code"},
	})
	if !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("an unknown kind gave %v, want ErrRejected", err)
	}
	if calls != 0 {
		t.Errorf("the carrier was called %d times for an unknown kind, want 0", calls)
	}
}

// Approval looks the template up one number at a time and maps the vendor's
// review status to a state of this package's vocabulary.
func TestApprovalLooksUpOneNumberAndMapsTheStatus(t *testing.T) {
	var got approvalRequest
	host := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, describeBody(1001, int64Ptr(statusApproved), ""))
	})
	s := newTestSender(t, host)
	approval, err := s.Approval(context.Background(), "1001")
	if err != nil {
		t.Fatalf("reading the approval: %v", err)
	}
	if !reflect.DeepEqual(got.TemplateIDSet, []uint64{1001}) {
		t.Errorf("TemplateIDSet is %v, want the single number looked up", got.TemplateIDSet)
	}
	if approval.State != sms.ApprovalApproved {
		t.Errorf("the state is %q, want approved", approval.State)
	}
}

// Each vendor review status maps to the state it means, asserted one code at a
// time: 0 is approved and in force, 1 is under review, 2 is approved but not yet
// in force and so must still hold a send back, -1 is turned down, and a code the
// list does not name is uncertain.
func TestEveryReviewStatusMapsToItsState(t *testing.T) {
	cases := []struct {
		name   string
		status int64
		want   sms.ApprovalState
	}{
		{"approved and in force", statusApproved, sms.ApprovalApproved},
		{"under review", statusUnderReview, sms.ApprovalPending},
		{"approved but not in force", statusApprovedNotInForce, sms.ApprovalPending},
		{"turned down", statusRejected, sms.ApprovalRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestSender(t, answerServer(t, describeBody(1001, &c.status, "")))
			approval, err := s.Approval(context.Background(), "1001")
			if err != nil {
				t.Fatalf("reading the approval: %v", err)
			}
			if approval.State != c.want {
				t.Errorf("the status %d is %q, want %q", c.status, approval.State, c.want)
			}
		})
	}
}

// A review status the vendor's list does not name is uncertain rather than
// guessed at.
func TestAnUnlistedReviewStatusIsUncertain(t *testing.T) {
	s := newTestSender(t, answerServer(t, describeBody(1001, int64Ptr(9), "")))
	if _, err := s.Approval(context.Background(), "1001"); !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("an unlisted review status gave %v, want ErrUncertain", err)
	}
}

// A turned-down template carries the reviewer's reason.
func TestARejectedApprovalCarriesTheReason(t *testing.T) {
	s := newTestSender(t, answerServer(t, describeBody(1001, int64Ptr(statusRejected), "the content is not specific enough")))
	approval, err := s.Approval(context.Background(), "1001")
	if err != nil {
		t.Fatalf("reading the approval: %v", err)
	}
	if approval.Reason != "the content is not specific enough" {
		t.Errorf("the reason is %q, want the reviewer's reply", approval.Reason)
	}
}

// A response that names the number nowhere means the vendor no longer holds the
// template: that is a withdrawal, not an error and not a blank state.
func TestAnAbsentTemplateIsWithdrawn(t *testing.T) {
	s := newTestSender(t, answerServer(t, describeEmptyBody()))
	approval, err := s.Approval(context.Background(), "1001")
	if err != nil {
		t.Fatalf("reading the approval: %v", err)
	}
	if approval.State != sms.ApprovalWithdrawn {
		t.Errorf("an absent template reads as %q, want withdrawn", approval.State)
	}
}

// A code that is not a template number is a caller defect: no carrier call is
// made.
func TestApprovalWithANonNumericCodeIsRefused(t *testing.T) {
	var calls int
	host := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, describeEmptyBody())
	})
	s := newTestSender(t, host)
	if _, err := s.Approval(context.Background(), "not-a-number"); !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("a non-numeric code gave %v, want ErrRejected", err)
	}
	if calls != 0 {
		t.Errorf("the carrier was called %d times for a non-numeric code, want 0", calls)
	}
}

// Retire deletes the template the code names, and classifies a carrier error the
// same way a send does.
func TestRetireDeletesTheNumber(t *testing.T) {
	var got retireRequest
	host := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Response":{"RequestId":"req-1"}}`)
	})
	s := newTestSender(t, host)
	if err := s.Retire(context.Background(), "1001"); err != nil {
		t.Fatalf("retiring: %v", err)
	}
	if got.TemplateID != 1001 {
		t.Errorf("TemplateID is %d, want the number retired", got.TemplateID)
	}
}

// A carrier error on a management call is classified: a rate limit is a
// throttle, and a code the table does not name is uncertain.
func TestManagementErrorsAreClassified(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{"a rate limit is throttled", errBody("LimitExceeded.AppDailyLimit"), sms.ErrThrottled},
		{"an unlisted code is uncertain", errBody("InternalError.UnknownError"), sms.ErrUncertain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestSender(t, answerServer(t, c.body))
			if _, err := s.Offer(context.Background(), sms.Submission{
				Name: "authn.code.login", Content: "${code}", Kind: sms.KindVerification,
				Locale: "zh-CN", Params: []string{"code"},
			}); !errors.Is(err, c.want) {
				t.Fatalf("the answer gave %v, want %v", err, c.want)
			}
		})
	}
}

// The credential reaches neither a management failure's text nor what the seam
// answers.
func TestTheCredentialReachesNoManagementError(t *testing.T) {
	const secret = "secret-key-example"
	s := newTestSender(t, answerServer(t, errBody("InternalError.UnknownError")))
	_, err := s.Offer(context.Background(), sms.Submission{
		Name: "authn.code.login", Content: "${code}", Kind: sms.KindVerification,
		Locale: "zh-CN", Params: []string{"code"},
	})
	if err == nil {
		t.Fatal("a carrier error gave no error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the credential is in the error text: %q", err)
	}
}

// The submission body is rewritten from named placeholders to positions in the
// order the parameters are declared.
func TestPositionalRewritesNamedPlaceholders(t *testing.T) {
	cases := []struct {
		name    string
		content string
		params  []string
		want    string
	}{
		{"in order", "${code} then ${ttl_minutes}", []string{"code", "ttl_minutes"}, "{1} then {2}"},
		{"out of order", "${ttl_minutes} then ${code}", []string{"code", "ttl_minutes"}, "{2} then {1}"},
		{"a name repeated in the body", "${code} ${code}", []string{"code"}, "{1} {1}"},
		{"a parameter the body never uses", "only ${code}", []string{"code", "ttl_minutes"}, "only {1}"},
		{"no placeholder at all", "a fixed body", []string{"code"}, "a fixed body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := positional(c.content, c.params)
			if err != nil {
				t.Fatalf("rewriting: %v", err)
			}
			if got != c.want {
				t.Errorf("the rewrite is %q, want %q", got, c.want)
			}
		})
	}
}

// A submission that declares a parameter twice cannot say which slot a
// placeholder means, and a body naming an undeclared parameter would hand the
// vendor a slot whose meaning was never stated: both are caller defects.
func TestPositionalRefusesAmbiguousAndUndeclaredPlaceholders(t *testing.T) {
	cases := []struct {
		name    string
		content string
		params  []string
	}{
		{"a parameter declared twice", "${code}", []string{"code", "code"}},
		{"a placeholder with no parameter", "${other}", []string{"code"}},
		{"an empty placeholder", "${}", []string{"code"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := positional(c.content, c.params); !errors.Is(err, sms.ErrRejected) {
				t.Fatalf("rewriting %q gave %v, want ErrRejected", c.content, err)
			}
		})
	}
}

// int64Ptr is a helper for the review-status tables.
func int64Ptr(v int64) *int64 { return &v }
