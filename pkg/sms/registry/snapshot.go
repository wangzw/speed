package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/sms"
)

// snapshot is the in-memory copy of the runtime template set.
//
// It holds the runtime records alone: the configuration source is frozen in the
// declaration and never changes, so it is held once in the product and not
// copied here. A snapshot is replaced as a whole and never written in place:
// reads take the pointer once and the copy they took does not change under
// them.
type snapshot struct {
	byName map[string]runtimeTemplate
}

// runtimeTemplate is one runtime template as the snapshot holds it: the ordered
// parameter names, the language-to-code table, the vendor's review state per
// language and the bookkeeping the management calls report.
type runtimeTemplate struct {
	name      string
	params    []string
	codes     map[string]string
	locales   []string
	approvals map[string]sms.Approval
	enabled   bool
	version   int64
	created   time.Time
	updated   time.Time
}

// record is the runtime template as it is reported to an operator.
func (r runtimeTemplate) record() sms.Record {
	return sms.Record{
		Name:      r.name,
		Source:    sms.SourceRuntime,
		Params:    append([]string(nil), r.params...),
		Locales:   append([]string(nil), r.locales...),
		Approvals: cloneApprovals(r.approvals),
		Enabled:   r.enabled,
		Version:   r.version,
		CreatedAt: r.created,
		UpdatedAt: r.updated,
	}
}

// inForce reports whether a runtime template is in force: it must be enabled and
// every language it holds a code for must be approved. A template approved in one
// language and still waiting in another is not in force, because a consumer's
// coverage check needs every language the template names.
func (r runtimeTemplate) inForce() bool {
	if !r.enabled {
		return false
	}
	for _, locale := range r.locales {
		if r.approvals[locale].State != sms.ApprovalApproved {
			return false
		}
	}
	return true
}

// row is one runtime template as a read returns it, with the structured columns
// still as the text the table holds.
type row struct {
	Name      string    `gorm:"column:name"`
	Params    string    `gorm:"column:params"`
	Codes     string    `gorm:"column:codes"`
	Approvals string    `gorm:"column:approvals"`
	Enabled   bool      `gorm:"column:enabled"`
	Version   int64     `gorm:"column:version"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// runtimeColumns is the column list every read of the runtime table selects, so
// the shape of a row and the reads that build one stay in step.
const runtimeColumns = "name, params, codes, approvals, enabled, version, created_at, updated_at"

// loadSnapshot reads the whole runtime table and builds a snapshot, refusing a
// row whose name the configuration also holds.
//
// A column that does not decode is a load failure rather than a row carried as
// broken: every row here was written by this module, so the structured columns
// are well-formed text, and one that is not is corruption the operator needs to
// see rather than a template silently missing from the set.
//
// The collision check lives here because this is the one place the two sources
// meet. Every write refuses a name the configuration holds, but a name can be
// registered first and written into the configuration afterwards, and the
// restart that follows is the first moment anything holds both. Left alone they
// would report as two records under one name, and the runtime one could not be
// removed — Remove refuses a configured name — so it could only be cleared by
// hand. Naming it here leaves the operator a way out that is a configuration
// edit, which is what put the name in both places to begin with.
//
// A load that fails inside a reload keeps the snapshot already in place; the
// startup load's failure stops the process.
func loadSnapshot(ctx context.Context, handle *gorm.DB, configured map[string]struct{}) (*snapshot, error) {
	var rows []row
	err := handle.WithContext(ctx).Raw("SELECT " + runtimeColumns + " FROM " + tableName).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("sms.registry: reading the %s table: %w", tableName, err)
	}
	loaded := &snapshot{byName: make(map[string]runtimeTemplate, len(rows))}
	for _, r := range rows {
		if _, both := configured[r.Name]; both {
			return nil, fmt.Errorf("sms.registry: the template %q is declared in this module's configuration and "+
				"held in the %s table at once; a name has one source, so remove one of the two", r.Name, tableName)
		}
		rec, err := decodeRuntime(r)
		if err != nil {
			return nil, err
		}
		loaded.byName[r.Name] = rec
	}
	return loaded, nil
}

// decodeRuntime turns a row into the runtime template the snapshot holds,
// decoding the three structured columns.
func decodeRuntime(r row) (runtimeTemplate, error) {
	var params []string
	if err := json.Unmarshal([]byte(r.Params), &params); err != nil {
		return runtimeTemplate{}, fmt.Errorf("sms.registry: the parameter list of the template %q does not "+
			"decode: %w", r.Name, err)
	}
	var codes map[string]string
	if err := json.Unmarshal([]byte(r.Codes), &codes); err != nil {
		return runtimeTemplate{}, fmt.Errorf("sms.registry: the code table of the template %q does not "+
			"decode: %w", r.Name, err)
	}
	approvals, err := decodeApprovals(r.Approvals)
	if err != nil {
		return runtimeTemplate{}, fmt.Errorf("sms.registry: the review table of the template %q does not "+
			"decode: %w", r.Name, err)
	}
	return runtimeTemplate{
		name:      r.Name,
		params:    params,
		codes:     codes,
		locales:   sortedKeys(codes),
		approvals: approvals,
		enabled:   r.Enabled,
		version:   r.Version,
		created:   r.CreatedAt,
		updated:   r.UpdatedAt,
	}, nil
}

// approvalRow is one language's verdict as the approvals column holds it. Its
// keys are this module's own and not the exported Approval's field names, so a
// change to those does not rewrite the table.
type approvalRow struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// encodeApprovals renders the per-language verdicts as the approvals column's
// JSON.
func encodeApprovals(approvals map[string]sms.Approval) string {
	rows := make(map[string]approvalRow, len(approvals))
	for locale, approval := range approvals {
		rows[locale] = approvalRow{State: string(approval.State), Reason: approval.Reason}
	}
	return mustJSON(rows)
}

// decodeApprovals reads the approvals column's JSON into the per-language
// verdicts the snapshot holds.
func decodeApprovals(text string) (map[string]sms.Approval, error) {
	var rows map[string]approvalRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, err
	}
	approvals := make(map[string]sms.Approval, len(rows))
	for locale, r := range rows {
		approvals[locale] = sms.Approval{State: sms.ApprovalState(r.State), Reason: r.Reason}
	}
	return approvals, nil
}

// cloneApprovals copies a review table so a record handed out shares nothing
// with the module's storage.
func cloneApprovals(approvals map[string]sms.Approval) map[string]sms.Approval {
	out := make(map[string]sms.Approval, len(approvals))
	for locale, approval := range approvals {
		out[locale] = approval
	}
	return out
}

// sortedKeys lists a code table's languages in a fixed order, so a record
// reports the same order every run.
func sortedKeys(codes map[string]string) []string {
	out := make([]string, 0, len(codes))
	for locale := range codes {
		out = append(out, locale)
	}
	sort.Strings(out)
	return out
}
