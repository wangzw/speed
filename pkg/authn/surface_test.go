package authn

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// exportedSurface is the root package's exported surface, written out, one
// entry per declaration a dependant can see and so one this module has to keep.
// The aliases are included with what they carry: an alias's fields, and the
// method set of the type behind it, are on the surface even though the source
// of this package declares none of them, which is why the list is read from the
// type checker's view and not from this package's syntax.
//
// The list is here rather than derived because deriving it would only restate
// whatever the code happens to say. Pinned, a new exported symbol has to be
// added by hand, and the entry is where the reader asks whether it belongs on
// the surface at all.
var exportedSurface = []string{
	"const AccessOpen",
	"const AccessOptional",
	"const AccessRequired",
	"const AccessUnknown",
	"const AssuranceMulti",
	"const AssuranceNone",
	"const AssuranceSingle",
	"const FactorActive",
	"const FactorDisabled",
	"const FactorPending",
	"const FactorSMS",
	"const FactorTOTP",
	"const FactorWebAuthn",
	"const FieldCode",
	"const FieldPassword",
	"const FieldPhone",
	"const FieldText",
	"const IdentifierEmail",
	"const IdentifierPhone",
	"const IdentifierUsername",
	"const MethodClientSecret",
	"const MethodOAuth",
	"const MethodOIDC",
	"const MethodPassword",
	"const MethodSAML",
	"const MethodSMS",
	"const MethodStatic",
	"const MethodTOTP",
	"const MethodWebAuthn",
	"const ScopeAccount",
	"const ScopeClient",
	"const ScopeFactor",
	"const ScopeIdentifier",
	"const ScopePhone",
	"const StaticPrefix",
	"field Accepts.Accounts",
	"field Accepts.Cookies",
	"field Accepts.Impersonation",
	"field Accepts.SSO",
	"field Accepts.Schemes",
	"field Credential.Cookie",
	"field Credential.Peer",
	"field Credential.Scheme",
	"field Credential.Value",
	"field External.Attributes",
	"field External.DisplayName",
	"field External.Email",
	"field External.EmailVerified",
	"field External.Provider",
	"field External.Subject",
	"field External.Tenant",
	"field ExternalLink.Email",
	"field ExternalLink.EmailVerified",
	"field ExternalLink.Provider",
	"field ExternalLink.Subject",
	"field FactorRecord.Account",
	"field FactorRecord.ActivatedAt",
	"field FactorRecord.CreatedAt",
	"field FactorRecord.ID",
	"field FactorRecord.Kind",
	"field FactorRecord.Label",
	"field FactorRecord.LastUsedAt",
	"field FactorRecord.Status",
	"field Field.Kind",
	"field Field.Name",
	"field Field.Required",
	"field Field.Secret",
	"field Identifier.ID",
	"field Identifier.Kind",
	"field Identifier.Value",
	"field Identifier.VerifiedAt",
	"field Identifier.VerifiedVia",
	"field Pass.Methods",
	"field Pass.UserVerified",
	"field Principal.Account",
	"field Principal.Actor",
	"field Principal.Assurance",
	"field Principal.Attributes",
	"field Principal.AuthTime",
	"field Principal.IDPAssured",
	"field Principal.Methods",
	"field Principal.Provider",
	"field Principal.Scope",
	"field Principal.Session",
	"field Proof.Account",
	"field Proof.Aliases",
	"field Proof.External",
	"field Proof.IDPAsserted",
	"field Proof.Identifier",
	"field Proof.Methods",
	"field Proof.Provider",
	"field Proof.UserVerified",
	"field Requirement.Assurance",
	"field Requirement.Local",
	"field Requirement.MaxAge",
	"field ThrottledError.RetryAfter",
	"func AccessFromContext",
	"func Authenticated",
	"func Denied",
	"func FromContext",
	"func ParseAccountID",
	"func StepData",
	"func StepEnrolled",
	"func StepPassed",
	"func StepPrompt",
	"func StepProven",
	"func StepRedirect",
	"func StepRefused",
	"func StepThrottled",
	"func StepUnavailable",
	"func Unavailable",
	"method AccountID.IsStatic",
	"method AccountID.String",
	"method Challenger.Challenge",
	"method CookieClearer.ClearCookie",
	"method External.Clone",
	"method Gate.Exempt",
	"method Gate.Impersonable",
	"method Gate.Optional",
	"method Identity.Account",
	"method Identity.Actor",
	"method Identity.Assurance",
	"method Identity.AssuranceFromIDP",
	"method Identity.Attribute",
	"method Identity.Attributes",
	"method Identity.AuthTime",
	"method Identity.Backend",
	"method Identity.Challenge",
	"method Identity.Impersonating",
	"method Identity.Key",
	"method Identity.Methods",
	"method Identity.Provider",
	"method Identity.Satisfies",
	"method Identity.Scope",
	"method Identity.Session",
	"method Method.Valid",
	"method Pass.Clone",
	"method Principal.Clone",
	"method Proof.Clone",
	"method ScopeGuard.Covers",
	"method Step.WithSends",
	"method Throttle.Check",
	"method Throttle.Fail",
	"method Throttle.Succeed",
	"method ThrottledError.Error",
	"method ThrottledError.Is",
	"method Verifier.Accepts",
	"method Verifier.Name",
	"method Verifier.Verify",
	"type Accepts",
	"type Access",
	"type AccountID",
	"type Assurance",
	"type Challenger",
	"type CookieClearer",
	"type Credential",
	"type External",
	"type ExternalLink",
	"type FactorKind",
	"type FactorRecord",
	"type FactorStatus",
	"type Field",
	"type Gate",
	"type Identifier",
	"type IdentifierKind",
	"type Identity",
	"type Method",
	"type Pass",
	"type Principal",
	"type Proof",
	"type Requirement",
	"type ScopeGuard",
	"type SessionID",
	"type Step",
	"type Throttle",
	"type ThrottleScope",
	"type ThrottledError",
	"type Verdict",
	"type Verifier",
	"var ErrAccessConflict",
	"var ErrBusy",
	"var ErrClaimConflict",
	"var ErrDrainTimeout",
	"var ErrExemptionUnguarded",
	"var ErrInsecureConfig",
	"var ErrInvalidAccountID",
	"var ErrInvalidClaim",
	"var ErrInvalidConfig",
	"var ErrNoRouter",
	"var ErrNoVerifier",
	"var ErrRoutesUnconfigured",
	"var ErrRoutesUnguarded",
	"var ErrThrottled",
	"var ErrUnknownEndpoint",
}

