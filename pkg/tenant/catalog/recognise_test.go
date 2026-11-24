package catalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// ShapeBase is an exported base struct a model can embed, as GORM needs it to
// be.
type ShapeBase struct {
	tenant.Model
}

type shapeBase struct {
	tenant.Model
}

// ShapeOuter embeds the convention through a base GORM does not read.
type ShapeOuter struct {
	shapeBase
}

type shapeLoop struct {
	*shapeLoop
}

// ShapeLike carries a field shaped like the convention's without being it.
type ShapeLike struct {
	TenantID tenant.ID `gorm:"column:tenant_id;not null"`
}

type shapeLike struct {
	TenantID tenant.ID `gorm:"column:tenant_id;not null"`
}

type orderMeta struct {
	TenantID string
}

type shapeValue struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeValue) TableName() string { return "shape_value" }

type shapePointer struct {
	*tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapePointer) TableName() string { return "shape_pointer" }

type shapeBaseValue struct {
	ShapeBase
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeBaseValue) TableName() string { return "shape_base" }

type shapeBasePointer struct {
	*ShapeBase
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeBasePointer) TableName() string { return "shape_base_pointer" }

type shapeNamed struct {
	Owner tenant.Model `gorm:"embedded"`
	ID    string       `gorm:"column:id;primaryKey"`
	Note  string       `gorm:"column:note"`
}

func (shapeNamed) TableName() string { return "shape_named" }

type shapeNamedPointer struct {
	Owner *tenant.Model `gorm:"embedded"`
	ID    string        `gorm:"column:id;primaryKey"`
	Note  string        `gorm:"column:note"`
}

func (shapeNamedPointer) TableName() string { return "shape_named_pointer" }

type shapePrefixed struct {
	Owner tenant.Model `gorm:"embedded;embeddedPrefix:owner_"`
	ID    string       `gorm:"column:id;primaryKey"`
	Note  string       `gorm:"column:note"`
}

func (shapePrefixed) TableName() string { return "shape_prefixed" }

type shapePrefixedValue struct {
	tenant.Model `gorm:"embeddedPrefix:owner_"`
	ID           string `gorm:"column:id;primaryKey"`
	Note         string `gorm:"column:note"`
}

func (shapePrefixedValue) TableName() string { return "shape_prefixed_value" }

type shapePrefixedBase struct {
	Base ShapeBase `gorm:"embedded;embeddedPrefix:owner_"`
	ID   string    `gorm:"column:id;primaryKey"`
	Note string    `gorm:"column:note"`
}

func (shapePrefixedBase) TableName() string { return "shape_prefixed_base" }

type shapeOwnTenantID struct {
	tenant.Model
	TenantID *string `gorm:"column:tenant_id"`
	ID       string  `gorm:"column:id;primaryKey"`
	Note     string  `gorm:"column:note"`
}

func (shapeOwnTenantID) TableName() string { return "shape_own_tenant_id" }

type shapeTenantIDFirst struct {
	TenantID string `json:"tenant"`
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeTenantIDFirst) TableName() string { return "shape_tenant_id_first" }

type shapeColumnTagged struct {
	tenant.Model
	Owner string `gorm:"column:tenant_id"`
	ID    string `gorm:"column:id;primaryKey"`
	Note  string `gorm:"column:note"`
}

func (shapeColumnTagged) TableName() string { return "shape_column_tagged" }

type shapeEmbeddingFirst struct {
	Meta orderMeta `gorm:"embedded"`
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeEmbeddingFirst) TableName() string { return "shape_embedding_first" }

type shapePrefixedBeside struct {
	Owner    tenant.Model `gorm:"embedded;embeddedPrefix:owner_"`
	TenantID string       `gorm:"column:tenant_id"`
	ID       string       `gorm:"column:id;primaryKey"`
	Note     string       `gorm:"column:note"`
}

func (shapePrefixedBeside) TableName() string { return "shape_prefixed_beside" }

