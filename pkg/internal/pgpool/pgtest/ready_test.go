package pgtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vislake/speed/pkg/core/coretest"
)

// refused is the error a ping gets while nothing listens on the published port
// yet: the shape the driver returns, with the kernel's refusal under the
// connection error it wraps.
func refused() error {
	return fmt.Errorf("failed to connect: %w",
		&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)})
}

// timeout is a net.Error whose deadline expired, as a ping that ran out of time
// reports it.
type timeout struct{}

func (timeout) Error() string   { return "i/o timeout" }
func (timeout) Timeout() bool   { return true }
func (timeout) Temporary() bool { return true }

// TestOnlyConnectionFailuresAreRetried pins which ping failures count as "not
// ready yet". A container that has just started refuses or resets the first
// connection, and its server answers "the database system is starting up" until
// it finishes initialising; a wrong password, a database that does not exist or
// a malformed address answers the same way however long the wait runs, so the
// wait has to tell them apart instead of spending its whole bound on either.
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
		"the ping timed out":              {context.DeadlineExceeded, true},
		"the dial timed out":              {&net.OpError{Op: "dial", Net: "tcp", Err: timeout{}}, true},
		"the server is still starting": {
			fmt.Errorf("failed to connect: %w", &pgconn.PgError{Code: cannotConnectNow, Message: "the database system is starting up"}),
			true,
		},
		"the password is wrong": {
			fmt.Errorf("failed to connect: %w", &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}),
			false,
		},
		"the database does not exist": {
			fmt.Errorf("failed to connect: %w", &pgconn.PgError{Code: "3D000", Message: "database does not exist"}),
			false,
		},
		"the address has no port": {
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.AddrError{Err: "missing port in address"}},
			false,
		},
		"the host does not resolve": {
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "no.invalid", IsNotFound: true}},
			false,
		},
		"a failure of no known class": {errors.New("the ping failed"), false},
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
	err := waitForServerUntil(time.Now().Add(time.Second), time.Millisecond, func() error {
		attempts++
		if attempts < 3 {
			return refused()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("waitForServerUntil: %v, want the third attempt to be answered", err)
	}
	if attempts != 3 {
		t.Errorf("the wait pinged %d times, want 3: a refused connection is not retried until the server answers", attempts)
	}
}

// TestAFailureThatRetryingCannotClearEndsTheWaitAtOnce holds the wait to
// reporting a failure that retrying cannot clear, rather than spending the
// bound on it.
func TestAFailureThatRetryingCannotClearEndsTheWaitAtOnce(t *testing.T) {
	attempts := 0
	failure := &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}
	err := waitForServerUntil(time.Now().Add(time.Hour), time.Millisecond, func() error {
		attempts++
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("waitForServerUntil returned %v, want the authentication failure", err)
	}
	if attempts != 1 {
		t.Errorf("the wait pinged %d times, want 1: a failure that retrying cannot clear was retried", attempts)
	}
}

// TestTheWaitEndsAtItsBound holds the wait to the bound it documents: a
// connection failure that never clears is reported when the deadline passes,
// not retried forever.
func TestTheWaitEndsAtItsBound(t *testing.T) {
	attempts := 0
	start := time.Now()
	err := waitForServerUntil(time.Now().Add(50*time.Millisecond), time.Millisecond, func() error {
		attempts++
		return refused()
	})
	if err == nil {
		t.Fatal("waitForServerUntil reported success although every attempt was refused")
	}
	if attempts < 2 {
		t.Errorf("the wait pinged %d times, want more than once before the bound ended it", attempts)
	}
	if elapsed := time.Since(start); elapsed > coretest.Scale(time.Second) {
		t.Errorf("the wait ran for %s, want it to stop near its 50ms bound", elapsed)
	}
}
