package sso

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

// TestAnUnsetResolverUsesTheSystemResolver checks that an unset resolver uses the system resolver.
func TestAnUnsetResolverUsesTheSystemResolver(t *testing.T) {
	reader := authntest.ConfigReaderAll(t, "{}", Module())
	cfg, err := readConfig(reader)
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	p := build(openSQLite(t), mounttest.New(t), cfg, reader)

	if got, want := reflect.ValueOf(p.lookup).Pointer(), reflect.ValueOf(systemLookup).Pointer(); got != want {
		t.Error("an unset resolver did not leave the system resolver in place")
	}
}

// TestAConfiguredResolverCarriesTheTXTLookup checks that a configured resolver carries the TXT lookup.
func TestAConfiguredResolverCarriesTheTXTLookup(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on a loopback UDP socket: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	body := fmt.Sprintf(`{"authn":{"sso":{"dns-resolver":%q}}}`, pc.LocalAddr().String())
	reader := authntest.ConfigReaderAll(t, body, Module())
	cfg, err := readConfig(reader)
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	p := build(openSQLite(t), mounttest.New(t), cfg, reader)

	if reflect.ValueOf(p.lookup).Pointer() == reflect.ValueOf(systemLookup).Pointer() {
		t.Fatal("a configured resolver left the system lookup in place")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _ = p.lookup(ctx, txtName("example.com"))

	if err = pc.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("setting the read deadline: %v", err)
	}
	buf := make([]byte, 1500)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("the configured resolver received no query: %v", err)
	}
	if n == 0 {
		t.Fatal("the configured resolver received an empty query")
	}
}

// TestAResolverAddressThatIsNotHostPortIsRefused checks that a resolver address that is not host:port is refused.
func TestAResolverAddressThatIsNotHostPortIsRefused(t *testing.T) {
	reader := authntest.ConfigReaderAll(t,
		`{"authn":{"sso":{"dns-resolver":"not-an-address"}}}`, Module())
	if _, err := readConfig(reader); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("readConfig = %v, want authn.ErrInvalidConfig", err)
	}
}
