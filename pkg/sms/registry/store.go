package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/sms"
)

// registryStore is the module's product and the implementation of sms.Registry.
//
// It holds the frozen configuration source and the runtime snapshot, which is
// replaced as a whole. The snapshot sits behind an atomic pointer because the
// replacement happens on a writer's goroutine, a subscription's and the
// poller's, while reads happen on every goroutine a consumer has. Every read and
// write of the runtime table is filtered by the mounted provider's name: one
// database serves one carrier.
type registryStore struct {
	handle   *gorm.DB
	provider sms.Provider
	bus      event.Bus
	logger   *slog.Logger
	config   moduleSettings
	tuned    *settingsReader

	// cancelPoll stops the poller and pollDone is closed once its goroutine has
	// returned. Both are written in Init and read in Stop, which the lifecycle
	// driver calls one after the other on its own goroutine.
	cancelPoll context.CancelFunc
	pollDone   chan struct{}

	// reloadMu serialises the reload path, a read of the table followed by a
	// swap. Several goroutines reach it — a writer's, the subscription's and the
	// poller's — and without it a reload whose read returned an older state
	// could swap after one that read a newer state, putting the staler snapshot
	// back and silently undoing a write this replica already reported as done.
	reloadMu sync.Mutex
	snapshot atomic.Pointer[snapshot]
}

// Resolve turns a message into what a carrier request needs, refusing one this
// registry may not send under.
//
// The sending switch is read first and, when it is false, every message is
// ErrNotSendable until an operator turns it back on. The configuration source is
// tried next and the message is handed to its declaration, which owns the
// structural checks and answers ErrRejected for an empty locale, an unknown
// template, a language with neither a code nor a body and a missing or surplus
// parameter. A template the configuration does not hold is looked up in the
// runtime snapshot: a disabled one is ErrNotSendable, and its code or body, the
// review state of the message's own language and its parameters are checked the
// same way. A template neither source holds is ErrRejected.
//
// Originator is not checked here: neither a declaration nor a row holds the
// carrier's identities, and the carrier validates the name in Send.
//
// A runtime read before the snapshot is loaded is ErrNotLoaded: the state is
// unavailable, and answering ErrRejected as though the template were absent
// would refuse a message the registry holds.
func (s *registryStore) Resolve(m sms.Message) (sms.Resolved, error) {
	if !s.tuned.sendingEnabled() {
		return sms.Resolved{}, fmt.Errorf("%w: %s is false, so this registry resolves no message for sending",
			sms.ErrNotSendable, sendingEnabledPath)
	}
	if strings.TrimSpace(m.Locale) == "" {
		return sms.Resolved{}, fmt.Errorf("%w: the message for the template %q names no language, and the "+
			"language is the caller's to set", sms.ErrRejected, m.Template)
	}
	if _, configured := s.config.names[m.Template]; configured {
		return s.config.declaration.Resolve(m)
	}
	current := s.snapshot.Load()
	if current == nil {
		return sms.Resolved{}, fmt.Errorf("%w: the template %q was resolved before the %s module's Init "+
			"loaded the runtime set", ErrNotLoaded, m.Template, ModuleName)
	}
	rec, held := current.byName[m.Template]
	if !held {
		return sms.Resolved{}, fmt.Errorf("%w: this registry holds no template %q", sms.ErrRejected, m.Template)
	}
	if !rec.enabled {
		return sms.Resolved{}, fmt.Errorf("%w: the template %q is disabled", sms.ErrNotSendable, m.Template)
	}
	return resolveRuntime(rec, m)
}

// describeState names a review state for an error, spelling the untracked state
// out so a reader is not shown an empty string.
func describeState(state sms.ApprovalState) string {
	if state == sms.ApprovalUnknown {
		return "not tracked"
	}
	return string(state)
}

