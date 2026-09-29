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

// The cases in this file are about GORM's association mode on scoped models.
// It writes the keys of the records it is given into a statement's conditions,
// and a record addressed by the rest of its key leaves the tenant half of it to
// the filter, as a write addressed by a struct's key does. A many-to-many
// association goes through a join table, which is scoped by the tenant the
// scoped models it joins put into a link, and refused where no one column
// carries it.

// assocContact is a scoped model on the other side of most associations.
type assocContact struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (assocContact) TableName() string { return "assoc_contacts" }

// assocTag is a global model on the other side of an association.
type assocTag struct {
	ID string `gorm:"column:id;primaryKey"`
}

func (assocTag) TableName() string { return "assoc_tags" }

// assocNote is a scoped model a deal has many of, by a foreign key that is the
// deal's whole key, and that belongs to its deal by that key.
type assocNote struct {
	tenant.Model
	ID     string     `gorm:"column:id;primaryKey"`
	DealID string     `gorm:"column:deal_id"`
	Deal   *assocDeal `gorm:"foreignKey:TenantID,DealID;references:TenantID,ID"`
}

func (assocNote) TableName() string { return "assoc_notes" }

// assocDeal is a scoped model declaring an association of each shape.
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
	LooseTags []assocTag     `gorm:"many2many:assoc_loose_tags;foreignKey:ID;joinForeignKey:DealID;joinReferences:TagID"`

	Notes []assocNote `gorm:"foreignKey:TenantID,DealID;references:TenantID,ID"`
}

func (assocDeal) TableName() string { return "assoc_deals" }

// assocLink is a join model embedding the convention, on the column both keys
// put their tenant on.
type assocLink struct {
	tenant.Model
	DealID    string `gorm:"column:deal_id;primaryKey"`
	ContactID string `gorm:"column:contact_id;primaryKey"`
}

func (assocLink) TableName() string { return "assoc_linked" }

// assocBareLink is a join model of the host's that does not embed the
// convention, with a plain field on the column both keys put their tenant on.
type assocBareLink struct {
	TenantID  string `gorm:"column:tenant_id;primaryKey"`
	DealID    string `gorm:"column:deal_id;primaryKey"`
	ContactID string `gorm:"column:contact_id;primaryKey"`
}

func (assocBareLink) TableName() string { return "assoc_bare" }

// assocStampedLink is a join model embedding the convention for an
// association joining both of its scoped models by the rest of their keys.
type assocStampedLink struct {
	tenant.Model
	DealID    string `gorm:"column:deal_id;primaryKey"`
	ContactID string `gorm:"column:contact_id;primaryKey"`
}

func (assocStampedLink) TableName() string { return "assoc_stamped" }

// assocMisplacedLink is a join model embedding the convention on a column
// other than the one both keys put their tenant on.
type assocMisplacedLink struct {
	Link      tenant.Model `gorm:"embedded;embeddedPrefix:link_"`
	TenantID  string       `gorm:"column:tenant_id;primaryKey"`
	DealID    string       `gorm:"column:deal_id;primaryKey"`
	ContactID string       `gorm:"column:contact_id;primaryKey"`
}

func (assocMisplacedLink) TableName() string { return "assoc_misplaced" }

// byName is how the read GORM's association mode makes of a many-to-many
// association — Find and Count, which join the join table into a read of the
// other side by its table name — is to be judged.
type byName int

const (
	// joinedInTenant is a read held to the context's tenant on the column
	// the joined table carries tenants on.
	joinedInTenant byName = iota
	// joinRefused is a read refused whatever its context.
	joinRefused
	// joinUnseen is a read whose join shows the filter no tenant: the owner's
	// key names no tenant column, and the other side is global. The join
	// table's own statements are refused, so no row reaches it through the
	// filter; the design writes this read as a non-guarantee.
	joinUnseen
)

