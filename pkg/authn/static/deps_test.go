package static

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// forbiddenDependencies are the import path prefixes the compile closure of this
// package does not hold: the database drivers, the message and cache clients,
// and the packages of the other authn halves. The closure does hold pkg/db,
// pkg/event and gorm, which pkg/settings imports, since the settings store this
// backend reads its parameters from is declared in the package that also
// carries its implementation; a driver or a client still arrives only when a
// host assembles one.
var forbiddenDependencies = []string{
	"gorm.io/driver/",
	"github.com/glebarez/",
	"github.com/jackc/",
	"github.com/mattn/go-sqlite3",
	"modernc.org/sqlite",
	"github.com/nats-io/",
	"github.com/redis/",
	"github.com/bradfitz/gomemcache",
	"github.com/vislake/speed/pkg/authn/gate",
	"github.com/vislake/speed/pkg/authn/authntest",
}

// TestTheClosureHoldsNoDriverAndNoOtherBackend holds what the package comment
// says a host that takes only this backend carries.
func TestTheClosureHoldsNoDriverAndNoOtherBackend(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, stderr.String())
	}
	deps := strings.Fields(string(out))
	if len(deps) == 0 {
		t.Fatal("go list reported no dependency")
	}
	for _, dep := range deps {
		for _, forbidden := range forbiddenDependencies {
			if strings.HasPrefix(dep, forbidden) {
				t.Errorf("the closure of this package holds %s, which a host taking only this backend does not carry", dep)
			}
		}
	}
}