// resolveRuntime checks one message against a runtime template and builds the
// resolved form. It carries the same checks the declaration makes for a
// configured template, over the parameter list and the code-or-body table the
// row holds, plus the vendor's review of the message's own language: the review
// is per language, so a template approved in one language sends in that language
// even while another is still pending. A carrier with no per-template review
// records NotRequired, which is sendable just as Approved is.
func resolveRuntime(rec runtimeTemplate, m sms.Message) (sms.Resolved, error) {
	code, hasCode := rec.codes[m.Locale]
	body, hasBody := rec.bodies[m.Locale]
	if !hasCode && !hasBody {
		return sms.Resolved{}, fmt.Errorf("%w: the template %q has neither a code nor a body for the language "+
			"%q", sms.ErrRejected, m.Template, m.Locale)
	}
	if approval := rec.approvals[m.Locale]; !sendableState(approval.State) {
		return sms.Resolved{}, fmt.Errorf("%w: the template %q is not sendable in the language %q; the "+
			"vendor review is %s", sms.ErrNotSendable, m.Template, m.Locale, describeState(approval.State))
	}
	ordered := make([]string, len(rec.params))
	params := make(map[string]string, len(rec.params))
	for i, name := range rec.params {
		value, ok := m.Params[name]
		if !ok {
			return sms.Resolved{}, fmt.Errorf("%w: the template %q needs the parameter %q for the language "+
				"%q and the message carries none", sms.ErrRejected, m.Template, name, m.Locale)
		}
		ordered[i] = value
		params[name] = value
	}
	extra := make([]string, 0, len(m.Params))
	for name := range m.Params {
		if !hasParam(rec.params, name) {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return sms.Resolved{}, fmt.Errorf("%w: the message carries the parameter %q, which the template %q "+
			"does not declare", sms.ErrRejected, extra[0], m.Template)
	}
	rendered := ""
	if hasBody {
		var err error
		rendered, err = sms.RenderBody(body, params)
		if err != nil {
			return sms.Resolved{}, err
		}
	}
	return sms.Resolved{
		To:         m.To,
		Template:   m.Template,
		Locale:     m.Locale,
		Kind:       rec.kind,
		Code:       code,
		Body:       rendered,
		Params:     params,
		Ordered:    ordered,
		Originator: m.Originator,
		Attributes: copyMapOrNil(rec.attributes),
		ValidFor:   m.ValidFor,
		DedupeID:   m.DedupeID,
	}, nil
}

// hasParam reports whether a parameter list holds a name.
func hasParam(params []string, name string) bool {
	for _, p := range params {
		if p == name {
			return true
		}
	}
	return false
}

// Declared reports the templates in force, in a stable order: the configuration
// templates and the runtime ones that are enabled and sendable in every language
// they name.
//
// The configuration templates are always reported, because they are always
// available. The runtime ones are reported from the snapshot, which is loaded
// in this module's Init: a consumer reading Declared in its own New, before
// this module initialises, sees the configuration set alone.
func (s *registryStore) Declared() []sms.Template {
	out := s.config.declaration.Declared()
	if current := s.snapshot.Load(); current != nil {
		for _, rec := range current.byName {
			if rec.inForce() {
				out = append(out, sms.Template{
					Name:    rec.name,
					Kind:    rec.kind,
					Params:  append([]string(nil), rec.params...),
					Locales: append([]string(nil), rec.locales...),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// List reports every template the registry holds, in a stable order: the
// configuration templates first by source, each as read-only and always
// enabled, and the runtime templates with their review state and bookkeeping.
//
// It reads the snapshot, and a call before the module's Init loaded it is
// ErrNotLoaded.
func (s *registryStore) List(_ context.Context) ([]sms.Record, error) {
	current := s.snapshot.Load()
	if current == nil {
		return nil, fmt.Errorf("%w: the templates were listed before the %s module's Init loaded the "+
			"runtime set", ErrNotLoaded, ModuleName)
	}
	configured := s.config.declaration.Declared()
	out := make([]sms.Record, 0, len(configured)+len(current.byName))
	for _, t := range configured {
		spec := s.config.specs[t.Name]
		out = append(out, sms.Record{
			Name:       t.Name,
			Carrier:    s.provider.Name(),
			Kind:       t.Kind,
			Source:     sms.SourceConfig,
			Params:     t.Params,
			Locales:    t.Locales,
			Bodies:     copyMapOrNil(spec.Bodies),
			Attributes: copyMapOrNil(spec.Attributes),
			Enabled:    true,
		})
	}
	for _, rec := range current.byName {
		out = append(out, rec.record(s.provider.Name()))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Register takes up a runtime template whose vendor code the operator already
// has. Every language it names starts pending, or NotRequired when the carrier
// has no per-template review, and the template starts enabled: nothing is known
// to be wrong with a template the operator vouches for, and the review state of
// each language is read from the vendor from then on where the vendor reviews.
//
// A name the configuration already holds is ErrImmutable. The spec is checked
// with sms.NewDeclaration's single-template path, so a defect is ErrInvalidConfig
// naming the template, and then against the mounted carrier's CarrierModel, so a
// shape this carrier cannot carry is refused before anything is written. The
// spec then lands through upsert, the same merge path a submission takes: a
// language a name already holds is ErrTemplateExists, a shape that disagrees with
// the one the name already describes is ErrInvalidConfig, and a new language for
// a name already held is merged in.
func (s *registryStore) Register(ctx context.Context, name string, spec sms.TemplateSpec) (sms.Record, error) {
	if _, configured := s.config.names[name]; configured {
		return sms.Record{}, fmt.Errorf("%w: the template %q comes from the configuration", sms.ErrImmutable, name)
	}
	if _, err := sms.NewDeclaration(map[string]sms.TemplateSpec{name: spec}); err != nil {
		return sms.Record{}, err
	}
	model := s.provider.Model()
	if err := validateAgainstModel(name, spec, model, model.Review.PerTemplate); err != nil {
		return sms.Record{}, err
	}
	return s.upsert(ctx, templateWrite{
		name:       name,
		kind:       normalizedKind(spec.Kind),
		params:     append([]string(nil), spec.Params...),
		codes:      orEmpty(spec.Codes),
		bodies:     orEmpty(spec.Bodies),
		attributes: orEmpty(spec.Attributes),
		approval:   initialApproval(model),
	})
}

// Offer hands a template to the vendor for review and takes up the code it
// answers with. The template starts enabled and every language it names starts
// pending, or NotRequired when the carrier has no per-template review.
//
// The vendor is called only when the carrier has a vendor-side resource to
// create: a carrier that sends by code creates or obtains one, while a carrier
// that only sends a rendered body records the body locally and calls no vendor.
// A failed vendor call leaves no half-written row: a row holding a template the
// vendor never accepted would be one this process would serve as pending for
// good. The submission's shape is checked before the vendor call too, so a
// defect the vendor would reject never reaches it. A name the configuration
// already holds is ErrImmutable.
//
// A language the name already holds — by a code or by a body — or a shape that
// disagrees with the one the name already describes, is refused before the
// vendor call, so a submission this process would decline does not ordinarily
// reach the vendor. A new language for a name already held is a legal merge: it
// reaches the vendor, and the languages already there are left untouched, their
// review states included.
//
// The conflicts are decided twice before the vendor is reached — once against
// this replica's snapshot and once against the row itself — because the vendor
// call is the step that cannot be taken back. A replica that missed a change
// event serves a stale set for as long as it runs, so a snapshot alone would let
// it offer a template another replica already holds; reading the row closes that.
// What remains is two submissions of one language racing inside the same few
// milliseconds, which the write still refuses — at the price of a vendor call
// already made. Closing that too would mean writing the row before the vendor
// code exists, leaving a template behind whenever the call fails.
//
// The code the vendor answers with is re-checked against the carrier model
// before it is stored, so a carrier that reviews per template is never left with
// a language the vendor gave no code for.
func (s *registryStore) Offer(ctx context.Context, sub sms.Submission) (sms.Record, error) {
	if _, configured := s.config.names[sub.Name]; configured {
		return sms.Record{}, fmt.Errorf("%w: the template %q comes from the configuration", sms.ErrImmutable, sub.Name)
	}
	current := s.snapshot.Load()
	if current == nil {
		return sms.Record{}, fmt.Errorf("%w: the template %q was offered before the %s module's Init "+
			"loaded the runtime set", ErrNotLoaded, sub.Name, ModuleName)
	}
	model := s.provider.Model()
	kind := normalizedKind(sub.Kind)
	shape := sms.TemplateSpec{
		Kind:       kind,
		Params:     append([]string(nil), sub.Params...),
		Bodies:     map[string]string{sub.Locale: sub.Content},
		Attributes: sub.Attributes,
	}
	if _, err := sms.NewDeclaration(map[string]sms.TemplateSpec{sub.Name: shape}); err != nil {
		return sms.Record{}, err
	}
	// No vendor code exists yet, so the code the carrier's review would need is
	// checked after the vendor answers.
	if err := validateAgainstModel(sub.Name, shape, model, false); err != nil {
		return sms.Record{}, err
	}
	if rec, held := current.byName[sub.Name]; held {
		if err := conflictOf(rec, sub.Name, kind, sub.Params, []string{sub.Locale}); err != nil {
			return sms.Record{}, err
		}
	}
	if err := s.refuseHeldConflict(ctx, sub.Name, kind, sub.Params, []string{sub.Locale}); err != nil {
		return sms.Record{}, err
	}
	code := ""
	if model.Content.TemplateCode {
		var err error
		code, err = s.provider.Offer(ctx, sub)
		if err != nil {
			return sms.Record{}, fmt.Errorf("sms.registry: offering the template %q to %s: %w",
				sub.Name, s.provider.Name(), err)
		}
	}
	write := templateWrite{
		name:       sub.Name,
		kind:       kind,
		params:     append([]string(nil), sub.Params...),
		codes:      map[string]string{},
		bodies:     map[string]string{},
		attributes: orEmpty(sub.Attributes),
		approval:   initialApproval(model),
	}
	if code != "" {
		write.codes[sub.Locale] = code
	}
	if !model.Review.PerTemplate {
		// The vendor keeps no template to fetch the body back from: the row is
		// where the locally rendered body lives.
		write.bodies[sub.Locale] = sub.Content
	}
	stored := sms.TemplateSpec{
		Kind:       write.kind,
		Params:     write.params,
		Codes:      write.codes,
		Bodies:     write.bodies,
		Attributes: write.attributes,
	}
	if err := validateAgainstModel(sub.Name, stored, model, model.Review.PerTemplate); err != nil {
		return sms.Record{}, err
	}
	return s.upsert(ctx, write)
}

// refuseHeldConflict answers the conflicts a submission has with the row the
// table holds under its name, which is the authority a snapshot is not. A name
// the table does not hold conflicts with nothing.
func (s *registryStore) refuseHeldConflict(ctx context.Context, name string, kind sms.Kind, params, locales []string) error {
	var held []row
	err := s.handle.WithContext(ctx).Raw("SELECT "+runtimeColumns+" FROM "+tableName+
		" WHERE carrier = ? AND name = ?", s.provider.Name(), name).Scan(&held).Error
	if err != nil {
		return fmt.Errorf("sms.registry: reading the template %q before offering it: %w", name, err)
	}
	if len(held) == 0 {
		return nil
	}
	rec, err := decodeRuntime(held[0])
	if err != nil {
		return err
	}
	return conflictOf(rec, name, kind, params, locales)
}

// conflictOf answers what a write and a template of the same name disagree
// about: a language the template already holds a code or a body for, a kind that
// describes a different purpose, and parameters that describe a different
// message shape. One name describes one message shape, and a vendor reviews a
// template per language, so none of these disagreements is one a merge could
// settle.
func conflictOf(rec runtimeTemplate, name string, kind sms.Kind, params, locales []string) error {
	for _, locale := range locales {
		_, hasCode := rec.codes[locale]
		_, hasBody := rec.bodies[locale]
		if hasCode || hasBody {
			return fmt.Errorf("%w: the template %q already holds the language %q",
				sms.ErrTemplateExists, name, locale)
		}
	}
	if !slices.Equal(rec.params, params) {
		return fmt.Errorf("%w: the template %q already describes a different message shape",
			sms.ErrInvalidConfig, name)
	}
	if normalizedKind(rec.kind) != normalizedKind(kind) {
		return fmt.Errorf("%w: the template %q already describes a different message kind",
			sms.ErrInvalidConfig, name)
	}
	return nil
}

// templateWrite is one write the merge path lands: the kind the template is
// for, the ordered parameter names, the code and body tables, the registration
// attributes, and the review state every language the write brings starts in.
type templateWrite struct {
	name       string
	kind       sms.Kind
	params     []string
	codes      map[string]string
	bodies     map[string]string
	attributes map[string]string
	approval   sms.ApprovalState
}

// initialApproval is the state a language starts in on a carrier: pending when
// the vendor reviews each template, and NotRequired when it has no such review.
func initialApproval(model sms.CarrierModel) sms.ApprovalState {
	if model.Review.PerTemplate {
		return sms.ApprovalPending
	}
	return sms.ApprovalNotRequired
}

// upsertAttempts bounds the optimistic-concurrency retries a merge makes before
// it gives up. A merge reads the row, merges into it and writes it back under a
// version check, so a write that matched no row means another writer changed the
// row in between and the read must be taken again. The bound is small because the
// contenders are the few writers of one template name, and exhausting it is a
// plain failure a caller answers fail-closed, never a silent overwrite.
const upsertAttempts = 3

// upsert lands a write, inserting the name or merging into the row it already
// has. It is the one write path for Register and Offer.
//
// A name the row does not hold is inserted, each language it brings starting in
// the write's initial review state. A name the row already holds is merged: the
// kind and the shape must agree, no language may already be there, and the
// languages already present are left untouched — their codes, their bodies and
// their review states. The read, the merge and the write happen in one
// transaction and the write is conditional on the version read, so a concurrent
// writer that changed the row is detected and the merge retried rather than
// overwritten.
func (s *registryStore) upsert(ctx context.Context, w templateWrite) (sms.Record, error) {
	for attempt := 0; attempt < upsertAttempts; attempt++ {
		rec, retry, err := s.mergeOnce(ctx, w)
		if err != nil {
			return sms.Record{}, err
		}
		if retry {
			continue
		}
		s.after(ctx, w.name, false)
		return rec.record(s.provider.Name()), nil
	}
	return sms.Record{}, fmt.Errorf("sms.registry: landing the template %q kept losing the concurrent "+
		"write race after %d attempts", w.name, upsertAttempts)
}

// mergeOnce is one attempt of upsert. retry is true when the write matched no
// row, which means another writer changed the row between the read and the write.
func (s *registryStore) mergeOnce(ctx context.Context, w templateWrite) (runtimeTemplate, bool, error) {
	var rec runtimeTemplate
	var retry bool
	carrier := s.provider.Name()
	err := s.handle.WithContext(landed(ctx)).Transaction(func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		var rows []row
		if err := tx.Raw("SELECT "+runtimeColumns+" FROM "+tableName+
			" WHERE carrier = ? AND name = ?", carrier, w.name).Scan(&rows).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if len(rows) == 0 {
			locales := effectiveLocales(w.codes, w.bodies)
			approvals := make(map[string]sms.Approval, len(locales))
			for _, locale := range locales {
				approvals[locale] = sms.Approval{State: w.approval}
			}
			sql := "INSERT INTO " + tableName + " (carrier, name, kind, params, codes, bodies, attributes, " +
				"approvals, enabled, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?) " +
				"ON CONFLICT (carrier, name) DO NOTHING RETURNING version"
			version, written, err := insertVersion(tx, sql, carrier, w.name, string(w.kind), mustJSON(w.params),
				mustJSON(orEmpty(w.codes)), mustJSON(orEmpty(w.bodies)), mustJSON(orEmpty(w.attributes)),
				encodeApprovals(approvals), true, now, now)
			if err != nil {
				return err
			}
			if !written {
				retry = true
				return nil
			}
			rec = runtimeTemplate{
				name:       w.name,
				kind:       w.kind,
				params:     append([]string(nil), w.params...),
				codes:      cloneMap(orEmpty(w.codes)),
				bodies:     cloneMap(orEmpty(w.bodies)),
				attributes: cloneMap(orEmpty(w.attributes)),
				locales:    locales,
				approvals:  cloneApprovals(approvals),
				enabled:    true,
				version:    version,
				created:    now,
				updated:    now,
			}
			return nil
		}
		existing, err := decodeRuntime(rows[0])
		if err != nil {
			return err
		}
		if !slices.Equal(existing.params, w.params) {
			return fmt.Errorf("%w: the template %q already describes a different message shape",
				sms.ErrInvalidConfig, w.name)
		}
		if normalizedKind(existing.kind) != normalizedKind(w.kind) {
			return fmt.Errorf("%w: the template %q already describes a different message kind",
				sms.ErrInvalidConfig, w.name)
		}
		brought := effectiveLocales(w.codes, w.bodies)
		for _, locale := range brought {
			_, hasCode := existing.codes[locale]
			_, hasBody := existing.bodies[locale]
			if hasCode || hasBody {
				return fmt.Errorf("%w: the template %q already holds the language %q",
					sms.ErrTemplateExists, w.name, locale)
			}
		}
		mergedCodes := cloneMap(existing.codes)
		mergedBodies := cloneMap(existing.bodies)
		mergedAttributes := cloneMap(existing.attributes)
		mergedApprovals := cloneApprovals(existing.approvals)
		for locale, code := range w.codes {
			mergedCodes[locale] = code
		}
		for locale, body := range w.bodies {
			mergedBodies[locale] = body
		}
		for key, value := range w.attributes {
			mergedAttributes[key] = value
		}
		for _, locale := range brought {
			mergedApprovals[locale] = sms.Approval{State: w.approval}
		}
		sql := "UPDATE " + tableName + " SET codes = ?, bodies = ?, attributes = ?, approvals = ?, " +
			"version = version + 1, updated_at = ? WHERE carrier = ? AND name = ? AND version = ? RETURNING version"
		version, written, err := insertVersion(tx, sql, mustJSON(mergedCodes), mustJSON(mergedBodies),
			mustJSON(mergedAttributes), encodeApprovals(mergedApprovals), now, carrier, w.name, existing.version)
		if err != nil {
			return err
		}
		if !written {
			retry = true
			return nil
		}
		rec = runtimeTemplate{
			name:       w.name,
			kind:       normalizedKind(existing.kind),
			params:     existing.params,
			codes:      mergedCodes,
			bodies:     mergedBodies,
			attributes: mergedAttributes,
			locales:    effectiveLocales(mergedCodes, mergedBodies),
			approvals:  mergedApprovals,
			enabled:    existing.enabled,
			version:    version,
			created:    existing.created,
			updated:    now,
		}
		return nil
	})
	if err != nil {
		return runtimeTemplate{}, false, err
	}
	return rec, retry, nil
}

// insertVersion runs an insert or an update with a RETURNING version clause and
// reports the version it wrote, or written=false when it matched no row. A
// conditional statement reports that through an empty result set.
func insertVersion(tx *gorm.DB, sql string, args ...any) (int64, bool, error) {
	var rows []struct {
		Version int64 `gorm:"column:version"`
	}
	if err := tx.Raw(sql, args...).Scan(&rows).Error; err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return rows[0].Version, true, nil
}

// SetEnabled turns a message's ability to go out under a runtime template on or
// off. It is orthogonal to the vendor's review: a template may be enabled
// before its review comes back, and a send still waits for the review.
//
// A configuration template is ErrImmutable and a name the runtime set does not
// hold is ErrNoTemplate. The write is conditional on the column changing, so a
// call whose target state equals the current one changes nothing and succeeds:
// setting an already-enabled template enabled is not an error, and it does not
// bump the version or publish a change.
func (s *registryStore) SetEnabled(ctx context.Context, name string, enabled bool) error {
	if _, configured := s.config.names[name]; configured {
		return fmt.Errorf("%w: the template %q comes from the configuration", sms.ErrImmutable, name)
	}
	current := s.snapshot.Load()
	if current == nil {
		return fmt.Errorf("%w: the template %q was changed before the %s module's Init loaded the runtime "+
			"set", ErrNotLoaded, name, ModuleName)
	}
	if _, held := current.byName[name]; !held {
		return fmt.Errorf("%w: this registry holds no template named %q", sms.ErrNoTemplate, name)
	}
	now := time.Now().UTC()
	sql := "UPDATE " + tableName + " SET enabled = ?, version = version + 1, updated_at = ? " +
		"WHERE carrier = ? AND name = ? AND enabled <> ? RETURNING version"
	versions, err := s.writeVersions(ctx, sql, enabled, now, s.provider.Name(), name, enabled)
	if err != nil {
		return fmt.Errorf("sms.registry: changing the enabled state of the template %q: %w", name, err)
	}
	if len(versions) == 0 {
		// The row already carries the target state: nothing changed, so there is
		// nothing to reload or publish.
		return nil
	}
	s.after(ctx, name, false)
	return nil
}

// Remove drops a runtime template from this process. It does not remove it at
// the vendor.
//
// A configuration template is ErrImmutable and a name the runtime set does not
// hold is ErrNoTemplate.
func (s *registryStore) Remove(ctx context.Context, name string) error {
	if _, configured := s.config.names[name]; configured {
		return fmt.Errorf("%w: the template %q comes from the configuration", sms.ErrImmutable, name)
	}
	current := s.snapshot.Load()
	if current == nil {
		return fmt.Errorf("%w: the template %q was removed before the %s module's Init loaded the runtime "+
			"set", ErrNotLoaded, name, ModuleName)
	}
	if _, held := current.byName[name]; !held {
		return fmt.Errorf("%w: this registry holds no template named %q", sms.ErrNoTemplate, name)
	}
	removed, err := s.exec(ctx, "DELETE FROM "+tableName+" WHERE carrier = ? AND name = ?",
		s.provider.Name(), name)
	if err != nil {
		return fmt.Errorf("sms.registry: removing the template %q: %w", name, err)
	}
	if removed == 0 {
		return fmt.Errorf("%w: this registry holds no template named %q", sms.ErrNoTemplate, name)
	}
	s.after(ctx, name, true)
	return nil
}

// after runs the bookkeeping a successful write owes: reload this replica's
// snapshot, then publish the change for the other replicas.
//
// The reload is this replica's own read of what it just wrote; the publish
// tells the others to reload. A failure of either does not undo the write,
// which is already in the table: the versions report it, and the poller's
// ticker converges a replica the event never reached.
func (s *registryStore) after(ctx context.Context, name string, deleted bool) {
	settled := landed(ctx)
	s.reload(settled)
	s.publish(settled, name, deleted)
}

// subscribe registers the reload handler for this module's own change events.
//
// The handler runs wherever the bus calls it, which on some backends is the
// publisher's own goroutine, so it does one thing and returns: replace the
// snapshot. That is safe whenever it runs, because a replica's own events come
// back to it and the replacement is idempotent.
func (s *registryStore) subscribe() {
	if s.bus == nil {
		return
	}
	s.bus.Subscribe(ChangedEventType, ModuleName, func(ctx context.Context, _ event.Event) error {
		s.reload(ctx)
		return nil
	})
}

// load replaces the snapshot and reports why it could not. Its failure in Init
// stops the process: a startup load that could not read the table means every
// read would answer as though the runtime set were empty while the table holds
// something else.
func (s *registryStore) load(ctx context.Context) error {
	_, err := s.swap(ctx)
	return err
}

// reload replaces the snapshot with a freshly loaded one, keeping the one in
// place on failure.
//
// It is the same path for every trigger after startup — a write, a delivered
// change event, the poller — because the operation is the same one. A failure
// records the reason and leaves reads serving a snapshot that is behind, which
// the poller's next pass closes.
func (s *registryStore) reload(ctx context.Context) {
	if _, err := s.swap(ctx); err != nil {
		if ctx.Err() != nil {
			s.logger.Debug("sms.registry: a reload was withdrawn before it finished; the previous snapshot "+
				"stays in use", "error", err)
			return
		}
		s.logger.Error("sms.registry: reloading the runtime template set failed; the previous snapshot "+
			"stays in use", "error", err)
	}
}

// swap loads one carrier's rows and replaces the snapshot with what it read. The
// read and the replacement happen under one lock so two concurrent loads cannot
// install their results out of order.
func (s *registryStore) swap(ctx context.Context) (*snapshot, error) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	loaded, err := loadSnapshot(ctx, s.handle, s.config.names, s.provider.Name(), s.provider.Model())
	if err != nil {
		return nil, err
	}
	s.snapshot.Store(loaded)
	return loaded, nil
}

// publish sends a change event. A failure here is recorded and goes no further:
// the change is in the table, and reporting the write as failed because its
// notification could not be sent would tell the caller the opposite of what
// happened.
func (s *registryStore) publish(ctx context.Context, name string, deleted bool) {
	if s.bus == nil {
		return
	}
	evt := event.Event{Type: ChangedEventType, Payload: Change{Name: name, Deleted: deleted}}
	if err := s.bus.Publish(ctx, evt); err != nil {
		s.logger.Error("sms.registry: publishing a template change failed", "name", name, "error", err)
	}
}

// Change is the payload of a change event: the template that changed and
// whether it was removed. A subscriber reloads the snapshot rather than
// applying the payload, so the payload is a marker and not a delta.
type Change struct {
	// Name is the logical template name that changed.
	Name string
	// Deleted reports a removal rather than a write.
	Deleted bool
}

// ownershipTable holds the one-row marker of which carrier adopted the legacy
// rows of a database. It is written once, in the transaction that rewrites those
// rows, and a later process mounting a different carrier fails its startup
// against it.
const ownershipTable = "sms_carrier_ownership"

// adopt claims the legacy rows of the database for the mounted carrier.
//
// A row whose carrier is empty predates the carrier column: it was written by the
// implementation this one superseded, which served one carrier per database. The
// first carrier to start against such a database rewrites those rows to its own
// name and writes the one-row ownership marker in the same transaction, so the
// rewrite and the claim land together or not at all. A later process mounting a
// different carrier reads the marker and fails its startup rather than silently
// taking the rows over; the same carrier starting again finds the marker and the
// adopted rows already in place.
//
// A database that holds no legacy row gets no marker: rows written under the
// carrier column are already separated, and two carriers may share one database
// without either loading the other's templates. The marker is written only when
// there is an ownership question to settle.
func (s *registryStore) adopt(ctx context.Context) error {
	carrier := s.provider.Name()
	err := s.handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		owner, found, err := readOwner(tx)
		if err != nil {
			return err
		}
		if !found {
			legacy, readErr := countLegacyRows(tx)
			if readErr != nil {
				return readErr
			}
			if legacy == 0 {
				return nil
			}
			// Two carriers starting against the same legacy rows race here. The
			// conditional insert leaves one marker, and the read below decides
			// which carrier owns the rows.
			if writeErr := tx.Exec("INSERT INTO "+ownershipTable+" (singleton, carrier) VALUES (1, ?) "+
				"ON CONFLICT (singleton) DO NOTHING", carrier).Error; writeErr != nil {
				return writeErr
			}
			owner, found, err = readOwner(tx)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("sms.registry: the ownership marker of the %s table could not be read "+
					"back after it was written", tableName)
			}
		}
		if owner != carrier {
			return fmt.Errorf("%w: the legacy templates in the %s table were adopted by the carrier %q, "+
				"and this process mounted %q; one database serves one carrier", sms.ErrInvalidConfig,
				tableName, owner, carrier)
		}
		if err := tx.Exec("UPDATE "+tableName+" SET carrier = ? WHERE carrier = ''", carrier).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("sms.registry: adopting the runtime template table: %w", err)
	}
	return nil
}

// readOwner reports the carrier the ownership marker names, and whether the
// marker is there at all.
func readOwner(tx *gorm.DB) (string, bool, error) {
	var rows []struct {
		Carrier string `gorm:"column:carrier"`
	}
	if err := tx.Raw("SELECT carrier FROM " + ownershipTable + " WHERE singleton = 1").Scan(&rows).Error; err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return rows[0].Carrier, true, nil
}

// countLegacyRows counts the rows written before the carrier column existed.
func countLegacyRows(tx *gorm.DB) (int64, error) {
	var rows []struct {
		Count int64 `gorm:"column:legacy_count"`
	}
	if err := tx.Raw("SELECT COUNT(*) AS legacy_count FROM " + tableName + " WHERE carrier = ''").
		Scan(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}

// writeVersions runs a write that returns the rows' versions: an insert or an
// update with a RETURNING clause. An empty result means no row was written,
// which is how a conditional statement reports that it matched nothing.
func (s *registryStore) writeVersions(ctx context.Context, sql string, args ...any) ([]int64, error) {
	var rows []struct {
		Version int64 `gorm:"column:version"`
	}
	err := s.commit(ctx, func(tx *gorm.DB) error {
		return tx.Raw(sql, args...).Scan(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	versions := make([]int64, 0, len(rows))
	for _, r := range rows {
		versions = append(versions, r.Version)
	}
	return versions, nil
}

// exec runs a write that reports only how many rows it touched.
func (s *registryStore) exec(ctx context.Context, sql string, args ...any) (int64, error) {
	var affected int64
	err := s.commit(ctx, func(tx *gorm.DB) error {
		res := tx.Exec(sql, args...)
		affected = res.RowsAffected
		return res.Error
	})
	return affected, err
}

// commit runs one write statement so that its result says whether it landed.
//
// A write statement here reads back what it wrote, so its result arrives after
// the server has executed it; a caller's context cancelled while the result is
// read would otherwise make the call fail for a change that is in the table.
// The statement therefore runs in a transaction whose begin and commit are out
// of the caller's cancellation, while the statement itself stays inside it: a
// cancellation before the statement's result is in rolls the transaction back,
// and one after it no longer reaches the commit.
func (s *registryStore) commit(ctx context.Context, statement func(tx *gorm.DB) error) error {
	return s.handle.WithContext(landed(ctx)).Transaction(func(tx *gorm.DB) error {
		return statement(tx.WithContext(ctx))
	})
}

// landed returns the context the work after a committed statement runs on: the
// caller's, with its values and without its cancellation. Once the statement
// has committed, the reload and the publish are this module's bookkeeping for a
// row already in the table rather than the caller's work.
func landed(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

// mustJSON encodes a value this module already holds in memory. It can only
// fail on a value the caller built from the configuration, which the decoder
// produced, so a failure is a defect here rather than anything a caller can
// reach; the encoding is written as a value the structured columns take and
// a failure would be visible as a row that does not load.
func mustJSON(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		// This is unreachable for the parameter lists and tables this module
		// writes: they hold strings and string maps and decode cleanly.
		panic(ModuleName + ": encoding a template value: " + err.Error())
	}
	return string(encoded)
}
