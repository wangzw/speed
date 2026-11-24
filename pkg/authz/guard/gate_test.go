package guard

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/internal/translation"
)

// TestGateRecordsRequirementsAndOpenRoutes checks that gate records requirements and open routes.
func TestGateRecordsRequirementsAndOpenRoutes(t *testing.T) {
	g := newGate(authztest.NewCatalog(authz.PermissionAccountRead))
	g.Require("GET", "/accounts", authz.PermissionAccountRead)
	g.Open("GET", "/health")

	if perm, ok := g.requirement("GET", "/accounts"); !ok || perm != authz.PermissionAccountRead {
		t.Errorf("requirement(GET /accounts) = %q, %t; want the declared permission", perm, ok)
	}
	if !g.Covered("GET", "/accounts") {
		t.Error("Covered(GET /accounts) = false for a route that requires a permission")
	}
	if g.isOpen("GET", "/accounts") {
		t.Error("isOpen(GET /accounts) = true for a route that requires a permission")
	}

	if !g.isOpen("GET", "/health") {
		t.Error("isOpen(GET /health) = false for a route declared open")
	}
	if !g.Covered("GET", "/health") {
		t.Error("Covered(GET /health) = false for a route declared open")
	}
	if _, ok := g.requirement("GET", "/health"); ok {
		t.Error("requirement(GET /health) reports a requirement for a route declared open")
	}
}

// TestGateDeclarationIsOneOrTheOther checks that gate declaration is one or the other.
func TestGateDeclarationIsOneOrTheOther(t *testing.T) {
	t.Run("a Require drops the open registration of the same route", func(t *testing.T) {
		g := newGate(authztest.NewCatalog(authz.PermissionAccountRead))
		g.Open("GET", "/accounts")
		g.Require("GET", "/accounts", authz.PermissionAccountRead)

		if g.isOpen("GET", "/accounts") {
			t.Error("the open registration survived a Require on the same route")
		}
		if perm, ok := g.requirement("GET", "/accounts"); !ok || perm != authz.PermissionAccountRead {
			t.Errorf("requirement(GET /accounts) = %q, %t; want the declared permission", perm, ok)
		}
	})

	t.Run("an Open drops the requirement of the same route", func(t *testing.T) {
		g := newGate(authztest.NewCatalog(authz.PermissionAccountRead))
		g.Require("GET", "/accounts", authz.PermissionAccountRead)
		g.Open("GET", "/accounts")

		if _, ok := g.requirement("GET", "/accounts"); ok {
			t.Error("the requirement survived an Open on the same route")
		}
		if !g.isOpen("GET", "/accounts") {
			t.Error("isOpen(GET /accounts) = false after an Open on the same route")
		}
	})
}

// TestGateVerifyHoldsPermissionsToTheCatalog checks that gate verify holds permissions to the catalog.
func TestGateVerifyHoldsPermissionsToTheCatalog(t *testing.T) {
	t.Run("a declared permission the catalog holds passes", func(t *testing.T) {
		g := newGate(authztest.NewCatalog(authz.PermissionAccountRead, authz.PermissionAuditRead))
		g.Require("GET", "/accounts", authz.PermissionAccountRead)
		g.Require("GET", "/api/v1/audit", authz.PermissionAuditRead)
		g.Open("GET", "/health")

		if err := g.verify(); err != nil {
			t.Fatalf("verify() = %v, want nil for permissions the catalog holds", err)
		}
	})

	t.Run("a declared permission the catalog does not hold fails", func(t *testing.T) {
		g := newGate(authztest.NewCatalog(authz.PermissionAccountRead))
		g.Open("GET", "/health")
		g.Require("POST", "/accounts", authz.PermissionAccountCreate)

		err := g.verify()
		if !errors.Is(err, authz.ErrUnknownPermission) {
			t.Fatalf("verify() = %v, want an error wrapping ErrUnknownPermission", err)
		}
		for _, want := range []string{"POST", "/accounts", string(authz.PermissionAccountCreate)} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("verify()'s error %q does not name %q", err, want)
			}
		}
	})
}

// TestGateLeavesAnUndeclaredRouteUncovered checks that gate leaves an undeclared route uncovered.
func TestGateLeavesAnUndeclaredRouteUncovered(t *testing.T) {
	g := newGate(authztest.NewCatalog(authz.PermissionAccountRead))
	g.Require("GET", "/accounts", authz.PermissionAccountRead)
	g.Open("GET", "/health")

	if g.Covered("GET", "/undeclared") {
		t.Error("Covered(GET /undeclared) = true for a route never declared")
	}
	if _, ok := g.requirement("GET", "/undeclared"); ok {
		t.Error("requirement(GET /undeclared) reports a requirement for a route never declared")
	}
	if g.isOpen("GET", "/undeclared") {
		t.Error("isOpen(GET /undeclared) = true for a route never declared")
	}
	if g.Covered("POST", "/accounts") {
		t.Error("Covered(POST /accounts) = true for a method never declared on a declared path")
	}
}

// TestGateVerifyChecksEveryDeclaration checks that gate verify checks every declaration.
func TestGateVerifyChecksEveryDeclaration(t *testing.T) {
	inCatalog := translation.Builtin()
	catalog := authztest.NewCatalog(inCatalog...)

	for i := 0; i < 500; i++ {
		g := newGate(catalog)
		for _, p := range inCatalog {
			g.Require("GET", "/"+string(p), p)
		}
		g.Require("GET", "/outside", authz.Permission("authz.guard.test.outside"))

		if err := g.verify(); !errors.Is(err, authz.ErrUnknownPermission) {
			t.Fatalf("verify() = %v, want an error wrapping ErrUnknownPermission for the out-of-catalog declaration", err)
		}
	}
}

// TestGateVerifyHoldsPermissionsToTheTranslation checks that gate verify holds permissions to the translation.
func TestGateVerifyHoldsPermissionsToTheTranslation(t *testing.T) {
	g := newGate(authztest.NewCatalog(authz.Permission("authz.guard.test.untranslatable")))
	g.Require("GET", "/x", authz.Permission("authz.guard.test.untranslatable"))

	err := g.verify()
	if !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("verify() = %v, want an error wrapping ErrUnknownPermission for a catalogued-but-untranslatable permission", err)
	}
	for _, want := range []string{"GET", "/x", "authz.guard.test.untranslatable", "translation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("verify()'s error %q does not name %q", err, want)
		}
	}
}

// TestTheGateIsSafeUnderConcurrentUse checks that the gate is safe under concurrent use.
func TestTheGateIsSafeUnderConcurrentUse(t *testing.T) {
	g := newGate(authztest.NewCatalog(authz.PermissionAccountRead, authz.PermissionAuditRead))
	const rounds = 300
	var wg sync.WaitGroup

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				g.Require("GET", "/accounts", authz.PermissionAccountRead)
				g.Open("GET", "/health")
				g.Require("POST", "/accounts", authz.PermissionAuditRead)
			}
		}(w)
	}
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				_ = g.Covered("GET", "/accounts")
				_, _ = g.requirement("POST", "/accounts")
				_ = g.isOpen("GET", "/health")
			}
		}()
	}
	wg.Wait()
}
