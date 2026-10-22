//go:build deploy

package main

import (
	"testing"

	"github.com/vislake/speed/pkg/authn/emailcode"
	"github.com/vislake/speed/pkg/mail/smtp"
)

func TestTheDeployAssemblyAddsTheMailPath(t *testing.T) {
	seen := make(map[string]bool, len(deployModules))
	for _, name := range deployModules {
		seen[name] = true
	}
	for _, name := range []string{smtp.ModuleName, emailcode.ModuleName} {
		if !seen[name] {
			t.Errorf("the deploy assembly does not name %s", name)
		}
	}
}
