package aliyun

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

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

// TestOfferCarriesTheTemplateContentAndSignature holds the CreateSmsTemplate
// request shape, with the body's ${name} placeholders intact.
func TestOfferCarriesTheTemplateContentAndSignature(t *testing.T) {
	var got struct {
		method, name, content, templateType, signName, remark string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.method = r.Method
		got.name = r.FormValue("TemplateName")
		got.content = r.FormValue("TemplateContent")
		got.templateType = r.FormValue("TemplateType")
		got.signName = r.FormValue("RelatedSignName")
		got.remark = r.FormValue("Remark")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","TemplateCode":"SMS_10000","RequestId":"req-1"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	code, err := s.Offer(t.Context(), oneSubmission())
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if got.method != http.MethodPost {
		t.Errorf("the carrier was called with %s, want POST", got.method)
	}
	if got.name != "login code" {
		t.Errorf("TemplateName = %q, want login code", got.name)
	}
	if got.content != "your code is ${code}, valid for ${ttl_minutes} minutes" {
		t.Errorf("TemplateContent = %q, want the submission body verbatim", got.content)
	}
	if got.templateType != "0" {
		t.Errorf("TemplateType = %q, want 0 for a verification template", got.templateType)
	}
	if got.signName != "Speed" {
		t.Errorf("RelatedSignName = %q, want the configured signature Speed", got.signName)
	}
	if got.remark != "the sign-in code" {
		t.Errorf("Remark = %q, want the submission's remark", got.remark)
	}
	if code != "SMS_10000" {
		t.Errorf("Offer = %q, want the carrier template code SMS_10000", code)
	}
}

// TestTheKindMapsToTheTemplateType checks the mapping from this package's kind vocabulary to Aliyun's template type.
func TestTheKindMapsToTheTemplateType(t *testing.T) {
	for _, c := range []struct {
		kind sms.Kind
		want string
	}{
		{sms.KindVerification, "0"},
		{sms.KindNotification, "1"},
		{sms.KindMarketing, "2"},
	} {
		t.Run(string(c.kind), func(t *testing.T) {
			var got string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				got = r.FormValue("TemplateType")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"Code":"OK","TemplateCode":"SMS_1"}`)
			}))
			defer server.Close()

			s := senderOver(t, server.Listener.Addr().String(), "http")
			sub := oneSubmission()
			sub.Kind = c.kind
			if _, err := s.Offer(t.Context(), sub); err != nil {
				t.Fatalf("Offer: %v", err)
			}
			if got != c.want {
				t.Errorf("TemplateType for %s = %q, want %q", c.kind, got, c.want)
			}
		})
	}
}

// TestOfferRefusesAnUnknownKind checks that a kind this carrier has no template type for is refused before the carrier is reached.
func TestOfferRefusesAnUnknownKind(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","TemplateCode":"SMS_1"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	sub := oneSubmission()
	sub.Kind = sms.Kind("something-else")
	if _, err := s.Offer(t.Context(), sub); !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("Offer answered %v, want the rejected sentinel", err)
	}
	if called {
		t.Error("the carrier was called for a submission with no template type")
	}
}

// TestOfferFailuresAreUncertain checks that a status that is not OK, a response
// with no template code and a call that did not complete are all uncertain.
func TestOfferFailuresAreUncertain(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		reply  string
	}{
		"a status that is not OK": {http.StatusOK, `{"Code":"isv.SMS_TEMPLATE_ILLEGAL","Message":"no"}`},
		"a transport failure":     {http.StatusInternalServerError, `{"Code":"InternalError"}`},
		"no template code":        {http.StatusOK, `{"Code":"OK"}`},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.reply)
			}))
			defer server.Close()

			s := senderOver(t, server.Listener.Addr().String(), "http")
			if _, err := s.Offer(t.Context(), oneSubmission()); !errors.Is(err, sms.ErrUncertain) {
				t.Fatalf("Offer answered %v, want the uncertain sentinel", err)
			}
		})
	}

	t.Run("a call that did not complete", func(t *testing.T) {
		s := senderOver(t, "127.0.0.1:1", "http")
		if _, err := s.Offer(t.Context(), oneSubmission()); !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("Offer answered %v, want the uncertain sentinel", err)
		}
	})
}

// TestTheApprovalStatesFollowTheReviewCode checks that Aliyun's TemplateStatus
// 0, 1, 2 and 10 become pending, approved, rejected and withdrawn.
func TestTheApprovalStatesFollowTheReviewCode(t *testing.T) {
	for _, c := range []struct {
		status string
		want   sms.ApprovalState
	}{
		{"0", sms.ApprovalPending},
		{"1", sms.ApprovalApproved},
		{"2", sms.ApprovalRejected},
		{"10", sms.ApprovalWithdrawn},
	} {
		t.Run("status "+c.status, func(t *testing.T) {
			server := httptest.NewServer(jsonReply(`{"Code":"OK","TemplateStatus":` + mustJSON(c.status) + `}`))
			defer server.Close()

			s := senderOver(t, server.Listener.Addr().String(), "http")
			got, err := s.Approval(t.Context(), "SMS_1")
			if err != nil {
				t.Fatalf("Approval: %v", err)
			}
			if got.State != c.want {
				t.Errorf("State = %q, want %q for the review status %s", got.State, c.want, c.status)
			}
		})
	}
}

// TestAnUnknownApprovalStatusIsUncertain checks that a review status this carrier does not know is uncertain rather than guessed.
func TestAnUnknownApprovalStatusIsUncertain(t *testing.T) {
	server := httptest.NewServer(jsonReply(`{"Code":"OK","TemplateStatus":"7"}`))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	if _, err := s.Approval(t.Context(), "SMS_1"); !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("Approval answered %v, want the uncertain sentinel", err)
	}
}

// TestTheApprovalCarriesTheRejectReason checks that the carrier's refusal text comes back as the approval reason.
func TestTheApprovalCarriesTheRejectReason(t *testing.T) {
	server := httptest.NewServer(jsonReply(`{"Code":"OK","TemplateStatus":"2",` +
		`"AuditInfo":{"RejectInfo":"contains a typo"}}`))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	got, err := s.Approval(t.Context(), "SMS_1")
	if err != nil {
		t.Fatalf("Approval: %v", err)
	}
	if got.State != sms.ApprovalRejected {
		t.Errorf("State = %q, want rejected", got.State)
	}
	if got.Reason != "contains a typo" {
		t.Errorf("Reason = %q, want the carrier's reject text", got.Reason)
	}
}

// TestRetireDeletesTheTemplateByCode holds the DeleteSmsTemplate request shape.
func TestRetireDeletesTheTemplateByCode(t *testing.T) {
	var got struct {
		method, templateCode string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.method = r.Method
		got.templateCode = r.FormValue("TemplateCode")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","TemplateCode":"SMS_1"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	if err := s.Retire(t.Context(), "SMS_1"); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if got.method != http.MethodPost {
		t.Errorf("the carrier was called with %s, want POST", got.method)
	}
	if got.templateCode != "SMS_1" {
		t.Errorf("TemplateCode = %q, want the code handed to Retire", got.templateCode)
	}
}

// TestRetireFailuresAreUncertain checks that a status that is not OK and a call that did not complete are uncertain.
func TestRetireFailuresAreUncertain(t *testing.T) {
	t.Run("a status that is not OK", func(t *testing.T) {
		server := httptest.NewServer(jsonReply(`{"Code":"isv.SMS_TEMPLATE_ILLEGAL","Message":"no"}`))
		defer server.Close()

		s := senderOver(t, server.Listener.Addr().String(), "http")
		if err := s.Retire(t.Context(), "SMS_1"); !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("Retire answered %v, want the uncertain sentinel", err)
		}
	})

	t.Run("a call that did not complete", func(t *testing.T) {
		s := senderOver(t, "127.0.0.1:1", "http")
		if err := s.Retire(t.Context(), "SMS_1"); !errors.Is(err, sms.ErrUncertain) {
			t.Fatalf("Retire answered %v, want the uncertain sentinel", err)
		}
	})
}

// TestAProviderCallIsNotRetried checks that a failing management call is never made again.
func TestAProviderCallIsNotRetried(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"Code":"InternalError"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	if _, err := s.Offer(t.Context(), oneSubmission()); !errors.Is(err, sms.ErrUncertain) {
		t.Fatalf("Offer answered %v, want the uncertain sentinel", err)
	}
	if calls != 1 {
		t.Errorf("the carrier was called %d times, want exactly one: the interface is not idempotent", calls)
	}
}
