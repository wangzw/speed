package demotest

import (
	"fmt"
	"os/exec"
)

// programPackage is the demo server's main package, built into the program a
// test package runs as a child.
const programPackage = "github.com/vislake/speed/starters/demo/server"

// BuildProgram builds the demo server program into out, with the race detector
// when race is true. The error carries the build's combined output.
func BuildProgram(out string, race bool) error {
	args := []string{"build"}
	if race {
		args = append(args, "-race")
	}
	args = append(args, "-o", out, programPackage)
	combined, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("building the program failed: %w\n%s", err, combined)
	}
	return nil
}
