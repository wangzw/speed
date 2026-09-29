package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The cases run the host as a real child process and reach it over sockets:
// what this host evaluates is assembly-level — that a statement written with
// no tenant condition comes back scoped, that a refusal lands where the chain
// says it does, that each stack answers the same way — and none of it is
// observable from inside a package.
//
// This file is the process side of the harness: building the binary, running a
// command of a stack, waiting for the ready record, reading the output as
// records and stopping a run. fixture_test.go builds a case on top of it,
// backends_test.go brings up what the stacks run on, children_test.go keeps a
// killed test binary's children from outliving it, and client_test.go makes the
// requests.

// hostBinary is the compiled program every case runs, built once in TestMain
// because building it is the slowest thing here.
var hostBinary string

// The bounds a case waits within. Exceeding one means the host hung, and the
// case says so with the output attached rather than with a timeout of the test
// binary.
var (
	// startLimit bounds how long a long-running command takes to write its
	// ready record. A binary built for the race detector starts slower.
	startLimit = scaledForRace(60 * time.Second)
	// stopLimit bounds how long a run takes to leave after SIGTERM.
	stopLimit = scaledForRace(30 * time.Second)
	// commandLimit bounds a one-shot command.
	commandLimit = scaledForRace(60 * time.Second)
)

const (
	// settleLimit bounds the wait for something a run writes after the
	// response, such as a request-level audit row.
	settleLimit = 5 * time.Second
	// pollInterval is how often a wait looks again.
	pollInterval = 20 * time.Millisecond
	// watchdogMargin is how long before the test binary's own deadline every
	// child still running is killed. Past the deadline the testing package
	// panics and no cleanup runs, so a child left running then outlives the
	// binary.
	watchdogMargin = 10 * time.Second
	// outputTail bounds how many lines of a run's output a report that is not
	// itself about that run carries.
	outputTail = 400
)

// raceWarning starts every report the race detector writes. A child that
// writes one has a data race in the host or in a module it runs, and the case
// fails naming it, whatever its exit status says.
const raceWarning = "WARNING: DATA RACE"

// scaledForRace lengthens a bound for a binary built with the race detector,
// which runs several times slower.
func scaledForRace(d time.Duration) time.Duration {
	if raceEnabled {
		return 3 * d
	}
	return d
}

func TestMain(m *testing.M) {
	reapOrphans()
	dir, err := os.MkdirTemp("", "crm-host")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "crm-host")
	// Built for the race detector when this binary is, so that the detector
	// watches the host and the modules it runs, which is where the requests
	// and the replicas are: the harness process only makes them.
	args := []string{"build"}
	if raceEnabled {
		args = append(args, "-race")
	}
	args = append(args, "-o", hostBinary, ".")
	if out, buildErr := exec.Command("go", args...).CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "building the host failed: %v\n%s", buildErr, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	children.forget()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// record is one log record taken apart: its message, its level and time, and
// its attributes, all as text. The host's narration is read as records rather
// than as lines because the rendering carries a timestamp, so a whole line is
// not something a case can state in advance.
type record map[string]string

// is reports whether this record carries a message and every one of the
// key-and-value attribute pairs given.
func (r record) is(msg string, attrs ...string) bool {
	if len(attrs)%2 != 0 {
		panic("an attribute filter is a sequence of key and value pairs")
	}
	if r["msg"] != msg {
		return false
	}
	for i := 0; i < len(attrs); i += 2 {
		if r[attrs[i]] != attrs[i+1] {
			return false
		}
	}
	return true
}

// parseTextRecord reads one line of the text format, reporting false for a line
// that is not a record at all: the startup diagnostics share the streams with
// the records, and so do the database module's statement logging and the
// span exporter's JSON.
func parseTextRecord(line string) (record, bool) {
	rec := record{}
	rest := strings.TrimRight(line, " \r")
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return nil, false
		}
		key := rest[:eq]
		if strings.ContainsAny(key, " \"{") {
			return nil, false
		}
		rest = rest[eq+1:]
		var value string
		switch {
		case strings.HasPrefix(rest, `"`):
			quoted, err := strconv.QuotedPrefix(rest)
			if err != nil {
				return nil, false
			}
			unquoted, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, false
			}
			value, rest = unquoted, rest[len(quoted):]
		default:
			if space := strings.IndexByte(rest, ' '); space >= 0 {
				value, rest = rest[:space], rest[space:]
			} else {
				value, rest = rest, ""
			}
		}
		rec[key] = value
		rest = strings.TrimLeft(rest, " ")
	}
	for _, key := range [...]string{"time", "level", "msg"} {
		if _, ok := rec[key]; !ok {
			return nil, false
		}
	}
	return rec, true
}

