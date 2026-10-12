package sms

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// stubReader is a configuration reader a case scripts.
type stubReader struct {
	cfg moduleConfig
	err error
}

func (s stubReader) Decode(_ string, target any) error {
	if s.err != nil {
		return s.err
	}
	*target.(*moduleConfig) = s.cfg
	return nil
}

var _ config.Reader = stubReader{}

// TestThePoolSizingIsHeldToItsDomain holds the two-item configuration: the
// defaults resolve, zero workers and a queue below one slot are refused as a
// configuration the module cannot run on, and the values decode through.
func TestThePoolSizingIsHeldToItsDomain(t *testing.T) {
	if _, err := (moduleConfig{Workers: 4, QueueSize: 1024}).resolve(); err != nil {
		t.Errorf("the defaults were refused: %v", err)
	}
	if _, err := (moduleConfig{Workers: 0, QueueSize: 10}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("zero workers resolved to %v, want ErrInvalidConfig", err)
	}
	if _, err := (moduleConfig{Workers: 1, QueueSize: -1}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("a negative queue resolved to %v, want ErrInvalidConfig", err)
	}
	// A zero-capacity queue refuses every send before it writes a row, so it is
	// not a configuration the module can run on.
	if _, err := (moduleConfig{Workers: 1, QueueSize: 0}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("a zero queue resolved to %v, want ErrInvalidConfig", err)
	}

	got, err := readConfig(stubReader{cfg: moduleConfig{Workers: 2, QueueSize: 8}})
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if got.Workers != 2 || got.QueueSize != 8 {
		t.Errorf("readConfig = %+v, want workers 2, queue 8", got)
	}

	if _, err := readConfig(stubReader{err: errors.New("no config")}); err == nil {
		t.Error("readConfig returned nil for a failing decode")
	}
}

// TestTheConfigSchemaNamesTheTwoItems holds the declaration: the namespace is the
// module name and the two items are declared.
func TestTheConfigSchemaNamesTheTwoItems(t *testing.T) {
	s := schema()
	if s.Namespace != ModuleName {
		t.Errorf("schema namespace = %q, want %q", s.Namespace, ModuleName)
	}
	for _, key := range []string{workersItem, queueSizeItem} {
		if _, ok := s.Items[key]; !ok {
			t.Errorf("the schema declares no item %q", key)
		}
	}
}
