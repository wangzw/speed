package aws

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestTheCarrierDeclaresItsModel holds the carrier's capability declaration: it
// sends a rendered body, reviews no template, lets a send pick its identity,
// accepts the Indian template identifier, bounds the body at 1600 runes and
// reports asynchronous delivery.
func TestTheCarrierDeclaresItsModel(t *testing.T) {
	model := newSender(testConfig(), nil).Model()
	if !model.Content.TextBody || model.Content.TemplateCode {
		t.Errorf("Content = %+v, want the rendered text body and no template code", model.Content)
	}
	if model.Review.PerTemplate {
		t.Error("Review.PerTemplate is true, yet AWS reviews no template")
	}
	if !model.Review.Originator {
		t.Error("Review.Originator is false, yet a send may name any declared identity")
	}
	if model.MaxBodyRunes != 1600 {
		t.Errorf("MaxBodyRunes = %d, want 1600", model.MaxBodyRunes)
	}
	if !model.AsyncReceipt {
		t.Error("AsyncReceipt is false, yet the vendor's delivery state arrives asynchronously")
	}
	if len(model.Countries) != 0 {
		t.Errorf("Countries = %v, want none: each identity states its own coverage", model.Countries)
	}
	if len(model.AttributeKeys) != 1 || model.AttributeKeys[0] != inTemplateID {
		t.Errorf("AttributeKeys = %v, want the Indian template identifier alone", model.AttributeKeys)
	}
	if len(model.Identities) != 2 {
		t.Fatalf("Identities = %+v, want the two the configuration declares", model.Identities)
	}
	primary := model.Identities[0]
	if primary.Name != "primary" || primary.Kind != sms.IdentityPool || primary.Value != "pool-primary" {
		t.Errorf("the first identity is %+v, want the configured pool", primary)
	}
	if len(primary.Countries) != 2 || primary.Countries[0] != "US" || primary.Countries[1] != "IN" {
		t.Errorf("the first identity's Countries = %v, want the configured coverage", primary.Countries)
	}
	if primary.Attributes[inEntityID] != "entity-7" {
		t.Errorf("the first identity's Attributes = %v, want the configured entity identifier", primary.Attributes)
	}
}

// TestTheModelHandsOutCopies holds that a caller cannot change what the process
// holds through the model it was handed.
func TestTheModelHandsOutCopies(t *testing.T) {
	s := newSender(testConfig(), nil)
	first := s.Model()
	first.Identities[0].Countries[0] = "ZZ"
	first.Identities[0].Attributes[inEntityID] = "changed"

	second := s.Model()
	if second.Identities[0].Countries[0] != "US" {
		t.Errorf("the second model's country = %q, want the configured US", second.Identities[0].Countries[0])
	}
	if second.Identities[0].Attributes[inEntityID] != "entity-7" {
		t.Errorf("the second model's attribute = %q, want the configured entity identifier",
			second.Identities[0].Attributes[inEntityID])
	}
}

// TestTheManagementSeamCallsNoVendor holds that offering, approving and retiring
// are local: AWS has no per-template review, so none of the three reaches the
// vendor, and the answers are the ones the registry expects.
func TestTheManagementSeamCallsNoVendor(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		accepted(w)
	}))
	defer server.Close()

	s := senderOver(t, server.URL)
	submission := sms.Submission{
		Name:    "login code",
		Content: "your code is ${code}",
		Kind:    sms.KindVerification,
		Locale:  "en-US",
		Params:  []string{"code"},
	}

	code, err := s.Offer(t.Context(), submission)
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if code != "" {
		t.Errorf("Offer = %q, want the empty code the registry files the submission under", code)
	}

	approval, err := s.Approval(t.Context(), "")
	if err != nil {
		t.Fatalf("Approval: %v", err)
	}
	if approval.State != sms.ApprovalNotRequired {
		t.Errorf("Approval.State = %q, want %q", approval.State, sms.ApprovalNotRequired)
	}

	if err := s.Retire(t.Context(), ""); err != nil {
		t.Fatalf("Retire: %v", err)
	}

	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("the vendor was called %d times from the management seam, want 0", n)
	}
}
