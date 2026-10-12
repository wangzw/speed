package saml

import (
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// fakeStore is a settings store whose values a case chooses, keyed by the full
// path; a key it does not hold reads as undeclared. It embeds the interface so
// that only Get, which is all the reader uses, has to be written.
type fakeStore struct {
	settings.Store
	values map[string]string
}

func (s *fakeStore) Get(_ settings.Layer, key string, target any) error {
	v, ok := s.values[key]
	if !ok {
		return errors.New("undeclared")
	}
	switch p := target.(type) {
	case *time.Duration:
		d, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		*p = d
	case *int:
		n, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		*p = n
	case *string:
		*p = v
	}
	return nil
}

func quietReader(store settings.Store) *reader {
	return newReader(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestTheSettingsReaderFallsBackToTheDeclaredDefault(t *testing.T) {
	cases := []struct {
		name  string
		store settings.Store
		want  time.Duration
	}{
		{"no store", nil, defaultAssertionMaxAge},
		{"undeclared", &fakeStore{values: map[string]string{}}, defaultAssertionMaxAge},
		{"outside the domain", &fakeStore{values: map[string]string{assertionMaxAgePath: "1h"}}, defaultAssertionMaxAge},
		{"in the domain", &fakeStore{values: map[string]string{assertionMaxAgePath: "3m"}}, 3 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quietReader(tc.store).assertionMaxAge(); got != tc.want {
				t.Errorf("assertionMaxAge = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTheOtherSettingsReadTheirDeclaredDefaults(t *testing.T) {
	r := quietReader(nil)
	if got := r.providerCacheTTL(); got != defaultProviderCache {
		t.Errorf("providerCacheTTL = %s, want %s", got, defaultProviderCache)
	}
	if got := r.egressTimeout(); got != defaultEgressTimeout {
		t.Errorf("egressTimeout = %s, want %s", got, defaultEgressTimeout)
	}
}
