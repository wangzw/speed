package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	nethttp "net/http"
	"reflect"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// preferencesModuleName is the name the preferences module is registered
// under: the module through which a tenant's owner and the platform's
// operators administer the settings the product reads.
const preferencesModuleName = "preferences"

// The routes. The three on the API endpoint act on the layer of the tenant the
// request names; the six on the admin endpoint act on the global layer and on
// the layer of the tenant the path names, for the platform's operators.
const (
	listSettingsRoute  = "GET /settings"
	writeSettingRoute  = "PUT /settings/{key}"
	deleteSettingRoute = "DELETE /settings/{key}"

	listGlobalSettingsRoute  = "GET /global-settings"
	writeGlobalSettingRoute  = "PUT /global-settings/{key}"
	deleteGlobalSettingRoute = "DELETE /global-settings/{key}"

	listTenantSettingsRoute  = "GET /tenants/{id}/settings"
	writeTenantSettingRoute  = "PUT /tenants/{id}/settings/{key}"
	deleteTenantSettingRoute = "DELETE /tenants/{id}/settings/{key}"
)

// The seam actions a settings change is recorded under.
const (
	settingWriteAction  = "setting.write"
	settingDeleteAction = "setting.delete"
)

//go:embed openapi/preferences-api.json
var preferencesAPIOpenAPI []byte

//go:embed openapi/preferences-admin.json
var preferencesAdminOpenAPI []byte

var (
	errNoSettingValue    = errors.New("the body carries no value")
	errNotTenantEditable = errors.New("no setting a tenant edits has this key")
	errNoSuchSetting     = errors.New("no setting this host lists has this key")
)

// settingKind is the declared type of a setting, as the API names it.
type settingKind string

const (
	textSetting     settingKind = "string"
	integerSetting  settingKind = "integer"
	durationSetting settingKind = "duration"
)

// kindOf names a declared type the way the API does. A type it does not name
// is one the API cannot show, and the startup fails on it rather than showing
// a value in a form nobody documented: a type of a package's own, a duration's
// included, is not the plain type its kind would suggest.
func kindOf(declared reflect.Type) (settingKind, bool) {
	switch {
	case declared == reflect.TypeFor[time.Duration]():
		return durationSetting, true
	case declared.PkgPath() != "":
		return "", false
	}
	switch declared.Kind() {
	case reflect.String:
		return textSetting, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return integerSetting, true
	}
	return "", false
}

// servedKey is what the host decides about one setting it shows: its key, and
// whether a tenant's owner or admin may write it in their own layer. The rest
// are the platform's: an operator writes them, in the global layer or in any
// tenant's.
type servedKey struct {
	key            string
	tenantEditable bool
}

// hiddenSetting is what the API shows in place of a sensitive value that is
// set. Whether one is set is what an administrator needs to know; the value is
// for the module that reads it.
const hiddenSetting = "********"

// servedKeys is every setting the host's API serves, in the order it lists
// them: every setting the host's modules and the platform's read for the
// tenant a request or an event names.
//
// A tenant tunes how its account listing pages, what its deals default to, and
// where and how its webhook is posted, the signing secret included. The rest
// are the platform's. A tenant raising its own rate limit would make the limit
// mean nothing; how long a consumer's claim on an event lasts has to outlast
// the bus's redelivery, which is the platform's to know; and how long a report
// stays cached is load the platform carries. Each value's domain is the
// module's that reads it: a module that declares one — the rate limiter does,
// for its window and its limit — has the settings module refuse a write
// outside it, and the others judge a value — a page size, a currency code, a
// URL, a bounded timeout, a lifetime — when they read it. The host states no
// domain of its own.
//
// What each key is — its type, its description, its default and whether it is
// sensitive — is its declaration's, and the settings module reports it through
// Store.Declared; the host states none of it again.
//
// sessions.ttl is not listed. The sessions module reads it in the global layer
// alone, at sign-in, where no tenant is resolved, and the admin routes write
// every listed key in any tenant's layer as well, where nothing would read it.
var servedKeys = []servedKey{
	{pageSizeSetting, true},
	{defaultCurrencySetting, true},
	{idempotencyTTLSetting, true},
	{webhookURLSetting, true},
	{webhookSecretSetting, true},
	{webhookTimeoutSetting, true},
	{webhookMaxAttemptsSetting, true},
	{webhookRetryDelaySetting, true},
	{"ratelimit.limit", false},
	{"ratelimit.window", false},
	{webhookClaimTTLSetting, false},
	{revenueClaimTTLSetting, false},
	{reportsCacheTTLSetting, false},
}

