package config

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// secretOptions carries one sensitive item of every shape a failed conversion
// can quote: a number read from text, a type that reads itself from text, a
// container, and a boolean argument.
type secretOptions struct {
	Pin      int
	Upstream netip.Addr
	Tokens   map[string]int
	Debug    bool
}

// secret is the text every case gives, and the text no failure may carry.
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
				"tokens":   {Sensitive: true, Description: "the token quotas"},
				"debug": {
					Origins: OriginFlag, FlagName: "vault-debug",
					Sensitive: true, Description: "whether the vault logs its secrets",
				},
			},
		}),
	)
}

// TestASensitiveValueStaysOutOfItsFailure pins that a value an item marks
// sensitive is not written into the error its conversion fails with. That
// error travels up through the stage that decoded it and is printed to
// standard error by the command-line library, which makes it one of the
// diagnostics a sensitive item is kept out of: a mistyped credential would
// otherwise land in the startup log of every failed deploy.
//
// The failure still names the item and says it did not convert, since that is
// what the operator has to fix.
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

// TestAnItemThatIsNotSensitiveKeepsItsValueInTheFailure is the other half:
// the value is what tells the operator which of their settings is wrong, and
// only a sensitive item gives that up.
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
