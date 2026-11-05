package settings

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// tenantDeclaration is the carrier the tenant-facing declaration cases mount.
type tenantDeclaration struct {
	Flag bool
}

// textTenantDeclaration mounts a string item, which a switch control has to
// refuse.
type textTenantDeclaration struct {
	Label string
}

// textBool is a bool underneath that writes and reads yes/no. classifyLeaf asks
// about the text form before the kind, so it is kindText; the tenant listing
// re-reads a switch's row with strconv.ParseBool, which cannot read its text.
type textBool bool

func (b textBool) MarshalText() ([]byte, error) {
	if b {
		return []byte("yes"), nil
	}
	return []byte("no"), nil
}

func (b *textBool) UnmarshalText(text []byte) error {
	switch string(text) {
	case "yes":
		*b = true
	case "no":
		*b = false
	default:
		return errors.New("a textBool is yes or no")
	}
	return nil
}

// textBoolTenantDeclaration mounts a bool whose text form is not bool text.
type textBoolTenantDeclaration struct {
	Flag textBool
}

// tenantItem is metadata complete enough for a tenant-facing item.
func tenantItem(access TenantAccess) Item {
	return Item{
		Description:    "whether the flag is on",
		TitleKey:       "test.tenant.flag.title",
		DescriptionKey: "test.tenant.flag.description",
		Control:        ControlSwitch,
		TenantAccess:   access,
	}
}

// declarationError builds the key table from the given modules and returns the
// failure it must produce.
func declarationError(t *testing.T, mods ...core.Module) error {
	t.Helper()
	_, err := buildKeyTable(registryWith(t, mods...))
	if err == nil {
		t.Fatal("the declaration was accepted, want a startup failure")
	}
	return err
}

// tenantSchema is one module's declaration of a single tenant-facing item.
func tenantSchema(tenantFacing bool, item Item) Schema {
	return Schema{
		Namespace:    "tenantns",
		TenantFacing: tenantFacing,
		Mounts:       []Mount{{Value: &tenantDeclaration{}}},
		Items:        map[string]Item{"flag": item},
	}
}

// An item that carries a tenant access requires the schema to say it is
// tenant-facing. The pair is the fail-closed switch: forgetting the schema-level
// declaration is a startup failure rather than an item exposed by accident.
func TestATenantFacingItemRequiresATenantFacingSchema(t *testing.T) {
	err := declarationError(t, declaring("test.tenant", tenantSchema(false, tenantItem(TenantReadWrite))))
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("the declaration answered %v, want ErrInvalidDeclaration", err)
	}
	for _, want := range []string{"test.tenant", `"flag"`, "TenantFacing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err, want)
		}
	}
}

// A tenant-facing item must carry the text and the control its surface renders.
// Each missing piece is a declaration defect of its own, so a partially written
// item cannot reach a tenant page as a bare key or an empty label.
func TestATenantFacingItemCarriesItsPresentation(t *testing.T) {
	complete := tenantItem(TenantReadWrite)
	cases := []struct {
		name   string
		mutate func(*Item)
		want   string
	}{
		{"no title key", func(item *Item) { item.TitleKey = "" }, "TitleKey"},
		{"no description key", func(item *Item) { item.DescriptionKey = "" }, "DescriptionKey"},
		{"no English description", func(item *Item) { item.Description = "" }, "Description"},
		{"no control", func(item *Item) { item.Control = ControlNone }, "Control"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item := complete
			c.mutate(&item)
			err := declarationError(t, declaring("test.tenant", tenantSchema(true, item)))
			if !errors.Is(err, ErrInvalidDeclaration) {
				t.Fatalf("the declaration answered %v, want ErrInvalidDeclaration", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the failure %q does not name the missing %s", err, c.want)
			}
		})
	}
}

// TenantAccess is a closed set. A value outside it is a declaration defect even
// when the schema is tenant-facing, because a surface could not tell what it
// grants.
func TestTenantAccessIsAClosedSet(t *testing.T) {
	err := declarationError(t, declaring("test.tenant", tenantSchema(true, tenantItem(TenantAccess("read_write")))))
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("the declaration answered %v, want ErrInvalidDeclaration", err)
	}
	for _, want := range []string{"read_write", "TenantNone", "TenantRead", "TenantReadWrite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err, want)
		}
	}
}

