package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// A test binary that is killed, or that panics at its deadline, runs none of its
// cleanups, and every host it started is left running — bound to its ports,
// holding pool connections to databases the next run drops. Each binary
// therefore keeps a record of the children it has running, and the next binary
// to start ends the children of any binary that is gone.
//
// The record is one file per test binary, named by its process id, in a fixed
// directory under the temporary directory: the first line is the host binary's
// path, and every other line is the process id of a child still running. A
// child is ended only if the process with that id is still running that
// binary, so a process id the system has since handed to something else is
// left alone.
//
// This is housekeeping: a failure to keep the record costs a stray process, not
// a wrong result, and is not reported.

// childrenDirName is the directory the records live in, under os.TempDir.
const childrenDirName = "speed-crmtest-children"

// childRecord is this binary's record of its running children.
type childRecord struct {
	mu   sync.Mutex
	live []int
}

var children childRecord

func childrenDir() string { return filepath.Join(os.TempDir(), childrenDirName) }

func (r *childRecord) path() string {
	return filepath.Join(childrenDir(), strconv.Itoa(os.Getpid()))
}

// add records a child that has started.
func (r *childRecord) add(pid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = append(r.live, pid)
	r.writeLocked()
}

// remove forgets a child that has exited.
func (r *childRecord) remove(pid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = slices.DeleteFunc(r.live, func(p int) bool { return p == pid })
	r.writeLocked()
}

// forget removes the record, once every child has been stopped.
func (r *childRecord) forget() {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = os.Remove(r.path())
}

func (r *childRecord) writeLocked() {
	lines := []string{hostBinary}
	for _, pid := range r.live {
		lines = append(lines, strconv.Itoa(pid))
	}
	if err := os.MkdirAll(childrenDir(), 0o700); err != nil {
		return
	}
	_ = writeFileAtomic(r.path(), []byte(strings.Join(lines, "\n")+"\n"))
}

// reapOrphans ends the children of test binaries that are gone, and removes
// their records and their build directories.
func reapOrphans() {
	entries, err := os.ReadDir(childrenDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		owner, err := strconv.Atoi(entry.Name())
		if err != nil || owner == os.Getpid() || processAlive(owner) {
			continue
		}
		path := filepath.Join(childrenDir(), entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		binary := lines[0]
		for _, line := range lines[1:] {
			pid, err := strconv.Atoi(line)
			if err != nil || !processAlive(pid) || !runs(pid, binary) {
				continue
			}
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Signal(syscall.SIGKILL)
			}
		}
		removeBuildDir(binary)
		_ = os.Remove(path)
	}
}

// runs reports whether a process is running a given binary, by the command
// line the system reports for it.
func runs(pid int, binary string) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := strings.TrimSpace(string(out))
	return command == binary || strings.HasPrefix(command, binary+" ")
}

// removeBuildDir removes the directory a gone binary built the host into, which
// TestMain would have removed had it returned. Only a directory of the shape
// TestMain creates is removed.
func removeBuildDir(binary string) {
	dir := filepath.Dir(binary)
	if filepath.Dir(dir) != filepath.Clean(os.TempDir()) || !strings.HasPrefix(filepath.Base(dir), "crm-host") {
		return
	}
	_ = os.RemoveAll(dir)
}
