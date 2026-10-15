package admin

import (
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/audit/journal"
)

// The views the query routes answer with. Each is built field by field from a
// journal.Row and never handed through as it stands, so that a field added to a
// row later does not reach a client by being there.

// recordView is one audit record as the query routes show it. The identifier is
// rendered as text so that no client reads it through a float, and the time is
// rendered in UTC. An empty tenant means the row belongs to no tenant — a
// platform operation or a system-level one.
type recordView struct {
	ID         string            `json:"id"`
	Tenant     string            `json:"tenant"`
	OccurredAt time.Time         `json:"occurred_at"`
	Actor      string            `json:"actor"`
	Source     string            `json:"source"`
	Action     string            `json:"action"`
	Object     string            `json:"object"`
	Outcome    string            `json:"outcome"`
	Purpose    string            `json:"purpose"`
	Detail     map[string]string `json:"detail"`
}

// recordPage is the list route's answer: one page of records and the cursor of
// the next, which is empty on the last page.
type recordPage struct {
	Entries []recordView `json:"entries"`
	Next    string       `json:"next"`
}

// recordViewOf builds one record's view from a read row.
func recordViewOf(r journal.Row) recordView {
	detail := r.Detail
	if detail == nil {
		// A record with no detail answers an empty object rather than a null, so
		// that a client reads a key-value table either way.
		detail = map[string]string{}
	}
	return recordView{
		ID:         strconv.FormatUint(r.ID, 10),
		Tenant:     r.Tenant,
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
