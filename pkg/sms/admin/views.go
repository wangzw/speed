package admin

import "github.com/vislake/speed/pkg/sms"

// The views the management routes answer with. Each is built field by field from
// a root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there.
//
// The view types are declared in operations.go, where the fragment's component
// name comes from the Go type name. The aliases here keep the module's own
// vocabulary for the handlers and the tests, which speak of a template view and
// an approval view.

// templateView is the type of one template in the module's own vocabulary.
type templateView = SmsAdminTemplate

// approvalView is the type of one vendor verdict in the module's own
// vocabulary.
type approvalView = SmsAdminApproval

// templateViewOf builds the view of one record.
func templateViewOf(r sms.Record) templateView {
	approvals := make(map[string]approvalView, len(r.Approvals))
	for locale, approval := range r.Approvals {
		approvals[locale] = approvalView{State: string(approval.State), Reason: approval.Reason}
	}
	return templateView{
		Name:      r.Name,
		Source:    string(r.Source),
		Params:    append([]string(nil), r.Params...),
		Locales:   append([]string(nil), r.Locales...),
		Approvals: approvals,
		Enabled:   r.Enabled,
		Version:   r.Version,
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	}
}
