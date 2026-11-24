//go:build integration

package postgres_test

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestNoBoundValueReachesTheOutput runs the engine-neutral logging case on the handle this engine's
// module delivers.
func TestNoBoundValueReachesTheOutput(t *testing.T) {
	dbsuite.BoundValuesReachNoOutput(t, postgres.ModuleName, "SQLSTATE 23505", pgtest.Acquire, rerun,
		func(t *testing.T, dsn string) *gorm.DB {
			capability, err := runAssembly(t, dsn)
			if err != nil {
				t.Fatalf("starting a host on this engine: %v", err)
			}
			return capability.DB()
		})
}

func rerun(t *testing.T, variable string) (stdout, stderr []byte, err error) {
	child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	child.Env = append(os.Environ(), variable)
	var out, errOut bytes.Buffer
	child.Stdout, child.Stderr = &out, &errOut
	err = child.Run()
	return out.Bytes(), errOut.Bytes(), err
}
