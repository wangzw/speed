package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/kv"
)

// TestTheSessionsBackendDeclaresWhatItTakes is the case for what the gateway
// reads once at startup: the bearer scheme and nothing else, the prefix the
// users table's identifiers begin with, and no capability to hand back an
// impersonation or an SSO provider. The backend is this host's own and not the
// library's account module, so it declares a namespace of its own, which is what
// holds it to the users it keeps; and a claimed capability would let a backend
// this host wrote hand over an actor.
func TestTheSessionsBackendDeclaresWhatItTakes(t *testing.T) {
	var backend authn.Verifier = &sessionsBackend{}
	if got := backend.Name(); got != "sessions" {
		t.Errorf("the backend reports the name %q, want sessions", got)
	}
	got := backend.Accepts()
	if !slices.Equal(got.Schemes, []string{"bearer"}) {
		t.Errorf("the backend takes the schemes %q, want bearer alone", got.Schemes)
	}
	if len(got.Cookies) != 0 {
		t.Errorf("the backend takes the cookies %q, want none", got.Cookies)
	}
	if got.Accounts != userIDPrefix {
		t.Errorf("the backend declares the account prefix %q, want the users table's %q", got.Accounts, userIDPrefix)
	}
	if got.Impersonation || got.SSO {
		t.Errorf("the backend declares impersonation %v and SSO %v, want neither", got.Impersonation, got.SSO)
	}
}

// TestAUserCreatedByTheHostIsInTheNamespaceTheBackendDeclares is the case for the
// premise the declaration above stands on: the identifier createUser draws for a
// user begins with the prefix the backend declares, fits the account identifier
// format, and does not begin with the static prefix, which only the operators'
// table may hand back. A user outside the declared prefix would be refused by
// the gateway at every request.
func TestAUserCreatedByTheHostIsInTheNamespaceTheBackendDeclares(t *testing.T) {
	database := newUsersDatabase(t)
	user, err := createUser(context.Background(), database, stubPasswords{}, "ab@crm.test", "Ab", "a long enough password")
	if err != nil {
		t.Fatalf("creating a user: %v", err)
	}
	declared := (&sessionsBackend{}).Accepts().Accounts
	if !strings.HasPrefix(user.ID, declared) {
		t.Errorf("the user identifier %q does not begin with the declared prefix %q", user.ID, declared)
	}
	account, err := authn.ParseAccountID(user.ID)
	if err != nil {
		t.Fatalf("the user identifier %q is no account identifier: %v", user.ID, err)
	}
	if account.IsStatic() {
		t.Errorf("the user identifier %q begins with the static prefix", user.ID)
	}
}

// stubPasswords derives nothing: the cases that create a user through it read
// the row, not the hash.
type stubPasswords struct{}

func (stubPasswords) hash(password string) (string, error) { return "hash:" + password, nil }
func (stubPasswords) decoy() string                        { return "decoy" }
func (stubPasswords) current(string) bool                  { return true }

// usersDatabase is a db.Database whose handle is a SQLite file holding the users
// table. It stands in for the database module only where a case reads the users
// table through DB.
type usersDatabase struct {
	db.Database
	handle *gorm.DB
}

func (d usersDatabase) DB() *gorm.DB { return d.handle }

// newUsersDatabase opens a SQLite database in the case's own directory with the
// users table in it.
func newUsersDatabase(t *testing.T) db.Database {
	t.Helper()
	handle, err := gorm.Open(sqlite.Dialector(filepath.Join(t.TempDir(), "users.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening the users database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(handle); err != nil {
			t.Errorf("closing the users database: %v", err)
		}
	})
	if err := handle.AutoMigrate(&User{}); err != nil {
		t.Fatalf("creating the users table: %v", err)
	}
	return usersDatabase{handle: handle}
}

// mapStore is a kv.KV over a map, for a case that reads a stored session. A
// failure it is told to report stands for the store being down.
type mapStore struct {
	kv.KV
	values map[string][]byte
	fail   error
}

func (m *mapStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	if m.fail != nil {
		return nil, false, m.fail
	}
	value, ok := m.values[key]
	return value, ok, nil
}

