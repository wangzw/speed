package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/log"
)

// The cases run the host as a real child process rather than calling into it.
// What this example has to prove is that a main assembled out of the module
// mechanism runs: the exit status, the two output streams and the reaction to
// a signal are all part of that, and none of them exists inside a test binary
// that rewrote os.Args on itself.

// hostBinary is the compiled program every case runs. It is built once, in
// TestMain, because building it is the slowest thing here by far.
var hostBinary string

// waitLimit bounds how long a case waits for the host to become ready or to
// leave. It is generous: exceeding it means the host hung, and the case says
// so with both streams attached rather than with a timeout of the test binary.
const waitLimit = 15 * time.Second

// filePollInterval is how often a case waiting on a log file looks at it again.
const filePollInterval = 20 * time.Millisecond

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "minimal-host")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "minimal-host")
	if out, err := exec.Command("go", "build", "-o", hostBinary, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the host failed: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// record is one log record taken apart: its message, its level and time, and
// its attributes, all as text. The host's narration is read as records rather
// than as lines because that is what it now is — the rendering carries a
// timestamp and the encoding differs per destination, so a whole line is not
// something a case can state in advance.
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

// String renders a record the way a failure message needs it: sorted is not
// worth the trouble, what a reader wants is the message and what came with it.
func (r record) String() string {
	parts := make([]string, 0, len(r))
	for k, v := range r {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

// parseTextRecord reads one line of the text format. It reports false for a
// line that is not a record at all: the startup diagnostics share the stream
// with the records, and a half-written line is what a case polling a file that
// is being appended to sees.
//
// The quoting is not optional. A value carrying a space — the greeting does —
// is written quoted, so splitting on spaces alone would tear it in two and
// leave the rest of the line unparseable.
func parseTextRecord(line string) (record, bool) {
	rec := record{}
	rest := strings.TrimRight(line, " ")
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return nil, false
		}
		key := rest[:eq]
		if strings.ContainsAny(key, " \"") {
			return nil, false
		}
		rest = rest[eq+1:]
		var value string
		if strings.HasPrefix(rest, `"`) {
			quoted, err := strconv.QuotedPrefix(rest)
			if err != nil {
				return nil, false
			}
			unquoted, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, false
			}
			value, rest = unquoted, rest[len(quoted):]
		} else if space := strings.IndexByte(rest, ' '); space >= 0 {
			value, rest = rest[:space], rest[space:]
		} else {
			value, rest = rest, ""
		}
		rec[key] = value
		rest = strings.TrimLeft(rest, " ")
	}
	// The three keys the standard library's text handler always writes. A
	// line without them is something other than a record, whatever else it
	// happens to carry an equals sign in.
	for _, key := range [...]string{"time", "level", "msg"} {
		if _, ok := rec[key]; !ok {
			return nil, false
		}
	}
	return rec, true
}

// parseJSONRecord reads one line of the JSON format. Numbers are kept as they
// were written rather than turned into floats, so an identifier reads back as
// the digits it was logged as.
func parseJSONRecord(line string) (record, bool) {
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return nil, false
	}
	rec := record{}
	for k, v := range fields {
		rec[k] = fmt.Sprint(v)
	}
	if _, ok := rec["msg"]; !ok {
		return nil, false
	}
	return rec, true
}

// records reads every record out of a stream or a file, skipping whatever is
// not one.
func records(text string, parse func(string) (record, bool)) []record {
	var out []record
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		if rec, ok := parse(line); ok {
			out = append(out, rec)
		}
	}
	return out
}

// textRecords reads the records out of a stream in the text format.
func textRecords(text string) []record { return records(text, parseTextRecord) }

// jsonRecords reads the records out of a stream or file in the JSON format.
func jsonRecords(text string) []record { return records(text, parseJSONRecord) }

// readLog reads the records a run left in its log file.
func readLog(t *testing.T, path string) []record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log file %s failed: %v", path, err)
	}
	return jsonRecords(string(data))
}

// countRecords is how many of the records carry the message and attributes.
func countRecords(recs []record, msg string, attrs ...string) int {
	n := 0
	for _, rec := range recs {
		if rec.is(msg, attrs...) {
			n++
		}
	}
	return n
}

// hasRecord reports whether any record carries the message and attributes.
func hasRecord(recs []record, msg string, attrs ...string) bool {
	return countRecords(recs, msg, attrs...) > 0
}

