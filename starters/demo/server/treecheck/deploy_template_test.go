package treecheck

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/starters/demo/server/internal/demotest"
)

const deployDirEnv = "TREECHECK_DEPLOY_DIR"

const deployCasePrefix = "TestTheDeployTemplate"

const mainPackage = "github.com/vislake/speed/starters/demo/server"

func commandLimit() time.Duration {
	return coretest.Scale(180 * time.Second)
}

// TestTheDeployTemplateCasesPass checks that the main package's deployment-template cases pass from
// the module root.
func TestTheDeployTemplateCasesPass(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "demo.test")
	args := []string{"test", "-c"}
	if raceEnabled {
		args = append(args, "-race")
	}
	args = append(args, "-o", binary, mainPackage)
	if out, err := runGo(t, args...); err != nil {
		t.Fatalf("building the main package's test binary failed: %v\n%s", err, out)
	}
	deployDir, err := filepath.Abs(filepath.Join("..", "..", "deploy"))
	if err != nil {
		t.Fatalf("resolving the deployment directory: %v", err)
	}
	env := append(demotest.ChildEnv(), deployDirEnv+"="+deployDir)
	names := listDeployCases(t, binary, env)
	stdout, stderr, runErr := runTestBinary(t, binary, env, "-test.run", "^"+deployCasePrefix, "-test.v")
	if runErr != nil {
		t.Fatalf("the deployment-template cases failed: %v\nstandard output:\n%s\nstandard error:\n%s",
			runErr, stdout, stderr)
	}
	for _, name := range names {
		if !strings.Contains(stdout, "--- PASS: "+name+" ") {
			t.Errorf("%s did not pass:\n%s", name, stdout)
		}
	}
	if strings.Contains(stdout, "--- SKIP: ") {
		t.Errorf("a deployment-template case skipped instead of running:\n%s", stdout)
	}
}

func listDeployCases(t *testing.T, binary string, env []string) []string {
	t.Helper()
	stdout, stderr, err := runTestBinary(t, binary, env, "-test.list", "^"+deployCasePrefix)
	if err != nil {
		t.Fatalf("listing the deployment-template cases failed: %v\nstandard output:\n%s\nstandard error:\n%s",
			err, stdout, stderr)
	}
	var names []string
	for _, line := range strings.Split(stdout, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		t.Fatalf("the main package lists no %s* case", deployCasePrefix)
	}
	return names
}

func runTestBinary(t *testing.T, binary string, env []string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit())
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = ".."
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("the test binary was still running after the limit and was killed: %w", ctx.Err())
	}
	return stdout.String(), stderr.String(), err
}

func runGo(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit())
	defer cancel()
	combined, err := exec.CommandContext(ctx, "go", args...).CombinedOutput()
	if ctx.Err() != nil {
		err = fmt.Errorf("go was still running after the limit and was killed: %w", ctx.Err())
	}
	return string(combined), err
}
