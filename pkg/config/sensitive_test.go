package config

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

type secretOptions struct {
	Pin      int
	Upstream netip.Addr
	Tokens   map[string]int
	Debug    bool
}

const secret = "hunter2-secret"

func secretLoader(t *testing.T) *loader {
	t.Helper()
	return loadOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("vault", Schema{
			Namespace: "vault",
			Mounts:    []Mount{{Value: &secretOptions{}}},
			Items: map[string]Item{
				"pin":      {Sensitive: true, Description: "the unlock PIN"},
				"upstream": {Sensitive: true, Description: "the upstream address"},
				"tokens": {
					Origins:   OriginPrimary | OriginEnv,
					Sensitive: true, Description: "the token quotas",
				},
				"debug": {
					Origins: OriginFlag, FlagName: "vault-debug",
					Sensitive: true, Description: "whether the vault logs its secrets",
				},
			},
		}),
	)
}

// TestASensitiveValueStaysOutOfItsFailure checks that a sensitive value stays out of its failure.
func TestASensitiveValueStaysOutOfItsFailure(t *testing.T) {
	check := func(t *testing.T, err error, class error) {
		t.Helper()
		if !errors.Is(err, class) {
			t.Fatalf("the conversion returned %v, want %v", err, class)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("the failure reads %q, which repeats the sensitive value", err)
		}
		if !strings.Contains(err.Error(), "vault") {
			t.Errorf("the failure reads %q, which does not name the item", err)
		}
	}

	t.Run("a number from the environment", func(t *testing.T) {
		t.Setenv("MYAPP_VAULT__PIN", secret)
		var opts secretOptions
		check(t, secretLoader(t).Reader().Decode("vault", &opts), ErrTypeMismatch)
	})
	t.Run("a type that reads itself from text", func(t *testing.T) {
		t.Setenv("MYAPP_VAULT__UPSTREAM", secret)
		var opts secretOptions
		check(t, secretLoader(t).Reader().Decode("vault", &opts), ErrTypeMismatch)
	})
	t.Run("a container from the primary source", func(t *testing.T) {
		l := secretLoader(t)
		if err := givePrimary(t, l, map[string]any{
			"vault": map[string]any{"tokens": map[string]any{"api": secret}},
		}); err != nil {
			t.Fatalf("giving the primary source failed: %v", err)
		}
		var opts secretOptions
		check(t, l.Reader().Decode("vault", &opts), ErrTypeMismatch)
	})
	t.Run("a boolean on the command line", func(t *testing.T) {
		check(t, giveFlags(t, secretLoader(t), []string{"vault"}, "--vault-debug="+secret), ErrTypeMismatch)
	})
}

func sensitiveReporter(t *testing.T) (*loader, *strings.Builder) {
	t.Helper()
	stderr := &strings.Builder{}
	reg := core.New()
	reg.Register(identifying("host", HostIdentity{Prefix: "MYAPP"}))
	reg.Register(declaring("vault", Schema{
		Namespace: "vault",
		Mounts:    []Mount{{Value: &secretOptions{}}},
		Items: map[string]Item{
			"pin":      {Origins: OriginPrimary | OriginEnv, Sensitive: true, Description: "the unlock PIN"},
			"upstream": {Origins: OriginPrimary | OriginEnv, Sensitive: true, Description: "the upstream address"},
			"tokens":   {Origins: OriginPrimary | OriginEnv, Sensitive: true, Description: "the token quotas"},
		},
	}))
	l, err := newLoader(reg, stderr)
	if err != nil {
		t.Fatalf("collecting the declarations failed: %v", err)
	}
	return l, stderr
}