// requireRecord asserts that a record is there and hands it back, so a case
// can go on to look at an attribute whose value it cannot state in advance.
func requireRecord(t *testing.T, recs []record, what, msg string, attrs ...string) record {
	t.Helper()
	for _, rec := range recs {
		if rec.is(msg, attrs...) {
			return rec
		}
	}
	t.Fatalf("%s carries no record %s:\n%s", what, describe(msg, attrs), formatRecords(recs))
	return nil
}

// requireNoRecord asserts that no record carries the message and attributes.
func requireNoRecord(t *testing.T, recs []record, what, msg string, attrs ...string) {
	t.Helper()
	if hasRecord(recs, msg, attrs...) {
		t.Errorf("%s carries a record %s and should not:\n%s",
			what, describe(msg, attrs), formatRecords(recs))
	}
}

// recordIndex gives the position of a record, failing the case when it is not
// there. Positions are what the shutdown order is read from.
func recordIndex(t *testing.T, recs []record, msg string, attrs ...string) int {
	t.Helper()
	for i, rec := range recs {
		if rec.is(msg, attrs...) {
			return i
		}
	}
	t.Fatalf("no record %s was written:\n%s", describe(msg, attrs), formatRecords(recs))
	return -1
}

// describe renders what a case was looking for.
func describe(msg string, attrs []string) string {
	out := "msg=" + msg
	for i := 0; i+1 < len(attrs); i += 2 {
		out += " " + attrs[i] + "=" + attrs[i+1]
	}
	return out
}

// formatRecords renders what was there instead.
func formatRecords(recs []record) string {
	lines := make([]string, 0, len(recs))
	for _, rec := range recs {
		lines = append(lines, rec.String())
	}
	if len(lines) == 0 {
		return "(no records at all)"
	}
	return strings.Join(lines, "\n")
}

// outcome is what one run of the host produced.
type outcome struct {
	stdout string
	stderr string
	code   int
}

// runSpec is one run: what the host is given, whether it is interrupted once
// it is ready, and where its readiness is observed.
type runSpec struct {
	env  []string
	args []string
	// interrupt says the run is stopped with a signal once it is ready
	// rather than left to leave on its own.
	interrupt bool
	// logFile, when set, is the file the readiness record is waited for in,
	// and standard output is then not watched at all. Exactly one observer
	// is active per run: a configuration with both a stdout and a file
	// output puts the record in both places, and two observers racing to
	// announce the same one record would close the same channel twice.
	logFile string
}

// runToCompletion runs the host and waits for it to leave on its own.
func runToCompletion(t *testing.T, env []string, args ...string) outcome {
	t.Helper()
	return runHost(t, runSpec{env: env, args: args})
}

// runUntilReady runs the host, waits for the ready record on standard output,
// then interrupts it and waits for it to leave.
func runUntilReady(t *testing.T, env []string, args ...string) outcome {
	t.Helper()
	return runHost(t, runSpec{env: env, args: args, interrupt: true})
}

// runUntilReadyInFile is the same for a run configured to log to a file: the
// records are not on standard output any more, which is the very thing those
// cases assert, so readiness is observed where the records actually go.
func runUntilReadyInFile(t *testing.T, env []string, logFile string, args ...string) outcome {
	t.Helper()
	return runHost(t, runSpec{env: env, args: args, interrupt: true, logFile: logFile})
}

