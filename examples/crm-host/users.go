package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
)

// usersModuleName is the name the users module is registered under. The
// credential-verifying backend it delivers reports the same name, so every
// identity it authenticates is written users/<user id>.
const (
	usersModuleName  = "users"
	usersBackendName = usersModuleName
)

// userTable is the table the user model maps to.
const userTable = "users"

// User is a CRM user. The table is global: a user exists once and belongs to
// tenants through memberships, and the credential check that reads this table
// runs before any tenant is resolved, where a scoped statement would be refused
// for having no tenant to be scoped to.
type User struct {
	ID           string    `gorm:"column:id;primaryKey"`
	Email        string    `gorm:"column:email;not null"`
	DisplayName  string    `gorm:"column:display_name;not null"`
	PasswordHash string    `gorm:"column:password_hash;not null"`
	Disabled     bool      `gorm:"column:disabled;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
}

// TableName fixes the table this model maps to.
func (User) TableName() string { return userTable }

//go:embed migrations/users
var usersMigrationRoot embed.FS

// usersMigrations drops the wrapping directories, so the tree the database
// module reads has one directory per dialect at its root.
var usersMigrations = subtree(usersMigrationRoot, "migrations/users")

// subtree is fs.Sub for a tree embedded at build time, where a failure is a
// mistake in this program rather than a condition to report.
func subtree(root embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(root, dir)
	if err != nil {
		panic("crm-host: the embedded tree " + dir + ": " + err.Error())
	}
	return sub
}

// The failures a user operation reports, for the callers that turn them into a
// status or an exit.
//
// The texts the backend hands the gateway as a refusal's cause are logged
// through a chain that masks the word following the name of an authentication
// scheme, unless that word is one of the fixed prose words the gateway's rule
// leaves alone, such as "token" or "credential". None of them puts a word
// after "Basic" or "bearer", so none depends on that list: a word outside it
// would reach the log masked, and the cause would say nothing.
var (
	errEmailTaken      = errors.New("a user with this email already exists")
	errInvalidEmail    = errors.New("the email is not an address this host accepts")
	errInvalidName     = errors.New("the display name is empty")
	errWeakPassword    = errors.New("the password is shorter than " + strconv.Itoa(minPasswordLength) + " characters")
	errBadCredentials  = errors.New("the email and password do not match a user")
	errUserDisabled    = errors.New("the user is disabled")
	errNotBasic        = errors.New("the credential's scheme is not Basic")
	errMalformedBasic  = errors.New("the credential does not decode to a name and a password")
	errSubjectMismatch = errors.New("the credential names a subject other than the one it was routed to")
	errMalformedHash   = errors.New("the stored password hash is not in this host's format")
)

// minPasswordLength is the shortest password a user is created with.
const minPasswordLength = 8

// The password derivation: PBKDF2 over HMAC-SHA-256 from the standard library.
// The stored form is pbkdf2-sha256$<iterations>$<salt>$<key>, salt and key in
// unpadded standard base64. The iteration count travels with each hash, so a
// hash keeps verifying at the count it was made with whatever count the
// configuration gives later.
const (
	hashScheme            = "pbkdf2-sha256"
	defaultHashIterations = 600_000
	hashSaltBytes         = 16
	hashKeyBytes          = 32
)

// passwords is the capability the users module stands for as the one place a
// CRM user's password is derived: the stored form of a new password, at the
// cost the configuration gives, the decoy a login naming no user is checked
// against, and whether a stored hash was derived at that cost. The users
// module delivers it exclusively, so a command whose modules create or sign in
// users names the users module itself.
type passwords interface {
	hash(password string) (string, error)
	decoy() string
	current(stored string) bool
}

// usersConfig carries the users module's input item.
type usersConfig struct {
	PasswordIterations int `config:"password-iterations"`
}

// usersSchema declares how costly a password's derivation is.
func usersSchema() config.Schema {
	return config.Schema{
		Namespace: usersModuleName,
		Mounts:    []config.Mount{{Value: &usersConfig{PasswordIterations: defaultHashIterations}}},
		Items: map[string]config.Item{
			"password-iterations": {Description: "how many PBKDF2-SHA-256 iterations a CRM user's password is " +
				"derived with; a positive count. A user created at another count keeps signing in, since each " +
				"stored hash carries its own, and that user's hash is derived again at this count on the " +
				"user's next sign-in"},
		},
	}
}

// errBadIterations refuses a derivation cost that is not a positive count.
var errBadIterations = errors.New(usersModuleName + ".password-iterations is not a positive count")

// derivation derives passwords at one cost. It is the users module's passwords.
type derivation struct {
	iterations int
	decoyHash  func() string
}

// newDerivation is a derivation at a cost, refused when the cost is not a
// positive count.
//
// The decoy is derived once, on the first login that names no user, so that
// such a login costs what a login naming a user does: without it the time a
// refusal takes says whether the email exists. It is derived at the configured
// cost, and a user's hash is checked at the count it carries, so the two cost
// the same for every user whose hash carries the configured count. A
// deployment that changes the count leaves each existing user at the old one
// until that user next signs in, when rederive writes the hash again at the
// configured count; until every user has, the time a login takes tells an
// address still at the old count from one no user has. The decoy is derived
// lazily because every command running this module would otherwise pay for
// it at start.
//
// A Basic credential is outside this. The gateway asks this backend whether it
// owns the subject before it asks it to verify, and a subject no user has is
// refused there, with no derivation; that question answers whether the address
// exists whatever a decoy costs, which is the existence oracle design-authn
// records as a cost of ownership.
func newDerivation(iterations int) (*derivation, error) {
	if iterations < 1 {
		return nil, fmt.Errorf("%w: %d", errBadIterations, iterations)
	}
	d := &derivation{iterations: iterations}
	d.decoyHash = sync.OnceValue(func() string {
		encoded, err := d.hash("decoy password nobody holds")
		if err != nil {
			panic("crm-host: deriving the decoy hash: " + err.Error())
		}
		return encoded
	})
	return d, nil
}

// hash derives the stored form of a password.
func (d *derivation) hash(password string) (string, error) {
	salt := make([]byte, hashSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, d.iterations, hashKeyBytes)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		hashScheme,
		strconv.Itoa(d.iterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	}, "$"), nil
}

// decoy is the hash a login naming no user is checked against.
func (d *derivation) decoy() string { return d.decoyHash() }

// current reports whether a stored hash carries the configured count.
func (d *derivation) current(stored string) bool {
	iterations, err := hashIterations(stored)
	return err == nil && iterations == d.iterations
}

// hashIterations reads the count a stored hash was derived at.
func hashIterations(stored string) (int, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return 0, errMalformedHash
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return 0, errMalformedHash
	}
	return iterations, nil
}

// passwordMatches reports whether a password derives the stored hash, at the
// count the hash carries. A stored value this host cannot read is an error
// rather than a mismatch: it is a fault in the table, not a wrong password.
func passwordMatches(stored, password string) (bool, error) {
	iterations, err := hashIterations(stored)
	if err != nil {
		return false, err
	}
	parts := strings.Split(stored, "$")
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, errMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false, errMalformedHash
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// normaliseEmail is the one form an email is stored and looked up in.
func normaliseEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// validEmail accepts an address with one @ and something on each side of it.
// It refuses the identity separator as well: a Basic user name carrying it is
// read by the gateway as owner and subject, so an address with one could never
// sign in with Basic.
func validEmail(email string) bool {
	local, domain, found := strings.Cut(email, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return false
	}
	if strings.Contains(email, authn.Separator) {
		return false
	}
	return !strings.ContainsFunc(email, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// createUser adds a user. The email is normalised before it is stored, so the
// lookup that follows a login finds the row whatever case the user typed.
//
// Whether the address is taken is the unique constraint's to decide. The read
// before the insert answers the ordinary duplicate with errEmailTaken before a
// password is derived; two creations of one address at once both pass it, and
// the insert of the second then fails on the constraint. The database module
// marks that refusal with gorm.ErrDuplicatedKey on either engine, and it is
// errEmailTaken as well: the identifier is drawn at random, so the email is
// the one unique column a second row can repeat. The refusal is answered as
// errEmailTaken alone, as the look-up's is: the driver's text under the mark
// names the constraint and the engine's own code, which differ between the
// engines and tell a caller nothing the conflict does not.
func createUser(ctx context.Context, database db.Database, pw passwords, email, displayName, password string) (User, error) {
	email = normaliseEmail(email)
	displayName = strings.TrimSpace(displayName)
	switch {
	case !validEmail(email):
		return User{}, errInvalidEmail
	case displayName == "":
		return User{}, errInvalidName
	case len(password) < minPasswordLength:
		return User{}, errWeakPassword
	}
	if _, found, err := userByEmail(ctx, database, email); err != nil {
		return User{}, err
	} else if found {
		return User{}, errEmailTaken
	}
	hashed, err := pw.hash(password)
	if err != nil {
		return User{}, err
	}
	id, err := newID("u_")
	if err != nil {
		return User{}, err
	}
	user := User{
		ID:           id,
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: hashed,
		CreatedAt:    time.Now().UTC(),
	}
	err = database.DB().WithContext(ctx).Create(&user).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return User{}, errEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("writing the user: %w", err)
	}
	return user, nil
}

// userByEmail finds a user by address. It reads with Find rather than First: a
// miss is an ordinary answer here, and First reports it through the database
// module's logger as an error line.
func userByEmail(ctx context.Context, database db.Database, email string) (User, bool, error) {
	var found []User
	err := database.DB().WithContext(ctx).Where("email = ?", normaliseEmail(email)).Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return User{}, false, err
	}
	return found[0], true, nil
}

// userByID finds a user by identifier.
func userByID(ctx context.Context, database db.Database, id string) (User, bool, error) {
	var found []User
	err := database.DB().WithContext(ctx).Where("id = ?", id).Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return User{}, false, err
	}
	return found[0], true, nil
}

// usersByID loads the users a set of identifiers names, keyed by identifier.
func usersByID(ctx context.Context, database db.Database, ids []string) (map[string]User, error) {
	out := make(map[string]User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var found []User
	if err := database.DB().WithContext(ctx).Where("id IN ?", ids).Find(&found).Error; err != nil {
		return nil, err
	}
	for _, u := range found {
		out[u.ID] = u
	}
	return out, nil
}

// listUsers returns every user, ordered by email.
func listUsers(ctx context.Context, database db.Database) ([]User, error) {
	var found []User
	err := database.DB().WithContext(ctx).Order("email").Find(&found).Error
	return found, err
}

// lookupUser finds the user a subject names. A subject carrying an @ is an
// email, which is what a person types into a Basic prompt; any other subject
// is a user identifier, which is what an identity this module authenticated
// carries and what the gateway asks about again once a verification has
// answered with one.
func lookupUser(ctx context.Context, database db.Database, subject string) (User, bool, error) {
	if strings.Contains(subject, "@") {
		return userByEmail(ctx, database, subject)
	}
	return userByID(ctx, database, subject)
}

// authenticate checks an email and a password, for the login route: the route
// is exempt from the gateway, so the check the gateway would have asked this
// module's backend for is made here by the same code.
func authenticate(ctx context.Context, database db.Database, pw passwords, email, password string) (User, error) {
	user, found, err := userByEmail(ctx, database, email)
	if err != nil {
		return User{}, err
	}
	if !found {
		// The derivation still runs, so the refusal takes as long as one for
		// an address that exists.
		_, _ = passwordMatches(pw.decoy(), password)
		return User{}, errBadCredentials
	}
	matched, err := passwordMatches(user.PasswordHash, password)
	if err != nil {
		return User{}, err
	}
	if !matched {
		return User{}, errBadCredentials
	}
	if user.Disabled {
		return User{}, errUserDisabled
	}
	rederive(ctx, database, pw, user, password)
	return user, nil
}

// rederive writes a user's password hash again at the configured cost, once
// the password has been checked against a hash that carries another count, so
// that the stored hashes come to the count the deployment gives as their
// users sign in. It runs only after a check that succeeded, since it needs the
// password itself, and only for a user who is signed in by it.
//
// The update is conditioned on the hash that was checked, so of two sign-ins
// at once the second rewrites nothing. A failure to derive or to write fails
// no sign-in: the password was checked and the old hash still verifies, so it
// is recorded and the next sign-in derives again.
func rederive(ctx context.Context, database db.Database, pw passwords, user User, password string) {
	if pw.current(user.PasswordHash) {
		return
	}
	hashed, err := pw.hash(password)
	if err == nil {
		err = database.DB().WithContext(ctx).Model(&User{}).
			Where("id = ? AND password_hash = ?", user.ID, user.PasswordHash).
			Update("password_hash", hashed).Error
	}
	if err != nil {
		log.FromContext(ctx).Error("writing a user's password hash again at the configured cost",
			"user_id", user.ID, "error", err)
	}
}

// userIDOf reads the CRM user an identity stands for. Two backends of this host
// authenticate CRM users — this one for a Basic credential and the sessions one
// for a bearer token — and both put the user identifier in the subject. Any
// other owner is not a CRM user: the platform's operators are authenticated by
// the static backend, and they belong to no tenant.
func userIDOf(id authn.Identity) (string, bool) {
	switch id.Owner() {
	case usersBackendName, sessionsBackendName:
		return id.Subject(), true
	default:
		return "", false
	}
}

// usersTable is the capability the users module stands for, so that a module
// whose tables reference the users table can declare a requirement on it: the
// database module applies migrations in the order the requirements give, and
// that order is the only one between two modules' migrations. It is a token
// only — nothing resolves it — and it is claimed exclusively, so a requirement
// on it never brings this module into a command.
type usersTable interface{ usersTable() }

// usersBackend is the users module's product: a credential-verifying backend
// for the Basic credentials of CRM users, and the derivation every new
// password of theirs goes through.
//
// It claims openly, because the set of users is a table that grows at run
// time and cannot be written out at startup. An open claim is answered by
// asking, which is what Owns does, and it answers for a subject in either of
// the two forms lookupUser reads.
type usersBackend struct {
	released
	*derivation
	db db.Database
}

// Name is the owner half of every identity this backend authenticates.
func (b *usersBackend) Name() string { return usersBackendName }

// usersTable marks the product as the users module's.
func (*usersBackend) usersTable() {}

// Claim is final when New returns, which is when the gateway may read it.
func (b *usersBackend) Claim() authn.Claim { return authn.Claim{Open: true} }

// Owns answers whether a subject names a CRM user. A disabled user is still
// owned here: the refusal is Verify's, so it is a refusal by the authority
// rather than a subject nobody answers for.
func (b *usersBackend) Owns(ctx context.Context, subject string) (bool, error) {
	_, found, err := lookupUser(ctx, b.db, subject)
	return found, err
}

// Verify checks a Basic credential against the users table, and brings the
// user's hash to the configured cost once it matches, as a login does.
//
// A store that cannot be read answers Unavailable, never Denied: telling a user
// their password is wrong because the database was unreachable is the one
// answer a backend must not give.
func (b *usersBackend) Verify(ctx context.Context, subject string, c authn.Credential) authn.Verdict {
	if c.Scheme != "basic" {
		return authn.Denied(errNotBasic)
	}
	name, password, ok := decodeBasic(c.Value)
	if !ok {
		return authn.Denied(errMalformedBasic)
	}
	// A qualified credential arrives as the raw pair, owner included.
	name = strings.TrimPrefix(name, usersBackendName+authn.Separator)
	if name != subject {
		return authn.Denied(errSubjectMismatch)
	}
	user, found, err := lookupUser(ctx, b.db, subject)
	if err != nil {
		return authn.Unavailable(err)
	}
	if !found {
		return authn.Denied(errBadCredentials)
	}
	matched, err := passwordMatches(user.PasswordHash, password)
	if err != nil {
		return authn.Unavailable(err)
	}
	if !matched {
		return authn.Denied(errBadCredentials)
	}
	if user.Disabled {
		return authn.Denied(errUserDisabled)
	}
	rederive(ctx, b.db, b.derivation, user, password)
	return authn.Authenticated(user.ID)
}

// decodeBasic reads the name and password out of a Basic credential's value.
func decodeBasic(value string) (name, password string, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	return strings.Cut(string(raw), ":")
}

// init registers the users module. It runs on serve, where the gateway asks it
// about Basic credentials and the admin endpoint creates users, and on the user
// command group, whose create derives the new user's password through it.
func init() {
	registerApplication(application{
		module: usersModule(),
		runsOn: []string{serveCommand, userGroup.word},
		models: []any{User{}},
	})
}

// usersModule declares the users table and the derivation cost, and delivers
// the backend.
//
// The backend's product needs the database handle at request time only, but it
// takes it in New and declares the requirement: the product is what the gateway
// collects in its own Init, and a backend whose handle arrived later would be
// one the gateway could reach before it was whole. The cost is read in New too,
// so a count that is not positive fails the startup rather than the first
// creation.
func usersModule() core.Module {
	return core.Module{
		Name: usersModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
		},
		// The verifier is delivered without an exclusive claim, as every
		// credential backend is: several backends answer side by side, each
		// for its own part of the subject space.
		Provides: []core.Provision{
			{Token: (*authn.Verifier)(nil)},
			{Token: (*usersTable)(nil), Exclusive: true},
			{Token: (*passwords)(nil), Exclusive: true},
		},
		Resources: []any{
			db.Migrations{FS: usersMigrations},
			usersSchema(),
		},

		// The backend's one input item decides how it derives, not whether it
		// runs, so its stance is enabled whatever the item says.
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, usersModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return nil, err
			}
			var cfg usersConfig
			if err = reader.Decode(usersModuleName, &cfg); err != nil {
				return nil, fmt.Errorf("reading %s: %w", usersModuleName, err)
			}
			derived, err := newDerivation(cfg.PasswordIterations)
			if err != nil {
				return nil, err
			}
			return &usersBackend{released: released{log: logger}, derivation: derived, db: database}, nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
