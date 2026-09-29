package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about which models the filter recognises as
// scoped. Embedding the convention is the declaration, and GORM flattens an
// embedded struct however it is reached: through a base struct the model
// embeds, through a pointer, or through a field tagged embedded. Each of those
// carries the convention's column onto the model, and each has to be scoped.

// OwnedBase is a base struct an application's models share. It is exported, as
// such a base is: GORM does not read an embedded struct of an unexported type.
type OwnedBase struct {
	tenant.Model
}

// nestedOrder reaches the convention through a base struct.
type nestedOrder struct {
	OwnedBase
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (nestedOrder) TableName() string { return "orders" }

// pointerOrder embeds the convention by pointer.
type pointerOrder struct {
	*tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (pointerOrder) TableName() string { return "orders" }

// taggedOrder carries the convention in a named field GORM is told to embed.
type taggedOrder struct {
	Owner tenant.Model `gorm:"embedded"`
	ID    string       `gorm:"column:id;primaryKey"`
	Note  string       `gorm:"column:note"`
}

func (taggedOrder) TableName() string { return "orders" }

// lookalike carries a field shaped like the convention's without being it.
type lookalike struct {
	TenantID tenant.ID `gorm:"column:tenant_id;not null"`
}

// lookalikeMemo embeds lookalike, and is global: the declaration is embedding
// the convention, not having a field of its name and type.
type lookalikeMemo struct {
	lookalike
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (lookalikeMemo) TableName() string { return "memos" }

func TestTheConventionIsRecognisedHoweverItIsEmbedded(t *testing.T) {
	cases := []struct {
		name  string
		many  func() any
		one   func(id string) any
		count func(any) int
	}{
		{
			name:  "through a base struct",
			many:  func() any { return &[]nestedOrder{} },
			one:   func(id string) any { return &nestedOrder{ID: id} },
			count: func(v any) int { return len(*v.(*[]nestedOrder)) },
		},
		{
			name:  "by pointer",
			many:  func() any { return &[]pointerOrder{} },
			one:   func(id string) any { return &pointerOrder{ID: id} },
			count: func(v any) int { return len(*v.(*[]pointerOrder)) },
		},
		{
			name:  "as a field tagged embedded",
			many:  func() any { return &[]taggedOrder{} },
			one:   func(id string) any { return &taggedOrder{ID: id} },
			count: func(v any) int { return len(*v.(*[]taggedOrder)) },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)

			refused := c.many()
			err := handle.WithContext(context.Background()).Find(refused).Error
			if !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Fatalf("a read with no tenant gave %v (%d rows), want ErrNoTenantInContext",
					err, c.count(refused))
			}

			got := c.many()
			if err := handle.WithContext(scopedContext("acme")).Find(got).Error; err != nil {
				t.Fatalf("reading: %v", err)
			}
			if n := c.count(got); n != 1 {
				t.Fatalf("a scoped read returned %d rows, want acme's one", n)
			}

			if err := handle.WithContext(scopedContext("acme")).Create(c.one("n")).Error; err != nil {
				t.Fatalf("inserting: %v", err)
			}
			requireRowTenant(t, handle, "n", "acme")
		})
	}
}

// A field of the identifier's type, under the convention's column name, does
// not make a model scoped when it is not the convention's own field.
func TestALookalikeOfTheConventionStaysGlobal(t *testing.T) {
	handle := open(t)
	if err := handle.WithContext(crossContext()).Exec(
		"INSERT INTO memos (id, tenant_id, note) VALUES ('m1', 'acme', 'x'), ('m2', 'globex', 'y')").
		Error; err != nil {
		t.Fatalf("seeding: %v", err)
	}
	var got []lookalikeMemo
	if err := handle.WithContext(context.Background()).Find(&got).Error; err != nil {
		t.Fatalf("a global read gave %v, want it left alone", err)
	}
	if len(got) != 2 {
		t.Fatalf("a global read returned %d rows, want both", len(got))
	}
}

// orderMeta is a struct a model embeds under a name, carrying a column the
// convention also declares.
type orderMeta struct {
	TenantID string
}

// nullableOrder embeds the convention and declares, beside it, a field of its
// own on the convention's column. Go's shadowing and GORM's parse agree: the
// model's own field is the one GORM reads and writes.
type nullableOrder struct {
	tenant.Model
	TenantID *string `gorm:"column:tenant_id"`
	ID       string  `gorm:"column:id;primaryKey"`
	Note     string  `gorm:"column:note"`
}

func (nullableOrder) TableName() string { return "orders" }