// TestTheIdentityCarriesTheTimeOfTheLoginNotOfTheRequest is the case for the
// authentication time the backend hands the gateway, through the Verify path: a
// session written hours ago is answered for with the time recorded in it, not
// the time of the request and not the time the user was created. Freshness
// requirements read that time, so a backend that stamped the request's would
// make every session look as new as the request carrying it.
func TestTheIdentityCarriesTheTimeOfTheLoginNotOfTheRequest(t *testing.T) {
	ctx := context.Background()
	database := newUsersDatabase(t)
	created := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	issued := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	if err := database.DB().Create(&User{
		ID: "u_ab12", Email: "ab@crm.test", DisplayName: "Ab", PasswordHash: "x", CreatedAt: created,
	}).Error; err != nil {
		t.Fatalf("writing a user: %v", err)
	}
	record, err := session{User: "u_ab12", Issued: issued}.encode()
	if err != nil {
		t.Fatalf("encoding a session: %v", err)
	}
	const token = "a-token"
	backend := &sessionsBackend{db: database, store: &mapStore{values: map[string][]byte{sessionKey(token): record}}}

	got, err := backend.principalOf(ctx, token)
	if err != nil {
		t.Fatalf("a token whose session holds was refused: %v", err)
	}
	if got.Account != "u_ab12" {
		t.Errorf("the principal names the account %q, want u_ab12", got.Account)
	}
	if !got.AuthTime.Equal(issued) {
		t.Errorf("the principal's authentication time is %s, want the session's %s", got.AuthTime, issued)
	}
}

