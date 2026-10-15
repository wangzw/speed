package admin

import (
	"time"

	"github.com/vislake/speed/pkg/sms"
)

// The views the management routes answer with. Each is built field by field from
// a root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there.

// templateView is one template as the management routes show it. It carries the
// template's logical name, where it came from, the parameters it substitutes,
// the languages it has a code for, the vendor's review verdict per language and
// whether a message may go out under it. It carries no vendor code: a code is
// the registry's own bookkeeping, and an operator reading which templates are in
// force needs to know that one is, not what number the vendor filed it under.
type templateView struct {
	Name      string                  `json:"name"`
	Source    string                  `json:"source"`
	Params    []string                `json:"params"`
	Locales   []string                `json:"locales"`
	Approvals map[string]approvalView `json:"approvals"`
	Enabled   bool                    `json:"enabled"`
	Version   int64                   `json:"version"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
}

// approvalView is the vendor's verdict on one template in one language as the
// management routes show it. A configured template carries no approvals at all:
// this process does not track its review.
type approvalView struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

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