// records reads every record out of a stream, skipping whatever is not one.
func records(text string) []record {
	var out []record
	for line := range strings.SplitSeq(text, "\n") {
		if rec, ok := parseTextRecord(line); ok {
			out = append(out, rec)
		}
	}
	return out
}

// findRecord reports whether some record in the stream matches, and returns it.
func findRecord(text, msg string, attrs ...string) (record, bool) {
	for _, rec := range records(text) {
		if rec.is(msg, attrs...) {
			return rec, true
		}
	}
	return nil, false
}

// tail is the last lines of a run's output, for a report that carries the
// output of several runs.
func tail(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= outputTail {
		return text
	}
	return fmt.Sprintf("[%d earlier lines left out]\n%s", len(lines)-outputTail,
		strings.Join(lines[len(lines)-outputTail:], "\n"))
}

// childRaceOptions are the race detector's options a child built with it runs
// under, ahead of any the test binary's own GORACE gives, which a later option
// of the same name overrides.
//
// The detector sleeps a second before a process exits, by default, so that
// goroutines still running can reach a race while it waits, and every command
// and every replica a case runs would pay that second on its way out, in the
// case's own time. A report already being written is still waited for once
// the sleep is over, so without it a child loses only the races its goroutines
// would have reached in the second after its work was done: the cases watch
// what a child does while it runs, and a race it reaches then is reported as
// it happens.
const childRaceOptions = "atexit_sleep_ms=0"

// childEnv is the environment a child process starts with: this process's own,
// less every variable under the host's prefix and every OpenTelemetry one, so
// that what a case configures is the whole of what the child reads. The
// exporters read OTEL_EXPORTER_OTLP_* on their own, outside the configuration
// manifest, and a developer's shell carrying one would change what a case sees.
// A child built with the race detector gets childRaceOptions as well.
func childEnv(extra []string) []string {
	var env []string
	var gorace string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, envPrefix+"_") || strings.HasPrefix(kv, "OTEL_") {
			continue
		}
		if options, ok := strings.CutPrefix(kv, "GORACE="); ok && raceEnabled {
			gorace = options
			continue
		}
		env = append(env, kv)
	}
	if raceEnabled {
		env = append(env, "GORACE="+strings.TrimSpace(childRaceOptions+" "+gorace))
	}
	return append(env, extra...)
}

// commandResult is what a one-shot command left behind.
type commandResult struct {
	stdout string
	stderr string
	err    error
}

// combined is both streams, for a failure message.
func (r commandResult) combined() string {
	return "standard output:\n" + r.stdout + "\nstandard error:\n" + r.stderr
}

