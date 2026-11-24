package catalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

type assocContact struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (assocContact) TableName() string { return "assoc_contacts" }

type assocTag struct {
	ID string `gorm:"column:id;primaryKey"`
}

func (assocTag) TableName() string { return "assoc_tags" }

type assocNote struct {
	tenant.Model
	ID     string     `gorm:"column:id;primaryKey"`
	DealID string     `gorm:"column:deal_id"`
	Deal   *assocDeal `gorm:"foreignKey:TenantID,DealID;references:TenantID,ID"`
}

func (assocNote) TableName() string { return "assoc_notes" }

type assocDeal struct {
	tenant.Model
	ID string `gorm:"column:id;primaryKey"`

	Linked    []assocContact `gorm:"many2many:assoc_linked;joinForeignKey:TenantID,DealID;joinReferences:TenantID,ContactID"`
	Built     []assocContact `gorm:"many2many:assoc_built;joinForeignKey:TenantID,DealID;joinReferences:TenantID,ContactID"`
	Bare      []assocContact `gorm:"many2many:assoc_bare;joinForeignKey:TenantID,DealID;joinReferences:TenantID,ContactID"`
	Renamed   []assocContact `gorm:"many2many:assoc_renamed;joinForeignKey:LinkTenant,DealID;joinReferences:LinkTenant,ContactID"`
	Owned     []assocContact `gorm:"many2many:assoc_owned;joinForeignKey:TenantID,DealID;references:ID;joinReferences:ContactID"`
	Tags      []assocTag     `gorm:"many2many:assoc_deal_tags;joinForeignKey:TenantID,DealID;joinReferences:TagID"`
	Split     []assocContact `gorm:"many2many:assoc_split"`
	Loose     []assocContact `gorm:"many2many:assoc_loose;foreignKey:ID;joinForeignKey:DealID;references:ID;joinReferences:ContactID"`
	Stamped   []assocContact `gorm:"many2many:assoc_stamped;foreignKey:ID;joinForeignKey:DealID;references:ID;joinReferences:ContactID"`
	Misplaced []assocContact `gorm:"many2many:assoc_misplaced;joinForeignKey:TenantID,DealID;joinReferences:TenantID,ContactID"`

	Notes []assocNote `gorm:"foreignKey:TenantID,DealID;references:TenantID,ID"`
}

func (assocDeal) TableName() string { return "assoc_deals" }

type assocLink struct {
	tenant.Model
	DealID    string `gorm:"column:deal_id;primaryKey"`
	ContactID string `gorm:"column:contact_id;primaryKey"`
}

func (assocLink) TableName() string { return "assoc_linked" }

type assocBareLink struct {
	TenantID  string `gorm:"column:tenant_id;primaryKey"`
	DealID    string `gorm:"column:deal_id;primaryKey"`
	ContactID string `gorm:"column:contact_id;primaryKey"`
}

func (assocBareLink) TableName() string { return "assoc_bare" }

type assocStampedLink struct {
	tenant.Model
	DealID    string `gorm:"column:deal_id;primaryKey"`
	ContactID string `gorm:"column:contact_id;primaryKey"`
}

func (assocStampedLink) TableName() string { return "assoc_stamped" }

type assocMisplacedLink struct {
	Link      tenant.Model `gorm:"embedded;embeddedPrefix:link_"`
	TenantID  string       `gorm:"column:tenant_id;primaryKey"`
	DealID    string       `gorm:"column:deal_id;primaryKey"`
	ContactID string       `gorm:"column:contact_id;primaryKey"`
}

func (assocMisplacedLink) TableName() string { return "assoc_misplaced" }

type byName int

const (
	joinedInTenant byName = iota
	joinRefused
)

type assocShape struct {
	name           string
	field          string
	join           any
	table          string
	columns        string
	tenant, target string
	seed           string
	expect         recognition
	byName         byName
	global         bool
	theirs         string
}

