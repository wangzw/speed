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
