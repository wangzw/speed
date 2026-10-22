package admin

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// The carrier route answers the mounted provider's declaration, shaped field by
// field: the model is read once from the Provider seam and every part of it —
// content support, review support, identities, countries, attribute keys, the
// body bound and the receipt mode — reaches the answer.
func TestTheCarrierRouteAnswersTheMountedModel(t *testing.T) {
	r := newRig(t)
	r.provider.name = "aws"
	r.provider.model = sms.CarrierModel{
		Content: sms.ContentSupport{TextBody: true},
		Review:  sms.ReviewSupport{Originator: true},
		Identities: []sms.Identity{{
			Name:       "us-main",
			Kind:       sms.IdentityMessagingService,
			Value:      "svc-1",
			Countries:  []string{"US"},
			Attributes: map[string]string{"IN_ENTITY_ID": "entity-1"},
		}},
		Countries:     []string{"US", "CA"},
		AttributeKeys: []string{"IN_TEMPLATE_ID"},
		MaxBodyRunes:  1600,
		AsyncReceipt:  true,
	}

	w := r.do(http.MethodGet, pathCarrier, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got carrierView
	decode(t, w, &got)
	if got.Name != "aws" {
		t.Errorf("name = %q, want the mounted provider's name", got.Name)
	}
	if got.Content.TemplateCode || !got.Content.TextBody {
		t.Errorf("content = %+v, want the body-only support the model declares", got.Content)
	}
	if got.Review.PerTemplate || !got.Review.Originator {
		t.Errorf("review = %+v, want no per-template review and a selectable originator", got.Review)
	}
	if !got.AsyncReceipt || got.MaxBodyRunes != 1600 {
		t.Errorf("async_receipt = %v, max_body_runes = %d; want true and 1600", got.AsyncReceipt, got.MaxBodyRunes)
	}
	if len(got.Countries) != 2 || got.Countries[0] != "US" || got.Countries[1] != "CA" {
		t.Errorf("countries = %v, want [US CA]", got.Countries)
	}
	if len(got.AttributeKeys) != 1 || got.AttributeKeys[0] != "IN_TEMPLATE_ID" {
		t.Errorf("attribute_keys = %v, want [IN_TEMPLATE_ID]", got.AttributeKeys)
	}
	if len(got.Identities) != 1 {
		t.Fatalf("identities = %+v, want one", got.Identities)
	}
	identity := got.Identities[0]
	if identity.Name != "us-main" || identity.Kind != string(sms.IdentityMessagingService) ||
		identity.Value != "svc-1" || len(identity.Countries) != 1 || identity.Countries[0] != "US" ||
		identity.Attributes["IN_ENTITY_ID"] != "entity-1" {
		t.Errorf("identity = %+v, want every field the model declares", identity)
	}
	if r.provider.modeled() != 1 {
		t.Errorf("Model was read %d times, want once per request", r.provider.modeled())
	}
	if len(r.registry.recorded()) != 0 {
		t.Errorf("the carrier route reached the registry: %+v", r.registry.recorded())
	}
}

// The carrier view is built field by field: it carries the model's fields and
// nothing else, so a field added to a carrier model later does not reach a client
// by being there.
func TestTheCarrierViewCarriesOnlyTheDeclaredFields(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodGet, pathCarrier, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got map[string]any
	decode(t, w, &got)
	want := map[string]bool{
		"name": true, "content": true, "review": true, "identities": true,
		"countries": true, "attribute_keys": true, "max_body_runes": true, "async_receipt": true,
	}
	for key := range got {
		if !want[key] {
			t.Errorf("the view carries %q, which is not a declared field", key)
		}
	}
	for key := range want {
		if _, ok := got[key]; !ok {
			t.Errorf("the view is missing %q", key)
		}
	}
}

// A guard denial is answered 403 and the provider's model is never read: the
// decision comes before the read.
func TestADeniedCarrierReadReachesNoProvider(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.do(http.MethodGet, pathCarrier, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if r.provider.modeled() != 0 {
		t.Errorf("Model was read %d times on a denied request, want none", r.provider.modeled())
	}
}