var assocShapes = []assocShape{
	{
		name: "through a join model embedding the convention on the column both keys share", field: "Linked",
		join: &assocLink{}, table: "assoc_linked",
		columns: "tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, contact_id TEXT NOT NULL",
		tenant:  "tenant_id", target: "contact_id",
		seed: "(tenant_id, deal_id, contact_id) VALUES ('acme', 'twin', 'a'), ('globex', 'twin', 'b')",
	},
	{
		name: "through a join model GORM builds on the column both keys share", field: "Built", table: "assoc_built",
		columns: "tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, contact_id TEXT NOT NULL",
		tenant:  "tenant_id", target: "contact_id",
		seed: "(tenant_id, deal_id, contact_id) VALUES ('acme', 'twin', 'a'), ('globex', 'twin', 'b')",
	},
	{
		name: "through a join model of the host's not embedding the convention", field: "Bare",
		join: &assocBareLink{}, table: "assoc_bare",
		columns: "tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, contact_id TEXT NOT NULL",
		tenant:  "tenant_id", target: "contact_id",
		seed: "(tenant_id, deal_id, contact_id) VALUES ('acme', 'twin', 'a'), ('globex', 'twin', 'b')",
	},
	{
		name: "through a join table whose shared column is named otherwise", field: "Renamed",
		table:   "assoc_renamed",
		columns: "link_tenant TEXT NOT NULL, deal_id TEXT NOT NULL, contact_id TEXT NOT NULL",
		tenant:  "link_tenant", target: "contact_id",
		seed: "(link_tenant, deal_id, contact_id) VALUES ('acme', 'twin', 'a'), ('globex', 'twin', 'b')",
	},
	{
		name: "through a join table carrying the owner's tenant alone", field: "Owned", table: "assoc_owned",
		columns: "tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, contact_id TEXT NOT NULL",
		tenant:  "tenant_id", target: "contact_id", theirs: "b@acme,b@globex",
		seed: "(tenant_id, deal_id, contact_id) VALUES ('acme', 'twin', 'a'), ('globex', 'twin', 'b')",
	},
	{
		name: "to a global model, through a join table carrying the owner's tenant", field: "Tags",
		table: "assoc_deal_tags", global: true,
		columns: "tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, tag_id TEXT NOT NULL",
		tenant:  "tenant_id", target: "tag_id",
		seed: "(tenant_id, deal_id, tag_id) VALUES ('acme', 'twin', 'a'), ('globex', 'twin', 'b')",
	},
	{
		name: "through a join model GORM builds with a tenant column for each side", field: "Split",
		table: "assoc_split", expect: refusedShape, byName: joinRefused,
		columns: "assoc_deal_tenant_id TEXT, assoc_deal_id TEXT, assoc_contact_tenant_id TEXT, assoc_contact_id TEXT",
		tenant:  "assoc_deal_tenant_id", target: "assoc_contact_id",
		seed: "(assoc_deal_tenant_id, assoc_deal_id, assoc_contact_tenant_id, assoc_contact_id) VALUES " +
			"('acme', 'twin', 'acme', 'a'), ('globex', 'twin', 'globex', 'b')",
	},
	{
		name: "through a join table carrying no tenant", field: "Loose", table: "assoc_loose",
		expect: refusedShape, byName: joinRefused,
		columns: "deal_id TEXT, contact_id TEXT", tenant: "'-'", target: "contact_id",
		seed: "(deal_id, contact_id) VALUES ('twin', 'a'), ('twin', 'b')",
	},
	{
		name: "through a join model embedding the convention, both keys joined without it", field: "Stamped",
		join: &assocStampedLink{}, table: "assoc_stamped", expect: refusedShape, byName: joinRefused,
		columns: "tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, contact_id TEXT NOT NULL",
		tenant:  "tenant_id", target: "contact_id",
		seed: "(tenant_id, deal_id, contact_id) VALUES ('acme', 'twin', 'a'), ('globex', 'twin', 'b')",
	},
	{
		name: "through a join model embedding the convention on another column", field: "Misplaced",
		join: &assocMisplacedLink{}, table: "assoc_misplaced", expect: refusedShape, byName: joinedInTenant,
		columns: "link_tenant_id TEXT, tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, contact_id TEXT NOT NULL",
		tenant:  "tenant_id", target: "contact_id",
		seed: "(link_tenant_id, tenant_id, deal_id, contact_id) VALUES ('acme', 'acme', 'twin', 'a'), " +
			"('globex', 'globex', 'twin', 'b')",
	},
}

// TestEveryAssociationShapeIsRecognised checks that every many-to-many shape is
// either held to the context's tenant or refused, the same way on both dialects.
func TestEveryAssociationShapeIsRecognised(t *testing.T) {
	for _, d := range shapeDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			createAssociationSides(t, handle)
			for _, s := range assocShapes {
				t.Run(s.name, func(t *testing.T) {
					createJoinTable(t, handle, s)
					switch s.expect {
					case scopedShape:
						requireScopedAssociation(t, handle, s)
					case refusedShape:
						requireRefusedAssociation(t, handle, s)
					}
				})
			}
		})
	}
}

