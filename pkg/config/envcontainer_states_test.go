package config

import (
	"errors"
	"strings"
	"testing"
)

type envTaggedMember struct {
	Wire string `config:"wire-name" json:"json-name"`
}

type envTaggedOptions struct {
	Members []envTaggedMember
}

func envTaggedLoader(t *testing.T) *loader {
	t.Helper()
	defaults := envTaggedOptions{}
	return loadOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("server", Schema{
			Namespace: "server",
			Mounts:    []Mount{{Value: &defaults}},
			Items: map[string]Item{
				"members": {Origins: OriginPrimary | OriginEnv},
			},
		}),
	)
}

// TestContainerJSONRejectsARepeatedKeyAtAnyDepth checks that container JSON rejects a repeated key at any depth.
func TestContainerJSONRejectsARepeatedKeyAtAnyDepth(t *testing.T) {
	t.Setenv("MYAPP_SERVER__SEEDS", `[{"email":"ada@example.com","email":"grace@example.com"}]`)
	l := envContainerLoader(t, envContainerOptions{}, map[string]Item{
		"seeds": {Origins: OriginPrimary | OriginEnv},
	})
	var opts envContainerOptions
	err := l.Reader().Decode("server", &opts)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("a repeated key inside a list element returned %v, want ErrTypeMismatch", err)
	}
	for _, want := range []string{"MYAPP_SERVER__SEEDS", "server.seeds", "email"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the rejection reads %q, which does not name %q", err, want)
		}
	}
}

// TestContainerMemberNamesAnswerToTheConfigTagFirst checks that container member names answer to the config tag first.
func TestContainerMemberNamesAnswerToTheConfigTagFirst(t *testing.T) {
	t.Run("the config tag names the member", func(t *testing.T) {
		t.Setenv("MYAPP_SERVER__MEMBERS", `[{"wire-name":"x"}]`)
		l := envTaggedLoader(t)
		var opts envTaggedOptions
		if err := l.Reader().Decode("server", &opts); err != nil {
			t.Fatalf("decoding the config-tag member failed: %v", err)
		}
		if len(opts.Members) != 1 || opts.Members[0].Wire != "x" {
			t.Fatalf("server.members decoded to %+v, want one member with x", opts.Members)
		}
	})

	t.Run("the json tag is not a second name", func(t *testing.T) {
		t.Setenv("MYAPP_SERVER__MEMBERS", `[{"json-name":"x"}]`)
		l := envTaggedLoader(t)
		var opts envTaggedOptions
		err := l.Reader().Decode("server", &opts)
		if !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("a member named only by the json tag returned %v, want ErrTypeMismatch", err)
		}
		if !strings.Contains(err.Error(), "json-name") {
			t.Fatalf("the rejection reads %q, which does not name the offending member", err)
		}
	})
}
