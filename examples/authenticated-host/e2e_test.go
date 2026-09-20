package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/static"
)

// The cases run the host as a real child process and reach it over a socket.
// What this example has to prove is that an assembly put together out of the
// module mechanism actually guards traffic, and a status code that never
// crossed a connection would not prove it.

// hostBinary is the compiled program every case runs, built once in TestMain
// because building it is the slowest thing here.
var hostBinary string

// waitLimit bounds how long a case waits for the host to start listening or to
// leave. Exceeding it means the host hung, and the case says so with the
// output attached rather than with a timeout of the test binary.
const waitLimit = 15 * time.Second

// The account every case authenticates with.
const (
	subject  = "alice"
	password = "s3cret"
	// qualified is what a handler behind the gateway sees. The owner is
	// the name the backend reports, and it is never dropped — not even
	// with one backend running.
	qualified = "local/" + subject
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "authenticated-host")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "authenticated-host")
	if out, err := exec.Command("go", "build", "-o", hostBinary, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the host failed: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// running is a host that is listening, together with what it has written.
type running struct {
	base string
	// output is everything the host put on its two streams, readable once
	// stop has returned.
	output func() string
	stop   func()
}

// startHost writes a configuration holding one account, starts the host on a
// free port and waits until it answers.
func startHost(t *testing.T) *running {
	t.Helper()

	hashed, err := static.Hash(password)
	if err != nil {
		t.Fatalf("deriving the password hash for this run: %v", err)
	}
	address := freeAddress(t)
	config := writeConfig(t, address, hashed, true)

	// The locator carries a scheme, which is what selects the transport: a
	// bare path names no transport, and config refuses it rather than
	// guessing that a local file was meant.
	cmd := exec.Command(hostBinary, "serve", "--config", "file://"+config)
	var mu sync.Mutex
	var written strings.Builder
	cmd.Stdout = lockedWriter{&mu, &written}
	cmd.Stderr = lockedWriter{&mu, &written}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the host: %v", err)
	}

	stopped := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Process.Signal(os.Interrupt)
			select {
			case <-stopped:
			case <-time.After(waitLimit):
				_ = cmd.Process.Kill()
				<-stopped
			}
		})
	}
	go func() {
		_ = cmd.Wait()
		close(stopped)
	}()
	t.Cleanup(stop)

	read := func() string {
		mu.Lock()
		defer mu.Unlock()
		return written.String()
	}
	waitUntilListening(t, address, read)
	return &running{base: "http://" + address, output: read, stop: stop}
}

// writeConfig writes a configuration file and returns its path. With guarded
// false the gateway's endpoint list is left out, which is the one input it
// stands down over.
func writeConfig(t *testing.T, address, hashed string, guarded bool) string {
	t.Helper()

	guardedSection := ""
	if guarded {
		guardedSection = "  gate:\n    endpoints:\n      - public\n"
	}
	// The subject table holds a hash and never the password itself: this
	// file is the one an operator checks in or mounts as a secret.
	body := fmt.Sprintf(`http:
  endpoints:
    public:
      address: %q
authn:
%s  static:
    name: local
    subjects:
      %s: %q
`, address, guardedSection, subject, hashed)

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	return path
}

// lockedWriter serialises the two streams into one buffer, since the host
// writes to both from goroutines of its own.
type lockedWriter struct {
	mu *sync.Mutex
	to *strings.Builder
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.to.Write(p)
}

// freeAddress picks an address nothing is listening on. There is a window
// between releasing it and the host binding it; it is the ordinary way to give
// a child process a port, and a case that loses the race fails on the bind
// rather than passing wrongly.
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("looking for a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

// waitUntilListening blocks until the host accepts a connection.
func waitUntilListening(t *testing.T, address string, output func() string) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the host did not start listening on %s within %s. It wrote:\n%s",
		address, waitLimit, output())
}

// call makes one request and returns the status and the body.
func call(t *testing.T, host *running, path string, credential func(*nethttp.Request)) (int, string) {
	t.Helper()
	r, err := nethttp.NewRequest(nethttp.MethodGet, host.base+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if credential != nil {
		credential(r)
	}
	resp, err := (&nethttp.Client{Timeout: waitLimit}).Do(r)
	if err != nil {
		t.Fatalf("calling %s: %v. The host wrote:\n%s", path, err, host.output())
	}
	defer resp.Body.Close()
	// Read to the end rather than taking one Read: a reader may hand back
	// fewer bytes than it has, and a short read would truncate the JSON and
	// fail a case whose subject behaved perfectly.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer to %s: %v", path, err)
	}
	return resp.StatusCode, string(body)
}

// withPassword puts a Basic credential on a request.
func withPassword(user, secret string) func(*nethttp.Request) {
	return func(r *nethttp.Request) { r.SetBasicAuth(user, secret) }
}

