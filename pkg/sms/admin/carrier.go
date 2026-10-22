package admin

import (
	"context"

	"github.com/vislake/speed/pkg/sms"
)

// handleCarrier answers GET /sms/carrier: the mounted carrier's declaration of
// what it can carry. It is a read, so the action is template.read. The model is
// read through the Provider seam — the management surface holds no Product — and
// is shaped into a view field by field, so a field added to a carrier model later
// does not reach a client by being there.
func (p *product) handleCarrier(_ context.Context, _ struct{}) (SmsAdminCarrier, error) {
	return carrierViewOf(p.provider.Name(), p.provider.Model()), nil
}

// carrierViewOf builds the view of the mounted carrier's model.
func carrierViewOf(name string, model sms.CarrierModel) SmsAdminCarrier {
	identities := make([]SmsAdminIdentity, len(model.Identities))
	for i, identity := range model.Identities {
		identities[i] = SmsAdminIdentity{
			Name:       identity.Name,
			Kind:       string(identity.Kind),
			Value:      identity.Value,
			Countries:  append([]string(nil), identity.Countries...),
			Attributes: copyStrings(identity.Attributes),
		}
	}
	return SmsAdminCarrier{
		Name: name,
		Content: SmsAdminContentSupport{
			TemplateCode: model.Content.TemplateCode,
			TextBody:     model.Content.TextBody,
		},
		Review: SmsAdminReviewSupport{
			PerTemplate: model.Review.PerTemplate,
			Originator:  model.Review.Originator,
		},
		Identities:    identities,
		Countries:     append([]string(nil), model.Countries...),
		AttributeKeys: append([]string(nil), model.AttributeKeys...),
		MaxBodyRunes:  model.MaxBodyRunes,
		AsyncReceipt:  model.AsyncReceipt,
	}
}

// copyStrings copies a string map, so a view never shares a map with the
// value it was built from.
func copyStrings(entries map[string]string) map[string]string {
	copied := make(map[string]string, len(entries))
	for key, value := range entries {
		copied[key] = value
	}
	return copied
}