// servedSetting is one served key with its declaration as the settings module
// reports it, and the name the API gives its type.
type servedSetting struct {
	settings.DeclaredItem
	kind           settingKind
	tenantEditable bool
}

// servedSettings joins the served keys to the declarations the settings module
// lists. A served key no module declares, or declared under a type the API
// does not show, fails the startup naming the key: every listing would answer
// 500 on it otherwise.
func servedSettings(declared []settings.DeclaredItem) ([]servedSetting, error) {
	byKey := make(map[string]settings.DeclaredItem, len(declared))
	for _, item := range declared {
		byKey[item.Key] = item
	}
	out := make([]servedSetting, 0, len(servedKeys))
	for _, k := range servedKeys {
		item, ok := byKey[k.key]
		if !ok {
			return nil, fmt.Errorf("%s: the host serves the setting %s, and no module declares it",
				preferencesModuleName, k.key)
		}
		kind, ok := kindOf(item.Type)
		if !ok {
			return nil, fmt.Errorf("%s: the setting %s is declared as %s, a type the settings API does not show",
				preferencesModuleName, k.key, item.Type)
		}
		out = append(out, servedSetting{DeclaredItem: item, kind: kind, tenantEditable: k.tenantEditable})
	}
	return out, nil
}

// lookup finds a served key. tenantSide narrows the search to the keys a
// tenant edits.
func (p *preferencesProduct) lookup(key string, tenantSide bool) (servedSetting, bool) {
	for _, s := range p.served {
		if s.Key == key && (s.tenantEditable || !tenantSide) {
			return s, true
		}
	}
	return servedSetting{}, false
}

// on lists the keys one side of the API serves.
func (p *preferencesProduct) on(tenantSide bool) []servedSetting {
	var out []servedSetting
	for _, s := range p.served {
		if s.tenantEditable || !tenantSide {
			out = append(out, s)
		}
	}
	return out
}

// resolve reads the value a read of the key resolves to in a layer, into a
// value of its declared type. A duration is answered as its text, which is the
// form it is written in.
func (s servedSetting) resolve(store settings.Store, layer settings.Layer) (any, error) {
	target := reflect.New(s.Type)
	if err := store.Get(layer, s.Key, target.Interface()); err != nil {
		return nil, err
	}
	value := target.Elem().Interface()
	if d, ok := value.(time.Duration); ok {
		return d.String(), nil
	}
	return value, nil
}

// settingView is one setting as the API shows it.
type settingView struct {
	Key            string      `json:"key"`
	Type           settingKind `json:"type"`
	Description    string      `json:"description"`
	TenantEditable bool        `json:"tenant_editable"`
	// Sensitive says the value is not shown: Written and Value read
	// hiddenSetting where a value is set, and Default is null.
	Sensitive bool `json:"sensitive"`
	// Default is the declared default in the declared type's own text form,
	// as the settings module reports it, and null for a sensitive setting,
	// whose default it withholds.
	Default *string `json:"default"`
	// Written is the text the layer's own row holds, and null when the layer
	// has none.
	Written *string `json:"written"`
	// Value is what a read in the layer resolves to: the layer's row, else
	// the global row, else the declared default.
	Value any `json:"value"`
}

