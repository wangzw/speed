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
// them. It holds one carrier's rows: the read that builds it filters by the
// mounted provider's name.
type snapshot struct {
	byName map[string]runtimeTemplate
}

// runtimeTemplate is one runtime template as the snapshot holds it: its kind,
// the ordered parameter names, the language-to-code and language-to-body tables,
// the vendor registration identifiers, the vendor's review state per language
// and the bookkeeping the management calls report.
type runtimeTemplate struct {
	name       string
	kind       sms.Kind
	params     []string
	codes      map[string]string
	bodies     map[string]string
	attributes map[string]string
	locales    []string
	approvals  map[string]sms.Approval
	enabled    bool
	version    int64
	created    time.Time
	updated    time.Time
}

// record is the runtime template as it is reported to an operator.
func (r runtimeTemplate) record(carrier string) sms.Record {
	return sms.Record{
		Name:       r.name,
		Carrier:    carrier,
		Kind:       r.kind,
		Source:     sms.SourceRuntime,
		Params:     append([]string(nil), r.params...),
		Locales:    append([]string(nil), r.locales...),
		Bodies:     copyMapOrNil(r.bodies),
		Attributes: copyMapOrNil(r.attributes),
		Approvals:  cloneApprovals(r.approvals),
		Enabled:    r.enabled,
		Version:    r.version,
		CreatedAt:  r.created,
		UpdatedAt:  r.updated,
	}
}

// inForce reports whether a runtime template is in force: it must be enabled and
// every language it names must carry a sendable review. Approved and NotRequired
// are sendable — the second is a carrier whose vendor has no per-template review
// — while Unknown is not: it says a review exists and this process does not
// follow it. A template approved in one language and still waiting in another is
// not in force, because a consumer's coverage check needs every language the
// template names.
func (r runtimeTemplate) inForce() bool {
	if !r.enabled {
		return false
	}
	for _, locale := range r.locales {
		if !sendableState(r.approvals[locale].State) {
			return false
		}
	}
	return true
}

// sendableState reports whether a message may go out under a template in this
// state. Only an approved review and the absence of any review qualify; every
// state that says the vendor has not settled the template, or that this process
// does not follow it, does not.
func sendableState(state sms.ApprovalState) bool {
	return state == sms.ApprovalApproved || state == sms.ApprovalNotRequired
}

// row is one runtime template as a read returns it, with the structured columns
// still as the text the table holds.
type row struct {
	Carrier    string    `gorm:"column:carrier"`
	Name       string    `gorm:"column:name"`
	Kind       string    `gorm:"column:kind"`
	Params     string    `gorm:"column:params"`
	Codes      string    `gorm:"column:codes"`
	Bodies     string    `gorm:"column:bodies"`
	Attributes string    `gorm:"column:attributes"`
	Approvals  string    `gorm:"column:approvals"`
	Enabled    bool      `gorm:"column:enabled"`
	Version    int64     `gorm:"column:version"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

// runtimeColumns is the column list every read of the runtime table selects, so
// the shape of a row and the reads that build one stay in step.
const runtimeColumns = "carrier, name, kind, params, codes, bodies, attributes, approvals, enabled, " +
	"version, created_at, updated_at"

// loadSnapshot reads one carrier's rows and builds a snapshot, refusing a row
// whose name the configuration also holds and one whose shape the mounted
// carrier's model does not accept.
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
// The carrier-shape check is here for the same reason a write checks it: a row
// written under one carrier's model may no longer fit the carrier this process
// mounted, and a load that carried it anyway would defer that to the first send.
//
// A load that fails inside a reload keeps the snapshot already in place; the
// startup load's failure stops the process.
func loadSnapshot(ctx context.Context, handle *gorm.DB, configured map[string]struct{}, carrier string,
	model sms.CarrierModel,
) (*snapshot, error) {
	var rows []row
	err := handle.WithContext(ctx).Raw("SELECT "+runtimeColumns+" FROM "+tableName+" WHERE carrier = ?", carrier).
		Scan(&rows).Error
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
		spec := sms.TemplateSpec{
			Kind:       rec.kind,
			Params:     rec.params,
			Codes:      rec.codes,
			Bodies:     rec.bodies,
			Attributes: rec.attributes,
		}
		if err := validateAgainstModel(rec.name, spec, model, model.Review.PerTemplate); err != nil {
			return nil, fmt.Errorf("sms.registry: the template %q in the %s table does not fit the "+
				"mounted carrier: %w", rec.name, tableName, err)
		}
		loaded.byName[r.Name] = rec
	}
	return loaded, nil
}

// decodeRuntime turns a row into the runtime template the snapshot holds,
// decoding the four structured columns.
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
	var bodies map[string]string
	if err := json.Unmarshal([]byte(r.Bodies), &bodies); err != nil {
		return runtimeTemplate{}, fmt.Errorf("sms.registry: the body table of the template %q does not "+
			"decode: %w", r.Name, err)
	}
	var attributes map[string]string
	if err := json.Unmarshal([]byte(r.Attributes), &attributes); err != nil {
		return runtimeTemplate{}, fmt.Errorf("sms.registry: the attribute table of the template %q does not "+
			"decode: %w", r.Name, err)
	}
	approvals, err := decodeApprovals(r.Approvals)
	if err != nil {
		return runtimeTemplate{}, fmt.Errorf("sms.registry: the review table of the template %q does not "+
			"decode: %w", r.Name, err)
	}
	codes, bodies, attributes = orEmpty(codes), orEmpty(bodies), orEmpty(attributes)
	return runtimeTemplate{
		name:       r.Name,
		kind:       normalizedKind(sms.Kind(r.Kind)),
		params:     params,
		codes:      codes,
		bodies:     bodies,
		attributes: attributes,
		locales:    effectiveLocales(codes, bodies),
		approvals:  approvals,
		enabled:    r.Enabled,
		version:    r.Version,
		created:    r.CreatedAt,
		updated:    r.UpdatedAt,
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

// sortedKeys lists a string map's keys in a fixed order, so a failure and a
// record report the same order every run.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// effectiveLocales is the effective language set of a template: the languages
// it has a code for and the languages it has a body for, together. It is the
// one statement of that set, so conflict detection, a merge, a resolve, Declared
// and in-force all agree on which languages a template names.
func effectiveLocales(codes, bodies map[string]string) []string {
	seen := make(map[string]struct{}, len(codes)+len(bodies))
	for locale := range codes {
		seen[locale] = struct{}{}
	}
	for locale := range bodies {
		seen[locale] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for locale := range seen {
		out = append(out, locale)
	}
	sort.Strings(out)
	return out
}

// cloneMap copies a string map so a record handed out shares nothing with the
// module's storage.
func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for key, value := range m {
		out[key] = value
	}
	return out
}

// copyMapOrNil copies a string map for a record, answering nil for an empty one
// so a template that carries none reports none rather than an empty table.
func copyMapOrNil(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return cloneMap(m)
}

// orEmpty answers the map itself, or an empty one when it is nil, so the
// structured columns are written as an empty JSON object and never as null.
func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