// A switch renders a two-state control, so an item under one has to be declared
// as bool. A non-boolean item behind a switch would pass the startup check and
// then fail the encoding of the tenant's whole list on every read, so the
// declaration is refused when the key table is built.
func TestASwitchControlRequiresABooleanItem(t *testing.T) {
	schema := Schema{
		Namespace:    "tenantns",
		TenantFacing: true,
		Mounts:       []Mount{{Value: &textTenantDeclaration{}}},
		Items:        map[string]Item{"label": tenantItem(TenantReadWrite)},
	}
	err := declarationError(t, declaring("test.tenant", schema))
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("the declaration answered %v, want ErrInvalidDeclaration", err)
	}
	for _, want := range []string{"test.tenant", `"label"`, `"switch"`, "bool"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err, want)
		}
	}
}

// A switch's item must be a plain bool, not merely a type whose underlying kind
// is bool. A named bool that carries its own text form classifies as kindText,
// and the tenant listing re-reads the stored text with strconv.ParseBool rather
// than through that type; its text — yes and no here — would fail the parse and
// turn every read of the list into a 503. Judging the classified kind rather
// than reflect.Kind refuses it at startup, where the defect is.
func TestASwitchControlRequiresAPlainBooleanItem(t *testing.T) {
	schema := Schema{
		Namespace:    "tenantns",
		TenantFacing: true,
		Mounts:       []Mount{{Value: &textBoolTenantDeclaration{}}},
		Items:        map[string]Item{"flag": tenantItem(TenantReadWrite)},
	}
	err := declarationError(t, declaring("test.tenant", schema))
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("the declaration answered %v, want ErrInvalidDeclaration", err)
	}
	for _, want := range []string{"test.tenant", `"flag"`, `"switch"`, "bool"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err, want)
		}
	}
}

// A complete declaration builds, and the metadata the module wrote is the
// metadata the key table keeps. TenantRead is part of the closed set even though
// no item declares it yet.
func TestACompleteTenantDeclarationBuilds(t *testing.T) {
	for _, access := range []TenantAccess{TenantRead, TenantReadWrite} {
		item := tenantItem(access)
		entries := keysOf(t, tenantSchema(true, item))
		got, ok := entries["tenantns.flag"]
		if !ok {
			t.Fatalf("the declaration produced no tenantns.flag: %v", sortedKeys(entries))
		}
		if got.item != item {
			t.Errorf("the key table keeps %+v, want %+v", got.item, item)
		}
	}
}

// An item with no tenant access needs no tenant metadata, and a tenant-facing
// schema may still carry platform-only items.
func TestAPlatformItemNeedsNoTenantMetadata(t *testing.T) {
	entries := keysOf(t, Schema{
		Namespace:    "platformns",
		TenantFacing: true,
		Mounts:       []Mount{{Value: &tenantDeclaration{}}},
		Items:        map[string]Item{"flag": {Description: "a platform flag"}},
	})
	got, ok := entries["platformns.flag"]
	if !ok {
		t.Fatalf("the declaration produced no platformns.flag: %v", sortedKeys(entries))
	}
	if got.item.TenantAccess != TenantNone {
		t.Errorf("the item's tenant access is %q, want TenantNone", got.item.TenantAccess)
	}
}

// Every registered schema is held to the tenant declaration, including the
// schemas of modules that declare nothing tenant-facing, and the failure names
// only the offending module's own key. A startup failure never reaches a
// tenant surface, and it does not disclose another module's keys either.
func TestEveryRegisteredSchemaIsHeldToTheTenantDeclaration(t *testing.T) {
	platform := declaring("test.platform", Schema{
		Namespace: "platformns",
		Mounts:    []Mount{{Value: &tenantDeclaration{}}},
		Items:     map[string]Item{"flag": {Description: "a platform flag"}},
	})
	item := tenantItem(TenantReadWrite)
	item.TitleKey = ""
	defective := declaring("test.tenant", tenantSchema(true, item))

	err := declarationError(t, platform, defective)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("the declaration answered %v, want ErrInvalidDeclaration", err)
	}
	if !strings.Contains(err.Error(), "test.tenant") || !strings.Contains(err.Error(), `"flag"`) {
		t.Errorf("the failure %q does not name the offending module and key", err)
	}
	if strings.Contains(err.Error(), "platformns.flag") {
		t.Errorf("the failure %q names another module's key", err)
	}
}