// settingsList is what a listing answers with. Tenant is empty for the global
// layer.
type settingsList struct {
	Tenant   string        `json:"tenant,omitempty"`
	Settings []settingView `json:"settings"`
}

// settingWrite is the body of a write. The value is handed to the settings
// module as it was decoded, so the discipline a value is held to — a duration
// written as text, a whole number for an integer — is the settings module's,
// the same one a configuration source's value meets.
type settingWrite struct {
	Value any `json:"value"`
}

// Validate refuses a body that carries no value: a JSON null, or no member at
// all, would be written as nothing.
func (s settingWrite) Validate() error {
	if s.Value == nil {
		return errNoSettingValue
	}
	return nil
}

// preferencesProduct is the preferences module's product. served is every key
// its API serves, joined to its declaration when the module is built.
type preferencesProduct struct {
	released
	store    settings.Store
	roster   tenant.Catalog
	recorder audit.Audit
	served   []servedSetting
}

// settingFailure is a refusal or a failure a handler answers with.
type settingFailure struct {
	status  int
	message string
}

// view answers one layer's listing of the keys given. The written texts come
// from the table, and the resolved values from this replica's snapshot.
func (p *preferencesProduct) view(ctx context.Context, layer settings.Layer, keys []servedSetting) ([]settingView, *settingFailure) {
	rows, err := p.store.List(ctx, layer)
	if err != nil {
		log.FromContext(ctx).Error("listing a settings layer", "error", err)
		return nil, &settingFailure{nethttp.StatusServiceUnavailable, "the settings table cannot be read"}
	}
	written := make(map[string]string, len(rows))
	for _, row := range rows {
		written[row.Key] = row.Value
	}
	out := make([]settingView, 0, len(keys))
	for _, k := range keys {
		v, f := p.entry(ctx, layer, k, written)
		if f != nil {
			return nil, f
		}
		out = append(out, v)
	}
	return out, nil
}

// entry answers one key of a layer, given the layer's written texts.
//
// A row that no longer decodes under the declared type is the table's
// mistake, and answers 500 rather than a value the key does not have.
//
// A sensitive value is the host's to keep out of its answers: the settings
// module keeps it out of its change events and out of the default Declared
// reports, and still stores its text and hands it back to a read. So where one
// is set, the written text and the resolved value both read hiddenSetting.
func (p *preferencesProduct) entry(ctx context.Context, layer settings.Layer, k servedSetting, written map[string]string) (settingView, *settingFailure) {
	value, err := k.resolve(p.store, layer)
	if err != nil {
		log.FromContext(ctx).Error("reading a setting", "key", k.Key, "error", err)
		return settingView{}, &settingFailure{nethttp.StatusInternalServerError, "the setting " + k.Key + " cannot be read"}
	}
	v := settingView{
		Key: k.Key, Type: k.kind, Description: k.Description, TenantEditable: k.tenantEditable,
		Sensitive: k.Sensitive, Value: value,
	}
	if !k.Sensitive {
		v.Default = &k.Default
	}
	if text, ok := written[k.Key]; ok {
		v.Written = &text
	}
	if k.Sensitive {
		hidden := hiddenSetting
		if v.Written != nil {
			v.Written = &hidden
		}
		if !reflect.ValueOf(value).IsZero() {
			v.Value = hidden
		}
	}
	return v, nil
}

// entryAfter answers one key of a layer as it stands after a change made
// through this replica, which serves the change at once.
func (p *preferencesProduct) entryAfter(ctx context.Context, layer settings.Layer, k servedSetting) (settingView, *settingFailure) {
	rows, err := p.store.List(ctx, layer)
	if err != nil {
		log.FromContext(ctx).Error("listing a settings layer", "error", err)
		return settingView{}, &settingFailure{nethttp.StatusServiceUnavailable, "the settings table cannot be read"}
	}
	written := map[string]string{}
	for _, row := range rows {
		if row.Key == k.Key {
			written[row.Key] = row.Value
		}
	}
	return p.entry(ctx, layer, k, written)
}

