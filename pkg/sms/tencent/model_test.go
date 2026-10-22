package tencent

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// The carrier's declaration: Tencent sends by the vendor template number,
// reviews every template, and the deployment sends under one signature.
func TestTheCarrierDeclaresItsModel(t *testing.T) {
	model := newTestSender(t, "127.0.0.1:1").Model()
	if !model.Content.TemplateCode || model.Content.TextBody {
		t.Errorf("Content = %+v, want the vendor template code and no text body", model.Content)
	}
	if !model.Review.PerTemplate {
		t.Error("Review.PerTemplate is false, yet Tencent reviews every template")
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

// A codes-only declaration — the shape this carrier has always sent under, with
// no body and a vendor number per language — still resolves and goes out as one
// SendSms carrying that number.
func TestACodesOnlyDeclarationResolvesAndSends(t *testing.T) {
	declaration, err := sms.NewDeclaration(map[string]sms.TemplateSpec{
		"authn.code.login": {
			Kind:   sms.KindVerification,
			Params: []string{"code"},
			Codes:  map[string]string{"zh-CN": "1001"},
		},
	})
	if err != nil {
		t.Fatalf("NewDeclaration: %v", err)
	}
	resolved, err := declaration.Resolve(sms.Message{
		To:       "+8613800138000",
		Template: "authn.code.login",
		Locale:   "zh-CN",
		Params:   map[string]string{"code": "123456"},
		DedupeID: "chal-codes-only",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Code != "1001" || resolved.Body != "" {
		t.Fatalf("Resolve answered Code %q and Body %q, want the declared number and no body",
			resolved.Code, resolved.Body)
	}

	var got wireRequest
	host := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("the request body is not the carrier's JSON: %v\n%s", err, data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody())
	})

	s := newTestSender(t, host)
	if _, err := s.Send(t.Context(), resolved); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.TemplateID != "1001" {
		t.Errorf("TemplateId = %q, want the resolved number 1001", got.TemplateID)
	}
	if !reflect.DeepEqual(got.TemplateParamSet, []string{"123456"}) {
		t.Errorf("TemplateParamSet = %v, want the one declared parameter", got.TemplateParamSet)
	}
}