// TestASensitiveContainerFromTheEnvironmentIsDecoded checks that a sensitive container from the environment is decoded.
func TestASensitiveContainerFromTheEnvironmentIsDecoded(t *testing.T) {
	t.Setenv("MYAPP_VAULT__TOKENS", `{"api":7}`)
	var opts secretOptions
	if err := secretLoader(t).Reader().Decode("vault", &opts); err != nil {
		t.Fatalf("decoding a sensitive container from the environment failed: %v", err)
	}
	if opts.Tokens["api"] != 7 {
		t.Fatalf("vault.tokens decoded to %#v, want the value the variable gave", opts.Tokens)
	}
}

// TestSensitiveItemsReportTheirSourceAndNotTheirValue checks that sensitive items report their source and not their value.
func TestSensitiveItemsReportTheirSourceAndNotTheirValue(t *testing.T) {
	t.Setenv("MYAPP_VAULT__PIN", "424242")
	t.Setenv("MYAPP_VAULT__TOKENS", `{"api":7}`)
	l, stderr := sensitiveReporter(t)
	l.ReportSensitive()
	report := stderr.String()
	for _, want := range []string{
		`sensitive item "vault.pin" takes its value from the environment variable MYAPP_VAULT__PIN`,
		`sensitive item "vault.tokens" takes its value from the environment variable MYAPP_VAULT__TOKENS`,
		`sensitive item "vault.upstream" takes its value from its declared default`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("the report reads %q, which does not say %q", report, want)
		}
	}
	for _, value := range []string{"424242", `{"api":7}`, "api"} {
		if strings.Contains(report, value) {
			t.Fatalf("the report reads %q, and it repeats the sensitive value %q", report, value)
		}
	}
}

// TestSensitiveItemReportNamesThePrimarySource checks that sensitive item report names the primary source.
func TestSensitiveItemReportNamesThePrimarySource(t *testing.T) {
	l, stderr := sensitiveReporter(t)
	givePrimaryWhole(t, l, map[string]any{
		"vault": map[string]any{"tokens": map[string]any{"api": 7}},
	})
	l.ReportSensitive()
	report := stderr.String()
	if want := `sensitive item "vault.tokens" takes its value from the primary config source`; !strings.Contains(report, want) {
		t.Fatalf("the report reads %q, which does not say %q", report, want)
	}
	if strings.Contains(report, "api") {
		t.Fatalf("the report reads %q, and it repeats the sensitive value's key", report)
	}
}

// TestLoadReportsSensitiveSources checks that load reports sensitive sources.
func TestLoadReportsSensitiveSources(t *testing.T) {
	t.Setenv("MYAPP_VAULT__TOKENS", `{"api":7}`)
	s := newLoaderSetup(t, HostIdentity{Prefix: "MYAPP"}, nil,
		declaring("vault", Schema{
			Namespace: "vault",
			Mounts:    []Mount{{Value: &secretOptions{}}},
			Items: map[string]Item{
				"tokens": {
					Origins:   OriginPrimary | OriginEnv,
					Sensitive: true, Description: "the token quotas",
				},
			},
		}),
	)
	if _, err := s.load(t, nil); err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	report := s.stderr.String()
	if want := `sensitive item "vault.tokens" takes its value from the environment variable MYAPP_VAULT__TOKENS`; !strings.Contains(report, want) {
		t.Fatalf("the diagnostics read %q, which does not say %q", report, want)
	}
	if strings.Contains(report, "api") {
		t.Fatalf("the diagnostics read %q, and they repeat the sensitive value", report)
	}
}

// TestAnItemThatIsNotSensitiveKeepsItsValueInTheFailure checks that an item that is not sensitive keeps its value in the failure.
func TestAnItemThatIsNotSensitiveKeepsItsValueInTheFailure(t *testing.T) {
	t.Setenv("MYAPP_SERVER__RETRIES", "three")
	var opts envOptions
	err := envLoader(t, "MYAPP").Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("the conversion returned %v, want ErrTypeMismatch", err)
	}
	if !strings.Contains(err.Error(), "three") {
		t.Errorf("the failure reads %q, which does not say what was given", err)
	}
}