// write writes a value into a layer and answers with the key as it now stands.
//
// The settings module encodes the value under the declared type and refuses
// what does not convert — a bare number for a duration, a fraction for an
// integer — and a value outside the domain its declaring module states, before
// anything is written, and those refusals answer 422 with the module's own
// text.
func (p *preferencesProduct) write(ctx context.Context, layer settings.Layer, k servedSetting, value any, actor string) (settingView, *settingFailure) {
	err := p.store.Set(ctx, settings.Write{Layer: layer, Key: k.Key, Value: value, Actor: actor})
	switch {
	case errors.Is(err, settings.ErrTypeMismatch), errors.Is(err, settings.ErrInvalidValue):
		return settingView{}, &settingFailure{nethttp.StatusUnprocessableEntity, err.Error()}
	case err != nil:
		return settingView{}, p.writeFailed(ctx, k, err)
	}
	return p.entryAfter(ctx, layer, k)
}

// remove deletes a layer's row for a key and answers with the key as it now
// resolves. Deleting a row that is not there is not an error.
func (p *preferencesProduct) remove(ctx context.Context, layer settings.Layer, k servedSetting, actor string) (settingView, *settingFailure) {
	if err := p.store.Delete(ctx, layer, k.Key, actor); err != nil {
		return settingView{}, p.writeFailed(ctx, k, err)
	}
	return p.entryAfter(ctx, layer, k)
}

// writeFailed classifies a write the settings module did not refuse on the
// value's account. Every served key was found among the declarations when the
// module was built, so the failure is the table's.
func (p *preferencesProduct) writeFailed(ctx context.Context, k servedSetting, err error) *settingFailure {
	log.FromContext(ctx).Error("writing a setting", "key", k.Key, "error", err)
	return &settingFailure{nethttp.StatusServiceUnavailable, "the settings table cannot be written"}
}

// record states a settings change through the seam. The request's own row says
// who called which route with what outcome; this row carries the member or
// operator who changed the value and what it was changed to, once, whatever
// capture of the change event a deployment configures — an event row carries no
// caller when the bus delivers it on its own context. A sensitive value is not
// recorded: the row says the key was written, as the change event does.
func (p *preferencesProduct) record(ctx context.Context, action string, v settingView, purpose string, detail map[string]string) *settingFailure {
	if detail == nil {
		detail = map[string]string{}
	}
	detail["key"] = v.Key
	if action == settingWriteAction && v.Written != nil && !v.Sensitive {
		detail["value"] = *v.Written
	}
	if err := p.recorder.Record(ctx, audit.Entry{
		Action:  action,
		Object:  "settings/" + v.Key,
		Outcome: "ok",
		Purpose: purpose,
		Detail:  detail,
	}); err != nil {
		log.FromContext(ctx).Error("recording a settings change", "key", v.Key, "error", err)
		return &settingFailure{nethttp.StatusInternalServerError, "the setting was changed and its audit record was not"}
	}
	return nil
}

// writeSettingFailure answers with a refusal or a failure.
func writeSettingFailure(w nethttp.ResponseWriter, f *settingFailure) {
	writeError(w, f.status, f.message)
}

// settingsLayerOf is the layer of the tenant the request names. The identifier
// is the one resolution put in the context, never one the request body or path
// carries.
func settingsLayerOf(self member) settings.Layer { return settings.ForTenant(string(self.TenantID)) }

// listSettings answers the settings a tenant edits.
func (p *preferencesProduct) listSettings(w nethttp.ResponseWriter, r *nethttp.Request) {
	self, ok := memberWith(w, r, roleAdmin)
	if !ok {
		return
	}
	views, f := p.view(r.Context(), settingsLayerOf(self), p.on(true))
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, settingsList{Tenant: string(self.TenantID), Settings: views})
}

