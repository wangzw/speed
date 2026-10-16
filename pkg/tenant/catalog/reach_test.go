package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// The cases in this file are about which statements the filter reaches: a
// statement GORM runs through a processor other than the query one, a table
// reached through an association join rather than through the model, and a
// subquery rendered inside another statement.

// A scan reads through GORM's row processor rather than its query processor,
// and a filter registered on the query processor alone never sees it.
func TestScanIsScopedToTheContext(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	var got []struct{ ID string }
	if err := handle.WithContext(scopedContext("acme")).Model(&order{}).Scan(&got).Error; err != nil {
		t.Fatalf("scanning: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("a scoped scan returned %v, want just acme's row", got)
	}
}

func TestScanWithoutATenantIsRefused(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	var got []struct{ ID string }
	err := handle.WithContext(context.Background()).Model(&order{}).Scan(&got).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("scanning with no tenant gave %v (rows %v), want ErrNoTenantInContext", err, got)
	}
}

func TestRowAndRowsAreScopedToTheContext(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	ctx := scopedContext("acme")

	var n int
	if err := handle.WithContext(ctx).Model(&order{}).Select("count(*)").Row().Scan(&n); err != nil {
		t.Fatalf("reading one row: %v", err)
	}
	if n != 1 {
		t.Fatalf("a scoped Row counted %d rows, want acme's one", n)
	}

	rows, err := handle.WithContext(ctx).Model(&order{}).Select("id").Rows()
	if err != nil {
		t.Fatalf("reading rows: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning a row: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating: %v", err)
	}
	if len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("scoped Rows returned %v, want just acme's row", ids)
	}
}

// Row has no error of its own to report through: the row it hands back is the
// one place a refusal can reach its caller. A refused Row hands back a row whose
// Scan fails with the refusal, rather than no row at all for the caller's Scan
// to dereference.
func TestARowWithoutATenantReportsTheRefusalThroughItsScan(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	requireRowRefusal(t, handle)
}

// refusedRows are the two shapes a Row with no tenant is refused in.
var refusedRows = map[string]func(tx *gorm.DB) *sql.Row{
	"a read of the model": func(tx *gorm.DB) *sql.Row {
		return tx.Model(&order{}).Select("count(*)").Row()
	},
	"statement text written over the model": func(tx *gorm.DB) *sql.Row {
		return tx.Model(&order{}).Raw("SELECT count(*) FROM orders").Row()
	},
}

func requireRowRefusal(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for name, row := range refusedRows {
		t.Run(name, func(t *testing.T) {
			n := -1
			err := noPanic(t, func() error { return row(handle.WithContext(context.Background())).Scan(&n) })
			if !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Fatalf("a Row with no tenant gave %v (count %d), want ErrNoTenantInContext", err, n)
			}
			if !strings.Contains(err.Error(), "order") {
				t.Fatalf("the refusal %q does not name the model", err)
			}
		})
	}
}

// A refused Row reaches no table, and it takes no connection of the pool
// either: on a pool whose one connection is held, the refusal is answered at
// once rather than when the caller's deadline ends, as a timeout. That holds for
// a Row refused for its model as for one refused for its context.
func TestARefusedRowTakesNoConnection(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	requireRowRefusedWithoutAConnection(t, handle)
}

func requireRowRefusedWithoutAConnection(t *testing.T, handle *gorm.DB) {
	t.Helper()
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's pool: %v", err)
	}
	pool.SetMaxOpenConns(1)
	held, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatalf("holding the pool's one connection: %v", err)
	}
	defer func() { _ = held.Close() }()

	rows := map[string]func(tx *gorm.DB) *sql.Row{
		"a read under a tenant of a model no tenant column can be taken from": func(tx *gorm.DB) *sql.Row {
			return tx.WithContext(tenantctx.WithID(tx.Statement.Context, "acme")).
				Model(&shapeUnexportedBase{}).Select("count(*)").Row()
		},
	}
	for name, row := range refusedRows {
		rows[name] = row
	}

	const deadline = 2 * time.Second
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()
			n := -1
			start := time.Now()
			err := noPanic(t, func() error { return row(handle.WithContext(ctx)).Scan(&n) })
			took := time.Since(start)
			if !errors.Is(err, tenant.ErrNoTenantInContext) {
				t.Fatalf("a refused Row on a held pool gave %v after %s, want ErrNoTenantInContext",
					err, took)
			}
			if took >= deadline/2 {
				t.Fatalf("the refused Row took %s to answer, want it answered without waiting for a connection",
					took)
			}
		})
	}
}