// runHost drives one run. The environment is exactly what the case gives and
// never the ambient one: a MINIHOST variable on the machine running the tests
// would otherwise change what a case observes.
func runHost(t *testing.T, spec runSpec) outcome {
	t.Helper()
	cmd := exec.Command(hostBinary, spec.args...)
	cmd.Env = append([]string{}, spec.env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the host's stdout failed: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the host failed: %v", err)
	}

	ready := make(chan struct{})
	var once sync.Once
	// The announcement goes through a sync.Once whatever the observer:
	// closing a closed channel takes the whole test binary down, and that is
	// too expensive a way to learn that a run had two observers.
	announceReady := func() { once.Do(func() { close(ready) }) }
	drained := make(chan struct{})
	stopWatching := make(chan struct{})
	defer close(stopWatching)

	// stdout is read as it arrives rather than collected at the end: the
	// interrupt may only be sent once the host says it is ready, and the
	// records written after it are what the shutdown order is read from.
	var collected bytes.Buffer
	go func() {
		defer close(drained)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			collected.WriteString(line)
			collected.WriteByte('\n')
			if spec.logFile != "" {
				continue
			}
			if rec, ok := parseTextRecord(line); ok && rec.is(readyMsg, log.ModuleAttrKey, appModuleName) {
				announceReady()
			}
		}
	}()

	if spec.logFile != "" {
		go func() {
			for {
				// The file is being appended to while it is read, so
				// its last line may be half a record. A line that
				// does not parse is skipped and the next pass looks
				// again; it is never a failure.
				if data, err := os.ReadFile(spec.logFile); err == nil &&
					hasRecord(jsonRecords(string(data)), readyMsg, log.ModuleAttrKey, appModuleName) {
					announceReady()
					return
				}
				select {
				case <-stopWatching:
					return
				case <-time.After(filePollInterval):
				}
			}
		}()
	}

	// Where the readiness record is waited for, named for the failure
	// message: a timeout has to say what was watched as well as what for.
	where := "standard output"
	if spec.logFile != "" {
		where = "the log file " + spec.logFile
	}
	fail := func(format string, args ...any) {
		t.Helper()
		_ = cmd.Process.Kill()
		<-drained
		_ = cmd.Wait()
		extra := ""
		if spec.logFile != "" {
			data, err := os.ReadFile(spec.logFile)
			if err != nil {
				extra = fmt.Sprintf("\nthe log file could not be read: %v", err)
			} else {
				extra = fmt.Sprintf("\nlog file:\n%s", data)
			}
		}
		t.Fatalf(format+"\nstdout:\n%s\nstderr:\n%s%s",
			append(args, collected.String(), stderr.String(), extra)...)
	}

	left := false
	if spec.interrupt {
		select {
		case <-ready:
		case <-drained:
			left = true
		case <-time.After(waitLimit):
			fail("no record %s turned up in %s within %s",
				describe(readyMsg, []string{log.ModuleAttrKey, appModuleName}), where, waitLimit)
		}
		if !left {
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				fail("interrupting the host failed: %v", err)
			}
		}
	}

	select {
	case <-drained:
	case <-time.After(waitLimit):
		fail("the host did not leave within %s of the interrupt", waitLimit)
	}

	code := 0
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("waiting for the host failed: %v", err)
		}
		code = exit.ExitCode()
	}
	return outcome{stdout: collected.String(), stderr: stderr.String(), code: code}
}

// fileURI renders an absolute path the way the file transport serves it: a URI
// with no host.
func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// locator is the config locator of a file under testdata.
func locator(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("resolving %s failed: %v", name, err)
	}
	return fileURI(path)
}

// loggingConfig writes a config source at run time and returns its locator
// together with the log file it names.
//
// The outputs item takes the primary config source alone — it is a list of
// structs, and such a leaf has no flat form an environment variable or a flag
// could carry — so a case that wants its records in a file has to have a config
// file, and the path in it is in a directory the case only learns at run time.
// The sources under testdata stay as they are for that reason.
func loggingConfig(t *testing.T, alsoStdout bool) (locator, logFile string) {
	t.Helper()
	dir := t.TempDir()
	logFile = filepath.Join(dir, "host.log")
	body := "log:\n  outputs:\n"
	if alsoStdout {
		body += "    - to: stdout\n      format: text\n"
	}
	body += "    - to: file\n      format: json\n      path: \"" + logFile + "\"\n"
	source := filepath.Join(dir, "minimal-host.yaml")
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the config source failed: %v", err)
	}
	return fileURI(source), logFile
}

// requireContains asserts that a stream carries a piece of text.
func requireContains(t *testing.T, stream, text, what string) {
	t.Helper()
	if !strings.Contains(stream, text) {
		t.Errorf("%s does not carry %q:\n%s", what, text, stream)
	}
}

// requireAbsent asserts that a stream carries no trace of a piece of text.
func requireAbsent(t *testing.T, stream, text, what string) {
	t.Helper()
	if strings.Contains(stream, text) {
		t.Errorf("%s carries %q and should not:\n%s", what, text, stream)
	}
}

// requireFailedRun asserts that a run failed, and that standard error says
// what over exactly once.
//
// Once is half of what this asserts. The command-line library reports the
// failure it rejected a run over, and a host that also printed the error it
// was handed back would put the same line on the stream twice — a defect
// neither the exit status nor the presence of the text would show.
func requireFailedRun(t *testing.T, got outcome, mustSay string) {
	t.Helper()
	if got.code == 0 {
		t.Errorf("the run left with status 0 and should have failed\nstdout:\n%s\nstderr:\n%s",
			got.stdout, got.stderr)
	}
	if n := strings.Count(got.stderr, mustSay); n != 1 {
		t.Errorf("stderr names %q %d times, want exactly once:\n%s", mustSay, n, got.stderr)
	}
}

