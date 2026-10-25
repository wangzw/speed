package humanverify

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/kv"
)

// errStubKV is the fault a case arms the store with.
var errStubKV = errors.New("the key-value store is unavailable")

// stubKV is an in-memory kv.KV for the module's own tests. It keeps no expiry
// logic: the cases that exercise an expiry run at the provider's level over a
// clock-driven store.
type stubKV struct {
	mu     sync.Mutex
	m      map[string][]byte
	incErr error
	getErr error
	setErr error
	casErr error
}

func newStubKV() *stubKV { return &stubKV{m: map[string][]byte{}} }

var _ kv.KV = (*stubKV)(nil)

func (k *stubKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.getErr != nil {
		return nil, false, k.getErr
	}
	v, ok := k.m[key]
	return append([]byte(nil), v...), ok, nil
}

func (k *stubKV) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.setErr != nil {
		return k.setErr
	}
	k.m[key] = append([]byte(nil), value...)
	return nil
}

func (k *stubKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	return nil
}

func (k *stubKV) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return k.IncrByFloatWithTTL(ctx, key, delta, 0)
}

func (k *stubKV) IncrByFloatWithTTL(_ context.Context, key string, delta float64, _ time.Duration) (float64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.incErr != nil {
		return 0, k.incErr
	}
	var current float64
	if v, ok := k.m[key]; ok {
		current, _ = strconv.ParseFloat(string(v), 64)
	}
	current += delta
	k.m[key] = []byte(strconv.FormatFloat(current, 'g', -1, 64))
	return current, nil
}

func (k *stubKV) CompareAndSwap(_ context.Context, key string, old, newVal []byte) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.casErr != nil {
		return false, k.casErr
	}
	if string(k.m[key]) != string(old) {
		return false, nil
	}
	k.m[key] = append([]byte(nil), newVal...)
	return true, nil
}

func (k *stubKV) SetIfAbsent(_ context.Context, key string, value []byte, _ time.Duration) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.m[key]; ok {
		return false, nil
	}
	k.m[key] = append([]byte(nil), value...)
	return true, nil
}