// assocShape is one way of declaring a many-to-many association between a
// scoped model and another, with the join table it goes through.
type assocShape struct {
	name  string
	field string
	// join is the join model SetupJoinTable names, nil where GORM builds it.
	join  any
	table string
	// columns are the join table's columns.
	columns string
	// tenant and target read one link back: the tenant it belongs to, and the
	// row it names beside the deal.
	tenant, target string
	// seed writes acme's twin naming a and globex's twin naming b.
	seed   string
	expect recognition
	byName byName
	// global marks an association whose other side is the global model.
	global bool
	// theirs is what a read across tenants of globex's association returns,
	// when it is not globex's b: a link naming the other side by the rest of
	// its key names that row in every tenant holding the key, and a read that
	// crosses tenants reaches each of them.
	theirs string
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
	{
		name: "to a global model, through a join table carrying no tenant", field: "LooseTags",
		table: "assoc_loose_tags", global: true, expect: refusedShape, byName: joinUnseen,
		columns: "deal_id TEXT, tag_id TEXT", tenant: "'-'", target: "tag_id",
		seed: "(deal_id, tag_id) VALUES ('twin', 'a'), ('twin', 'b')",
	},
}

// Every shape of a many-to-many association is recognised the same way on
// both dialects. Through a join table judged scoped, each statement of the
// association mode stays in the context's tenant with the owner and the
// records given addressed by the rest of their keys — the reads, the
// additions, the removals, the replacement and the preload — a link naming
// another tenant's key is refused, and every statement is refused with no
// tenant. Through one no one tenant column can be taken from, every statement
// through the join table is refused whatever the context.
func TestEveryAssociationShapeIsRecognised(t *testing.T) {
	dialects := []struct {
		name string
		open func(*testing.T) *gorm.DB
	}{
		{"sqlite", openShapesOnSQLite},
		{"postgres", openShapesOnPostgres},
	}
	for _, d := range dialects {
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

// keyedDeal is the owner of every association, addressed by the rest of its
// key.
func keyedDeal() *assocDeal { return &assocDeal{ID: "twin"} }

// dealOf is the owner, naming its tenant.
func dealOf(owner string) *assocDeal {
	return &assocDeal{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: "twin"}
}

// keyedTarget is a record of the association's other side, addressed by the
// rest of its key.
func keyedTarget(s assocShape, id string) any { return targetOf(s, id, "") }

// targetOf is a record of the association's other side naming its tenant, or
// leaving it unwritten when owner is empty.
func targetOf(s assocShape, id, owner string) any {
	if s.global {
		return &assocTag{ID: id}
	}
	return &assocContact{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
}

// targets is a pointer to an empty slice of the association's other side.
func targets(s assocShape) any {
	if s.global {
		return &[]assocTag{}
	}
	return &[]assocContact{}
}

// namedTargets lists the records a read of the association returned, in order,
// each as its identifier and, on a scoped side, its tenant.
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

// associationStatements runs each statement of the association mode once
// against a fresh association on the owner, with records of the other side
// naming owner as their tenant, and returns what each gave.
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

// createAssociationSides creates the tables on either side of the
// associations and writes two tenants' rows into them, past the filter: a deal
// named twin in each, contacts a, b and c in acme and a and b in globex, and
// the global tags a, b and c.
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

// createJoinTable creates the shape's join table, names its join model when it
// has one, and writes one link for each tenant into it, past the filter.
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

// requireLinks asserts the twin's links, each written as tenant:target.
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

// A deletion of links whose owner and records are loaded runs: GORM writes its
// conditions as a WHERE of their own inside the statement's, and the tenant
// condition put beside them keeps that WHERE a group of conditions rather than
// a clause with its keyword.
func TestALinkDeletionOfLoadedRecordsRuns(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{
		"sqlite": openShapesOnSQLite, "postgres": openShapesOnPostgres,
	} {
		t.Run(name, func(t *testing.T) {
			handle := open(t)
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

// A deal has many notes by its whole key, and each note belongs to its deal by
// it. The association mode reads and adds notes of a deal addressed by the rest
// of its key within the context's tenant, and finds a note's deal the same way.
// Taking a note off its deal writes a null into each column of the foreign key,
// the tenant column among them, and a write emptying the tenant column is
// refused; the unscoped removal, which deletes the note, stays in the tenant.
func TestAnAssociationByTheWholeKeyStaysInItsTenant(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{
		"sqlite": openShapesOnSQLite, "postgres": openShapesOnPostgres,
	} {
		t.Run(name, func(t *testing.T) {
			handle := open(t)
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

// requireNotes asserts the notes, each written as id=tenant:deal.
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