// TestAGuardedRouteNeedsACredentialAndAnExemptOneDoesNot is the whole point of
// the assembly, held from outside the process.
func TestAGuardedRouteNeedsACredentialAndAnExemptOneDoesNot(t *testing.T) {
	host := startHost(t)

	t.Run("the guarded route without a credential", func(t *testing.T) {
		status, _ := call(t, host, "/orders", nil)
		if status != nethttp.StatusUnauthorized {
			t.Errorf("answered %d, want %d", status, nethttp.StatusUnauthorized)
		}
	})

	t.Run("the guarded route with the right credential", func(t *testing.T) {
		status, body := call(t, host, "/orders", withPassword(subject, password))
		if status != nethttp.StatusOK {
			t.Fatalf("answered %d, want %d. The host wrote:\n%s",
				status, nethttp.StatusOK, host.output())
		}
		var answered orders
		if err := json.Unmarshal([]byte(body), &answered); err != nil {
			t.Fatalf("reading the answer %q: %v", body, err)
		}
		if answered.Caller != qualified {
			t.Errorf("the handler saw the caller %q, want %q: the qualification is not "+
				"dropped for a single-backend assembly, or a policy written against it "+
				"would go wrong the day a second backend arrives",
				answered.Caller, qualified)
		}
	})

	t.Run("the guarded route with the wrong password", func(t *testing.T) {
		status, _ := call(t, host, "/orders", withPassword(subject, "not the password"))
		if status != nethttp.StatusUnauthorized {
			t.Errorf("answered %d, want %d: a wrong password is a refusal, and it has to be "+
				"told apart from the module being unable to answer",
				status, nethttp.StatusUnauthorized)
		}
	})

	t.Run("the guarded route under a subject nobody claims", func(t *testing.T) {
		status, _ := call(t, host, "/orders", withPassword("mallory", password))
		if status != nethttp.StatusUnauthorized {
			t.Errorf("answered %d, want %d", status, nethttp.StatusUnauthorized)
		}
	})

	t.Run("the exempt route without a credential", func(t *testing.T) {
		status, body := call(t, host, "/health", nil)
		if status != nethttp.StatusOK {
			t.Fatalf("answered %d, want %d. The host wrote:\n%s",
				status, nethttp.StatusOK, host.output())
		}
		if !strings.Contains(body, `"status":"ok"`) {
			t.Errorf("the exempt route answered %q, want the health document", body)
		}
	})

	t.Run("a route nobody registered", func(t *testing.T) {
		// The gateway runs ahead of routing, so an unauthenticated
		// caller is told it needs a credential rather than told which
		// paths exist.
		status, _ := call(t, host, "/nothing-here", nil)
		if status != nethttp.StatusUnauthorized {
			t.Errorf("answered %d, want %d: an unauthenticated caller learns nothing about "+
				"which routes the endpoint carries", status, nethttp.StatusUnauthorized)
		}
	})
}

// TestTheStartupDiagnosticsSayWhatIsGuardedAndWhoTurnedUp holds the two lines
// the design asks for unconditionally on the success path.
//
// The roster is the only way to notice a backend that the host imported but
// never wrote into the command: the registry says nothing about a module no
// command took in.
func TestTheStartupDiagnosticsSayWhatIsGuardedAndWhoTurnedUp(t *testing.T) {
	host := startHost(t)
	// One request, so that the host has certainly passed Start by the time
	// the output is read.
	call(t, host, "/health", nil)
	host.stop()

	written := host.output()
	for _, want := range []string{
		// The release unit's name, not the module's: a reader scanning
		// the diagnostics is looking for what authentication did.
		"authn: ",
		"the credential-verifying backends taking part",
		"local",
		"the exemption table, frozen",
		"public",
		healthRoute,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the startup diagnostics do not carry %q. The host wrote:\n%s",
				want, written)
		}
	}
}

// TestTheHashCommandProducesWhatTheTableHolds holds the path an operator takes
// to write the first account, and holds that no password is ever written into
// configuration in the clear.
func TestTheHashCommandProducesWhatTheTableHolds(t *testing.T) {
	cmd := exec.Command(hostBinary, "hash")
	cmd.Stdin = strings.NewReader(password + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the hash command: %v", err)
	}

	encoded := strings.TrimSpace(string(out))
	if strings.Contains(encoded, password) {
		t.Fatalf("the hash %q carries the password itself", encoded)
	}
	parsed, err := static.ParseHash(encoded)
	if err != nil {
		t.Fatalf("the hash command wrote %q, which the table cannot read: %v", encoded, err)
	}
	_ = parsed
}

// TestAGuardedRouteWithNoGatewayStopsTheStartup holds what happens when the
// gateway states itself disabled under a command that carries guarded routes.
//
// The host does not come up serving them unguarded. The module that registered
// the exemption takes the gateway up in its own Init, finds no provider, and
// ends the startup — which turns the worst failure this design has (a run that
// silently serves everything without a credential) into a process that says
// what it lacked and refuses to start.
func TestAGuardedRouteWithNoGatewayStopsTheStartup(t *testing.T) {
	hashed, err := static.Hash(password)
	if err != nil {
		t.Fatalf("deriving the password hash: %v", err)
	}
	config := writeConfig(t, freeAddress(t), hashed, false)

	out, err := exec.Command(hostBinary, "serve", "--config", "file://"+config).CombinedOutput()
	if err == nil {
		t.Fatalf("the host started with no endpoint to guard, and every route on it would be "+
			"served without a credential. It wrote:\n%s", out)
	}

	written := string(out)
	// The stand-down reason names the key to write, which is the whole of
	// what an operator has to change.
	for _, want := range []string{"authn.gate.endpoints", "no endpoint is guarded"} {
		if !strings.Contains(written, want) {
			t.Errorf("the failure does not carry %q, so it does not say what to fix. The "+
				"host wrote:\n%s", want, written)
		}
	}
}

// TestACommandThatAssemblesNothingAssemblesNothing holds that a command with
// an empty participating set does not quietly take in the gateway, which every
// import in this host's main would otherwise put in the available set.
func TestACommandThatAssemblesNothingAssemblesNothing(t *testing.T) {
	out, err := exec.Command(hostBinary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("running the version command: %v\n%s", err, out)
	}
	written := string(out)
	if !strings.Contains(written, hostVersion) {
		t.Errorf("the version command answered %q, want %q", written, hostVersion)
	}
	if strings.Contains(written, "authn.gate:") {
		t.Errorf("the version command wrote the gateway's startup diagnostics (%q), so it "+
			"assembled a module its command names none of", written)
	}
}