// authntestSurface is the exported surface of pkg/authn/authntest, pinned the
// same way and for the same reason: the package is what other modules' tests
// build an identity with, so an entry added to it is a way of building one that
// the gateway's own checks are then asked to match, and an entry removed breaks
// every test that calls it. It has no type with methods or fields to list, only
// the option constructors, the two context builders, the configuration reader
// builders and the defaults a test leaves unsaid.
var authntestSurface = []string{
	"const DefaultAccount",
	"const DefaultBackend",
	"const DefaultChallenge",
	"func Anonymous",
	"func ConfigReader",
	"func ConfigReaderAll",
	"func Context",
	"func DefaultAuthTime",
	"func WithAccess",
	"func WithAccount",
	"func WithActor",
	"func WithAssurance",
	"func WithAttributes",
	"func WithAuthTime",
	"func WithBackend",
	"func WithChallenge",
	"func WithIDPAssured",
	"func WithMethods",
	"func WithProvider",
	"func WithScope",
	"func WithSession",
	"type Option",
}

// forbiddenNames are names the surface may never carry, whatever the kind:
// the ownership model has no place in it, and the readers of a verdict and of an
// identity's internals are functions of the internal packages and not part of
// this one.
var forbiddenNames = []string{
	"Owner", "Subject", "Separator", "Claim", "OutcomeOf", "PrincipalOf", "CauseOf",
}

// forbiddenExceptions are entries that carry a forbidden name and are not what
// the name forbids. External.Subject and ExternalLink.Subject are an external
// provider's own identifier for the person, the "sub" of an OIDC token, which
// has nothing to do with the subject space of a credential backend that an
// Identity.Subject would have read.
var forbiddenExceptions = []string{
	"field External.Subject", "field ExternalLink.Subject",
}

