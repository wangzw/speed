package tenantadmin

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/settings"
)

var errUnreachable = errors.New("the database is unreachable")

const (
	mfaKey = "tenant.admission.require-mfa"
	ssoKey = "tenant.admission.require-sso"
)

func admissionItem() settings.Item {
	return settings.Item{
		Description:    "whether a tenant's members must satisfy this policy to make a request in that tenant; per tenant",
		TitleKey:       "tenant.admission.item.title",
		DescriptionKey: "tenant.admission.item.description",
		Control:        settings.ControlSwitch,
		Danger:         settings.DangerCritical,
		Effect:         settings.EffectNextSignIn,
		TenantAccess:   settings.TenantReadWrite,
	}
}

func admissionItemFor(key string) settings.Item {
	item := admissionItem()
	item.TitleKey = key + ".title"
	item.DescriptionKey = key + ".description"
	return item
}

func admissionDeclared() []settings.DeclaredItem {
	return []settings.DeclaredItem{
		decl(mfaKey, "tenant.admission", reflect.TypeFor[bool](), "false", admissionItemFor(mfaKey)),
		decl(ssoKey, "tenant.admission", reflect.TypeFor[bool](), "false", admissionItemFor(ssoKey)),
		decl("jobs.db.url", "jobs.db", reflect.TypeFor[string](), "", settings.Item{Description: "the job store"}),
		decl("ratelimit.window", "ratelimit", reflect.TypeFor[int](), "60", settings.Item{Description: "the rate window"}),
		decl("authn.session.idle-timeout", "authn.session", reflect.TypeFor[int](), "30", settings.Item{Description: "the idle timeout"}),
		decl("org.create.max-organizations", "org.create", reflect.TypeFor[int](), "5", settings.Item{Description: "the organization creation limit"}),
		decl("org.create.max-per-hour", "org.create", reflect.TypeFor[int](), "3", settings.Item{Description: "the hourly organization creation limit"}),
	}
}