type shapePrefixedTaken struct {
	Owner  tenant.Model `gorm:"embedded;embeddedPrefix:owner_"`
	Holder string       `gorm:"column:owner_tenant_id"`
	ID     string       `gorm:"column:id;primaryKey"`
	Note   string       `gorm:"column:note"`
}

func (shapePrefixedTaken) TableName() string { return "shape_prefixed_taken" }

type shapeLookalike struct {
	ShapeLike
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeLookalike) TableName() string { return "shape_lookalike" }

type shapeUnexportedLookalike struct {
	shapeLike
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeUnexportedLookalike) TableName() string { return "shape_unexported_lookalike" }

type shapeOwnColumn struct {
	TenantID tenant.ID `gorm:"column:tenant_id;not null"`
	ID       string    `gorm:"column:id;primaryKey"`
	Note     string    `gorm:"column:note"`
}

func (shapeOwnColumn) TableName() string { return "shape_own_column" }

type shapeUnexportedBase struct {
	shapeBase
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeUnexportedBase) TableName() string { return "shape_unexported_base" }

type shapeOuterBase struct {
	ShapeOuter
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeOuterBase) TableName() string { return "shape_outer_base" }

type shapeSelfEmbedding struct {
	shapeLoop
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeSelfEmbedding) TableName() string { return "shape_self_embedding" }

type shapeUnexportedNamed struct {
	owner tenant.Model `gorm:"embedded"`
	ID    string       `gorm:"column:id;primaryKey"`
	Note  string       `gorm:"column:note"`
}

func (shapeUnexportedNamed) TableName() string { return "shape_unexported_named" }

type shapeIgnored struct {
	tenant.Model `gorm:"-"`
	ID           string `gorm:"column:id;primaryKey"`
	Note         string `gorm:"column:note"`
}

func (shapeIgnored) TableName() string { return "shape_ignored" }

type shapeTwice struct {
	Seller tenant.Model `gorm:"embedded;embeddedPrefix:seller_"`
	Buyer  tenant.Model `gorm:"embedded;embeddedPrefix:buyer_"`
	ID     string       `gorm:"column:id;primaryKey"`
	Note   string       `gorm:"column:note"`
}

func (shapeTwice) TableName() string { return "shape_twice" }

type shapeDoubled struct {
	tenant.Model
	ShapeBase
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeDoubled) TableName() string { return "shape_doubled" }

type shapeNamedUpper struct {
	Owner tenant.Model `gorm:"EMBEDDED"`
	ID    string       `gorm:"column:id;primaryKey"`
	Note  string       `gorm:"column:note"`
}

func (shapeNamedUpper) TableName() string { return "shape_named_upper" }

type shapeUnexportedUpper struct {
	owner tenant.Model `gorm:"Embedded"`
	ID    string       `gorm:"column:id;primaryKey"`
	Note  string       `gorm:"column:note"`
}

func (shapeUnexportedUpper) TableName() string { return "shape_unexported_upper" }