// surfaceOf type-checks the non-test files of the package in dir and renders its
// exported surface, one entry per declaration.
//
// A package-level name gives one entry by its kind. A named type, or an alias
// of one, gives in addition one entry per exported field of a struct and one
// per exported method of the method set of its pointer, which for an alias is
// the method set of the type it names. Embedding is flattened, because what a
// dependant can call is what the method set says.
func surfaceOf(dir string) ([]string, error) {
	bp, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range bp.GoFiles {
		f, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, perr)
		}
		files = append(files, f)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(bp.Name, fset, files, nil)
	if err != nil {
		return nil, fmt.Errorf("type-checking %s: %w", dir, err)
	}

	var out []string
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.Const:
			out = append(out, "const "+name)
		case *types.Var:
			out = append(out, "var "+name)
		case *types.Func:
			out = append(out, "func "+name)
		case *types.TypeName:
			out = append(out, "type "+name)
			out = append(out, membersOf(name, o.Type())...)
		}
	}
	slices.Sort(out)
	return out, nil
}

// membersOf renders the exported fields and methods of a named type.
func membersOf(name string, typ types.Type) []string {
	typ = types.Unalias(typ)
	var out []string
	if st, ok := typ.Underlying().(*types.Struct); ok {
		for f := range st.Fields() {
			if f.Exported() {
				out = append(out, "field "+name+"."+f.Name())
			}
		}
	}
	recv := typ
	if _, isInterface := typ.Underlying().(*types.Interface); !isInterface {
		recv = types.NewPointer(typ)
	}
	ms := types.NewMethodSet(recv)
	for sel := range ms.Methods() {
		if sel.Obj().Exported() {
			out = append(out, "method "+name+"."+sel.Obj().Name())
		}
	}
	return out
}

// surfaceDiff reports, in both directions, how got and want differ: an entry in
// the source and not in the pinned list, and one the list names that the source
// no longer has.
func surfaceDiff(got, want []string) []string {
	var out []string
	for _, entry := range got {
		if !slices.Contains(want, entry) {
			out = append(out, fmt.Sprintf("the source exports %q, which the pinned surface does not list. "+
				"Add it to exportedSurface only after deciding it belongs on the surface", entry))
		}
	}
	for _, entry := range want {
		if !slices.Contains(got, entry) {
			out = append(out, fmt.Sprintf("the pinned surface lists %q, which the source no longer exports. "+
				"Removing an entry is a breaking change for every dependant", entry))
		}
	}
	return out
}

// forbiddenIn reports the entries whose name is one of forbiddenNames.
func forbiddenIn(entries []string) []string {
	var out []string
	for _, e := range entries {
		name := e[strings.LastIndexAny(e, ". ")+1:]
		if slices.Contains(forbiddenNames, name) && !slices.Contains(forbiddenExceptions, e) {
			out = append(out, e)
		}
	}
	return out
}

// rootSurface is the package's own surface, computed once: type-checking it
// type-checks the standard library packages it imports from source, which is
// the slow part, and two cases read the result.
var rootSurface = sync.OnceValues(func() ([]string, error) { return surfaceOf(".") })

// TestExportedSurfaceIsPinned compares what the type checker says the package
// exports with the list above, in both directions: a symbol added to the code
// and not to the list fails, and so does one removed from the code while the
// list still names it.
func TestExportedSurfaceIsPinned(t *testing.T) {
	got, err := rootSurface()
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(exportedSurface)
	slices.Sort(want)
	for _, msg := range surfaceDiff(got, want) {
		t.Error(msg)
	}
}

// TestTheAuthntestSurfaceIsPinned compares what the type checker says
// pkg/authn/authntest exports with its list, in both directions. The list
// is read from the source, so a symbol that is added without anyone deciding it
// belongs in the test builders, or one that is removed while its callers in other
// modules still stand, fails here.
func TestTheAuthntestSurfaceIsPinned(t *testing.T) {
	got, err := surfaceOf("authntest")
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(authntestSurface)
	slices.Sort(want)
	for _, msg := range surfaceDiff(got, want) {
		t.Error(msg)
	}
	for _, name := range []string{"func Anonymous", "func WithChallenge", "func ConfigReader", "func ConfigReaderAll"} {
		if !slices.Contains(got, name) {
			t.Errorf("the source does not export %q, which the design and the module's AGENTS.md list", name)
		}
	}
}

