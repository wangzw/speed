package postgres

import (
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for registries that do not
// inherit the process-level registrations.
func Module() core.Module { return db.NewModule(spec()) }

func spec() db.Spec {
	return db.Spec{
		ModuleName:                  ModuleName,
		ConfigNamespace:             ConfigNamespace,
		Dialect:                     Dialect,
		Dialector:                   Dialector,
		ConstraintViolation:         constraintViolation,
		NewMigrationLock:            NewMigrationLock,
		DefaultMigrationLockTimeout: DefaultMigrationLockTimeout,
	}
}
