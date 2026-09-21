package natstest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnExistingStoppedContainerIsStartedNotRemoved pins what happens to a
// container that is there and not running: it is another test binary's
// container a moment before it comes up, or an interrupted run's leftover, and
// in either case removing it reaches into the other binary's window and takes
// its server with it.
//
// The fake docker below records every call, so the case asserts on what was
// asked of docker rather than on timing: the run must start the existing
// container, and it must not remove it.
func TestAnExistingStoppedContainerIsStartedNotRemoved(t *testing.T) {
	shim := newDockerShim(t)

	ensureServer(t)

	calls := shim.calls(t)
	if strings.Contains(calls, "rm") {
		t.Fatalf("the existing stopped container was removed:\n%s", calls)
	}
	if !strings.Contains(calls, "start "+containerName) {
		t.Fatalf("the existing stopped container was not started:\n%s", calls)
	}
}

// dockerShim is a fake docker on PATH. It answers the inspect and start calls
// the fixture makes, keeps the running flag in a file, and records every call.
// The container it describes starts out created-but-not-running, which is the
// state the case above is about.
type dockerShim struct {
	dir string
}

func newDockerShim(t *testing.T) *dockerShim {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
dir="$(dirname "$0")"
echo "$@" >>"$dir/calls.log"
case "$1" in
inspect)
	case "$3" in
	"{{.State.Running}}")
		if [ -f "$dir/running" ]; then echo true; else echo false; fi
		;;
	"{{.Name}}")
		echo /` + containerName + `
		;;
	"{{.State.Status}}")
		if [ -f "$dir/running" ]; then echo running; else echo created; fi
		;;
	*)
		exit 1
		;;
	esac
	;;
run)
	touch "$dir/running"
	echo ` + containerName + `
	;;
start)
	touch "$dir/running"
	echo ` + containerName + `
	;;
port)
	echo 127.0.0.1:49152
	;;
*)
	exit 1
	;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the docker shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &dockerShim{dir: dir}
}

func (s *dockerShim) calls(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(s.dir, "calls.log"))
	if err != nil {
		return ""
	}
	return string(body)
}
