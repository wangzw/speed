package sms

import (
	"bytes"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	out := &lockedBuffer{}
	prev := slog.Default()
	prevFlags, prevPrefix := log.Flags(), log.Prefix()
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})
	return out
}

func render(s authn.Step) string {
	var b strings.Builder
	fmt.Fprintf(&b, "kind=%s name=%q private=%q fields=%+v pass=%+v",
		step.KindOf(s), step.NameOf(s), step.PrivateOf(s), step.FieldsOf(s), step.PassOf(s))
	fmt.Fprintf(&b, " retry=%s cause=%v", step.RetryAfterOf(s), step.CauseOf(s))
	return b.String()
}

// TestTheCodeAndThePhoneReachNoLogAndNoAnswer checks that the code and the phone reach no log line and no answer.
func TestTheCodeAndThePhoneReachNoLogAndNoAnswer(t *testing.T) {
	logs := captureLogs(t)
	r := newRig()

	prompt, err := r.f.Challenge(t.Context(), flow(1, "", nil), record())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if got := render(prompt); strings.Contains(got, sentinelPhone) {
		t.Errorf("the challenge answer carries the phone: %s", got)
	}

	r.cs.verifyErr = authn.ErrInvalidCredentials
	refused, err := r.f.Verify(t.Context(), flow(0, "", step.PrivateOf(prompt)), record(), submit(map[string]string{fieldCode: sentinelCode}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := render(refused); strings.Contains(got, sentinelCode) || strings.Contains(got, sentinelPhone) {
		t.Errorf("the verification answer carries the code or the phone: %s", got)
	}
	if got := logs.String(); strings.Contains(got, sentinelCode) || strings.Contains(got, sentinelPhone) {
		t.Errorf("the code or the phone reached a log line:\n%s", got)
	}
}