// runCommand runs one command of the binary to completion. A run that outlives
// commandLimit, or comes within watchdogMargin of the test binary's own
// deadline, is killed and reported as such, so a command that should have
// failed at startup and served instead cannot pass for one that failed.
func runCommand(t *testing.T, args, env []string, stdin string) commandResult {
	t.Helper()
	limit := commandLimit
	if deadline, ok := t.Deadline(); ok {
		limit = min(limit, time.Until(deadline)-watchdogMargin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, hostBinary, args...)
	configureChild(cmd)
	cmd.Env = childEnv(env)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	var err error
	if err = cmd.Start(); err == nil {
		children.add(cmd.Process.Pid)
		err = cmd.Wait()
		children.remove(cmd.Process.Pid)
	}
	if ctx.Err() != nil {
		err = fmt.Errorf("the command was still running after %s and was killed: %w", limit, ctx.Err())
	}
	res := commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
	if strings.Contains(res.stderr, raceWarning) {
		t.Errorf("%s reported a data race:\n%s", strings.Join(args, " "), res.combined())
	}
	return res
}

// process is a long-running command of the binary: serve or worker.
type process struct {
	name    string
	command string
	cmd     *exec.Cmd

	mu      sync.Mutex
	written strings.Builder

	exited  chan struct{}
	waitErr error

	stopOnce sync.Once
	// reported says the run's output has been put in a failure message, so
	// the report at the end of a failed case need not carry it again.
	reported atomic.Bool
}

// launch starts a long-running command and registers its stop as a cleanup,
// without waiting for it: a caller starting several replicas at once waits for
// each afterwards. It never fails the case itself; a command that could not be
// started reads as one that exited before it was ready.
//
// Three cleanups are registered, and they run in the reverse of the order they
// are registered in: the stop first, holding the run to a clean exit; then the
// race detector's report, which fails the case whatever the stop found; then,
// if the case failed and nothing has carried this run's output yet, the
// output — so a failed request is reported with what the host wrote about it
// even when the run itself stopped cleanly.
func launch(t *testing.T, name, command string, args, env []string) *process {
	p := &process{name: name, command: command, exited: make(chan struct{})}
	t.Cleanup(func() {
		out := p.output()
		switch {
		case strings.Contains(out, raceWarning):
			t.Errorf("%s reported a data race; it wrote:\n%s", p.name, out)
		case t.Failed() && !p.reported.Load():
			t.Logf("%s wrote, in the case that failed:\n%s", p.name, tail(out))
		}
	})
	p.cmd = exec.Command(hostBinary, args...)
	configureChild(p.cmd)
	p.cmd.Env = childEnv(env)
	p.cmd.Stdout = lockedWriter{p}
	p.cmd.Stderr = lockedWriter{p}
	if err := p.cmd.Start(); err != nil {
		p.waitErr = err
		close(p.exited)
		// Stopping a process that never ran has nothing to report beyond
		// what the wait for it already did.
		p.abandon()
		return p
	}
	children.add(p.cmd.Process.Pid)
	var watchdog *time.Timer
	if deadline, ok := t.Deadline(); ok {
		watchdog = time.AfterFunc(time.Until(deadline)-watchdogMargin, func() { _ = p.cmd.Process.Kill() })
	}
	go func() {
		p.waitErr = p.cmd.Wait()
		if watchdog != nil {
			watchdog.Stop()
		}
		children.remove(p.cmd.Process.Pid)
		close(p.exited)
	}()
	t.Cleanup(func() { p.stop(t) })
	return p
}

// output is everything the run has written so far, both streams in the order
// they arrived.
func (p *process) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.written.String()
}

// outputForReport is the output, marked as carried by a failure message.
func (p *process) outputForReport() string {
	p.reported.Store(true)
	return p.output()
}

// awaitReady polls until the run writes its ready record, and reports why it
// never did: it exited first, or the bound passed.
//
// Waiting on a listening port instead would hand the case a run whose modules
// may not all have started: the sockets are open before the command's action
// runs.
func (p *process) awaitReady() error {
	deadline := time.Now().Add(startLimit)
	for {
		if p.ready() {
			return nil
		}
		select {
		case <-p.exited:
			if p.ready() {
				return nil
			}
			if p.waitErr == nil {
				return errors.New("exited with status 0 before it was ready")
			}
			return fmt.Errorf("exited before it was ready (%w)", p.waitErr)
		case <-time.After(pollInterval):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("never reported itself ready within %s", startLimit)
		}
	}
}

// waitReady waits for the ready record and fails the case when it never comes.
func (p *process) waitReady(t *testing.T) {
	t.Helper()
	if err := p.awaitReady(); err != nil {
		p.settle()
		t.Fatalf("%s %v; it wrote:\n%s", p.name, err, p.outputForReport())
	}
}

// settle is what follows a run that did not come up, once its failure has been
// reported: one that exited has nothing more to report at its stop, and one
// still starting is left to the stop, which terminates it.
func (p *process) settle() {
	select {
	case <-p.exited:
		p.abandon()
	default:
	}
}

// abandon gives the run's stop up: a run that is replaced or whose failure has
// been reported is not held to a clean exit as well.
func (p *process) abandon() { p.stopOnce.Do(func() {}) }

// ready reports whether the run has written its ready record.
func (p *process) ready() bool {
	_, ok := findRecord(p.output(), readyMsg, "command", p.command)
	return ok
}