// Demonstration one: every command has its own help, the command-line library
// renders it out of what that command assembles, and the library is what
// answers — the exit status of a run that asked for help is its verdict, and
// so is the rejection of a word that is no command at all.

func TestHelpExitsZeroAndListsFlags(t *testing.T) {
	got := runToCompletion(t, nil, "serve", "--help")

	if got.code != 0 {
		t.Errorf("asking for help left with status %d, and it is the library that answers it",
			got.code)
	}
	// The placeholder each module declared, in place of the type name the
	// command-line library would otherwise print after the argument.
	requireContains(t, got.stdout, "--salutation WORD", "the help output")
	requireContains(t, got.stdout, "--remote-addr HOST:PORT", "the help output")
	requireContains(t, got.stdout, "the word the built-in greeter opens with", "the help output")
	// A required argument says so. It hands over no default, so the column
	// that tells an argument the run needs from one it can do without is
	// empty for it, and the mark is what is left to read the difference off.
	requireContains(t, got.stdout, "the address of the greeting service (required)",
		"the help output")
	// The default reaches the reader through the usage text. The argument's
	// own default is left empty, so that an argument nobody wrote does not
	// shadow the value the config source gave, and the text is where the
	// reader is told what the value would have been.
	requireContains(t, got.stdout, "(default "+localDefaults.Salutation+")", "the help output")
	// The logging module is assembled into this command like any other, so
	// its own items are in the same help output.
	requireContains(t, got.stdout, "--log-level LEVEL", "the help output")
	// The reserved argument is registered on the root, so every command
	// inherits it rather than declaring it again. Its placeholder is this
	// module's own, like every other argument's: the name of the Go type
	// the library holds the value in would be the one line here that names
	// a type rather than a value.
	requireContains(t, got.stdout, "--config LOCATOR", "the help output")
	requireAbsent(t, got.stdout, "--config string", "the help output")
	if got.stderr != "" {
		t.Errorf("asking for help wrote to stderr:\n%s", got.stderr)
	}
}

func TestHelpHidesSensitiveDefault(t *testing.T) {
	got := runToCompletion(t, nil, "serve", "--help")

	requireContains(t, got.stdout, "--remote-token TOKEN", "the help output")
	requireContains(t, got.stdout, "the credential the greeting service authenticates with",
		"the help output")
	// The only evidence that a sensitive item's default is kept away from
	// the command-line library rather than merely left out of one rendering:
	// the library prints the default it was given, so a default it was given
	// would be here.
	requireAbsent(t, got.stdout, remoteDefaults.Token, "the help output")
}

func TestHelpIsAnsweredBeforeAnUnreachableSourceIsRead(t *testing.T) {
	got := runToCompletion(t, nil, "--config=file:///nowhere/minimal-host.yaml", "serve", "--help")

	// The source is never opened: the library answers the request for help
	// and no command runs, so the step that would have read it is not
	// reached. A locator pointing nowhere fails any run that gets that far,
	// which is what makes the status here worth asserting.
	if got.code != 0 {
		t.Errorf("asking for help left with status %d although the source is unreachable", got.code)
	}
	requireContains(t, got.stdout, "--salutation WORD", "the help output")
}

func TestEachCommandHelpListsOnlyItsOwnParticipants(t *testing.T) {
	root := runToCompletion(t, nil, "--help")

	// The root does nothing itself, so the program's name alone answers
	// with the list of what can be written after it.
	for _, command := range [...]string{"serve", "greet", "version"} {
		requireContains(t, root.stdout, command, "the root help output")
	}
	requireContains(t, root.stdout, "--config LOCATOR", "the root help output")
	// The root takes in no module, so no module's arguments are on it.
	requireAbsent(t, root.stdout, "--salutation", "the root help output")
	requireAbsent(t, root.stdout, "--log-level", "the root help output")

	// version assembles nothing, so it offers nothing beyond what the root
	// hands down.
	version := runToCompletion(t, nil, "version", "--help")
	for _, flag := range [...]string{"--salutation", "--remote-addr", "--log-level"} {
		requireAbsent(t, version.stdout, flag, "the version help output")
	}

	greet := runToCompletion(t, nil, "greet", "--help")
	requireContains(t, greet.stdout, "--salutation WORD", "the greet help output")
	requireContains(t, greet.stdout, "--remote-addr HOST:PORT", "the greet help output")
	// greet names the two greeters and never names logging. Logging is on
	// this command because both of them require it, and this argument is the
	// outward sign of it: taking a module in took in the provider of what
	// that module requires, and no line of the host says so.
	requireContains(t, greet.stdout, "--log-level LEVEL", "the greet help output")
}