func keyedDeal() *assocDeal { return &assocDeal{ID: "twin"} }

func dealOf(owner string) *assocDeal {
	return &assocDeal{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: "twin"}
}

func keyedTarget(s assocShape, id string) any { return targetOf(s, id, "") }

func targetOf(s assocShape, id, owner string) any {
	if s.global {
		return &assocTag{ID: id}
	}
	return &assocContact{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
}

func targets(s assocShape) any {
	if s.global {
		return &[]assocTag{}
	}
	return &[]assocContact{}
}

func namedTargets(out any) string {
	var ids []string
	switch v := out.(type) {
	case *[]assocTag:
		for _, tag := range *v {
			ids = append(ids, tag.ID)
		}
	case *[]assocContact:
		for _, c := range *v {
			ids = append(ids, c.ID+"@"+string(c.TenantID))
		}
	default:
		return fmt.Sprintf("%T", out)
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

func associationStatements(db *gorm.DB, s assocShape, owner func() *assocDeal, tenantOf string,
	reads bool,
) map[string]error {
	assoc := func() *gorm.Association { return db.Model(owner()).Association(s.field) }
	out := map[string]error{
		"Append":  assoc().Append(targetOf(s, "c", tenantOf)),
		"Replace": assoc().Replace(targetOf(s, "c", tenantOf)),
		"Delete":  assoc().Delete(targetOf(s, "a", tenantOf)),
		"Clear":   assoc().Clear(),
		"Preload": db.Preload(s.field).Find(&[]assocDeal{}).Error,
	}
	if reads {
		out["Find"] = assoc().Find(targets(s))
		counted := assoc()
		counted.Count()
		out["Count"] = counted.Error
	}
	return out
}

func requireScopedAssociation(t *testing.T, handle *gorm.DB, s assocShape) {
	acme := handle.WithContext(scopedContext("acme"))
	for what, err := range associationStatements(handle.WithContext(context.Background()), s, keyedDeal, "", true) {
		if !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("%s with no tenant gave %v, want ErrNoTenantInContext", what, err)
		}
	}
	requireLinks(t, handle, s, "acme:a globex:b")

	assoc := func() *gorm.Association { return acme.Model(keyedDeal()).Association(s.field) }
	found := targets(s)
	want := "a@acme"
	if s.global {
		want = "a"
	}
	if err := assoc().Find(found); err != nil || namedTargets(found) != want {
		t.Errorf("a read of the association under acme gave %v and %q, want acme's %s", err, namedTargets(found), want)
	}
	counted := assoc()
	if n := counted.Count(); counted.Error != nil || n != 1 {
		t.Errorf("a count of the association under acme gave %v and %d, want 1", counted.Error, n)
	}

	if err := assoc().Append(keyedTarget(s, "c")); err != nil {
		t.Errorf("an addition under acme gave %v", err)
	}
	requireLinks(t, handle, s, "acme:a acme:c globex:b")
	if err := assoc().Delete(keyedTarget(s, "a")); err != nil {
		t.Errorf("a removal under acme gave %v", err)
	}
	requireLinks(t, handle, s, "acme:c globex:b")
	if err := assoc().Replace(keyedTarget(s, "b")); err != nil {
		t.Errorf("a replacement under acme gave %v", err)
	}
	requireLinks(t, handle, s, "acme:b globex:b")

	var deals []assocDeal
	if err := acme.Preload(s.field).Find(&deals).Error; err != nil || len(deals) != 1 {
		t.Errorf("a preload under acme gave %v and %d deals, want acme's twin", err, len(deals))
	} else if got := reflect.ValueOf(deals[0]).FieldByName(s.field).Len(); got != 1 {
		t.Errorf("a preload under acme gave the twin %d records, want its one", got)
	}

	foreign := acme.Model(dealOf("globex")).Association(s.field).Delete(keyedTarget(s, "b"))
	if !errors.Is(foreign, tenant.ErrExplicitTenantMismatch) {
		t.Errorf("a removal under acme from an owner naming globex gave %v, want ErrExplicitTenantMismatch", foreign)
	}
	requireLinks(t, handle, s, "acme:b globex:b")
	peeked := targets(s)
	if err := acme.Model(dealOf("globex")).Association(s.field).Find(peeked); err != nil || namedTargets(peeked) != "" {
		t.Errorf("a read under acme of an owner naming globex gave %v and %q, want nothing", err, namedTargets(peeked))
	}

	across := handle.WithContext(tenant.CrossTenant(context.Background()))
	theirs := targets(s)
	wantTheirs := "b@globex"
	switch {
	case s.theirs != "":
		wantTheirs = s.theirs
	case s.global:
		wantTheirs = "b"
	}
	if err := across.Model(dealOf("globex")).Association(s.field).Find(theirs); err != nil ||
		namedTargets(theirs) != wantTheirs {
		t.Errorf("a read across tenants of globex's association gave %v and %q, want %s", err,
			namedTargets(theirs), wantTheirs)
	}

	if err := assoc().Clear(); err != nil {
		t.Errorf("clearing the association under acme gave %v", err)
	}
	requireLinks(t, handle, s, "globex:b")
}

func requireRefusedAssociation(t *testing.T, handle *gorm.DB, s assocShape) {
	contexts := []struct {
		name     string
		ctx      context.Context
		owner    func() *assocDeal
		tenantOf string
	}{
		{"no tenant", context.Background(), keyedDeal, ""},
		{"acme", scopedContext("acme"), keyedDeal, ""},
		{"across tenants", tenant.CrossTenant(context.Background()), func() *assocDeal { return dealOf("acme") }, "acme"},
	}
	for _, c := range contexts {
		db := handle.WithContext(c.ctx)
		for what, err := range associationStatements(db, s, c.owner, c.tenantOf, s.byName == joinRefused) {
			switch {
			case !errors.Is(err, tenant.ErrNoTenantInContext):
				t.Errorf("%s under %s gave %v, want ErrNoTenantInContext", what, c.name, err)
			case c.name != "no tenant" && !strings.Contains(err.Error(), s.table):
				t.Errorf("%s under %s gave %q, which does not name the join table", what, c.name, err)
			}
		}
	}
	if s.byName == joinedInTenant {
		found := targets(s)
		err := handle.WithContext(scopedContext("acme")).Model(keyedDeal()).Association(s.field).Find(found)
		if err != nil || namedTargets(found) != "a@acme" {
			t.Errorf("a read of the association under acme, joined by name, gave %v and %q, want acme's a",
				err, namedTargets(found))
		}
	}
	requireLinks(t, handle, s, "acme:a globex:b")
}

func createAssociationSides(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for _, stmt := range []string{
		"CREATE TABLE assoc_deals (tenant_id TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY (tenant_id, id))",
		"CREATE TABLE assoc_contacts (tenant_id TEXT NOT NULL, id TEXT NOT NULL, name TEXT, " +
			"PRIMARY KEY (tenant_id, id))",
		"CREATE TABLE assoc_tags (id TEXT PRIMARY KEY)",
		"CREATE TABLE assoc_notes (tenant_id TEXT NOT NULL, id TEXT NOT NULL, deal_id TEXT, " +
			"PRIMARY KEY (tenant_id, id))",
		"INSERT INTO assoc_deals (tenant_id, id) VALUES ('acme', 'twin'), ('globex', 'twin')",
		"INSERT INTO assoc_contacts (tenant_id, id, name) VALUES ('acme', 'a', 'a at acme'), " +
			"('acme', 'b', 'b at acme'), ('acme', 'c', 'c at acme'), ('globex', 'a', 'a at globex'), " +
			"('globex', 'b', 'b at globex')",
		"INSERT INTO assoc_tags (id) VALUES ('a'), ('b'), ('c')",
		"INSERT INTO assoc_notes (tenant_id, id, deal_id) VALUES ('acme', 'n1', 'twin'), ('globex', 'n2', 'twin')",
	} {
		if err := handle.Exec(stmt).Error; err != nil {
			t.Fatalf("preparing the sides: %v", err)
		}
	}
}

func createJoinTable(t *testing.T, handle *gorm.DB, s assocShape) {
	t.Helper()
	if err := handle.Exec(fmt.Sprintf("CREATE TABLE %s (%s)", s.table, s.columns)).Error; err != nil {
		t.Fatalf("creating the join table: %v", err)
	}
	if err := handle.Exec(fmt.Sprintf("INSERT INTO %s %s", s.table, s.seed)).Error; err != nil {
		t.Fatalf("seeding the join table: %v", err)
	}
	if s.join != nil {
		if err := handle.SetupJoinTable(&assocDeal{}, s.field, s.join); err != nil {
			t.Fatalf("naming the join model: %v", err)
		}
	}
}

func requireLinks(t *testing.T, handle *gorm.DB, s assocShape, want string) {
	t.Helper()
	var rows []struct {
		Tenant string
		Target string
	}
	if err := handle.WithContext(tenant.CrossTenant(context.Background())).
		Raw(fmt.Sprintf("SELECT %s AS tenant, %s AS target FROM %s ORDER BY 1, 2", s.tenant, s.target, s.table)).
		Scan(&rows).Error; err != nil {
		t.Fatalf("reading the links back: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Tenant+":"+r.Target)
	}
	if s.tenant == "'-'" {
		want = strings.NewReplacer("acme:", "-:", "globex:", "-:").Replace(want)
	}
	if strings.Join(got, " ") != want {
		t.Errorf("the join table holds %q, want %q", strings.Join(got, " "), want)
	}
}

// TestALinkDeletionOfLoadedRecordsRuns checks that deleting links with the owner
// and records loaded runs.
func TestALinkDeletionOfLoadedRecordsRuns(t *testing.T) {
	for _, d := range shapeDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			createAssociationSides(t, handle)
			s := assocShapes[0]
			createJoinTable(t, handle, s)
			acme := handle.WithContext(scopedContext("acme"))
			loaded := &assocContact{Model: tenant.Model{TenantID: "acme"}, ID: "a"}
			if err := acme.Model(dealOf("acme")).Association(s.field).Delete(loaded); err != nil {
				t.Fatalf("removing a loaded contact from a loaded deal under acme gave %v", err)
			}
			requireLinks(t, handle, s, "globex:b")
		})
	}
}

