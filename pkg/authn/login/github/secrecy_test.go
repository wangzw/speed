package github

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

type secrecyReader struct {
	cfg  moduleConfig
	gate gateConfig
	err  error
}

func (r secrecyReader) Decode(path string, target any) error {
	if r.err != nil {
		return r.err
	}
	switch path {
	case ConfigNamespace:
		if c, ok := target.(*moduleConfig); ok {
			*c = r.cfg
		}
	case gateNamespace:
		if g, ok := target.(*gateConfig); ok {
			*g = r.gate
		}
	}
	return nil
}

func gateWithMount(publicURL, basePath string) gateConfig {
	var g gateConfig
	g.Mount.PublicURL = publicURL
	g.Mount.Bases.Default = basePath
	return g
}

// TestAConfigurationErrorNeverRepeatsTheClientSecret holds the secret out of every configuration failure's text.
func TestAConfigurationErrorNeverRepeatsTheClientSecret(t *testing.T) {
	const secret = "s3cr3t-github-client-sentinel-9f8e7d6c"
	readerFailure := errors.New("the section cannot be read")
	for name, tc := range map[string]struct {
		reader config.Reader
		want   error
	}{
		"a client secret with no public base": {
			reader: secrecyReader{cfg: moduleConfig{ClientID: "the-id", ClientSecret: secret}},
			want:   authn.ErrInvalidConfig,
		},
		"a client secret with no base path": {
			reader: secrecyReader{
				cfg:  moduleConfig{ClientID: "the-id", ClientSecret: secret},
				gate: gateWithMount("https://app.example", ""),
			},
			want: authn.ErrInvalidConfig,
		},
		"a client secret with a reader that cannot read": {
			reader: secrecyReader{err: readerFailure},
			want:   readerFailure,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := readConfig(tc.reader)
			if !errors.Is(err, tc.want) {
				t.Fatalf("readConfig = %v, want one wrapping %v", err, tc.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the failure %q repeats the client secret", err)
			}
		})
	}
}
