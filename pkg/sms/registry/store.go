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

type registryStore struct {
	handle   *gorm.DB
	provider sms.Provider
	bus      event.Bus
	logger   *slog.Logger
	config   moduleSettings
	tuned    *settingsReader

	cancelPoll context.CancelFunc
	pollDone   chan struct{}

	// reloadMu serialises the reload path, a read of the table followed by a swap;
	// without it a staler read could swap last and undo a write already reported done.
	reloadMu sync.Mutex
	snapshot atomic.Pointer[snapshot]
}

// Resolve turns a message into what a carrier request needs, refusing one this
// registry may not send under. The sending switch, the configuration source and
// then the runtime snapshot decide in that order; a read before the snapshot loads is ErrNotLoaded.
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

func describeState(state sms.ApprovalState) string {
	if state == sms.ApprovalUnknown {
		return "not tracked"
	}
	return string(state)
}

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

func hasParam(params []string, name string) bool {
	for _, p := range params {
		if p == name {
			return true
		}
	}
	return false
}

// Declared reports the templates in force in a stable order: the configuration
// templates and the runtime ones enabled and sendable in every language. Before
// Init loads the snapshot it reports the configuration set alone.
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

// List reports every template the registry holds in a stable order, and a call
// before Init loaded the snapshot is ErrNotLoaded.
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

// Register takes up a runtime template whose vendor code the operator already has:
// every language starts pending or NotRequired, and the template starts enabled. A
// configured name is ErrImmutable, a defect ErrInvalidConfig and a held language ErrTemplateExists.
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

// Offer hands a template to the vendor for review and takes up the code, or
// records the body locally. A configured name is ErrImmutable, a held language
// is ErrTemplateExists, and a failed call leaves no row.
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

type templateWrite struct {
	name       string
	kind       sms.Kind
	params     []string
	codes      map[string]string
	bodies     map[string]string
	attributes map[string]string
	approval   sms.ApprovalState
}

func initialApproval(model sms.CarrierModel) sms.ApprovalState {
	if model.Review.PerTemplate {
		return sms.ApprovalPending
	}
	return sms.ApprovalNotRequired
}

const upsertAttempts = 3

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

// SetEnabled turns a runtime template on or off, orthogonally to the vendor review.
// A configured name is ErrImmutable and an unknown one ErrNoTemplate; setting the
// current state changes nothing and publishes nothing.
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
		return nil
	}
	s.after(ctx, name, false)
	return nil
}

// Remove drops a runtime template from this process; it does not remove it at the
// vendor. A configured name is ErrImmutable and an unknown one ErrNoTemplate.
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

func (s *registryStore) after(ctx context.Context, name string, deleted bool) {
	settled := landed(ctx)
	s.reload(settled)
	s.publish(settled, name, deleted)
}

func (s *registryStore) subscribe() {
	if s.bus == nil {
		return
	}
	s.bus.Subscribe(ChangedEventType, ModuleName, func(ctx context.Context, _ event.Event) error {
		s.reload(ctx)
		return nil
	})
}

func (s *registryStore) load(ctx context.Context) error {
	_, err := s.swap(ctx)
	return err
}

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

func (s *registryStore) publish(ctx context.Context, name string, deleted bool) {
	if s.bus == nil {
		return
	}
	evt := event.Event{Type: ChangedEventType, Payload: Change{Name: name, Deleted: deleted}}
	if err := s.bus.Publish(ctx, evt); err != nil {
		s.logger.Error("sms.registry: publishing a template change failed", "name", name, "error", err)
	}
}

// Change is the payload of a change event: the template that changed and whether
// it was removed. A subscriber reloads rather than applying it, so it is a marker.
type Change struct {
	// Name is the logical template name that changed.
	Name string
	// Deleted reports a removal rather than a write.
	Deleted bool
}

const ownershipTable = "sms_carrier_ownership"

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

func (s *registryStore) exec(ctx context.Context, sql string, args ...any) (int64, error) {
	var affected int64
	err := s.commit(ctx, func(tx *gorm.DB) error {
		res := tx.Exec(sql, args...)
		affected = res.RowsAffected
		return res.Error
	})
	return affected, err
}

// commit runs one write statement in a transaction whose begin and commit are out
// of the caller's cancellation while the statement stays inside it, so a result
// read after the commit does not fail a change already in the table.
func (s *registryStore) commit(ctx context.Context, statement func(tx *gorm.DB) error) error {
	return s.handle.WithContext(landed(ctx)).Transaction(func(tx *gorm.DB) error {
		return statement(tx.WithContext(ctx))
	})
}

func landed(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

// mustJSON encodes a value this module already holds; its failure is unreachable
// and panics.
func mustJSON(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		panic(ModuleName + ": encoding a template value: " + err.Error())
	}
	return string(encoded)
}