// writeSetting writes a value into the layer of the tenant the request names.
// The actor the change event carries is the member's user identifier.
func (p *preferencesProduct) writeSetting(w nethttp.ResponseWriter, r *nethttp.Request) {
	self, ok := memberWith(w, r, roleAdmin)
	if !ok {
		return
	}
	k, listed := p.lookup(r.PathValue("key"), true)
	if !listed {
		writeError(w, nethttp.StatusNotFound, errNotTenantEditable.Error())
		return
	}
	var body settingWrite
	if err := speedhttp.Decode(r, &body); err != nil {
		writeError(w, speedhttp.StatusFor(err), err.Error())
		return
	}
	ctx := r.Context()
	v, f := p.write(ctx, settingsLayerOf(self), k, body.Value, self.UserID)
	if f == nil {
		f = p.record(ctx, settingWriteAction, v, "", nil)
	}
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, v)
}

// deleteSetting removes the tenant's own row for a key, which reverts it to the
// global value or the declared default.
func (p *preferencesProduct) deleteSetting(w nethttp.ResponseWriter, r *nethttp.Request) {
	self, ok := memberWith(w, r, roleAdmin)
	if !ok {
		return
	}
	k, listed := p.lookup(r.PathValue("key"), true)
	if !listed {
		writeError(w, nethttp.StatusNotFound, errNotTenantEditable.Error())
		return
	}
	ctx := r.Context()
	v, f := p.remove(ctx, settingsLayerOf(self), k, self.UserID)
	if f == nil {
		f = p.record(ctx, settingDeleteAction, v, "", nil)
	}
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, v)
}

// operatorActor is the owner and subject of the operator calling an admin route.
// The operator check sits on the admin endpoint's chain, so an identity is
// always there; its absence means a path that skipped the gateway.
func operatorActor(w nethttp.ResponseWriter, r *nethttp.Request, route string) (string, bool) {
	caller, ok := authn.FromContext(r.Context())
	if !ok {
		log.FromContext(r.Context()).Error("a guarded route was reached with no identity", "route", route)
		writeError(w, nethttp.StatusInternalServerError, "the request carries no identity")
		return "", false
	}
	return caller.Key(), true
}

// pathSettingKey reads the path's key against every key the host lists.
func (p *preferencesProduct) pathSettingKey(w nethttp.ResponseWriter, r *nethttp.Request) (servedSetting, bool) {
	k, listed := p.lookup(r.PathValue("key"), false)
	if !listed {
		writeError(w, nethttp.StatusNotFound, errNoSuchSetting.Error())
	}
	return k, listed
}

// listGlobalSettings answers the global layer of every key the host lists.
func (p *preferencesProduct) listGlobalSettings(w nethttp.ResponseWriter, r *nethttp.Request) {
	views, f := p.view(r.Context(), settings.Global(), p.on(false))
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, settingsList{Settings: views})
}

// globalSettingDetail names the layer on the seam row of a global change.
func globalSettingDetail() map[string]string { return map[string]string{"layer": "global"} }

// writeGlobalSetting writes a value into the global layer, which every tenant
// without a row of its own reads.
func (p *preferencesProduct) writeGlobalSetting(w nethttp.ResponseWriter, r *nethttp.Request) {
	actor, ok := operatorActor(w, r, writeGlobalSettingRoute)
	if !ok {
		return
	}
	k, ok := p.pathSettingKey(w, r)
	if !ok {
		return
	}
	var body settingWrite
	if err := speedhttp.Decode(r, &body); err != nil {
		writeError(w, speedhttp.StatusFor(err), err.Error())
		return
	}
	ctx := r.Context()
	v, f := p.write(ctx, settings.Global(), k, body.Value, actor)
	if f == nil {
		f = p.record(ctx, settingWriteAction, v, "", globalSettingDetail())
	}
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, v)
}

