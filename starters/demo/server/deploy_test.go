//go:build deploy

package main

import (
	"testing"

	"github.com/vislake/speed/pkg/authn/emailcode"
	githublogin "github.com/vislake/speed/pkg/authn/login/github"
	googlelogin "github.com/vislake/speed/pkg/authn/login/google"
	"github.com/vislake/speed/pkg/mail/smtp"
)

func TestTheDeployAssemblyNamesTheMailAndSocialPaths(t *testing.T) {
	seen := make(map[string]bool, len(deployModules))
	for _, name := range deployModules {
		seen[name] = true
	}
	for _, name := range []string{smtp.ModuleName, emailcode.ModuleName, googlelogin.ModuleName, githublogin.ModuleName} {
		if !seen[name] {
			t.Errorf("the deploy assembly does not name %s", name)
		}
	}
}

// socialCredentials is the part of a social method's section these cases read:
// the OAuth application's identifier and secret.
type socialCredentials struct {
	ClientID     string `config:"client-id"`
	ClientSecret string `config:"client-secret"`
}

// TestTheDeployTemplateCarriesTheSocialCredentials holds the deployment
// template, rendered the way deploy/entrypoint.sh renders it, to the sections
// the two social login methods read. A method named in the assembly with an
// empty or misplaced credential fails the startup, so the placeholders have to
// land under the namespaces the methods decode: authn.login.google and
// authn.login.github.
func TestTheDeployTemplateCarriesTheSocialCredentials(t *testing.T) {
	locator := writeConfigFile(t, renderDeployTemplate(t))
	for namespace, want := range map[string]socialCredentials{
		googlelogin.ConfigNamespace: {
			ClientID:     "google-client.example.test",
			ClientSecret: "google-client-secret",
		},
		githublogin.ConfigNamespace: {
			ClientID:     "github-client",
			ClientSecret: "github-client-secret",
		},
	} {
		var got socialCredentials
		readEffective(t, "deploy/fly.yaml.tmpl", locator, namespace, &got)
		if got != want {
			t.Errorf("%s resolved to %+v, want %+v; the template's placeholder for that item is not "+
				"where the method reads it", namespace, got, want)
		}
	}
}