// TestAReplacementKeepsTheRecordsItIsGivenInTheirTenant checks that a replacement
// keeps the links to the given records and removes the rest, within the tenant.
func TestAReplacementKeepsTheRecordsItIsGivenInTheirTenant(t *testing.T) {
	for _, d := range shapeDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			createAssociationSides(t, handle)
			s := assocShapes[0]
			createJoinTable(t, handle, s)
			acme := handle.WithContext(scopedContext("acme"))
			kept := &assocContact{ID: "c"}
			err := acme.Model(keyedDeal()).Omit(s.field + ".*").Association(s.field).Replace(kept)
			if err != nil {
				t.Fatalf("replacing the links with c under acme gave %v", err)
			}
			if kept.TenantID != "" {
				t.Fatalf("the record kept was saved and given tenant %q, so this case keys nothing empty", kept.TenantID)
			}
			requireLinks(t, handle, s, "acme:c globex:b")
		})
	}
}

// TestAnAssociationByTheWholeKeyStaysInItsTenant checks that has-many and
// belongs-to associations keyed by the whole key stay in the context's tenant, and
// that emptying the tenant column is refused.
func TestAnAssociationByTheWholeKeyStaysInItsTenant(t *testing.T) {
	for _, d := range shapeDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			createAssociationSides(t, handle)
			acme := handle.WithContext(scopedContext("acme"))
			notes := func() *gorm.Association { return acme.Model(keyedDeal()).Association("Notes") }

			var found []assocNote
			if err := notes().Find(&found); err != nil || len(found) != 1 || found[0].ID != "n1" {
				t.Fatalf("a read of the twin's notes under acme gave %v and %+v, want acme's n1", err, found)
			}
			if err := notes().Append(&assocNote{ID: "n3"}); err != nil {
				t.Fatalf("adding a note under acme gave %v", err)
			}
			requireNotes(t, handle, "n1=acme:twin n2=globex:twin n3=acme:twin")

			var deal assocDeal
			if err := acme.Model(&assocNote{ID: "n1", DealID: "twin"}).Association("Deal").Find(&deal); err != nil ||
				deal.TenantID != "acme" {
				t.Fatalf("a read of a note's deal under acme gave %v and %+v, want acme's twin", err, deal)
			}

			for what, err := range map[string]error{
				"removing a note": notes().Delete(&assocNote{ID: "n3"}),
				"clearing notes":  notes().Clear(),
			} {
				if !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
					t.Errorf("%s under acme gave %v, want ErrExplicitTenantMismatch", what, err)
				}
			}
			requireNotes(t, handle, "n1=acme:twin n2=globex:twin n3=acme:twin")

			if err := acme.Model(keyedDeal()).Association("Notes").Unscoped().Delete(&assocNote{ID: "n3"}); err != nil {
				t.Fatalf("deleting a note through the association under acme gave %v", err)
			}
			requireNotes(t, handle, "n1=acme:twin n2=globex:twin")

			none := handle.WithContext(context.Background()).Model(keyedDeal()).Association("Notes")
			if err := none.Find(&found); !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Errorf("a read of the notes with no tenant gave %v, want ErrNoTenantInContext", err)
			}
		})
	}
}

