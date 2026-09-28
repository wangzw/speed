package dbsuite

import (
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"
)

// constraintSchema is the pair of tables the constraint cases collide with, in
// the SQL both engines accept. The later parent's foreign key is checked at
// COMMIT rather than at the statement, so a refusal can be reported by the
// commit alone.
var constraintSchema = []string{
	`CREATE TABLE constraint_parents (id INTEGER PRIMARY KEY, code TEXT NOT NULL UNIQUE,
		CHECK (code <> 'forbidden'))`,
	`CREATE TABLE constraint_children (id INTEGER PRIMARY KEY,
		parent_id INTEGER REFERENCES constraint_parents (id),
		later_parent_id INTEGER REFERENCES constraint_parents (id) DEFERRABLE INITIALLY DEFERRED)`,
}

// constraintParent is a row of constraint_parents.
type constraintParent struct {
	ID   uint
	Code string
}

func (constraintParent) TableName() string { return "constraint_parents" }

// constraintChild is a row of constraint_children.
type constraintChild struct {
	ID            uint
	ParentID      *uint
	LaterParentID *uint
}

func (constraintChild) TableName() string { return "constraint_children" }

// ConstraintRefusals holds a handle an engine's module delivered to the error
// contract the database capability states for a constraint refusal: a
// statement a unique constraint refuses — a primary key's included — reports
// an error in which errors.Is finds gorm.ErrDuplicatedKey, one a foreign key
// refuses an error in which it finds gorm.ErrForeignKeyViolated, and neither
// class is found in a refusal of any other kind. The contract is the same on
// every engine, which is what lets one check map a duplicate to a conflict
// whichever engine the host deploys on, so every engine runs these cases.
//
// carriesDriverError reports whether an error still carries the engine's own
// driver error, which the classification keeps beside the class.
//
// The handle must enforce foreign keys; SQLite does so only on a connection
// that turned them on.
func ConstraintRefusals(t *testing.T, handle *gorm.DB, carriesDriverError func(error) bool) {
	t.Helper()
	for _, statement := range constraintSchema {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating the tables the cases collide with: %v", err)
		}
	}
	if err := handle.Create(&constraintParent{ID: 1, Code: "taken"}).Error; err != nil {
		t.Fatalf("writing the row the cases collide with: %v", err)
	}
	if err := handle.Create(&constraintParent{ID: 2, Code: "free"}).Error; err != nil {
		t.Fatalf("writing the row the update case moves: %v", err)
	}
	missing := uint(999)

	cases := []struct {
		name  string
		want  error
		write func(db *gorm.DB) error
	}{
		{"a second row under a unique key", gorm.ErrDuplicatedKey, func(db *gorm.DB) error {
			return db.Create(&constraintParent{ID: 10, Code: "taken"}).Error
		}},
		{"a second row under the primary key", gorm.ErrDuplicatedKey, func(db *gorm.DB) error {
			return db.Create(&constraintParent{ID: 1, Code: "another"}).Error
		}},
		{"an update onto a unique key", gorm.ErrDuplicatedKey, func(db *gorm.DB) error {
			return db.Model(&constraintParent{}).Where("id = ?", 2).Update("code", "taken").Error
		}},
		{"native SQL through Exec", gorm.ErrDuplicatedKey, func(db *gorm.DB) error {
			return db.Exec("INSERT INTO constraint_parents (id, code) VALUES (?, ?)", 11, "taken").Error
		}},
		{"an INSERT with RETURNING read through Scan", gorm.ErrDuplicatedKey, func(db *gorm.DB) error {
			var id uint
			return db.Raw("INSERT INTO constraint_parents (id, code) VALUES (?, ?) RETURNING id", 12, "taken").
				Scan(&id).Error
		}},
		{"a write inside a transaction", gorm.ErrDuplicatedKey, func(db *gorm.DB) error {
			return db.Transaction(func(tx *gorm.DB) error {
				return tx.Create(&constraintParent{ID: 13, Code: "taken"}).Error
			})
		}},
		{"a row naming a parent that does not exist", gorm.ErrForeignKeyViolated, func(db *gorm.DB) error {
			return db.Create(&constraintChild{ID: 1, ParentID: &missing}).Error
		}},
		{"a deferred foreign key the commit refuses", gorm.ErrForeignKeyViolated, func(db *gorm.DB) error {
			return db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&constraintChild{ID: 2, LaterParentID: &missing}).Error; err != nil {
					return fmt.Errorf("the insert itself was refused, before the commit: %w", err)
				}
				return nil
			})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.write(handle)
			if !errors.Is(err, c.want) {
				t.Fatalf("the refused statement reported %v, want %v in its chain", err, c.want)
			}
			for _, other := range []error{gorm.ErrDuplicatedKey, gorm.ErrForeignKeyViolated} {
				if !errors.Is(c.want, other) && errors.Is(err, other) {
					t.Errorf("the refusal reports %v as well: %v", other, err)
				}
			}
			if !carriesDriverError(err) {
				t.Errorf("the refusal no longer carries the driver's own error: %#v", err)
			}
		})
	}

	t.Run("a refusal inside a savepoint leaves the transaction usable", func(t *testing.T) {
		err := handle.Transaction(func(tx *gorm.DB) error {
			inner := tx.Transaction(func(nested *gorm.DB) error {
				return nested.Create(&constraintParent{ID: 20, Code: "taken"}).Error
			})
			if !errors.Is(inner, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("the nested write reported %w, want a duplicated key", inner)
			}
			return tx.Create(&constraintParent{ID: 21, Code: "after-the-savepoint"}).Error
		})
		if err != nil {
			t.Fatalf("the transaction around a refused savepoint failed: %v", err)
		}
		var count int64
		if err := handle.Model(&constraintParent{}).Where("id = ?", 21).Count(&count).Error; err != nil {
			t.Fatalf("counting the row written after the savepoint: %v", err)
		}
		if count != 1 {
			t.Errorf("%d rows written after the savepoint, want the one", count)
		}
	})

	t.Run("a refusal of another kind is neither class", func(t *testing.T) {
		for name, write := range map[string]func() error{
			"a NULL into a NOT NULL column": func() error {
				return handle.Exec("INSERT INTO constraint_parents (id, code) VALUES (?, NULL)", 30).Error
			},
			"a row a CHECK constraint refuses": func() error {
				return handle.Create(&constraintParent{ID: 31, Code: "forbidden"}).Error
			},
			"a statement naming a column the table does not have": func() error {
				return handle.Exec("INSERT INTO constraint_parents (id, code, absent) VALUES (?, ?, ?)",
					32, "absent", 1).Error
			},
		} {
			err := write()
			if err == nil {
				t.Errorf("%s went through", name)
				continue
			}
			if errors.Is(err, gorm.ErrDuplicatedKey) || errors.Is(err, gorm.ErrForeignKeyViolated) {
				t.Errorf("%s reported %v, a class the contract keeps for unique and foreign key refusals",
					name, err)
			}
		}
	})
}