func TestUnknownSubcommandIsReportedByTheLibrary(t *testing.T) {
	got := runToCompletion(t, nil, "migrate")

	// A word that selects no command is the library's to reject, and this
	// host adds no error of its own for it.
	requireFailedRun(t, got, "migrate")
}

// Demonstration two: the command decides which modules the run assembles, how
// far the lifecycle advances, and what comes in with what the command named.

func TestVersionAssemblesNothing(t *testing.T) {
	got := runToCompletion(t, nil, "version")

	if got.code != 0 {
		t.Errorf("printing the version left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	requireContains(t, got.stdout, hostVersion, "stdout")
	// An empty participating set read as "everything" would assemble the
	// whole host here, and the modules would narrate it: an endpoint, a
	// readiness record, a stop and a close for each of them. What tells
	// those from the version line itself is the module attribute, which
	// every record a module writes carries and nothing the host writes
	// outside a module does.
	for _, rec := range textRecords(got.stdout) {
		if rec[log.ModuleAttrKey] != "" {
			t.Errorf("version assembled module %q, and it takes in none:\n%s",
				rec[log.ModuleAttrKey], rec)
		}
	}
	// Nothing was prepared either, so no module had a stance to report.
	if got.stderr != "" {
		t.Errorf("printing the version wrote to stderr:\n%s", got.stderr)
	}
}

func TestFlagOutsideTheParticipatingSetIsRejected(t *testing.T) {
	got := runToCompletion(t, nil, "version", "--remote-addr=x:1")

	requireFailedRun(t, got, "remote-addr")

	// The control. Without it an implementation that registered every
	// argument on every command and one that registered none at all would
	// both satisfy the half above: what is asserted is that the command
	// line follows the participating set, not that this argument is
	// unwelcome everywhere.
	ok := runUntilReady(t, nil, "serve", "--remote-addr=cli:7000")
	if ok.code != 0 {
		t.Errorf("the same argument on a command that does take the module in left with "+
			"status %d\nstderr:\n%s", ok.code, ok.stderr)
	}
	requireRecord(t, textRecords(ok.stdout), "stdout", endpointMsg, endpointAttrKey, "cli:7000")
}

func TestSourceKeysOutsideTheParticipatingSetAreNotUnknown(t *testing.T) {
	// The source configures both greeters, and version takes neither of
	// them in. An unknown key is judged against every declaration there is
	// rather than against the command's own set: judging it against the set
	// would make this same file unknown-ridden under every command that
	// does not assemble all of it, which is one config file per command.
	got := runToCompletion(t, nil, "--config="+locator(t, "remote.yaml"), "version")

	if got.code != 0 {
		t.Errorf("a source carrying the keys of modules this command does not assemble left "+
			"it with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	requireContains(t, got.stdout, hostVersion, "stdout")
}

// The host module carries the environment variable prefix, and greet does not
// name it: greet names the two greeters, and neither of them requires it. The
// prefix reaches the run all the same, because collection runs over every
// registered module before any command is looked at.
//
// serve cannot show this. Taking in every available module takes the host
// module in too, so a collection scoped to the participating set would look
// identical under serve and lose the prefix only under greet — and every other
// case that sets a MINIHOST variable runs serve.
func TestHostIdentityReachesACommandThatDoesNotNameTheHostModule(t *testing.T) {
	env := []string{"MINIHOST_GREETER__LOCAL__SALUTATION=from-env"}
	got := runToCompletion(t, env, "greet")

	if got.code != 0 {
		t.Errorf("greeting left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	requireContains(t, got.stdout, "from-env, world", "stdout")
	// The control on the same run: without the prefix the variable names no
	// item, the declared default stands, and the greeting would read this
	// way instead.
	requireAbsent(t, got.stdout, localDefaults.Salutation+", world", "stdout")
}

func TestGreetStopsAtConstructionAndStillReleases(t *testing.T) {
	got := runToCompletion(t, nil, "greet")

	if got.code != 0 {
		t.Errorf("greeting left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	requireContains(t, got.stdout, localDefaults.Salutation+", world", "stdout")

	recs := textRecords(got.stdout)
	// Two reasons hold for the absence of these two records, and both are
	// the property: the drive stops at construction, so Start and Serve
	// never run, and the application module is not in this command's set
	// either. The depth is what is under test here — the set has the
	// version case to itself — and a greet that later took the application
	// module in would leave this half proving the depth alone.
	requireNoRecord(t, recs, "stdout", endpointMsg)
	requireNoRecord(t, recs, "stdout", readyMsg)
	// What was constructed is released all the same. One advance in and one
	// advance out, whatever depth it reached: a shallow drive is not a way
	// to leak what it built, and the host has no second call to remember.
	requireRecord(t, recs, "stdout", stopMsg, log.ModuleAttrKey, localModuleName)
	requireRecord(t, recs, "stdout", closeMsg, log.ModuleAttrKey, localModuleName)
}

// Demonstration three: the primary config source and the environment reach the
// same input item, and the environment wins.

func TestPrimarySourceValueTakesEffect(t *testing.T) {
	got := runUntilReady(t, nil, "--config="+locator(t, "local.yaml"), "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	recs := textRecords(got.stdout)
	requireRecord(t, recs, "stdout", greetingMsg, greetingAttrKey, "from-file, world")
	requireRecord(t, recs, "stdout", endpointMsg, endpointAttrKey, "local")
}

func TestLocatorAfterTheSubcommandIsTheSameRun(t *testing.T) {
	got := runUntilReady(t, nil, "serve", "--config="+locator(t, "local.yaml"))

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	// The reserved argument is registered on the root, so every command
	// inherits it rather than declaring one of its own. Written after the
	// command it names the same argument as written before it, and the case
	// above reads the same source the other way round.
	requireRecord(t, textRecords(got.stdout), "stdout", greetingMsg, greetingAttrKey,
		"from-file, world")
}

func TestFileThenEnvOverride(t *testing.T) {
	env := []string{"MINIHOST_GREETER__REMOTE__ADDR=from-env:9999"}
	got := runUntilReady(t, env, "--config="+locator(t, "remote.yaml"), "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	requireRecord(t, textRecords(got.stdout), "stdout", endpointMsg, endpointAttrKey, "from-env:9999")
	requireAbsent(t, got.stdout, "from-file:1234", "stdout")
}

func TestLocatorFromEnvVar(t *testing.T) {
	env := []string{"MINIHOST_CONFIG=" + locator(t, "remote.yaml")}
	got := runUntilReady(t, env, "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	requireRecord(t, textRecords(got.stdout), "stdout", endpointMsg, endpointAttrKey, "from-file:1234")
}

func TestUnknownPrefixedEnvIsDiagnosedNotFatal(t *testing.T) {
	env := []string{"MINIHOST_SERVICE_HOST=10.0.0.1"}
	got := runUntilReady(t, env, "serve")

	if got.code != 0 {
		t.Errorf("a variable under the prefix that no item reads left the host with status %d",
			got.code)
	}
	requireRecord(t, textRecords(got.stdout), "stdout", readyMsg, log.ModuleAttrKey, appModuleName)
	requireContains(t, got.stderr, "MINIHOST_SERVICE_HOST", "the startup diagnostics")
}

// Demonstration four: two providers of one capability, and the built-in one
// stands down as soon as the other is configured.

func TestLocalRunsWhenRemoteUnconfigured(t *testing.T) {
	got := runUntilReady(t, nil, "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	requireRecord(t, textRecords(got.stdout), "stdout", endpointMsg, endpointAttrKey, "local")
	requireContains(t, got.stderr, `module "`+remoteModuleName+`" is not enabled`,
		"the startup diagnostics")
	requireContains(t, got.stderr, "no connection address is configured",
		"the startup diagnostics")
	requireAbsent(t, got.stderr, `module "`+localModuleName+`" is not enabled`,
		"the startup diagnostics")
}

func TestRemoteWinsAndLocalStandsDown(t *testing.T) {
	got := runUntilReady(t, nil, "serve", "--remote-addr=cli:7000")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	requireRecord(t, textRecords(got.stdout), "stdout", endpointMsg, endpointAttrKey, "cli:7000")
	requireContains(t, got.stderr, `module "`+localModuleName+`" is not enabled`,
		"the startup diagnostics")
	requireContains(t, got.stderr, "already has an explicitly enabled provider",
		"the startup diagnostics")
	requireContains(t, got.stderr, `"`+remoteModuleName+`"`, "the startup diagnostics")
	requireAbsent(t, got.stderr, `module "`+remoteModuleName+`" is not enabled`,
		"the startup diagnostics")
}

// Demonstration five: cancelling the context stops and closes every module in
// reverse dependency order.

func TestSigintTriggersReverseOrderShutdown(t *testing.T) {
	got := runUntilReady(t, nil, "serve")

	if got.code != 0 {
		t.Errorf("an interrupt is the ordinary way to stop, and the host left with status %d",
			got.code)
	}
	recs := textRecords(got.stdout)
	appStop := recordIndex(t, recs, stopMsg, log.ModuleAttrKey, appModuleName)
	greeterStop := recordIndex(t, recs, stopMsg, log.ModuleAttrKey, localModuleName)
	appClose := recordIndex(t, recs, closeMsg, log.ModuleAttrKey, appModuleName)
	greeterClose := recordIndex(t, recs, closeMsg, log.ModuleAttrKey, localModuleName)

	// The application requires the greeter capability, so the greeter is
	// constructed first and released last, in both stages.
	if appStop > greeterStop {
		t.Errorf("the consumer stopped after its provider: app at %d, greeter at %d\n%s",
			appStop, greeterStop, got.stdout)
	}
	if appClose > greeterClose {
		t.Errorf("the consumer closed after its provider: app at %d, greeter at %d\n%s",
			appClose, greeterClose, got.stdout)
	}
	// Stop drains and Close releases, so nothing is released while another
	// module may still be draining.
	if max(appStop, greeterStop) > min(appClose, greeterClose) {
		t.Errorf("a module closed before every module had stopped:\n%s", got.stdout)
	}
}

// Demonstration six: a record says which module wrote it, and the dependency
// on logging orders the run around the module that declared it.

func TestRecordsCarryTheWritingModule(t *testing.T) {
	got := runUntilReady(t, nil, "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	recs := textRecords(got.stdout)
	// Neither module puts its own name into the call: the name is on the
	// logger each took through Named, so a module logging through the
	// process default logger instead would write these same messages with
	// no module attribute at all. The key these records are read by is the
	// one pkg/log exports, and what is read is the child process's real
	// output: a binding that moved to another key would leave these records
	// unfound rather than satisfy the case.
	requireRecord(t, recs, "stdout", stopMsg, log.ModuleAttrKey, appModuleName)
	requireRecord(t, recs, "stdout", closeMsg, log.ModuleAttrKey, localModuleName)
}

func TestDependantCloseRecordStillReachesTheFile(t *testing.T) {
	source, logFile := loggingConfig(t, false)
	got := runUntilReadyInFile(t, nil, logFile, "--config="+source, "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	recs := readLog(t, logFile)
	// Close runs in reverse dependency order, and both modules declared the
	// logging dependency, so logging is closed after them and these records
	// still reach the destination. Were it closed first, its writer would
	// already be in the closed state and these records would be dropped —
	// while the process would still exit cleanly, which is why the exit
	// status cannot be the judge of this.
	for _, module := range [...]string{appModuleName, localModuleName} {
		stop := recordIndex(t, recs, stopMsg, log.ModuleAttrKey, module)
		closed := recordIndex(t, recs, closeMsg, log.ModuleAttrKey, module)
		if closed < stop {
			t.Errorf("%s closed at %d, before it stopped at %d:\n%s",
				module, closed, stop, formatRecords(recs))
		}
	}
}

func TestModuleRecordDuringNewReachesTheConfiguredFile(t *testing.T) {
	source, logFile := loggingConfig(t, false)
	got := runUntilReadyInFile(t, nil, logFile, "--config="+source, "serve", "--remote-addr=cli:7000")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	// The record is written from inside the remote greeter's New, and it is
	// already in the configured file: declaring the dependency on the
	// logging capability is what put logging ahead of it.
	requireRecord(t, readLog(t, logFile), "the log file", configuredMsg,
		log.ModuleAttrKey, remoteModuleName, addrAttrKey, "cli:7000")
	requireNoRecord(t, textRecords(got.stdout), "stdout", configuredMsg)
}

// Demonstration seven: the logger travels on the context, injected where the
// context is created.

func TestContextPathCarriesTheInjectedLogger(t *testing.T) {
	source, logFile := loggingConfig(t, false)
	got := runUntilReadyInFile(t, nil, logFile, "--config="+source, "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	// The call site that writes this record holds no logger and names no
	// module: it asks the context. That the record carries the application
	// module's name and lands in the configured file is what says the
	// injection put the module's own logger there — an injection that had
	// passed log.Default() would put this record on standard output with no
	// module attribute, and the call site would look exactly the same.
	greeting := requireRecord(t, readLog(t, logFile), "the log file", greetingMsg,
		log.ModuleAttrKey, appModuleName)
	if greeting[greetingIDAttrKey] == "" {
		t.Errorf("the greeting record carries no %s, so the attributes bound at the "+
			"injection point did not travel with the logger:\n%s", greetingIDAttrKey, greeting)
	}
	requireNoRecord(t, textRecords(got.stdout), "stdout", greetingMsg)
}

// Demonstration eight: the configuration decides where records go, and the
// process default logger is not part of that.

func TestDefaultOutputIsTextOnStdout(t *testing.T) {
	got := runUntilReady(t, nil, "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	// Exactly one: the default is a single output to standard output, and a
	// configuration that ended up with two of them would write every record
	// twice down the same stream.
	if n := countRecords(textRecords(got.stdout), readyMsg, log.ModuleAttrKey, appModuleName); n != 1 {
		t.Errorf("stdout carries %d ready records in the text format, want exactly 1:\n%s",
			n, got.stdout)
	}
	if recs := textRecords(got.stderr); len(recs) != 0 {
		t.Errorf("stderr carries log records, and the default output is standard output:\n%s",
			formatRecords(recs))
	}
}

func TestConfiguredOutputTakesRecordsOffStdout(t *testing.T) {
	source, logFile := loggingConfig(t, false)
	got := runUntilReadyInFile(t, nil, logFile, "--config="+source, "serve")

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	inFile := readLog(t, logFile)
	onStdout := textRecords(got.stdout)
	requireRecord(t, inFile, "the log file", readyMsg, log.ModuleAttrKey, appModuleName)
	requireRecord(t, inFile, "the log file", stopMsg, log.ModuleAttrKey, appModuleName)
	requireNoRecord(t, onStdout, "stdout", readyMsg, log.ModuleAttrKey, appModuleName)
	requireNoRecord(t, onStdout, "stdout", stopMsg, log.ModuleAttrKey, appModuleName)
	// The closing record is the host's own, written from the action the
	// serving command arrived at rather than by a module, so it goes
	// through the process default logger. That one writes to standard
	// output whatever the configuration says, which is the whole point of
	// the split: a record turning up here rather than in the file is how a
	// path that never had a logger injected is spotted.
	requireRecord(t, onStdout, "stdout", stoppedMsg)
	requireNoRecord(t, inFile, "the log file", stoppedMsg)
}

// Demonstration nine: redaction masks the keys a module registered, in every
// destination at once.

func TestConfiguredCredentialNeverReachesAnyDestination(t *testing.T) {
	const credential = "e2e-credential-7f3c9a21"

	source, logFile := loggingConfig(t, true)
	got := runUntilReady(t, nil, "--config="+source, "serve",
		"--remote-addr=cli:7000", "--remote-token="+credential)

	if got.code != 0 {
		t.Errorf("the host left with status %d\nstderr:\n%s", got.code, got.stderr)
	}
	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading the log file %s failed: %v", logFile, err)
	}
	requireAbsent(t, got.stdout, credential, "stdout")
	requireAbsent(t, got.stderr, credential, "the startup diagnostics")
	requireAbsent(t, string(logged), credential, "the log file")

	// The masked value alone would not say much: a run that never received
	// the credential would mask the placeholder default just as thoroughly,
	// and a case that only looked for the absence of the value would pass on
	// a module that stopped logging the credential at all. token_given is
	// derived from the credential without carrying it, so it is true only if
	// the value from the command line really reached the binding.
	//
	// The replacement asserted below is the one pkg/log exports, and it is
	// asserted on both destinations' real output: a redaction layer leaving
	// any other text behind would leave these records unfound.
	for _, side := range [...]struct {
		what string
		recs []record
	}{
		{"stdout", textRecords(got.stdout)},
		{"the log file", jsonRecords(string(logged))},
	} {
		requireRecord(t, side.recs, side.what, configuredMsg,
			log.ModuleAttrKey, remoteModuleName,
			remoteTokenKey, log.MaskedText,
			tokenGivenKey, "true")
	}
}