// deleteGlobalSetting removes the global row for a key.
func (p *preferencesProduct) deleteGlobalSetting(w nethttp.ResponseWriter, r *nethttp.Request) {
	actor, ok := operatorActor(w, r, deleteGlobalSettingRoute)
	if !ok {
		return
	}
	k, ok := p.pathSettingKey(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	v, f := p.remove(ctx, settings.Global(), k, actor)
	if f == nil {
		f = p.record(ctx, settingDeleteAction, v, "", globalSettingDetail())
	}
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, v)
}

// crossTenantSettingPurpose is what the seam row of an operator's change to a
// tenant's layer states.
const crossTenantSettingPurpose = "a platform operator changed a tenant's setting from the admin endpoint, " +
	"outside any request scoped to that tenant"

// pathTenant reads the tenant the path names off the roster.
//
// The layer an operator writes is named by a roster read, as a write reaching
// into a tenant from outside its requests has to be: the settings module does
// not know the roster, and would write a layer for an identifier no tenant has.
// A disabled tenant is still on the roster, and an operator still configures
// it. The read is this replica's snapshot, so a tenant another replica
// registered is found once this replica has reread the roster.
func (p *preferencesProduct) pathTenant(w nethttp.ResponseWriter, r *nethttp.Request) (tenant.Tenant, bool) {
	id := r.PathValue("id")
	if !tenant.IsIdentifier(id) {
		writeError(w, nethttp.StatusUnprocessableEntity, errNoTenantID.Error())
		return tenant.Tenant{}, false
	}
	t, found, err := p.roster.Get(r.Context(), tenant.ID(id))
	switch {
	case err != nil:
		log.FromContext(r.Context()).Error("reading the roster", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the roster cannot be read")
		return tenant.Tenant{}, false
	case !found:
		writeError(w, nethttp.StatusNotFound, errNoSuchTenant.Error())
		return tenant.Tenant{}, false
	}
	return t, true
}

// tenantSettingDetail names the tenant on the seam row of an operator's change
// to its layer.
//
// A seam row carries the tenant of the scope its work ran in, and this change
// runs in none: the settings table is not scoped, the layer is named by the
// roster read rather than by a context, and the admin endpoint resolves no
// tenant. So the row is among the platform's rows, as the operator's grant's
// is, and the tenant's own trail does not show it; the detail is what names
// the tenant. An operator's read of one tenant's trail is another matter: it
// reads that tenant's rows through the filter, which takes the tenant's scope,
// so it runs entered and its row is in that tenant's trail.
func tenantSettingDetail(t tenant.Tenant) map[string]string {
	return map[string]string{"layer": "tenant", "tenant": string(t.ID)}
}

// listTenantSettings answers one tenant's layer of every key the host lists.
func (p *preferencesProduct) listTenantSettings(w nethttp.ResponseWriter, r *nethttp.Request) {
	t, ok := p.pathTenant(w, r)
	if !ok {
		return
	}
	views, f := p.view(r.Context(), settings.ForTenant(string(t.ID)), p.on(false))
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, settingsList{Tenant: string(t.ID), Settings: views})
}

// writeTenantSetting writes a value into the layer of the tenant the path
// names: a platform key such as the tenant's rate limit, or one the tenant
// edits itself.
func (p *preferencesProduct) writeTenantSetting(w nethttp.ResponseWriter, r *nethttp.Request) {
	actor, ok := operatorActor(w, r, writeTenantSettingRoute)
	if !ok {
		return
	}
	t, ok := p.pathTenant(w, r)
	if !ok {
		return
	}
	k, ok := p.pathSettingKey(w, r)
	if !ok {
		return
	}
	var body settingWrite
	if err := speedhttp.Decode(r, &body); err != nil {
		writeError(w, speedhttp.StatusFor(err), err.Error())
		return
	}
	ctx := r.Context()
	v, f := p.write(ctx, settings.ForTenant(string(t.ID)), k, body.Value, actor)
	if f == nil {
		f = p.record(ctx, settingWriteAction, v, crossTenantSettingPurpose, tenantSettingDetail(t))
	}
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, v)
}

