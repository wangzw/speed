package treecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/starters/demo/server/internal/demotest"
)

var hostBinary string

// TestMain builds the program once and runs the contract cases.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "treecheck")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "demo")
	if err := demotest.BuildProgram(hostBinary, raceEnabled); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
