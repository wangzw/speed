package admin

import (
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit/journal"
)

// The views the query routes answer with. Each is built field by field from a
// journal.Row and never handed through as it stands, so that a field added to a
// row later does not reach a client by being there.

// AuditAdminRecord is one audit record as the query routes show it. The
// identifier is rendered as text so that no client reads it through a
// float, and the time is rendered in UTC.
type AuditAdminRecord struct {
	_ struct{} `description:"One audit row as the query routes render it: the recorded fact, the identity and capture path behind it, and what it acted on."`

	// ID is the row identifier, rendered as text.
	ID string `json:"id" required:"true" description:"The row identifier, rendered as text"`
	// Tenant is empty when the row belongs to no tenant.
	Tenant string `json:"tenant" required:"true" description:"Empty when the row belongs to no tenant"`
	// OccurredAt is when the recorded fact happened, in UTC.
	OccurredAt time.Time `json:"occurred_at" required:"true" description:"When the recorded fact happened, in UTC"`
	// Actor is the acting identity's responsibility key, empty for a system
	// operation; an impersonated account is named in detail.on_behalf_of.
	Actor string `json:"actor" required:"true" description:"The acting identity's responsibility key, that is the identity that really acted; empty when the row records a system operation. An impersonated account is named in detail.on_behalf_of instead"`
	// Source is the capture path that produced the row.
	Source string `json:"source" required:"true" description:"The capture path that produced the row"`
	// Action is what was done, as the emitting call site spells it.
	Action string `json:"action" required:"true" description:"What was done, as the emitting call site and the reader agree to spell it; the audit module fixes no vocabulary"`
	// Object is the type of the thing acted on, with its identifier.
	Object string `json:"object" required:"true" description:"The type of the thing acted on, together with its identifier"`
	// Outcome is how the action ended.
	Outcome string `json:"outcome" required:"true" description:"How the action ended"`
	// Purpose is why the action was taken.
	Purpose string `json:"purpose" required:"true" description:"Why the action was taken"`
	// Detail is empty when the row carries none.
	Detail map[string]string `json:"detail" required:"true" description:"Empty object when the row carries no detail"`
}

// AuditAdminRecordPage is the list route's answer: one page of records and the
// cursor of the next, which is empty on the last page.
type AuditAdminRecordPage struct {
	_ struct{} `description:"One page of audit records and the cursor that continues after them."`

	// Entries are the records on this page, newest first.
	Entries []AuditAdminRecord `json:"entries" required:"true" description:"The records on this page, newest first"`
	// Next is the cursor of the next page, empty on the last page.
	Next string `json:"next" required:"true" description:"The cursor of the next page, empty on the last page"`
}

func recordViewOf(r journal.Row, tenantName string) AuditAdminRecord {
	detail := r.Detail
	if detail == nil {
		// A record with no detail answers an empty object rather than a null, so
		// that a client reads a key-value table either way.
		detail = map[string]string{}
	}
	return AuditAdminRecord{
		ID:         strconv.FormatUint(r.ID, 10),
		Tenant:     tenantName,
		OccurredAt: r.OccurredAt.UTC(),
		Actor:      r.Actor,
		Source:     r.Source,
		Action:     r.Action,
		Object:     r.Object,
		Outcome:    r.Outcome,
		Purpose:    r.Purpose,
		Detail:     detail,
	}
}