// TestTheSurfaceHoldsNoNameOfTheOwnershipModel holds the list itself, and the
// source behind it, away from the forbidden names. The pinned list
// is a hand-written file, so a name could be re-added to it by the same hand
// that re-added it to the code; the list and the checker's view are both asked.
func TestTheSurfaceHoldsNoNameOfTheOwnershipModel(t *testing.T) {
	for _, e := range forbiddenIn(exportedSurface) {
		t.Errorf("the pinned surface lists %q, a name the surface may not carry", e)
	}
	got, err := rootSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range forbiddenIn(got) {
		t.Errorf("the source exports %q, a name the surface may not carry", e)
	}
}

// writeSurfacePackage writes a package of the given files into a fresh
// directory and returns it.
func writeSurfacePackage(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestTheSurfaceCheckSeesWhatItIsMeantToSee runs the checker over a package
// built to hold each shape the real one relies on, and over a violating one, so
// the pin cannot pass by seeing nothing.
//
// The shapes: an alias whose target carries methods and fields (the alias form
// the root uses for Identity and Principal), an embedded method promoted into
// the method set, a pointer-receiver method, an interface with its methods, an
// unexported type whose methods are not on the surface, a test file whose
// declarations are not on it, and the forbidden names.
func TestTheSurfaceCheckSeesWhatItIsMeantToSee(t *testing.T) {
	dir := writeSurfacePackage(t, map[string]string{
		"p.go": `package p

type body struct{ Open int; hidden int }

func (body) Read() {}
func (*body) Write() {}

type Embedded struct{}

func (Embedded) Promoted() {}

type Wrapper struct {
	Embedded
	Visible int
	invisible int
}

type Alias = body

type Shape = Wrapper

type Reader interface {
	Read()
	hidden()
}

type unexported struct{ Field int }

func (unexported) Method() {}

const Const = 1

var Var = 2

func Func() {}

func Owner() {}

type Claim struct{}
`,
		"p_test.go": "package p\n\nfunc InTestOnly() {}\n",
	})
	got, err := surfaceOf(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"const Const", "var Var", "func Func",
		"type Alias",
		"field Alias.Open",
		"method Alias.Read",
		"method Alias.Write", // a pointer receiver, which the method set of *T holds
		"field Shape.Visible",
		"method Shape.Promoted", // promoted through the embedded field
		"type Reader", "method Reader.Read",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("the checker does not list %q; got %q", want, got)
		}
	}
	for _, unwanted := range []string{
		"field Alias.hidden", "method Reader.hidden", "field Shape.invisible",
		"type unexported", "method unexported.Method", "func InTestOnly",
	} {
		if slices.Contains(got, unwanted) {
			t.Errorf("the checker lists %q, which is not on the surface", unwanted)
		}
	}

	if bad := forbiddenIn(got); len(bad) != 2 || !slices.Contains(bad, "func Owner") || !slices.Contains(bad, "type Claim") {
		t.Errorf("the forbidden-name check finds %q, want func Owner and type Claim", bad)
	}
	if bad := forbiddenIn([]string{
		"method Identity.Subject", "const StaticPrefix", "var ErrClaimConflict", "field External.Subject",
	}); len(bad) != 1 || bad[0] != "method Identity.Subject" {
		t.Errorf("the forbidden-name check finds %q among a forbidden method, a constant, a longer name and "+
			"a provider's subject field, want the method alone", bad)
	}

	if diff := surfaceDiff(got, slices.Clone(got)); len(diff) != 0 {
		t.Errorf("a surface differs from itself: %q", diff)
	}
	extra := surfaceDiff(append(slices.Clone(got), "func Added"), got)
	missing := surfaceDiff(got, append(slices.Clone(got), "func Removed"))
	if len(extra) != 1 || !strings.Contains(extra[0], `"func Added"`) {
		t.Errorf("a symbol added to the source is reported as %q", extra)
	}
	if len(missing) != 1 || !strings.Contains(missing[0], `"func Removed"`) {
		t.Errorf("a symbol removed from the source is reported as %q", missing)
	}
}