func requireNotes(t *testing.T, handle *gorm.DB, want string) {
	t.Helper()
	var rows []struct {
		ID     string
		Tenant *string
		Deal   *string
	}
	if err := handle.WithContext(tenant.CrossTenant(context.Background())).
		Raw("SELECT id, tenant_id AS tenant, deal_id AS deal FROM assoc_notes ORDER BY id").
		Scan(&rows).Error; err != nil {
		t.Fatalf("reading the notes back: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		tenantOf, dealOf := "null", "null"
		if r.Tenant != nil {
			tenantOf = *r.Tenant
		}
		if r.Deal != nil {
			dealOf = *r.Deal
		}
		got = append(got, r.ID+"="+tenantOf+":"+dealOf)
	}
	if strings.Join(got, " ") != want {
		t.Errorf("the notes are %q, want %q", strings.Join(got, " "), want)
	}
}

type assocLooseDeal struct {
	tenant.Model
	ID   string     `gorm:"column:id;primaryKey"`
	Tags []assocTag `gorm:"many2many:assoc_loose_tags;foreignKey:ID;joinForeignKey:DealID;joinReferences:TagID"`
}

func (assocLooseDeal) TableName() string { return "assoc_deals" }

type assocTextDeal struct {
	tenant.Model
	TenantID string     `gorm:"column:tenant_id;primaryKey"`
	ID       string     `gorm:"column:id;primaryKey"`
	Tags     []assocTag `gorm:"many2many:assoc_text_tags;joinForeignKey:TenantID,DealID;joinReferences:TagID"`
}

func (assocTextDeal) TableName() string { return "assoc_deals" }

// TestAnAssociationWhoseReadCannotBeHeldToATenantRefusesItsOwner checks that a
// scoped model declaring an association whose read cannot be held to a tenant is
// refused on every statement.
func TestAnAssociationWhoseReadCannotBeHeldToATenantRefusesItsOwner(t *testing.T) {
	for _, d := range shapeDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			createAssociationSides(t, handle)
			for _, stmt := range []string{
				"CREATE TABLE assoc_loose_tags (deal_id TEXT, tag_id TEXT)",
				"INSERT INTO assoc_loose_tags (deal_id, tag_id) VALUES ('twin', 'a'), ('twin', 'b')",
				"CREATE TABLE assoc_text_tags (tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, tag_id TEXT NOT NULL)",
				"INSERT INTO assoc_text_tags (tenant_id, deal_id, tag_id) VALUES ('acme', 'twin', 'a'), " +
					"('globex', 'twin', 'b')",
			} {
				if err := handle.Exec(stmt).Error; err != nil {
					t.Fatalf("preparing the join tables: %v", err)
				}
			}
			owners := []struct {
				name, table string
				model       func() any
				keyed       func() any
				all         func() any
			}{
				{
					"joining by the rest of its key", "assoc_loose_tags",
					func() any { return &assocLooseDeal{} },
					func() any { return &assocLooseDeal{ID: "twin"} },
					func() any { return &[]assocLooseDeal{} },
				},
				{
					"through a tenant field that is a string", "assoc_text_tags",
					func() any { return &assocTextDeal{} },
					func() any { return &assocTextDeal{ID: "twin"} },
					func() any { return &[]assocTextDeal{} },
				},
			}
			for _, o := range owners {
				for _, c := range []struct {
					name string
					ctx  context.Context
				}{
					{"no tenant", context.Background()},
					{"acme", scopedContext("acme")},
					{"across tenants", tenant.CrossTenant(context.Background())},
				} {
					db := handle.WithContext(c.ctx)
					for what, err := range map[string]error{
						"Find":    db.Find(o.all()).Error,
						"Create":  db.Create(o.keyed()).Error,
						"Update":  db.Model(o.model()).Where("id = ?", "twin").Update("id", "twin").Error,
						"Delete":  db.Where("id = ?", "none").Delete(o.model()).Error,
						"Preload": db.Preload("Tags").Find(o.all()).Error,
						"Append":  db.Model(o.keyed()).Association("Tags").Append(&assocTag{ID: "c"}),
					} {
						switch {
						case !errors.Is(err, tenant.ErrNoTenantInContext):
							t.Errorf("%s through a deal %s under %s gave %v, want ErrNoTenantInContext",
								what, o.name, c.name, err)
						case !strings.Contains(err.Error(), "Tags") || !strings.Contains(err.Error(), o.table):
							t.Errorf("%s through a deal %s under %s gave %q, which names neither the association "+
								"nor its join table", what, o.name, c.name, err)
						}
					}
				}
			}
			var deals int64
			if err := handle.WithContext(crossContext()).Raw("SELECT count(*) FROM assoc_deals").
				Scan(&deals).Error; err != nil || deals != 2 {
				t.Errorf("the deals are %d (%v), want the two seeded", deals, err)
			}
			requireLinks(t, handle, assocShape{table: "assoc_loose_tags", tenant: "'-'", target: "tag_id"},
				"acme:a globex:b")
			requireLinks(t, handle, assocShape{table: "assoc_text_tags", tenant: "tenant_id", target: "tag_id"},
				"acme:a globex:b")
		})
	}
}

