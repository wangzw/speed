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

// TestTheSettingsReaderFallsBackToTheDeclaredDefault checks that the settings reader falls back to the declared default.
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

// TestTheOtherSettingsReadTheirDeclaredDefaults checks that the other settings read their declared defaults.
func TestTheOtherSettingsReadTheirDeclaredDefaults(t *testing.T) {
	r := quietReader(nil)
	if got := r.providerCacheTTL(); got != defaultProviderCache {
		t.Errorf("providerCacheTTL = %s, want %s", got, defaultProviderCache)
	}
	if got := r.egressTimeout(); got != defaultEgressTimeout {
		t.Errorf("egressTimeout = %s, want %s", got, defaultEgressTimeout)
	}
}
