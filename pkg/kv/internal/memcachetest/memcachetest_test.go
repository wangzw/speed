package memcachetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestASpaceEndsAtItsOnlyDot pins the shape the isolation rests on. An entry's
// name says which space it is in only because a space's one dot is its last
// byte: a dot earlier in a space, or none at all, would let a longer space
// start with a shorter one, and two cases' stores could then name one entry.
// Every byte is one Memcached accepts in a key, and two calls never return the
// same space.
func TestASpaceEndsAtItsOnlyDot(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		space := Space(t)
		if strings.Index(space, spaceEnd) != len(space)-len(spaceEnd) {
			t.Fatalf("the space %q does not end at its only dot", space)
		}
		for _, b := range []byte(space) {
			if b <= ' ' || b == 0x7f {
				t.Fatalf("the space %q holds the byte %#x, which Memcached refuses in a key", space, b)
			}
		}
		if seen[space] {
			t.Fatalf("the space %q was handed out twice", space)
		}
		seen[space] = true
	}
}

// TestAnExistingStoppedContainerIsStartedNotRemoved pins what happens to a
// container that is there and not running: it is another test binary's
// container a moment before it comes up, or an interrupted run's leftover, and
// in either case removing it reaches into the other binary's window and takes
// its server with it.
//
// The fake docker records every call, so the case asserts on what was asked of
// docker rather than on timing.
func TestAnExistingStoppedContainerIsStartedNotRemoved(t *testing.T) {
	shim := newDockerShim(t, shimExists)

	ensureServer(t)

	calls := shim.calls(t)
	if strings.Contains(calls, "rm ") {
		t.Fatalf("the existing stopped container was removed:\n%s", calls)
	}
	if !strings.Contains(calls, "start "+containerName) {
		t.Fatalf("the existing stopped container was not started:\n%s", calls)
	}
}

// TestACreateLostToAnotherBinaryWaitsForTheWinner covers the other half of a
// cold start shared by several binaries. Each finds no container and creates
// one; docker gives the name to one of them and refuses the rest. A refused
// create is not a failure of the case: the container it lost to is coming up,
// and the loser waits for it rather than failing or making one of its own.
func TestACreateLostToAnotherBinaryWaitsForTheWinner(t *testing.T) {
	shim := newDockerShim(t, shimLosesTheCreate)

	ensureServer(t)

	calls := shim.calls(t)
	if strings.Contains(calls, "rm ") {
		t.Fatalf("losing the create removed the winner's container:\n%s", calls)
	}
	if got := strings.Count(calls, "run "); got != 1 {
		t.Fatalf("the create was attempted %d times, want once and then a wait:\n%s", got, calls)
	}
}

// TestADockerFailureCarriesDockersOwnWords pins the text a failure reaches the
// case with. docker explains itself on stderr and exits with a bare status, so
// an error built from the status alone reads "exit status 125" and names
// nothing to act on.
func TestADockerFailureCarriesDockersOwnWords(t *testing.T) {
	newDockerShim(t, shimLosesTheCreate)

	_, err := docker(t, "run", "-d", "--name", containerName, image)
	if err == nil {
		t.Fatal("the refused create reported no error")
	}
	if !strings.Contains(err.Error(), "is already in use") {
		t.Fatalf("the error does not carry what docker said: %v", err)
	}
}

// shimState is where the fake docker's container starts out.
type shimState int

const (
	// shimExists is a container that is there and not running.
	shimExists shimState = iota
	// shimLosesTheCreate is no container at all, and another binary whose
	// create wins the name at the same moment: this one's create is refused,
	// and the winner's container is running by the time it looks again.
	shimLosesTheCreate
)

// dockerShim is a fake docker on PATH. It answers the calls the fixture makes,
// keeps the container's state in files, and records every call.
type dockerShim struct {
	dir string
}

func newDockerShim(t *testing.T, state shimState) *dockerShim {
	t.Helper()
	dir := t.TempDir()
	if state == shimExists {
		if err := os.WriteFile(filepath.Join(dir, "exists"), nil, 0o600); err != nil {
			t.Fatalf("writing the shim's state: %v", err)
		}
	}
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
		if [ -f "$dir/exists" ]; then echo /` + containerName + `; else echo "Error: No such object" >&2; exit 1; fi
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
	if [ ! -f "$dir/exists" ]; then
		touch "$dir/exists" "$dir/running"
		echo "docker: Error response from daemon: Conflict. The container name \"/` + containerName +
		`\" is already in use by another container." >&2
		exit 125
	fi
	touch "$dir/running"
	;;
start)
	touch "$dir/running"
	echo ` + containerName + `
	;;
rm)
	rm -f "$dir/exists" "$dir/running"
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