type shapeShadowed struct {
	Owner string `gorm:"column:TENANT_ID"`
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (shapeShadowed) TableName() string { return "shape_shadowed" }

type shapeDuplicated struct {
	tenant.Model
	ID   string    `gorm:"column:id;primaryKey"`
	Note string    `gorm:"column:note"`
	Meta orderMeta `gorm:"embedded"`
}

func (shapeDuplicated) TableName() string { return "shape_duplicated" }

type shapeDefaulted struct {
	tenant.Model
	ID   string        `gorm:"column:id;primaryKey"`
	Note string        `gorm:"column:note"`
	Meta defaultedMeta `gorm:"embedded"`
}

func (shapeDefaulted) TableName() string { return "shape_defaulted" }

type recognition int

const (
	scopedShape recognition = iota
	globalShape
	refusedShape
)

type shape struct {
	name                 string
	expect               recognition
	table                string
	column               string
	extra, extraPostgres string
	keyOutside           bool
	row                  func(id, owner string) any
	unwritten            func(id string) any
	stray                func(id string) any
}

var shapes = []shape{
	{
		name: "embedded by value", table: "shape_value", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeValue{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "embedded by pointer", table: "shape_pointer", column: "tenant_id",
		row: func(id, owner string) any {
			o := &shapePointer{ID: id}
			if owner != "" {
				o.Model = &tenant.Model{TenantID: tenant.ID(owner)}
			}
			return o
		},
	},
	{
		name: "through an exported base", table: "shape_base", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeBaseValue{ShapeBase: ShapeBase{Model: tenant.Model{TenantID: tenant.ID(owner)}}, ID: id}
		},
	},
	{
		name: "through an exported base by pointer", table: "shape_base_pointer", column: "tenant_id",
		row: func(id, owner string) any {
			o := &shapeBasePointer{ID: id}
			if owner != "" {
				o.ShapeBase = &ShapeBase{Model: tenant.Model{TenantID: tenant.ID(owner)}}
			}
			return o
		},
	},
	{
		name: "as a named field tagged embedded", table: "shape_named", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeNamed{Owner: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "as a named pointer field tagged embedded", table: "shape_named_pointer", column: "tenant_id",
		row: func(id, owner string) any {
			o := &shapeNamedPointer{ID: id}
			if owner != "" {
				o.Owner = &tenant.Model{TenantID: tenant.ID(owner)}
			}
			return o
		},
	},
	{
		name: "as a named field under an embedded prefix", table: "shape_prefixed", column: "owner_tenant_id",
		row: func(id, owner string) any {
			return &shapePrefixed{Owner: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "embedded by value under an embedded prefix", table: "shape_prefixed_value", column: "owner_tenant_id",
		row: func(id, owner string) any {
			return &shapePrefixedValue{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "through an exported base under an embedded prefix", table: "shape_prefixed_base",
		column: "owner_tenant_id",
		row: func(id, owner string) any {
			return &shapePrefixedBase{Base: ShapeBase{Model: tenant.Model{TenantID: tenant.ID(owner)}}, ID: id}
		},
	},
	{
		name: "beside a field of the model's own named TenantID", table: "shape_own_tenant_id", column: "tenant_id",
		keyOutside: true,
		row: func(id, owner string) any {
			o := &shapeOwnTenantID{ID: id}
			if owner != "" {
				o.TenantID = &owner
			}
			return o
		},
	},
	{
		name: "after a field named TenantID", table: "shape_tenant_id_first", column: "tenant_id",
		keyOutside: true,
		row:        func(id, owner string) any { return &shapeTenantIDFirst{TenantID: owner, ID: id} },
	},
	{
		name: "beside a field of another name tagged with its column", table: "shape_column_tagged",
		column:     "tenant_id",
		keyOutside: true,
		row:        func(id, owner string) any { return &shapeColumnTagged{Owner: owner, ID: id} },
	},
	{
		name: "after an embedding GORM binds its column to first", table: "shape_embedding_first",
		column:     "tenant_id",
		keyOutside: true,
		row: func(id, owner string) any {
			return &shapeEmbeddingFirst{Meta: orderMeta{TenantID: owner}, ID: id}
		},
	},
	{
		name:  "under an embedded prefix beside a field named TenantID on another column",
		table: "shape_prefixed_beside", column: "owner_tenant_id", extra: ", tenant_id TEXT",
		row: func(id, owner string) any {
			return &shapePrefixedBeside{Owner: tenant.Model{TenantID: tenant.ID(owner)}, TenantID: "unjudged", ID: id}
		},
	},
	{
		name:  "under an embedded prefix beside a field tagged with the prefixed column",
		table: "shape_prefixed_taken", column: "owner_tenant_id",
		keyOutside: true,
		row:        func(id, owner string) any { return &shapePrefixedTaken{Holder: owner, ID: id} },
	},
	{
		name: "embedded twice on one column", table: "shape_doubled", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeDoubled{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "as a named field tagged embedded in capitals", table: "shape_named_upper", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeNamedUpper{Owner: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name:  "after a field of the model's own on another spelling of its column",
		table: "shape_shadowed", column: "tenant_id", extraPostgres: `, "TENANT_ID" TEXT`,
		row: func(id, owner string) any {
			return &shapeShadowed{Owner: owner, Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
		unwritten: func(id string) any { return &shapeShadowed{Owner: "acme", ID: id} },
		stray:     func(id string) any { return &shapeShadowed{Owner: "globex", ID: id} },
	},
	{
		name: "beside an embedding repeating its column that GORM never writes", table: "shape_duplicated",
		column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeDuplicated{
				Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id, Meta: orderMeta{TenantID: "globex"},
			}
		},
	},
	{
		name:  "beside an embedding repeating its column that GORM writes when it holds a value",
		table: "shape_defaulted", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeDefaulted{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
		stray: func(id string) any {
			return &shapeDefaulted{ID: id, Meta: defaultedMeta{TenantID: "globex"}}
		},
	},
	{
		name: "a lookalike of the convention through an exported base", expect: globalShape,
		table: "shape_lookalike", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeLookalike{ShapeLike: ShapeLike{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "a lookalike of the convention through an unexported base", expect: globalShape,
		table: "shape_unexported_lookalike", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeUnexportedLookalike{shapeLike: shapeLike{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "embedding a struct that embeds a pointer to itself", expect: globalShape,
		table: "shape_self_embedding", column: "tenant_id",
		row: func(id, _ string) any {
			return &shapeSelfEmbedding{shapeLoop: shapeLoop{shapeLoop: &shapeLoop{}}, ID: id}
		},
	},
	{
		name: "a field of the model's own on the convention's column", expect: globalShape,
		table: "shape_own_column", column: "tenant_id",
		row: func(id, owner string) any { return &shapeOwnColumn{TenantID: tenant.ID(owner), ID: id} },
	},
	{
		name: "through an unexported base", expect: refusedShape, table: "shape_unexported_base",
		column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeUnexportedBase{shapeBase: shapeBase{Model: tenant.Model{TenantID: tenant.ID(owner)}}, ID: id}
		},
	},
	{
		name: "through an exported base that embeds an unexported one", expect: refusedShape,
		table: "shape_outer_base", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeOuterBase{
				ShapeOuter: ShapeOuter{shapeBase: shapeBase{Model: tenant.Model{TenantID: tenant.ID(owner)}}}, ID: id,
			}
		},
	},
	{
		name: "as an unexported named field tagged embedded", expect: refusedShape,
		table: "shape_unexported_named", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeUnexportedNamed{owner: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "as an unexported named field tagged embedded in another case", expect: refusedShape,
		table: "shape_unexported_upper", column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeUnexportedUpper{owner: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "embedded and tagged for GORM to ignore", expect: refusedShape, table: "shape_ignored",
		column: "tenant_id",
		row: func(id, owner string) any {
			return &shapeIgnored{Model: tenant.Model{TenantID: tenant.ID(owner)}, ID: id}
		},
	},
	{
		name: "embedded twice on two columns", expect: refusedShape, table: "shape_twice",
		column: "seller_tenant_id", extra: ", buyer_tenant_id TEXT",
		row: func(id, owner string) any {
			return &shapeTwice{
				Seller: tenant.Model{TenantID: tenant.ID(owner)},
				Buyer:  tenant.Model{TenantID: tenant.ID(owner)}, ID: id,
			}
		},
	},
}

// TestEveryDeclarationOfTheConventionIsRecognised checks that every declaration
// shape is recognised the same way on both dialects: scoped, refused, or global.
func TestEveryDeclarationOfTheConventionIsRecognised(t *testing.T) {
	for _, d := range shapeDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			for _, s := range shapes {
				t.Run(s.name, func(t *testing.T) {
					createShapeTable(t, handle, s)
					switch s.expect {
					case scopedShape:
						requireScopedShape(t, handle, s)
					case globalShape:
						requireGlobalShape(t, handle, s)
					case refusedShape:
						requireRefusedShape(t, handle, s)
					}
				})
			}
		})
	}
}

func requireScopedShape(t *testing.T, handle *gorm.DB, s shape) {
	none := handle.WithContext(context.Background())
	acme := handle.WithContext(tenantctx.WithID(context.Background(), "acme"))
	across := handle.WithContext(tenant.CrossTenant(context.Background()))

	for what, err := range everyStatement(t, none, s) {
		if !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("%s with no tenant gave %v, want ErrNoTenantInContext", what, err)
		}
	}
	requireShapeRows(t, handle, s, "a=acme b=globex")

	got := shapeRows(s)
	if err := acme.Find(got).Error; err != nil || shapeIDs(got) != "a" {
		t.Errorf("a read under acme gave %v and rows %q, want acme's a", err, shapeIDs(got))
	}
	var n int64
	if err := acme.Model(s.row("", "")).Count(&n).Error; err != nil || n != 1 {
		t.Errorf("a count under acme gave %v and %d, want 1", err, n)
	}
	all := shapeRows(s)
	if err := across.Find(all).Error; err != nil || shapeIDs(all) != "a,b" {
		t.Errorf("a read across tenants gave %v and rows %q, want both", err, shapeIDs(all))
	}

	updated := acme.Model(s.row("", "")).Where("1 = 1").Update("note", "changed")
	if updated.Error != nil || updated.RowsAffected != 1 {
		t.Errorf("an update of every row under acme gave %v, affecting %d rows, want acme's one",
			updated.Error, updated.RowsAffected)
	}
	if note := shapeNote(t, handle, s, "b"); note != "for globex" {
		t.Errorf("an update under acme left globex's note %q", note)
	}
	moved := acme.Model(s.row("", "")).Where("id = ?", "a").Updates(s.row("", "globex")).Error
	if !errors.Is(moved, tenant.ErrExplicitTenantMismatch) {
		t.Errorf("an update under acme moving a row to globex gave %v, want ErrExplicitTenantMismatch", moved)
	}
	keyed := acme.Delete(s.row("b", ""))
	if keyed.Error != nil || keyed.RowsAffected != 0 {
		t.Errorf("a delete under acme of globex's row by its key gave %v, affecting %d rows, want none",
			keyed.Error, keyed.RowsAffected)
	}
	deleted := acme.Where("1 = 1").Delete(s.row("", ""))
	if deleted.Error != nil || deleted.RowsAffected != 1 {
		t.Errorf("a delete of every row under acme gave %v, affecting %d rows, want acme's one",
			deleted.Error, deleted.RowsAffected)
	}
	requireShapeRows(t, handle, s, "b=globex")

	unwritten := s.row("n", "")
	if s.unwritten != nil {
		unwritten = s.unwritten("n")
	}
	if err := acme.Create(unwritten).Error; err != nil {
		t.Errorf("an insert under acme with the tenant column unwritten gave %v", err)
	}
	if err := acme.Create(s.row("m", "globex")).Error; !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
		t.Errorf("an insert under acme naming globex gave %v, want ErrExplicitTenantMismatch", err)
	}
	if s.stray != nil {
		if err := acme.Create(s.stray("s")).Error; !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
			t.Errorf("an insert under acme naming globex through a field GORM writes beside the tenant "+
				"column gave %v, want ErrExplicitTenantMismatch", err)
		}
	}
	requireShapeRows(t, handle, s, "b=globex n=acme")

	byKey := acme.Delete(s.row("n", "globex"))
	switch {
	case s.keyOutside && (byKey.Error != nil || byKey.RowsAffected != 1):
		t.Errorf("a delete under acme by a key, the tenant field outside it naming globex, gave %v, "+
			"affecting %d rows, want acme's n", byKey.Error, byKey.RowsAffected)
	case !s.keyOutside && !errors.Is(byKey.Error, tenant.ErrExplicitTenantMismatch):
		t.Errorf("a delete under acme by a key naming globex gave %v, want ErrExplicitTenantMismatch", byKey.Error)
	}
}

func requireGlobalShape(t *testing.T, handle *gorm.DB, s shape) {
	none := handle.WithContext(context.Background())
	acme := handle.WithContext(tenantctx.WithID(context.Background(), "acme"))

	for name, db := range map[string]*gorm.DB{"no tenant": none, "acme": acme} {
		got := shapeRows(s)
		if err := db.Find(got).Error; err != nil || shapeIDs(got) != "a,b" {
			t.Errorf("a read under %s gave %v and rows %q, want both, unconditioned", name, err, shapeIDs(got))
		}
	}
	updated := acme.Model(s.row("", "")).Where("1 = 1").Update("note", "changed")
	if updated.Error != nil || updated.RowsAffected != 2 {
		t.Errorf("an update of every row under acme gave %v, affecting %d rows, want both, unconditioned",
			updated.Error, updated.RowsAffected)
	}
	requireShapeRows(t, handle, s, "a=acme b=globex")
}

func requireRefusedShape(t *testing.T, handle *gorm.DB, s shape) {
	contexts := map[string]context.Context{
		"no tenant":      context.Background(),
		"acme":           tenantctx.WithID(context.Background(), "acme"),
		"across tenants": tenant.CrossTenant(context.Background()),
	}
	for name, ctx := range contexts {
		for what, err := range everyStatement(t, handle.WithContext(ctx), s) {
			if !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Errorf("%s under %s gave %v, want ErrNoTenantInContext", what, name, err)
				continue
			}
			if !strings.Contains(err.Error(), reflect.TypeOf(s.row("", "")).Elem().Name()) {
				t.Errorf("%s under %s gave %q, which does not name the model", what, name, err)
			}
		}
	}
	requireShapeRows(t, handle, s, "a=acme b=globex")
}

func everyStatement(t *testing.T, db *gorm.DB, s shape) map[string]error {
	t.Helper()
	var n int64
	return map[string]error{
		"Find":  db.Find(shapeRows(s)).Error,
		"Count": db.Model(s.row("", "")).Count(&n).Error,
		"Row": withoutPanic(t, func() error {
			return db.Model(s.row("", "")).Select("count(*)").Row().Scan(&n)
		}),
		"Create": db.Create(s.row("z", "acme")).Error,
		"Update": db.Model(s.row("", "")).Where("1 = 1").Update("note", "z").Error,
		"Delete": db.Where("1 = 1").Delete(s.row("", "")).Error,
	}
}

func withoutPanic(t *testing.T, fn func() error) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panicked: %v", r)
		}
	}()
	return fn()
}

func shapeRows(s shape) any {
	return reflect.New(reflect.SliceOf(reflect.TypeOf(s.row("", "")).Elem())).Interface()
}

func shapeIDs(rows any) string {
	v := reflect.ValueOf(rows).Elem()
	ids := make([]string, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		ids = append(ids, v.Index(i).FieldByName("ID").String())
	}
	return strings.Join(ids, ",")
}

func createShapeTable(t *testing.T, handle *gorm.DB, s shape) {
	t.Helper()
	extra := s.extra
	if handle.Name() == postgresDialector {
		extra += s.extraPostgres
	}
	ddl := fmt.Sprintf("CREATE TABLE %s (%s TEXT NOT NULL, id TEXT NOT NULL, note TEXT%s, PRIMARY KEY (%s, id))",
		s.table, s.column, extra, s.column)
	if err := handle.Exec(ddl).Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if err := handle.Exec(fmt.Sprintf("INSERT INTO %s (%s, id, note) VALUES "+
		"('acme', 'a', 'for acme'), ('globex', 'b', 'for globex')", s.table, s.column)).Error; err != nil {
		t.Fatalf("seeding the table: %v", err)
	}
}

func requireShapeRows(t *testing.T, handle *gorm.DB, s shape, want string) {
	t.Helper()
	var rows []struct {
		Tenant string
		ID     string
	}
	if err := handle.WithContext(tenant.CrossTenant(context.Background())).
		Raw(fmt.Sprintf("SELECT %s AS tenant, id FROM %s ORDER BY id", s.column, s.table)).
		Scan(&rows).Error; err != nil {
		t.Fatalf("reading the table back: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.ID+"="+r.Tenant)
	}
	if strings.Join(got, " ") != want {
		t.Errorf("the table holds %q, want %q", strings.Join(got, " "), want)
	}
}

func shapeNote(t *testing.T, handle *gorm.DB, s shape, id string) string {
	t.Helper()
	var note string
	if err := handle.WithContext(tenant.CrossTenant(context.Background())).
		Raw(fmt.Sprintf("SELECT note FROM %s WHERE id = ?", s.table), id).
		Row().Scan(&note); err != nil {
		t.Fatalf("reading a note back: %v", err)
	}
	return note
}

func openShapesOnSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	return filteredShapes(t, sqlitedriver.Open("file:"+t.Name()+"?mode=memory&cache=shared"))
}

func filteredShapes(t *testing.T, dialector gorm.Dialector) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := handle.Use(newFilter()); err != nil {
		t.Fatalf("installing the filter: %v", err)
	}
	return handle
}

type shapeHolder struct {
	ID       string              `gorm:"column:id;primaryKey"`
	ItemID   string              `gorm:"column:item_id"`
	Item     shapePrefixed       `gorm:"foreignKey:ItemID;references:ID"`
	HiddenID string              `gorm:"column:hidden_id"`
	Hidden   shapeUnexportedBase `gorm:"foreignKey:HiddenID;references:ID"`
}

func (shapeHolder) TableName() string { return "shape_holders" }

// TestAJoinIsRecognisedAsTheModelItReaches checks that a join is recognised as
// the model it reaches, scoped on a renamed column and refused without a tenant column.
func TestAJoinIsRecognisedAsTheModelItReaches(t *testing.T) {
	for _, d := range shapeDialects {
		t.Run(d.name, func(t *testing.T) {
			handle := d.open(t)
			for _, s := range shapes {
				if s.table == "shape_prefixed" || s.table == "shape_unexported_base" {
					createShapeTable(t, handle, s)
				}
			}
			for _, stmt := range []string{
				"CREATE TABLE shape_holders (id TEXT PRIMARY KEY, item_id TEXT, hidden_id TEXT)",
				"INSERT INTO shape_holders (id, item_id, hidden_id) VALUES ('h1', 'a', 'a'), ('h2', 'b', 'b')",
			} {
				if err := handle.Exec(stmt).Error; err != nil {
					t.Fatalf("preparing the holders: %v", err)
				}
			}
			acme := handle.WithContext(tenantctx.WithID(context.Background(), "acme"))

			var got []shapeHolder
			if err := acme.Joins("Item").Order("shape_holders.id").Find(&got).Error; err != nil {
				t.Fatalf("a read joined to the prefixed model under acme gave %v", err)
			}
			if len(got) != 2 || got[0].Item.ID != "a" || got[1].Item.ID != "" {
				t.Fatalf("a read joined to the prefixed model under acme returned %+v, "+
					"want h1 joined to acme's a and h2 joined to nothing", got)
			}
			var refused []shapeHolder
			err := handle.WithContext(context.Background()).Joins("Item").Find(&refused).Error
			if !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Fatalf("a read joined to the prefixed model with no tenant gave %v, want ErrNoTenantInContext", err)
			}

			for name, ctx := range map[string]context.Context{
				"acme":           tenantctx.WithID(context.Background(), "acme"),
				"across tenants": tenant.CrossTenant(context.Background()),
			} {
				var hidden []shapeHolder
				err := handle.WithContext(ctx).Joins("Hidden").Find(&hidden).Error
				if !errors.Is(err, tenant.ErrNoTenantInContext) || !strings.Contains(err.Error(), "shapeUnexportedBase") {
					t.Errorf("a read under %s joined to a model embedding the convention where GORM does not "+
						"read it gave %v (rows %+v), want its refusal", name, err, hidden)
				}
			}
		})
	}
}