// TestAnUnreadableSessionIsRefusedAndAFailingStoreIsUnavailable is the case for
// which way each failure of the lookup goes. A token that holds no session, a
// record that is not this host's, a user that is gone and a user that is
// disabled are refusals, which end the request in a 401; a store or a table that
// cannot answer is unavailable, which ends it in a 503 and is never read as a
// refusal, so a caller who can make the store fail cannot make a user's token
// look wrong.
func TestAnUnreadableSessionIsRefusedAndAFailingStoreIsUnavailable(t *testing.T) {
	ctx := context.Background()
	database := newUsersDatabase(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, u := range []User{
		{ID: "u_live", Email: "live@crm.test", DisplayName: "Live", PasswordHash: "x", CreatedAt: now},
		{ID: "u_off", Email: "off@crm.test", DisplayName: "Off", PasswordHash: "x", Disabled: true, CreatedAt: now},
	} {
		if err := database.DB().Create(&u).Error; err != nil {
			t.Fatalf("writing the user %s: %v", u.ID, err)
		}
	}
	recordFor := func(user string) []byte {
		record, err := session{User: user, Issued: now}.encode()
		if err != nil {
			t.Fatalf("encoding a session: %v", err)
		}
		return record
	}
	store := &mapStore{values: map[string][]byte{
		sessionKey("live"):      recordFor("u_live"),
		sessionKey("off"):       recordFor("u_off"),
		sessionKey("gone"):      recordFor("u_gone"),
		sessionKey("malformed"): []byte("u_live"),
	}}
	backend := &sessionsBackend{db: database, store: store}

	refused := map[string]error{
		"nobody":    errUnknownSession,
		"malformed": errMalformedSession,
		"gone":      errNoSuchUser,
		"off":       errUserDisabled,
	}
	for token, cause := range refused {
		_, err := backend.principalOf(ctx, token)
		if !errors.Is(err, cause) || !isRefusal(err) {
			t.Errorf("the token %q was answered with %v, want the refusal %v", token, err, cause)
		}
	}
	if _, err := backend.principalOf(ctx, "live"); err != nil {
		t.Errorf("a live session was refused: %v", err)
	}

	store.fail = errors.New("the store is down")
	if _, err := backend.principalOf(ctx, "live"); err == nil || isRefusal(err) {
		t.Errorf("a failing store was answered with %v, want an error that is no refusal", err)
	}
}

// TestASessionRecordsWhenItsLoginHappened is the case for what the store holds
// under a token: the user and the time of the login, read back as written, and
// refused when it names no user, no time or is not a record at all. The time is
// the authentication time of the identity the backend answers with, so a record
// without one must not be read as a session with a zero time.
func TestASessionRecordsWhenItsLoginHappened(t *testing.T) {
	issued := time.Date(2026, time.October, 11, 9, 30, 0, 123, time.UTC)
	stored, err := session{User: "u_ab12", Issued: issued}.encode()
	if err != nil {
		t.Fatalf("encoding a session: %v", err)
	}
	held, err := readSession(stored)
	if err != nil {
		t.Fatalf("reading back what encode wrote: %v", err)
	}
	if held.User != "u_ab12" || !held.Issued.Equal(issued) {
		t.Errorf("the session read back is %+v, want u_ab12 issued %s", held, issued)
	}

	refused := map[string][]byte{
		"an empty value":         nil,
		"a bare user identifier": []byte("u_ab12"),
		"no user":                []byte(`{"issued":"2026-10-11T09:30:00Z"}`),
		"an empty user":          []byte(`{"user":"","issued":"2026-10-11T09:30:00Z"}`),
		"no time":                []byte(`{"user":"u_ab12"}`),
		"the zero time":          []byte(`{"user":"u_ab12","issued":"0001-01-01T00:00:00Z"}`),
		"a time that is no time": []byte(`{"user":"u_ab12","issued":"yesterday"}`),
	}
	for name, value := range refused {
		if _, err := readSession(value); err == nil {
			t.Errorf("a record with %s was read as a session", name)
		}
	}
}

// TestThePrincipalIsTheUsersAccountSignedInByPassword is the case for what the
// backend claims about a token that holds: the user's account, the password
// method alone, one factor, and the time the session records, never the time of
// the request.
func TestThePrincipalIsTheUsersAccountSignedInByPassword(t *testing.T) {
	issued := time.Date(2026, time.October, 11, 9, 30, 0, 0, time.UTC)
	got := principalFor("u_ab12", issued)
	if got.Account != "u_ab12" || got.Actor != "" || got.Session != "" {
		t.Errorf("the principal names the account %q, actor %q and session %q, want u_ab12 alone", got.Account, got.Actor, got.Session)
	}
	if !slices.Equal(got.Methods, []authn.Method{authn.MethodPassword}) {
		t.Errorf("the principal's methods are %v, want the password alone", got.Methods)
	}
	if got.Assurance != authn.AssuranceSingle {
		t.Errorf("the principal's assurance is %v, want a single factor", got.Assurance)
	}
	if !got.AuthTime.Equal(issued) {
		t.Errorf("the principal's authentication time is %s, want the session's %s", got.AuthTime, issued)
	}
}

// TestAnAccountOfTheStaticTableIsNoCRMUser is the case for how the host reads an
// identity: any account that is not under the static prefix is a user
// identifier, an operator's account is nobody's, and no identity at all reads as
// no user.
func TestAnAccountOfTheStaticTableIsNoCRMUser(t *testing.T) {
	read := func(ctx context.Context) (string, bool) {
		id, ok := authn.FromContext(ctx)
		if !ok {
			return "", false
		}
		return userIDOf(id)
	}

	user := authntest.Context(t, context.Background(), authntest.WithAccount("u_ab12"), authntest.WithBackend(sessionsBackendName))
	if got, ok := read(user); !ok || got != "u_ab12" {
		t.Errorf("a session's identity reads as user %q (%v), want u_ab12", got, ok)
	}
	staticOperator := authntest.Context(t, context.Background(), authntest.WithAccount(operatorAccount), authntest.WithBackend("static"))
	if got, ok := read(staticOperator); ok {
		t.Errorf("the operator's identity reads as user %q, want no user", got)
	}
	if got, ok := read(context.Background()); ok {
		t.Errorf("no identity reads as user %q, want no user", got)
	}
	var zero authn.Identity
	if got, ok := userIDOf(zero); ok {
		t.Errorf("the zero identity reads as user %q, want no user", got)
	}
}
