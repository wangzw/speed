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

// The in-memory copy of the runtime template set; replaced as a whole, never
// written in place, and holds one carrier's rows.
type snapshot struct {
	byName map[string]runtimeTemplate
}

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

func sendableState(state sms.ApprovalState) bool {
	return state == sms.ApprovalApproved || state == sms.ApprovalNotRequired
}

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

const runtimeColumns = "carrier, name, kind, params, codes, bodies, attributes, approvals, enabled, " +
	"version, created_at, updated_at"

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
		if _, err := sms.NewDeclaration(map[string]sms.TemplateSpec{rec.name: spec}); err != nil {
			return nil, fmt.Errorf("sms.registry: the template %q in the %s table is not self-consistent: %w",
				rec.name, tableName, err)
		}
		loaded.byName[r.Name] = rec
	}
	return loaded, nil
}

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

type approvalRow struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

func encodeApprovals(approvals map[string]sms.Approval) string {
	rows := make(map[string]approvalRow, len(approvals))
	for locale, approval := range approvals {
		rows[locale] = approvalRow{State: string(approval.State), Reason: approval.Reason}
	}
	return mustJSON(rows)
}

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

func cloneApprovals(approvals map[string]sms.Approval) map[string]sms.Approval {
	out := make(map[string]sms.Approval, len(approvals))
	for locale, approval := range approvals {
		out[locale] = approval
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

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

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for key, value := range m {
		out[key] = value
	}
	return out
}

func copyMapOrNil(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return cloneMap(m)
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