// TestAnAssociationReadWithNoOwnerInTheStatementStaysGlobal pins that the read of
// an association's other side with no owner in the statement is not held to a
// tenant; closing that read changes this case.
func TestAnAssociationReadWithNoOwnerInTheStatementStaysGlobal(t *testing.T) {
	for _, d := range shapeDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			createAssociationSides(t, handle)
			for _, stmt := range []string{
				"CREATE TABLE assoc_loose_tags (deal_id TEXT, tag_id TEXT)",
				"INSERT INTO assoc_loose_tags (deal_id, tag_id) VALUES ('twin', 'a'), ('twin', 'b')",
				"CREATE TABLE assoc_text_tags (tenant_id TEXT NOT NULL, deal_id TEXT NOT NULL, tag_id TEXT NOT NULL)",
				"INSERT INTO assoc_text_tags (tenant_id, deal_id, tag_id) VALUES ('acme', 'twin', 'a'), " +
					"('globex', 'twin', 'b')",
			} {
				if err := handle.Exec(stmt).Error; err != nil {
					t.Fatalf("preparing the join tables: %v", err)
				}
			}
			for _, c := range []struct {
				name string
				ctx  context.Context
			}{
				{"acme", scopedContext("acme")},
				{"no tenant", context.Background()},
				{"across tenants", tenant.CrossTenant(context.Background())},
			} {
				db := handle.WithContext(c.ctx)
				found := &[]assocTag{}
				if err := db.Model(&assocLooseDeal{ID: "twin"}).Association("Tags").Find(found); err != nil ||
					namedTargets(found) != "a,b" {
					t.Errorf("a read of the association joining by the rest of its key under %s gave %v and %q, "+
						"want the key's links in every tenant, a,b", c.name, err, namedTargets(found))
				}
				counted := db.Model(&assocLooseDeal{ID: "twin"}).Association("Tags")
				if n := counted.Count(); counted.Error != nil || n != 2 {
					t.Errorf("a count of that association under %s gave %v and %d, want 2", c.name, counted.Error, n)
				}
			}
			for _, c := range []struct {
				name  string
				ctx   context.Context
				owner string
				want  string
			}{
				{"acme naming acme", scopedContext("acme"), "acme", "a"},
				{"acme naming globex", scopedContext("acme"), "globex", "b"},
				{"no tenant naming globex", context.Background(), "globex", "b"},
			} {
				found := &[]assocTag{}
				owner := &assocTextDeal{TenantID: c.owner, ID: "twin"}
				if err := handle.WithContext(c.ctx).Model(owner).Association("Tags").Find(found); err != nil ||
					namedTargets(found) != c.want {
					t.Errorf("a read of the association through a tenant field under %s gave %v and %q, want %s",
						c.name, err, namedTargets(found), c.want)
				}
			}
		})
	}
}