type tenantSetting struct {
	Key              string `json:"key"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	Type             string `json:"type"`
	Control          string `json:"control"`
	Danger           string `json:"danger"`
	Effect           string `json:"effect"`
	TenantAccess     string `json:"tenant_access"`
	EffectiveValue   string `json:"effective_value"`
	PlatformValue    string `json:"platform_value"`
	TenantValue      string `json:"tenant_value"`
	LockedByPlatform bool   `json:"locked_by_platform"`
	HasRow           bool   `json:"has_row"`
	Source           string `json:"source"`
}

type tenantEnvelope struct {
	Settings       []tenantSetting `json:"settings"`
	Writable       bool            `json:"writable"`
	ReadOnlyReason string          `json:"read_only_reason"`
}

func listTenant(t *testing.T, r *rig) tenantEnvelope {
	t.Helper()
	w := r.do(http.MethodGet, base, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the listing ended with %d: %s", w.Code, w.Body.String())
	}
	var envelope tenantEnvelope
	decode(t, w, &envelope)
	return envelope
}

func noStoreWrites(calls []storeCall) bool {
	for _, c := range calls {
		if c.op == "Set" || c.op == "Delete" {
			return false
		}
	}
	return true
}

func keysOfSettings(envelope tenantEnvelope) []string {
	keys := make([]string, len(envelope.Settings))
	for i, item := range envelope.Settings {
		keys[i] = item.Key
	}
	return keys
}

// TestTheTenantSurfaceListsOnlyTheDeclaredItems checks that only items whose declarations expose them are listed.
func TestTheTenantSurfaceListsOnlyTheDeclaredItems(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()

	envelope := listTenant(t, r)
	got := keysOfSettings(envelope)
	want := []string{mfaKey, ssoKey}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the listing holds %v, want %v", got, want)
	}
	for _, platform := range []string{
		"jobs.db.url",
		"ratelimit.window",
		"authn.session.idle-timeout",
		"org.create.max-organizations",
		"org.create.max-per-hour",
	} {
		if strings.Contains(envelopeBody(t, r), platform) {
			t.Errorf("the answer names the platform key %q", platform)
		}
	}
}

func envelopeBody(t *testing.T, r *rig) string {
	t.Helper()
	w := r.do(http.MethodGet, base, nil)
	return w.Body.String()
}

// TestTheNamespaceReadRouteIsGone checks that no per-namespace read route exists and its path answers a routing 404.
func TestTheNamespaceReadRouteIsGone(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()

	for _, target := range []string{base + "/jobs.db", base + "/tenant.admission", base + "/app"} {
		w := r.do(http.MethodGet, target, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s ended with %d, want 404", target, w.Code)
		}
		body := w.Body.String()
		for _, leak := range []string{"jobs.db", "ratelimit", "tenant.admission", "require-mfa", "source", "default", "row"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s answered %q, which names %q", target, body, leak)
			}
		}
	}
}

// TestAPlatformWriteIsTheSame404AnUndeclaredKeyGets checks that both write
// routes answer a platform key and an undeclared one with the same bare 404.
func TestAPlatformWriteIsTheSame404AnUndeclaredKeyGets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		target string
		body   any
	}{
		{"platform set", http.MethodPut, base + "/jobs.db/url", map[string]any{"value": "postgres://"}},
		{"platform delete", http.MethodDelete, base + "/jobs.db/url", nil},
		{"undeclared set", http.MethodPut, base + "/jobs/ghost", map[string]any{"value": "x"}},
		{"undeclared delete", http.MethodDelete, base + "/jobs/ghost", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = admissionDeclared()

			w := r.do(tc.method, tc.target, tc.body)
			if w.Code != http.StatusNotFound {
				t.Fatalf("the request ended with %d, want 404: %s", w.Code, w.Body.String())
			}
			if w.Body.Len() != 0 {
				t.Errorf("the 404 carried %q, want a bare answer", w.Body.String())
			}
			if got := r.writeSeam.askedTenants(); len(got) != 0 {
				t.Errorf("the write seam was asked about %v", got)
			}
			if got := r.audit.entries(); len(got) != 0 {
				t.Errorf("the audit recorded %v", got)
			}
			if got := r.store.ops(); len(got) != 0 {
				t.Errorf("the settings store was reached: %v", got)
			}
		})
	}
}

// TestTheAdmissionPoliciesAreVisibleAndWritable checks that the two admission policies are visible and writable.
func TestTheAdmissionPoliciesAreVisibleAndWritable(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()

	w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": true})
	if w.Code != http.StatusNoContent {
		t.Fatalf("the write ended with %d: %s", w.Code, w.Body.String())
	}
	if !r.store.holdsLayerKey("Set", settings.ForTenant("acme"), mfaKey) {
		t.Errorf("the settings store was not asked to write %s on the acme layer: %v", mfaKey, r.store.recorded())
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Action != actionSet || rows[0].Object != mfaKey {
		t.Fatalf("the audit rows are %+v, want one set row for %s", rows, mfaKey)
	}

	w = r.do(http.MethodDelete, base+"/tenant.admission/require-sso", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("the delete ended with %d: %s", w.Code, w.Body.String())
	}
	if !r.store.holdsLayerKey("Delete", settings.ForTenant("acme"), ssoKey) {
		t.Errorf("the settings store was not asked to delete %s: %v", ssoKey, r.store.recorded())
	}
}

// TestPlatformEnforcementRefusesDisabling checks that a platform-enforced policy
// cannot be turned off, while writing it and dropping the tenant row stay legal.
func TestPlatformEnforcementRefusesDisabling(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	r.store.rows = []settings.Entry{
		{Key: mfaKey, Tenant: "", Value: "true"},
		{Key: mfaKey, Tenant: "acme", Value: "false"},
	}

	w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": false})
	if w.Code != http.StatusConflict {
		t.Fatalf("the write ended with %d, want 409: %s", w.Code, w.Body.String())
	}
	if p := problemOf(t, w); p.Type != "urn:speed:settings:tenant-admission-enforced" {
		t.Errorf("the problem type is %q, want the enforcement URN", p.Type)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("a refused write landed %v", got)
	}
	if got := r.store.recorded(); !noStoreWrites(got) {
		t.Errorf("a refused write changed a row: %v", got)
	}

	w = r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": true})
	if w.Code != http.StatusNoContent {
		t.Fatalf("writing the enforced value ended with %d, want 204: %s", w.Code, w.Body.String())
	}

	w = r.do(http.MethodDelete, base+"/tenant.admission/require-mfa", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("dropping the tenant row ended with %d, want 204: %s", w.Code, w.Body.String())
	}
}

// TestTheEffectiveValueIsPlatformOrTenant checks that the effective value is the platform policy OR the tenant policy.
func TestTheEffectiveValueIsPlatformOrTenant(t *testing.T) {
	cases := []struct {
		name      string
		platform  string
		tenant    string
		hasTenant bool
		effective string
		locked    bool
		source    string
		platformV string
		tenantV   string
	}{
		{"platform on, tenant off", "true", "false", true, "true", true, "global", "true", "false"},
		{"platform off, tenant on", "false", "true", true, "true", false, "tenant", "false", "true"},
		{"both off", "false", "false", true, "false", false, "tenant", "false", "false"},
		{"platform on, no tenant row", "true", "", false, "true", true, "global", "true", "false"},
		// A global row saying false is still the source when the tenant has no row: source
		// names where the value was read, not whether the policy is on.
		{"platform off, no tenant row", "false", "", false, "false", false, "global", "false", "false"},
		{"no rows", "", "", false, "false", false, "default", "false", "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = admissionDeclared()
			if tc.platform != "" {
				r.store.rows = append(r.store.rows, settings.Entry{Key: mfaKey, Tenant: "", Value: tc.platform})
			}
			if tc.hasTenant {
				r.store.rows = append(r.store.rows, settings.Entry{Key: mfaKey, Tenant: "acme", Value: tc.tenant})
			}

			envelope := listTenant(t, r)
			item := envelope.Settings[0]
			if item.EffectiveValue != tc.effective {
				t.Errorf("effective_value = %q, want %q", item.EffectiveValue, tc.effective)
			}
			if item.PlatformValue != tc.platformV {
				t.Errorf("platform_value = %q, want %q", item.PlatformValue, tc.platformV)
			}
			if item.TenantValue != tc.tenantV {
				t.Errorf("tenant_value = %q, want %q", item.TenantValue, tc.tenantV)
			}
			if item.LockedByPlatform != tc.locked {
				t.Errorf("locked_by_platform = %t, want %t", item.LockedByPlatform, tc.locked)
			}
			if item.Source != tc.source {
				t.Errorf("source = %q, want %q", item.Source, tc.source)
			}
			if item.HasRow != tc.hasTenant {
				t.Errorf("has_row = %t, want %t", item.HasRow, tc.hasTenant)
			}
		})
	}
}

// TestTheViewCarriesEveryDeclaredField checks that every declared field reaches the answer.
func TestTheViewCarriesEveryDeclaredField(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()

	envelope := listTenant(t, r)
	item := envelope.Settings[0]
	if item.Key != mfaKey {
		t.Errorf("key = %q", item.Key)
	}
	if item.Title == "" {
		t.Error("title is empty: with no localizer it falls back to the declared English text")
	}
	if item.Description == "" {
		t.Error("description is empty")
	}
	if item.Type != "bool" {
		t.Errorf("type = %q, want bool", item.Type)
	}
	if item.Control != "switch" {
		t.Errorf("control = %q, want switch", item.Control)
	}
	if item.Danger != "critical" {
		t.Errorf("danger = %q, want critical", item.Danger)
	}
	if item.Effect != "next-sign-in" {
		t.Errorf("effect = %q, want next-sign-in", item.Effect)
	}
	if item.TenantAccess != "read-write" {
		t.Errorf("tenant_access = %q, want read-write", item.TenantAccess)
	}
}

// TestAReadOnlyDeclarationIsVisibleButNotWritable checks that both write routes answer 403 read-only.
func TestAReadOnlyDeclarationIsVisibleButNotWritable(t *testing.T) {
	r := newRig(t)
	item := settings.Item{
		Description:    "a policy the platform alone writes",
		TitleKey:       "app.policy.title",
		DescriptionKey: "app.policy.description",
		Control:        settings.ControlSwitch,
		TenantAccess:   settings.TenantRead,
	}
	r.store.declared = []settings.DeclaredItem{
		decl("app.policy", "app", reflect.TypeFor[bool](), "false", item),
	}

	envelope := listTenant(t, r)
	if got := keysOfSettings(envelope); !reflect.DeepEqual(got, []string{"app.policy"}) {
		t.Fatalf("the listing holds %v, want app.policy", got)
	}
	// The refused writes must not add a seam question to the one the listing makes.
	asked := len(r.writeSeam.askedTenants())
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		var body any
		if method == http.MethodPut {
			body = map[string]any{"value": true}
		}
		w := r.do(method, base+"/app/policy", body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s ended with %d, want 403: %s", method, w.Code, w.Body.String())
			continue
		}
		if p := problemOf(t, w); p.Type != "urn:speed:settings:read-only" {
			t.Errorf("%s problem type = %q, want urn:speed:settings:read-only", method, p.Type)
		}
	}
	if got := r.writeSeam.askedTenants(); len(got) != asked {
		t.Errorf("a read-only item asked the seam about %v, want only the listing's question", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("a read-only item landed %v", got)
	}
	if got := r.store.recorded(); !noStoreWrites(got) {
		t.Errorf("a read-only item changed a row: %v", got)
	}
}

// TestAMaskedWriteIsRefusedBeforeAnythingRuns checks that writing the mask back is a 422 before the seam, audit or store.
func TestAMaskedWriteIsRefusedBeforeAnythingRuns(t *testing.T) {
	r := newRig(t)
	item := settings.Item{
		Description:    "a credential",
		TitleKey:       "app.secret.title",
		DescriptionKey: "app.secret.description",
		Control:        settings.ControlSwitch,
		TenantAccess:   settings.TenantReadWrite,
		Sensitive:      true,
	}
	r.store.declared = []settings.DeclaredItem{
		decl("app.secret", "app", reflect.TypeFor[string](), "", item),
	}

	w := r.do(http.MethodPut, base+"/app/secret", map[string]any{"value": maskedValue})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the write ended with %d, want 422: %s", w.Code, w.Body.String())
	}
	if p := problemOf(t, w); p.Type != "urn:speed:settings:masked-value" {
		t.Errorf("the problem type is %q, want the masked-value URN", p.Type)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("a masked write landed %v", got)
	}
	if got := r.writeSeam.askedTenants(); len(got) != 0 {
		t.Errorf("a masked write asked the seam about %v", got)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("a masked write reached the settings store: %v", got)
	}
}

// TestAProtectedTenantIsReadOnly checks that a protected tenant is reported read-only and both write routes refuse.
func TestAProtectedTenantIsReadOnly(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	r.writeSeam.refuse()

	envelope := listTenant(t, r)
	if envelope.Writable {
		t.Error("writable is true for a protected tenant")
	}
	if envelope.ReadOnlyReason != "protected" {
		t.Errorf("read_only_reason = %q, want protected", envelope.ReadOnlyReason)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		var body any
		if method == http.MethodPut {
			body = map[string]any{"value": true}
		}
		w := r.do(method, base+"/tenant.admission/require-mfa", body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s ended with %d, want 403", method, w.Code)
		}
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("a protected write landed %v", got)
	}
	if got := r.store.recorded(); !noStoreWrites(got) {
		t.Errorf("a protected write changed a row: %v", got)
	}
}

// TestTheTenantListingStaysAvailableWhenTheSeamCannotAnswer checks that the
// listing still answers 200 with writable false and reason unavailable.
func TestTheTenantListingStaysAvailableWhenTheSeamCannotAnswer(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	r.writeSeam.fail(errUnreachable)

	w := r.do(http.MethodGet, base, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the listing ended with %d, want 200: %s", w.Code, w.Body.String())
	}
	var envelope tenantEnvelope
	decode(t, w, &envelope)
	if envelope.Writable {
		t.Error("writable is true when the seam could not answer")
	}
	if envelope.ReadOnlyReason != "unavailable" {
		t.Errorf("read_only_reason = %q, want unavailable", envelope.ReadOnlyReason)
	}
	if got, want := keysOfSettings(envelope), []string{mfaKey, ssoKey}; !reflect.DeepEqual(got, want) {
		t.Errorf("the listing holds %v, want %v", got, want)
	}
	if body := w.Body.String(); strings.Contains(body, errUnreachable.Error()) {
		t.Errorf("the answer leaks the seam's cause: %s", body)
	}
	if got := r.writeSeam.askedTenants(); len(got) != 1 || got[0] != "acme" {
		t.Errorf("the seam was asked about %v, want [acme]", got)
	}
}

// TestTheTenantListingFailsWhenALayerCannotAnswer checks that a layer read failure is a 503.
func TestTheTenantListingFailsWhenALayerCannotAnswer(t *testing.T) {
	for name, script := range map[string]func(*rig){
		"tenant rows": func(r *rig) { r.store.listErr = errUnreachable },
		"global rows": func(r *rig) { r.store.globalListErr = errUnreachable },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = admissionDeclared()
			script(r)
			if w := r.do(http.MethodGet, base, nil); w.Code != http.StatusServiceUnavailable {
				t.Fatalf("the listing ended with %d, want 503", w.Code)
			}
		})
	}
}

// TestTheFragmentNamesNoPlatformKeyOrOldWording checks that the fragment names
// no concrete platform key and carries no platform-only wording.
func TestTheFragmentNamesNoPlatformKeyOrOldWording(t *testing.T) {
	body, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatalf("reading %s: %v", fragmentPath, err)
	}
	text := string(body)
	for _, leak := range []string{
		"jobs.db", "notification.delivery", "objectstore.metadata", "sms.budget",
		"ratelimit", "authn.", "tenant.admission", "platform-only",
	} {
		if strings.Contains(text, leak) {
			t.Errorf("the fragment names %q", leak)
		}
	}
}

// TestAPlatformOnlyKeyIsA404OnBothWriteRoutes checks that a platform-only key is invisible on both write routes, as on the read.
func TestAPlatformOnlyKeyIsA404OnBothWriteRoutes(t *testing.T) {
	for _, key := range []string{"/org.create/max-organizations", "/org.create/max-per-hour"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+key, func(t *testing.T) {
				r := newRig(t)
				r.store.declared = admissionDeclared()
				var body any
				if method == http.MethodPut {
					body = map[string]any{"value": 10}
				}
				w := r.do(method, base+key, body)
				if w.Code != http.StatusNotFound {
					t.Fatalf("the request ended with %d, want 404", w.Code)
				}
				if w.Body.Len() != 0 {
					t.Errorf("the 404 carried the body %q, want none", w.Body.String())
				}
				if got := r.audit.entries(); len(got) != 0 {
					t.Errorf("a platform-only key reached the audit: %v", got)
				}
				if got := r.store.ops(); len(got) != 0 {
					t.Errorf("a platform-only key reached the settings surface: %v", got)
				}
				if got := r.writeSeam.askedTenants(); len(got) != 0 {
					t.Errorf("a platform-only key reached the write seam: %v", got)
				}
			})
		}
	}
}