// customer, country and purchase are joined by associations: two scoped
// models, and a global one between them.
type country struct {
	ID   string `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (country) TableName() string { return "countries" }

type customer struct {
	tenant.Model
	ID        string  `gorm:"column:id;primaryKey"`
	Name      string  `gorm:"column:name"`
	CountryID string  `gorm:"column:country_id"`
	Country   country `gorm:"foreignKey:CountryID;references:ID"`
}

func (customer) TableName() string { return "customers" }

type purchase struct {
	tenant.Model
	ID         string   `gorm:"column:id;primaryKey"`
	CustomerID string   `gorm:"column:customer_id"`
	Customer   customer `gorm:"foreignKey:CustomerID;references:ID"`
}

func (purchase) TableName() string { return "purchases" }

// countryStats is a global model whose association reaches a scoped one.
type countryStats struct {
	ID        string   `gorm:"column:id;primaryKey"`
	CountryID string   `gorm:"column:country_id"`
	Customer  customer `gorm:"foreignKey:CountryID;references:CountryID"`
}

func (countryStats) TableName() string { return "countries" }

// seedJoined gives two tenants one customer identifier each, under a shared
// country, and acme one purchase of it.
func seedJoined(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for _, ddl := range []string{
		"CREATE TABLE countries (id TEXT PRIMARY KEY, name TEXT, country_id TEXT)",
		"CREATE TABLE customers (tenant_id TEXT NOT NULL, id TEXT NOT NULL, name TEXT, " +
			"country_id TEXT, PRIMARY KEY (tenant_id, id))",
		"CREATE TABLE purchases (tenant_id TEXT NOT NULL, id TEXT NOT NULL, customer_id TEXT, " +
			"PRIMARY KEY (tenant_id, id))",
	} {
		if err := handle.Exec(ddl).Error; err != nil {
			t.Fatalf("creating a table: %v", err)
		}
	}
	for _, stmt := range []string{
		"INSERT INTO countries (id, name, country_id) VALUES ('nl', 'the Netherlands', 'nl')",
		"INSERT INTO customers (tenant_id, id, name, country_id) VALUES " +
			"('acme', 'c1', 'acme''s customer', 'nl'), ('globex', 'c1', 'globex''s customer', 'nl')",
		"INSERT INTO purchases (tenant_id, id, customer_id) VALUES ('acme', 'p1', 'c1')",
	} {
		if err := handle.WithContext(crossContext()).Exec(stmt).Error; err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
}

// An association join renders its own ON clause from the relationship. Two
// tenants may reuse one customer identifier under the composite key, so the
// joined side has to be held to the context's tenant as well.
func TestAnAssociationJoinIsScopedOnBothSides(t *testing.T) {
	handle := open(t)
	seedJoined(t, handle)

	var got []purchase
	if err := handle.WithContext(scopedContext("acme")).Joins("Customer").Find(&got).Error; err != nil {
		t.Fatalf("reading with a join: %v", err)
	}
	if len(got) != 1 || got[0].Customer.Name != "acme's customer" {
		t.Fatalf("a scoped join returned %+v, want acme's purchase joined to acme's customer", got)
	}
}

// A nested join passes through a global table; only the scoped levels carry
// the condition.
func TestANestedJoinThroughAGlobalTableIsScopedWhereItIsScoped(t *testing.T) {
	handle := open(t)
	seedJoined(t, handle)

	var got []purchase
	if err := handle.WithContext(scopedContext("acme")).
		Joins("Customer").Joins("Customer.Country").Find(&got).Error; err != nil {
		t.Fatalf("reading with a nested join: %v", err)
	}
	if len(got) != 1 || got[0].Customer.Name != "acme's customer" ||
		got[0].Customer.Country.Name != "the Netherlands" {
		t.Fatalf("a nested join returned %+v, want acme's customer and its country", got)
	}
}

// A nested join given alone renders every level of its path, the scoped one
// in the middle included, from that one join entry; the middle level is held
// to the tenant as the last would be.
func TestANestedJoinGivenAloneIsScopedAtEveryScopedLevel(t *testing.T) {
	handle := open(t)
	seedJoined(t, handle)

	var got []purchase
	if err := handle.WithContext(scopedContext("acme")).
		Joins("Customer.Country").Find(&got).Error; err != nil {
		t.Fatalf("reading with a nested join: %v", err)
	}
	if len(got) != 1 || got[0].Customer.Name != "acme's customer" ||
		got[0].Customer.Country.Name != "the Netherlands" {
		t.Fatalf("a nested join given alone returned %+v, want acme's purchase, customer and country", got)
	}
}

// delivery reaches two scoped rows through two associations of one model.
type delivery struct {
	tenant.Model
	ID         string   `gorm:"column:id;primaryKey"`
	CustomerID string   `gorm:"column:customer_id"`
	BuyerID    string   `gorm:"column:buyer_id"`
	Customer   customer `gorm:"foreignKey:CustomerID;references:ID"`
	Buyer      customer `gorm:"foreignKey:BuyerID;references:ID"`
}

func (delivery) TableName() string { return "deliveries" }

// Each scoped join of a statement is held to the tenant, not only the first:
// the second association reaches a customer identifier both tenants use.
func TestEveryScopedJoinOfAStatementIsScoped(t *testing.T) {
	handle := open(t)
	seedJoined(t, handle)
	if err := handle.Exec("CREATE TABLE deliveries (tenant_id TEXT NOT NULL, id TEXT NOT NULL, " +
		"customer_id TEXT, buyer_id TEXT, PRIMARY KEY (tenant_id, id))").Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if err := handle.WithContext(crossContext()).Exec("INSERT INTO deliveries (tenant_id, id, customer_id, " +
		"buyer_id) VALUES ('acme', 'd1', 'c1', 'c1')").Error; err != nil {
		t.Fatalf("seeding: %v", err)
	}

	var got []delivery
	if err := handle.WithContext(scopedContext("acme")).
		Joins("Customer").Joins("Buyer").Find(&got).Error; err != nil {
		t.Fatalf("reading with two joins: %v", err)
	}
	if len(got) != 1 || got[0].Customer.Name != "acme's customer" || got[0].Buyer.Name != "acme's customer" {
		t.Fatalf("two scoped joins returned %+v, want acme's delivery joined to acme's customer twice", got)
	}
}

// A global model reaching a scoped one through a join reaches that model's
// rows, and is scoped by them: refused without a tenant, held to it with one.
func TestAJoinFromAGlobalModelToAScopedOneIsScoped(t *testing.T) {
	handle := open(t)
	seedJoined(t, handle)

	var refused []countryStats
	err := handle.WithContext(context.Background()).Joins("Customer").Find(&refused).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("a join to a scoped model with no tenant gave %v (rows %+v), want ErrNoTenantInContext",
			err, refused)
	}

	var got []countryStats
	if err := handle.WithContext(scopedContext("globex")).Joins("Customer").Find(&got).Error; err != nil {
		t.Fatalf("reading with a join: %v", err)
	}
	if len(got) != 1 || got[0].Customer.Name != "globex's customer" {
		t.Fatalf("a scoped join from a global model returned %+v, want globex's customer", got)
	}

	var all []countryStats
	if err := handle.WithContext(crossContext()).Joins("Customer").Find(&all).Error; err != nil {
		t.Fatalf("reading across tenants: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("a crossing join returned %+v, want both customers", all)
	}
}

// A subquery built on a handle with no tenant refuses, and the refusal must
// reach the statement it is part of rather than be rendered as an empty set.
func TestASubqueryWithoutATenantFailsTheStatement(t *testing.T) {
	handle := open(t)
	seed(t, handle)
	requireSubqueryRefusal(t, handle)
}

func requireSubqueryRefusal(t *testing.T, handle *gorm.DB) {
	t.Helper()
	sub := handle.Model(&order{}).Select("id")
	var got []order
	err := handle.WithContext(scopedContext("acme")).Where("id IN (?)", sub).Find(&got).Error
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("a subquery with no tenant gave %v (rows %v), want ErrNoTenantInContext", err, got)
	}
}

// A subquery built on the statement's own context is scoped like the rest.
func TestASubqueryOnTheSameContextIsScoped(t *testing.T) {
	handle := open(t)
	seed(t, handle)

	tx := handle.WithContext(scopedContext("acme"))
	var got []order
	if err := tx.Where("id IN (?)", tx.Model(&order{}).Select("id")).Find(&got).Error; err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("a scoped subquery returned %v, want acme's row", got)
	}
}