// stop asks the run to leave with SIGTERM and holds it to leaving cleanly: exit
// status 0, and the stopped record written. It runs once, whether a case calls
// it or the cleanup does, and a run that had already exited is reported rather
// than signalled.
//
// The exit status is asserted, not collected: a shutdown whose Stop or Close
// failed ends the drive with an error and the process with status 1, and that
// is a defect of whichever module failed, which a harness that ignored the
// status would never report.
//
// A run that never wrote its ready record is only terminated. It is one the
// case gave up on while it was still starting — a replica launched beside one
// that failed to come up — and SIGTERM cancels whichever stage it was in, so
// its exit status says how that stage ended rather than how a stop did.
func (p *process) stop(t *testing.T) {
	t.Helper()
	p.stopOnce.Do(func() {
		select {
		case <-p.exited:
			t.Errorf("%s had already exited (%v) before it was asked to stop; it wrote:\n%s",
				p.name, p.waitErr, p.outputForReport())
			return
		default:
		}
		wasReady := p.ready()
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Errorf("signalling %s: %v", p.name, err)
		}
		select {
		case <-p.exited:
		case <-time.After(stopLimit):
			_ = p.cmd.Process.Kill()
			<-p.exited
			t.Errorf("%s did not leave within %s of SIGTERM and was killed; it wrote:\n%s",
				p.name, stopLimit, p.outputForReport())
			return
		}
		if !wasReady {
			return
		}
		if p.waitErr != nil {
			t.Errorf("%s left with %v after SIGTERM, want exit status 0; it wrote:\n%s",
				p.name, p.waitErr, p.outputForReport())
			return
		}
		if _, ok := findRecord(p.output(), stoppedMsg, "command", p.command); !ok {
			t.Errorf("%s wrote no %q record after SIGTERM; it wrote:\n%s", p.name, stoppedMsg, p.outputForReport())
		}
	})
}

// kill ends the run with SIGKILL, as a crash or an out-of-memory kill would:
// no stage of it runs, so nothing it holds is given back and nothing it
// received and did not finish is acknowledged. Its stop is given up, since a
// killed run has no clean exit to be held to.
func (p *process) kill(t *testing.T) {
	t.Helper()
	p.abandon()
	if err := p.cmd.Process.Kill(); err != nil {
		select {
		case <-p.exited:
			t.Fatalf("%s had already exited (%v) before it was killed; it wrote:\n%s", p.name, p.waitErr,
				p.outputForReport())
		default:
			t.Fatalf("killing %s: %v", p.name, err)
		}
	}
	select {
	case <-p.exited:
	case <-time.After(stopLimit):
		t.Fatalf("%s was still running %s after SIGKILL", p.name, stopLimit)
	}
}

// lockedWriter collects a child's two streams into one buffer under a lock,
// because a case reads it while the child writes.
type lockedWriter struct{ p *process }

func (w lockedWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	return w.p.written.Write(b)
}

// handedOut is every port freeAddress has handed out in this test binary.
//
// The operating system is free to hand one port to two callers in turn once
// the first has given it back, and every reservation here gives it back before
// the child binds it; with cases running in parallel, two of them could be
// handed the same port and one child would lose it to the other. Remembering
// the ports rules that out within the binary. What remains is a process outside
// it binding the port in the window before the child does, which the serving
// fixture answers by relaunching once on fresh ports.
var handedOut = struct {
	sync.Mutex
	ports map[string]bool
}{ports: map[string]bool{}}

// freeAddress asks the operating system for a port no earlier call has handed
// out, and gives it back for a child to bind.
func freeAddress(t *testing.T) string {
	t.Helper()
	const attempts = 1000
	for range attempts {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("asking for a free port: %v", err)
		}
		addr := l.Addr().String()
		if err := l.Close(); err != nil {
			t.Fatalf("releasing the port: %v", err)
		}
		handedOut.Lock()
		taken := handedOut.ports[addr]
		handedOut.ports[addr] = true
		handedOut.Unlock()
		if !taken {
			return addr
		}
	}
	t.Fatalf("the operating system answered %d requests for a free port with ports already handed out", attempts)
	return ""
}

// writeFileAtomic replaces a file whole, and leaves it alone when it already
// holds these bytes.
func writeFileAtomic(path string, body []byte) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, body) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// eventually polls a condition until it holds or the bound passes.
func eventually(within time.Duration, holds func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if holds() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}
