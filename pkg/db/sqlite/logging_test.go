package sqlite_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

// TestNoBoundValueReachesTheOutput runs the engine-neutral logging case on the
// handle this engine's module delivers.
func TestNoBoundValueReachesTheOutput(t *testing.T) {
	dbsuite.BoundValuesReachNoOutput(t,
		func(t *testing.T) string { return filepath.Join(t.TempDir(), "logged.db") },
		rerun,
		func(t *testing.T, dsn string) *gorm.DB { return deliver(t, dsn).DB() })
}

// rerun runs the calling test again, alone, in a copy of this test binary
// whose environment carries variable as well, and returns what the copy wrote
// to its standard output and to its standard error, apart.
func rerun(t *testing.T, variable string) (stdout, stderr []byte, err error) {
	child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	child.Env = append(os.Environ(), variable)
	var out, errOut bytes.Buffer
	child.Stdout, child.Stderr = &out, &errOut
	err = child.Run()
	return out.Bytes(), errOut.Bytes(), err
}