// renamedOrder declares its own TenantID ahead of the convention, under the
// column GORM's naming gives it, which is the convention's.
type renamedOrder struct {
	TenantID string `json:"tenant"`
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (renamedOrder) TableName() string { return "orders" }

// ownerOrder binds the convention's column to a field of another name.
type ownerOrder struct {
	tenant.Model
	Owner string `gorm:"column:tenant_id"`
	ID    string `gorm:"column:id;primaryKey"`
	Note  string `gorm:"column:note"`
}

func (ownerOrder) TableName() string { return "orders" }

// metaOrder reaches the convention's column first through a struct embedded
// under a name, at the same depth as the convention: GORM binds the column to
// the one declared first.
type metaOrder struct {
	Meta orderMeta `gorm:"embedded"`
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (metaOrder) TableName() string { return "orders" }

// A model that embeds the convention is scoped whatever else it declares on the
// convention's column. When another field of the model takes that column —
// declared on the model itself, under another name, or reached through an
// embedding GORM puts first — GORM reads and writes that field in place of the
// convention's, and the filter fills and judges the field GORM binds.
func TestAModelThatEmbedsTheConventionIsScopedWhateverTakesItsColumn(t *testing.T) {
	cases := []struct {
		name  string
		many  func() any
		count func(any) int
		// row is a row with this identifier whose field on the tenant
		// column holds owner, or is left unwritten when owner is empty.
		row func(id, owner string) any
	}{
		{
			name:  "a field of the model's own named like the convention's",
			many:  func() any { return &[]nullableOrder{} },
			count: func(v any) int { return len(*v.(*[]nullableOrder)) },
			row: func(id, owner string) any {
				o := &nullableOrder{ID: id}
				if owner != "" {
					o.TenantID = &owner
				}
				return o
			},
		},
		{
			name:  "a field declared ahead of the convention",
			many:  func() any { return &[]renamedOrder{} },
			count: func(v any) int { return len(*v.(*[]renamedOrder)) },
			row:   func(id, owner string) any { return &renamedOrder{TenantID: owner, ID: id} },
		},
		{
			name:  "a field of another name on the column",
			many:  func() any { return &[]ownerOrder{} },
			count: func(v any) int { return len(*v.(*[]ownerOrder)) },
			row:   func(id, owner string) any { return &ownerOrder{Owner: owner, ID: id} },
		},
		{
			name:  "an embedding GORM puts ahead of the convention",
			many:  func() any { return &[]metaOrder{} },
			count: func(v any) int { return len(*v.(*[]metaOrder)) },
			row:   func(id, owner string) any { return &metaOrder{Meta: orderMeta{TenantID: owner}, ID: id} },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handle := open(t)
			seed(t, handle)
			acme := handle.WithContext(scopedContext("acme"))

			refused := c.many()
			err := handle.WithContext(context.Background()).Find(refused).Error
			if !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Fatalf("a read with no tenant gave %v (%d rows), want ErrNoTenantInContext",
					err, c.count(refused))
			}

			got := c.many()
			if err := acme.Find(got).Error; err != nil {
				t.Fatalf("reading: %v", err)
			}
			if n := c.count(got); n != 1 {
				t.Fatalf("a scoped read returned %d rows, want acme's one", n)
			}

			if err := acme.Create(c.row("x", "")).Error; err != nil {
				t.Fatalf("inserting a row with its tenant column unwritten: %v", err)
			}
			requireRowTenant(t, handle, "x", "acme")

			requireMismatch(t, acme.Create(c.row("y", "globex")).Error)
			if rows := tenantsOf(t, handle)["y"]; len(rows) != 0 {
				t.Fatalf("a refused insert left rows %v", rows)
			}

			requireMismatch(t, acme.Model(c.row("", "")).Where("id = ?", "a").
				Updates(c.row("", "globex")).Error)
			requireRowTenant(t, handle, "a", "acme")

			// The field is outside the model's key, and GORM addresses a
			// delete by the key alone: what the field holds is not read.
			keyed := acme.Delete(c.row("a", "globex"))
			if keyed.Error != nil || keyed.RowsAffected != 1 {
				t.Fatalf("a scoped delete addressed by a key gave %v, affecting %d rows, want acme's a",
					keyed.Error, keyed.RowsAffected)
			}

			deleted := acme.Where("1 = 1").Delete(c.row("", ""))
			if deleted.Error != nil || deleted.RowsAffected != 1 {
				t.Fatalf("a scoped delete of every row gave %v, affecting %d rows, want acme's one left",
					deleted.Error, deleted.RowsAffected)
			}
			if got := tenantsOf(t, handle); len(got) != 1 || len(got["b"]) != 1 || got["b"][0] != "globex" {
				t.Fatalf("the scoped deletes left %v, want globex's b alone", got)
			}
		})
	}
}
