package catalog

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/i18n"
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

// TestTheDefaultLocaleIsHeldToItsDomain holds the one-item configuration: a
// well-shaped default resolves, a value that names no BCP 47 language tag is
// refused as a configuration the module cannot run on, and the value decodes
// through.
func TestTheDefaultLocaleIsHeldToItsDomain(t *testing.T) {
	if _, err := (moduleConfig{DefaultLocale: "en-US"}).resolve(); err != nil {
		t.Errorf("the default was refused: %v", err)
	}
	// An empty value names no locale, so it is refused as well: a default that
	// resolves nowhere cannot be the fallback every message relies on.
	for _, bad := range []string{"", "en_US", "1234", "!"} {
		if _, err := (moduleConfig{DefaultLocale: bad}).resolve(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("resolve(%q) = %v, want ErrInvalidConfig", bad, err)
		}
	}

	got, err := readConfig(stubReader{cfg: moduleConfig{DefaultLocale: "ja-JP"}})
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if got.DefaultLocale != i18n.Locale("ja-JP") {
		t.Errorf("readConfig = %q, want ja-JP", got.DefaultLocale)
	}

	if _, err := readConfig(stubReader{err: errors.New("no config")}); err == nil {
		t.Error("readConfig returned nil for a failing decode")
	}
}

// TestTheConfigSchemaNamesTheDefaultItem holds the declaration: the namespace is
// the i18n namespace the SMS module also reads under, and the default-locale
// item is declared there.
func TestTheConfigSchemaNamesTheDefaultItem(t *testing.T) {
	s := schema()
	if s.Namespace != i18n.ConfigNamespace {
		t.Errorf("schema namespace = %q, want %q", s.Namespace, i18n.ConfigNamespace)
	}
	if _, ok := s.Items[i18n.DefaultLocaleItem]; !ok {
		t.Errorf("the schema declares no item %q", i18n.DefaultLocaleItem)
	}
}
