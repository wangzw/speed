package aliyun

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestTheCarrierDeclaresItsModel checks the carrier's declaration: it sends by
// template code, reviews every template, and names one signature identity.
func TestTheCarrierDeclaresItsModel(t *testing.T) {
	model := newSender(resolved{signName: "Speed"}, nil).Model()
	if !model.Content.TemplateCode || model.Content.TextBody {
		t.Errorf("Content = %+v, want the vendor template code and no text body", model.Content)
	}
	if !model.Review.PerTemplate {
		t.Error("Review.PerTemplate is false, yet Aliyun reviews every template")
	}
	if model.Review.Originator {
		t.Error("Review.Originator is true, yet a send goes out under the configured signature alone")
	}
	if model.MaxBodyRunes != 0 || model.AsyncReceipt || len(model.AttributeKeys) != 0 || len(model.Countries) != 0 {
		t.Errorf("Model = %+v, want no body bound, no asynchronous receipt, no attribute keys and no country list",
			model)
	}
	if len(model.Identities) != 1 {
		t.Fatalf("Identities = %+v, want the deployment's one signature", model.Identities)
	}
	identity := model.Identities[0]
	if identity.Kind != sms.IdentitySignName {
		t.Errorf("the identity's Kind = %q, want %q", identity.Kind, sms.IdentitySignName)
	}
	if identity.Value != "Speed" || identity.Name != "Speed" {
		t.Errorf("the identity is %+v, want the configured signature Speed", identity)
	}
}

// TestACodesOnlyDeclarationResolvesAndSends checks that a codes-only declaration
// resolves and goes out as one SendSms carrying that code.
func TestACodesOnlyDeclarationResolvesAndSends(t *testing.T) {
	declaration, err := sms.NewDeclaration(map[string]sms.TemplateSpec{
		"authn.code.login": {
			Kind:   sms.KindVerification,
			Params: []string{"code"},
			Codes:  map[string]string{"zh-CN": "SMS_10000"},
		},
	})
	if err != nil {
		t.Fatalf("NewDeclaration: %v", err)
	}
	resolved, err := declaration.Resolve(sms.Message{
		To:       "+8613800000000",
		Template: "authn.code.login",
		Locale:   "zh-CN",
		Params:   map[string]string{"code": "123456"},
		DedupeID: "chal-codes-only",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Code != "SMS_10000" || resolved.Body != "" {
		t.Fatalf("Resolve answered Code %q and Body %q, want the declared code and no body",
			resolved.Code, resolved.Body)
	}

	var got struct{ templateCode, templateParam string }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.templateCode = r.FormValue("TemplateCode")
		got.templateParam = r.FormValue("TemplateParam")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","BizId":"biz-1"}`)
	}))
	defer server.Close()

	s := senderOver(t, server.Listener.Addr().String(), "http")
	if _, err := s.Send(t.Context(), resolved); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.templateCode != "SMS_10000" {
		t.Errorf("TemplateCode = %q, want the resolved code SMS_10000", got.templateCode)
	}
	if got.templateParam != `{"code":"123456"}` {
		t.Errorf("TemplateParam = %q, want the one declared parameter", got.templateParam)
	}
}
