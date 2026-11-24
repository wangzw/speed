package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func (r *rig) sendFrom(source, number string) (string, error) {
	return r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: number, Source: source, Deliver: true,
	})
}

// TestThePerSourceHourlyBudgetThrottles checks that the per-source hourly budget throttles.
func TestThePerSourceHourlyBudgetThrottles(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(perSourceHourlyPath, 2)

	const source = "203.0.113.7"
	for i := range 2 {
		if _, err := r.sendFrom(source, fmt.Sprintf("+86138002%05d", i)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := r.sendFrom(source, "+8613800299999"); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the third send from one source returned %v, want ErrThrottled", err)
	}
	if _, err := r.sendFrom("203.0.113.8", "+8613800288888"); err != nil {
		t.Fatalf("a send from another source was refused: %v", err)
	}
}

// TestThePerSourceDailyBudgetThrottles checks that the per-source daily budget throttles.
func TestThePerSourceDailyBudgetThrottles(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(perSourceDailyPath, 2)

	const source = "203.0.113.7"
	for i := range 2 {
		if _, err := r.sendFrom(source, fmt.Sprintf("+86138003%05d", i)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := r.sendFrom(source, "+8613800399999"); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the third send from one source within the day returned %v, want ErrThrottled", err)
	}
}

// TestAnEmptySourceIsFailOpen checks that an empty source is fail-open.
func TestAnEmptySourceIsFailOpen(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(perSourceHourlyPath, 1)
	r.store.set(perSourceDailyPath, 1)

	for i := range 3 {
		if _, err := r.loginTo(fmt.Sprintf("+86138004%05d", i)); err != nil {
			t.Fatalf("send %d with no source returned %v, want success", i, err)
		}
	}
}

// TestTheSourceAddressIsNormalisedBeforeCounting checks that the source address is normalized before counting.
func TestTheSourceAddressIsNormalisedBeforeCounting(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(perSourceHourlyPath, 1)

	if _, err := r.sendFrom("2001:db8::1", "+8613800500000"); err != nil {
		t.Fatalf("the first send: %v", err)
	}
	if _, err := r.sendFrom("2001:0db8:0000:0000:0000:0000:0000:0001", "+8613800599999"); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a second spelling of one address returned %v, want ErrThrottled", err)
	}
}

// TestTheSourceIsKeptOnlyAsADigest checks that the source is kept only as a digest.
func TestTheSourceIsKeptOnlyAsADigest(t *testing.T) {
	const source = "203.0.113.9"
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()

	id, err := r.sendFrom(source, phone)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	mac, ok := r.column(id, "source_mac")
	if !ok {
		t.Fatal("the send wrote no row")
	}
	got, isText := mac.(string)
	if !isText || got == "" {
		t.Fatalf("source_mac = %v, want the keyed digest", mac)
	}
	if got == source {
		t.Error("source_mac holds the address in plaintext")
	}
	if want := r.database.BlindIndex(source); got != want {
		t.Errorf("source_mac = %q, want the blind index of the address", got)
	}
	if text := r.allText(); strings.Contains(text, source) {
		t.Errorf("the table holds the address in plaintext: %s", text)
	}
	if logs := r.logs.String(); strings.Contains(logs, source) {
		t.Errorf("a log line carries the address:\n%s", logs)
	}

	plain, err := r.loginTo("+8613800599991")
	if err != nil {
		t.Fatalf("Send with no source: %v", err)
	}
	if v, ok := r.column(plain, "source_mac"); !ok || v != nil {
		t.Errorf("a send with no source wrote source_mac = %v, want NULL", v)
	}
}
