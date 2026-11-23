package saml

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	configyaml "github.com/vislake/speed/pkg/config/format/yaml"
	configfile "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

func TestOnlyTheKeyFileItemIsSensitive(t *testing.T) {
	s := configSchema()
	key, ok := s.Items[spKeyFileItem]
	if !ok {
		t.Fatalf("the schema does not declare %s", spKeyFileItem)
	}
	if !key.Sensitive {
		t.Errorf("the %s item is not marked sensitive", spKeyFileItem)
	}
	for _, name := range []string{spCertFileItem, spNextCertFileItem} {
		item, ok := s.Items[name]
		if !ok {
			t.Fatalf("the schema does not declare %s", name)
		}
		if item.Sensitive {
			t.Errorf("the %s item is marked sensitive, want the certificate published and not secret", name)
		}
	}
}

// TestTheSchemaDeclaresTheEnableSwitch holds the switch in the schema and off by
// default, which is what makes a deployment that never names it serve nothing.
func TestTheSchemaDeclaresTheEnableSwitch(t *testing.T) {
	if _, ok := configSchema().Items[enabledItem]; !ok {
		t.Fatalf("the schema does not declare %s", enabledItem)
	}
	if configDefaults.Enabled {
		t.Error("the declared default enables the module; a deployment that never names it must serve nothing")
	}
}

// TestTheKeyFileIsReadBesideTheEnableSwitch holds the cross-namespace read the
// enterprise provider register makes. It decodes sp-key-file out of
// authn.sso.saml with the real configuration machinery while the same section
// also carries the enable switch: an unknown key that broke the decode would be
// read by the register as "no service provider key" and silently swallow the
// deployment's signing setup.
func TestTheKeyFileIsReadBesideTheEnableSwitch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "authn.sso.saml:\n  enabled: true\n  sp-key-file: /tmp/sp-key.pem\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(configyaml.Module())
	reg.Register(configfile.Module())
	reg.Register(Module())
	reader, err := config.Load(context.Background(), reg, []string{"--config", "file://" + path})
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	// The register's own view of the section: the key file alone, decoded from a
	// section that also declares enabled.
	var key struct {
		SPKeyFile string `config:"sp-key-file"`
	}
	if err := reader.Decode(ConfigNamespace, &key); err != nil {
		t.Fatalf("decoding the service provider key from the section carrying the switch: %v", err)
	}
	if key.SPKeyFile != "/tmp/sp-key.pem" {
		t.Errorf("the decoded key file = %q, want the configured path: the enable switch beside it "+
			"must not turn the section into one the register reads as unconfigured", key.SPKeyFile)
	}
}
