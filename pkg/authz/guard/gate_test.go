package guard

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
)

// A declaration is read back as the kind it was made: a required permission, or
// open. A route declared to require a permission is covered and hands its
// permission back; a route declared open is covered and reads open; a route
// never declared carries neither.
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

// A declaration is one kind or the other, and redeclaring a route flips it: a
// Require after an Open leaves only the requirement, an Open after a Require
// leaves only the open registration. The cleared half must be gone from its own
// read as well as from Covered, or the middleware would see a route that both
// requires a permission and is open, and the route's meaning would depend on
// which read it made first.
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

// verify holds every declared permission to the catalog: a declaration of a
// permission the catalog holds passes, and one the catalog does not hold fails
// with ErrUnknownPermission, naming the route and the permission. An open
// declaration names no permission and is never checked.
func TestGateVerifyHoldsPermissionsToTheCatalog(t *testing.T) {
	t.Run("a declared permission the catalog holds passes", func(t *testing.T) {
		g := newGate(authztest.NewCatalog(authz.PermissionAccountRead, authz.PermissionAuditRead))
		g.Require("GET", "/accounts", authz.PermissionAccountRead)
		g.Require("GET", "/audit", authz.PermissionAuditRead)
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

// A route never declared carries no declaration of either kind, and the key is
// the (method, path) pair: a method never declared on a declared path is as
// undeclared as a path never seen. A gate that covered everything, or that
// keyed a declaration by the path alone, would let a request through as
// declared when it is not.
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

// verify holds every declaration to the catalog, not just the first it meets. A
// gate with several in-catalog declarations and one outside the catalog reports
// the failure whichever way the map is ordered; a verify that stopped after the
// first declaration it saw would answer nil on most runs, when the out-of-catalog
// one happened not to be first. The check is repeated because the range order of
// a Go map varies run to run, so the case cannot be satisfied by luck; what it
// holds is that the loop visits all of them.
func TestGateVerifyChecksEveryDeclaration(t *testing.T) {
	inCatalog := make([]authz.Permission, 0, len(translation))
	for p := range translation {
		inCatalog = append(inCatalog, authz.Permission(p))
	}
	catalog := authztest.NewCatalog(inCatalog...)

	// The out-of-catalog permission sits among many in-catalog ones, so a verify
	// that only checked the first declaration would nearly always miss it.
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

// A declared permission the catalog holds but the translation cannot express
// fails verify the same way: the request path decides through the translation,
// so such a route would deny every request forever with no startup symptom. The
// check exists so the miss is a startup failure.
func TestGateVerifyHoldsPermissionsToTheTranslation(t *testing.T) {
	g := newGate(authztest.NewCatalog(authz.Permission("authz.guard.test.untranslatable")))
	g.Require("GET", "/x", authz.Permission("authz.guard.test.untranslatable"))

	err := g.verify()
	if !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("verify() = %v, want an error wrapping ErrUnknownPermission for a catalogued-but-untranslatable permission", err)
	}
	// The wording distinguishes this refusal from the catalog one: both wrap the
	// same sentinel, and the caller reads which check refused from the text.
	for _, want := range []string{"GET", "/x", "authz.guard.test.untranslatable", "translation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("verify()'s error %q does not name %q", err, want)
		}
	}
}

// The declaration table is written by modules in the Init window and read on the
// request path, so its reads and writes are taken under a lock. This case drives
// writers and readers against one gate at once: under -race, dropping the lock
// makes it fail, because concurrent access to its maps is then a data race. It
// asserts no value — what it holds is the absence of a race, which only the race
// detector can observe.
func TestTheGateIsSafeUnderConcurrentUse(t *testing.T) {
	g := newGate(authztest.NewCatalog(authz.PermissionAccountRead, authz.PermissionAuditRead))
	const rounds = 300
	var wg sync.WaitGroup

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				// The same keys on purpose: writes to one entry collide when the
				// lock is gone.
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
