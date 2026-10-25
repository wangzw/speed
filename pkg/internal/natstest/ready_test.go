package natstest

import (
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

// refused is the error a dial gets while nothing listens on the published port
// yet: the shape net.DialTimeout actually returns, with the kernel's refusal
// under a *net.OpError.
func refused() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// timeout is a net.Error whose deadline expired, as a dial with a timeout that
// ran out reports it.
type timeout struct{}

func (timeout) Error() string   { return "i/o timeout" }
func (timeout) Timeout() bool   { return true }
func (timeout) Temporary() bool { return true }

// TestOnlyConnectionFailuresAreRetried pins which dial failures count as "not
// ready yet". A container that has just started refuses or resets the first
// connection; a malformed address or an unresolvable host answers the same way
// however long the wait runs, so the wait has to tell them apart instead of
// spending its whole bound on either.
func TestOnlyConnectionFailuresAreRetried(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"the connection is refused": {refused(), true},
		"the connection is reset": {
			&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)},
			true,
		},
		"the peer closed without a reply": {io.EOF, true},
		"the peer closed mid-reply":       {io.ErrUnexpectedEOF, true},
		"the dial timed out":              {&net.OpError{Op: "dial", Net: "tcp", Err: timeout{}}, true},
		"the address has no port": {
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.AddrError{Err: "missing port in address"}},
			false,
		},
		"the host does not resolve": {
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "no.invalid", IsNotFound: true}},
			false,
		},
		"a failure of no known class": {errors.New("the dial failed"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := retryableConnectionError(tc.err); got != tc.want {
				t.Errorf("retryableConnectionError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestAConnectionFailureIsRetriedUntilTheServerAnswers holds the wait to
// retrying the class the classification admits.
func TestAConnectionFailureIsRetriedUntilTheServerAnswers(t *testing.T) {
	attempts := 0
	err := waitReadyUntil(time.Now().Add(time.Second), time.Millisecond, func() error {
		attempts++
		if attempts < 3 {
			return refused()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("waitReadyUntil: %v, want the third attempt to be accepted", err)
	}
	if attempts != 3 {
		t.Errorf("the wait dialled %d times, want 3: a refused connection is not retried until the server answers", attempts)
	}
}

// TestAFailureAboutTheAddressEndsTheWaitAtOnce holds the wait to reporting a
// failure that retrying cannot clear, rather than spending the bound on it.
func TestAFailureAboutTheAddressEndsTheWaitAtOnce(t *testing.T) {
	attempts := 0
	failure := &net.AddrError{Err: "missing port in address", Addr: "127.0.0.1"}
	err := waitReadyUntil(time.Now().Add(time.Hour), time.Millisecond, func() error {
		attempts++
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("waitReadyUntil returned %v, want the malformed-address error", err)
	}
	if attempts != 1 {
		t.Errorf("the wait dialled %d times, want 1: an address that cannot become dialable was retried", attempts)
	}
}

// TestTheWaitEndsAtItsBound holds the wait to the bound it documents: a
// connection failure that never clears is reported when the deadline passes,
// not retried forever.
func TestTheWaitEndsAtItsBound(t *testing.T) {
	attempts := 0
	start := time.Now()
	err := waitReadyUntil(time.Now().Add(50*time.Millisecond), time.Millisecond, func() error {
		attempts++
		return refused()
	})
	if err == nil {
		t.Fatal("waitReadyUntil reported success although every attempt was refused")
	}
	if attempts < 2 {
		t.Errorf("the wait dialled %d times, want more than once before the bound ended it", attempts)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the wait ran for %s, want it to stop near its 50ms bound", elapsed)
	}
}
