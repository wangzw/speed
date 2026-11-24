package catalog

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/i18n"
)

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

// TestTheDefaultLocaleIsHeldToItsDomain checks that a well-shaped default
// resolves and a value naming no BCP 47 tag is refused.
func TestTheDefaultLocaleIsHeldToItsDomain(t *testing.T) {
	if _, err := (moduleConfig{DefaultLocale: "en-US"}).resolve(); err != nil {
		t.Errorf("the default was refused: %v", err)
	}
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

// TestTheConfigSchemaNamesTheDefaultItem checks that the schema declares the
// default-locale item under the i18n namespace.
func TestTheConfigSchemaNamesTheDefaultItem(t *testing.T) {
	s := schema()
	if s.Namespace != i18n.ConfigNamespace {
		t.Errorf("schema namespace = %q, want %q", s.Namespace, i18n.ConfigNamespace)
	}
	if _, ok := s.Items[i18n.DefaultLocaleItem]; !ok {
		t.Errorf("the schema declares no item %q", i18n.DefaultLocaleItem)
	}
}

// TestTheCatalogsDirItemIsDeclaredAndCarried checks that the schema declares
// catalogs-dir and resolve carries its value unchanged.
func TestTheCatalogsDirItemIsDeclaredAndCarried(t *testing.T) {
	s := schema()
	if _, ok := s.Items[catalogsDirItem]; !ok {
		t.Errorf("the schema declares no item %q", catalogsDirItem)
	}
	got, err := readConfig(stubReader{cfg: moduleConfig{DefaultLocale: "en-US", CatalogsDir: "/no/such/dir"}})
	if err != nil {
		t.Fatalf("readConfig refused a directory that names nothing: %v", err)
	}
	if got.CatalogsDir != "/no/such/dir" {
		t.Errorf("CatalogsDir = %q, want %q", got.CatalogsDir, "/no/such/dir")
	}
}