// deleteTenantSetting removes the row the tenant the path names holds for a
// key.
func (p *preferencesProduct) deleteTenantSetting(w nethttp.ResponseWriter, r *nethttp.Request) {
	actor, ok := operatorActor(w, r, deleteTenantSettingRoute)
	if !ok {
		return
	}
	t, ok := p.pathTenant(w, r)
	if !ok {
		return
	}
	k, ok := p.pathSettingKey(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	v, f := p.remove(ctx, settings.ForTenant(string(t.ID)), k, actor)
	if f == nil {
		f = p.record(ctx, settingDeleteAction, v, crossTenantSettingPurpose, tenantSettingDetail(t))
	}
	if f != nil {
		writeSettingFailure(w, f)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, v)
}

// init registers the preferences module. It runs on serve: its routes sit on the
// API and admin endpoints. It issues no statement of its own: the settings
// table is the settings module's, written through its surface.
func init() {
	registerApplication(application{
		module: preferencesModule(),
		runsOn: []string{serveCommand},
		journalled: []string{
			writeSettingRoute, deleteSettingRoute,
			writeGlobalSettingRoute, deleteGlobalSettingRoute,
			writeTenantSettingRoute, deleteTenantSettingRoute,
		},
	})
}

// preferencesModule binds the routes that read and write the settings layers of
// the keys the host serves. It declares no setting of its own: each key it
// serves is declared by the module that reads it.
//
// It requires the settings surface, the roster and the audit seam, which it
// takes up. The settings surface is read on the request path, never in Init,
// and the requirement is what fails a command that names this module and
// forgets it at resolution. The admin routes sit behind the operator check the
// admin module registers on that endpoint, and the API routes behind the
// membership layer.
func preferencesModule() core.Module {
	return core.Module{
		Name: preferencesModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*audit.Audit)(nil)},
		},
		Resources: []any{
			speedhttp.Spec{Endpoint: apiEndpoint, Document: preferencesAPIOpenAPI},
			speedhttp.Spec{Endpoint: adminEndpoint, Document: preferencesAdminOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, preferencesModuleName)
			if err != nil {
				return nil, err
			}
			store, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return nil, err
			}
			// The settings module builds its key table in its own New, which
			// runs first by the requirement, and Declared reads nothing else.
			served, err := servedSettings(store.Declared())
			if err != nil {
				return nil, err
			}
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return nil, err
			}
			recorder, err := core.Resolve[audit.Audit](reg)
			if err != nil {
				return nil, err
			}
			return &preferencesProduct{
				released: released{log: logger},
				store:    store,
				roster:   roster,
				recorder: recorder,
				served:   served,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*preferencesProduct)
			if !ok {
				return errForeignInstance(preferencesModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return err
			}
			admin, err := router.Endpoint(adminEndpoint)
			if err != nil {
				return err
			}
			api.Route(listSettingsRoute, nethttp.HandlerFunc(product.listSettings))
			api.Route(writeSettingRoute, nethttp.HandlerFunc(product.writeSetting))
			api.Route(deleteSettingRoute, nethttp.HandlerFunc(product.deleteSetting))
			admin.Route(listGlobalSettingsRoute, nethttp.HandlerFunc(product.listGlobalSettings))
			admin.Route(writeGlobalSettingRoute, nethttp.HandlerFunc(product.writeGlobalSetting))
			admin.Route(deleteGlobalSettingRoute, nethttp.HandlerFunc(product.deleteGlobalSetting))
			admin.Route(listTenantSettingsRoute, nethttp.HandlerFunc(product.listTenantSettings))
			admin.Route(writeTenantSettingRoute, nethttp.HandlerFunc(product.writeTenantSetting))
			admin.Route(deleteTenantSettingRoute, nethttp.HandlerFunc(product.deleteTenantSetting))
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
