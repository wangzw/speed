package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	yamlv3 "go.yaml.in/yaml/v3"

	"github.com/vislake/speed/pkg/authn"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/mfa/mfatest"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
)

// The account the end-to-end configuration seeds, and the TOTP seed the cases
// write. The seed is fixed so that a person can import it into an authenticator
// app; it is a fixture and not a credential.
const (
	memberUsername = "member"
	memberPassword = "member-dev-secret"
	totpSeed       = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
)

// realConfigPath answers the end-to-end configuration the demo runs on, from
// this package's directory. It is absolute so that a case can
// change the working directory afterwards without losing it.
func realConfigPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "config.yaml"))
	if err != nil {
		t.Fatalf("resolving the end-to-end configuration: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the end-to-end configuration is not there: %v", err)
	}
	return path
}

// databaseSectionOf reads the configuration's db.sqlite section directly,
// without going through the command's own extraction. It is the way a case
// holds the command to the service's database rather than to the one the case
// made up.
func databaseSectionOf(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	var document map[string]any
	if err := yamlv3.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parsing the configuration: %v", err)
	}
	value, found := lookup(document, strings.Split(sqlite.ConfigNamespace, "."))
	if !found {
		t.Fatalf("the configuration names no %s section", sqlite.ConfigNamespace)
	}
	section, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("the configuration's %s section is not a mapping", sqlite.ConfigNamespace)
	}
	return section
}

// encryptionKeyOf reads the database encryption key out of the configuration
// directly. It is the way a case holds the command to the service's key rather
// than to a key the command made up.
func encryptionKeyOf(t *testing.T, path string) string {
	t.Helper()
	key, _ := databaseSectionOf(t, path)["encryption-key"].(string)
	if key == "" {
		t.Fatalf("the configuration's %s section names no encryption key", sqlite.ConfigNamespace)
	}
	return key
}

// databaseLocatorOf reads the database locator out of the configuration
// directly. The command resolves it against the working directory it runs in,
// and a case that stood the service's database up has to resolve it the same
// way instead of writing the file name it happens to have today.
func databaseLocatorOf(t *testing.T, path string) string {
	t.Helper()
	locator, _ := databaseSectionOf(t, path)["dsn"].(string)
	if locator == "" {
		t.Fatalf("the configuration's %s section names no database locator", sqlite.ConfigNamespace)
	}
	return locator
}

// resolveLocator resolves the configuration's database locator against a
// working directory the way the service and the command both do.
func resolveLocator(work, locator string) string {
	if filepath.IsAbs(locator) {
		return locator
	}
	return filepath.Join(work, locator)
}

// productMigration reads one of the platform's migration files, from the pkg
// module beside this starter. The command does not assemble the multi-factor or
// TOTP modules, so their tables are the ones a service's migrate run leaves in
// the database; the case builds them from the same files rather than from a
// copy, so a renamed column breaks this case instead of passing silently.
func productMigration(t *testing.T, relative string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", relative))
	if err != nil {
		t.Fatalf("resolving %s: %v", relative, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the migration %s: %v", relative, err)
	}
	return string(raw)
}

// prepareService stands for the service's seed and migrate runs in a throwaway
// working directory, and answers the database locator resolved in it, the
// encryption key, and the account the configuration seeds. It changes the
// working directory to the throwaway one, so every later relative locator in
// the process resolves there.
func prepareService(t *testing.T, source string) (dsn, key string, id authn.AccountID) {
	t.Helper()
	key = encryptionKeyOf(t, source)
	locator := databaseLocatorOf(t, source)
	// The factor tables are read before the working directory moves, because
	// their paths are relative to this package.
	factorTable := productMigration(t, filepath.Join("pkg", "authn", "mfa", "migrations", "sqlite",
		"0001_authn_factors.sql"))
	seedTable := productMigration(t, filepath.Join("pkg", "authn", "mfa", "totp", "migrations", "sqlite",
		"0001_authn_totp_secrets.sql"))

	work := t.TempDir()
	dsn = resolveLocator(work, locator)
	if err := os.MkdirAll(filepath.Dir(dsn), 0o700); err != nil {
		t.Fatalf("making the database directory: %v", err)
	}
	t.Chdir(work)
	id = seedServiceDatabase(t, dsn, key, []string{factorTable, seedTable})
	return dsn, key, id
}

// seedServiceDatabase stands for the service's seed and migrate runs: it writes
// the end-to-end account through the account module and the provisioning
// stand-in, and builds the factor tables the service's own migration set
// creates. The key is read from the configuration, so the database it writes is
// the service's database under the service's key.
func seedServiceDatabase(t *testing.T, dsn, key string, migrations []string) authn.AccountID {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q,"encryption-key":%q}},"authn":{"account":{}}}`,
		dsn, key)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), authnaccount.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(authnaccount.Module())
	// The account module requires a provisioning seam. This case writes the
	// account store alone; the stand-in records the calls and writes nothing.
	reg.Register(provisioningtest.Module())
	reg.Supply(reader, config.ModuleName)

	var id authn.AccountID
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, authnaccount.ModuleName, provisioningtest.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			admin, err := core.Resolve[authn.AccountAdmin](reg)
			if err != nil {
				return err
			}
			account, err := admin.Create(ctx, authn.CreateAccount{
				Kind:     authn.KindUser,
				Username: memberUsername,
				Password: memberPassword,
				Via:      "admin",
			})
			if err != nil {
				return err
			}
			id = account.ID
			for _, migration := range migrations {
				if err := applyMigration(database, migration); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("seeding the service database: %v", err)
	}
	return id
}

// applyMigration runs the statements of one migration file on the handle.
func applyMigration(database db.Database, script string) error {
	for _, statement := range strings.Split(script, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := database.DB().Exec(statement).Error; err != nil {
			return fmt.Errorf("applying a migration statement: %w", err)
		}
	}
	return nil
}

// withDatabase opens the database at dsn under key, the way an assembly does,
// and runs check while the handle is up. The handle is released when the drive
// returns, so a later open of the same file has nothing else holding it.
func withDatabase(t *testing.T, dsn, key string, check func(context.Context, db.Database) error) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q,"encryption-key":%q}}}`, dsn, key)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{sqlite.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			return check(ctx, database)
		},
	})
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
}

// decodeOutputLine decodes the one JSON line the command writes on success. It
// answers a map rather than the command's own type, so the key names and the
// omitted key are held to the calling convention rather than to the struct.
func decodeOutputLine(t *testing.T, stdout string) map[string]string {
	t.Helper()
	if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("factorseed wrote %q to stdout, want exactly one newline-terminated JSON line", stdout)
	}
	var written map[string]string
	if err := json.Unmarshal([]byte(stdout), &written); err != nil {
		t.Fatalf("the output line is not a JSON object of strings: %v\n%s", err, stdout)
	}
	return written
}

// factorByID finds one factor in a listing.
func factorByID(factors []authn.FactorRecord, id string) (authn.FactorRecord, bool) {
	for _, factor := range factors {
		if factor.ID == id {
			return factor, true
		}
	}
	return authn.FactorRecord{}, false
}

// assertActivatedWithin holds a factor's activation instant to the age the
// caller asked for: the command reads its own clock, so the instant it wrote
// lies between the instants the case observed around the run, less the age.
func assertActivatedWithin(t *testing.T, record authn.FactorRecord, from, to time.Time, age time.Duration) {
	t.Helper()
	earliest := from.Add(-age)
	latest := to.Add(-age)
	if record.ActivatedAt.Before(earliest) || record.ActivatedAt.After(latest) {
		t.Errorf("the %s factor was activated at %s, want between %s and %s: the command has to "+
			"backdate it by %s", record.Kind, record.ActivatedAt, earliest, latest, age)
	}
}

// TestTheConfigurationLocatorShapesNameOneFile holds the three shapes the
// harness writes to one reading of the same file. The service's file transport
// parses a locator as a URI, so file:/etc/app.yaml, file:///etc/app.yaml and
// file://localhost/etc/app.yaml all name that path; a command that only strips
// the file: prefix reads the localhost shape as a directory named localhost.
func TestTheConfigurationLocatorShapesNameOneFile(t *testing.T) {
	source := realConfigPath(t)
	namespaces := []string{sqlite.ConfigNamespace, authnaccount.ConfigNamespace}
	want, err := minimalConfig(source, namespaces)
	if err != nil {
		t.Fatalf("reading the configuration at its bare path: %v", err)
	}
	locators := []struct {
		name  string
		value string
	}{
		{"file: with an absolute path", "file:" + source},
		{"file:// with an absolute path", "file://" + source},
		{"file://localhost with an absolute path", "file://localhost" + source},
	}
	for _, tc := range locators {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parse([]string{"--config", tc.value, "--account", memberUsername, "--sms-age", "1m"})
			if err != nil {
				t.Fatalf("parsing %q failed: %v", tc.value, err)
			}
			got, err := minimalConfig(opts.configPath, namespaces)
			if err != nil {
				t.Fatalf("reading the configuration through %q failed: %v", tc.value, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("the %q locator read a different document than the bare path", tc.value)
			}
		})
	}
}

// TestTheConfigurationLocatorDecodesPercentEscapes holds a name with a space to
// its unescaped and escaped spellings reading one file. The opaque shape is
// where url.Parse leaves the name encoded, so it is the one that would reach the
// filesystem as three characters without the decode.
func TestTheConfigurationLocatorDecodesPercentEscapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my config.yaml")
	body := "db:\n  sqlite:\n    dsn: \"./var/e2e.db\"\n    encryption-key: \"k\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	t.Chdir(dir)
	escaped := (&url.URL{Scheme: "file", Path: path}).String()
	locators := []struct {
		name  string
		value string
		want  string
	}{
		{"an encoded opaque relative path", "file:my%20config.yaml", "my config.yaml"},
		{"an encoded absolute path", escaped, path},
	}
	for _, tc := range locators {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parse([]string{"--config", tc.value, "--account", memberUsername, "--sms-age", "1m"})
			if err != nil {
				t.Fatalf("parsing %q failed: %v", tc.value, err)
			}
			if opts.configPath != tc.want {
				t.Errorf("%q resolved to %q, want %q", tc.value, opts.configPath, tc.want)
			}
			if _, err := os.ReadFile(opts.configPath); err != nil {
				t.Errorf("reading the configuration through %q failed: %v", tc.value, err)
			}
		})
	}
}

// TestTheCommandSeedsFactorsFromTheRealConfiguration is the acceptance case:
// the command runs the way the harness runs it, against the end-to-end
// configuration, and the factors it wrote are read back through the product's
// own seams under the key that configuration states. A command that extracted
// the key wrongly, or wrote plaintext, fails the read.
func TestTheCommandSeedsFactorsFromTheRealConfiguration(t *testing.T) {
	source := realConfigPath(t)
	dsn, key, id := prepareService(t, source)

	t0 := time.Now().UTC()
	var stdout, stderr bytes.Buffer
	code := execute([]string{
		"--config", "file:" + source,
		"--account", memberUsername,
		"--totp-secret", totpSeed,
		"--totp-age", "2h",
		"--sms-age", "1m",
	}, &stdout, &stderr)
	t1 := time.Now().UTC()
	if code != 0 {
		t.Fatalf("factorseed exited %d: %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("factorseed wrote to stderr: %s", stderr.String())
	}

	written := decodeOutputLine(t, stdout.String())
	if len(written) != 2 {
		t.Fatalf("the output holds %d keys, want exactly totp and sms: %v", len(written), written)
	}
	totpID := written["totp"]
	smsID := written["sms"]
	if totpID == "" {
		t.Fatalf("the output names no TOTP factor: %v", written)
	}
	if smsID == "" {
		t.Fatalf("the output names no SMS factor: %v", written)
	}

	// The read is an assembly of its own, under the key read straight from the
	// configuration, so it holds the command to the service's key.
	withDatabase(t, dsn, key, func(ctx context.Context, database db.Database) error {
		seasoned, err := mfatest.Seasoned(ctx, database, id, time.Hour)
		if err != nil {
			return fmt.Errorf("reading the seasoned answer: %w", err)
		}
		if !seasoned {
			t.Error("the TOTP factor the command wrote is not seasoned at a one-hour minimum age")
		}
		secret, found, err := mfatest.TOTPSecret(ctx, database, totpID)
		if err != nil {
			return fmt.Errorf("reading the TOTP seed: %w", err)
		}
		if !found {
			t.Errorf("the TOTP factor %s holds no seed", totpID)
		} else if secret != totpSeed {
			t.Errorf("the TOTP seed reads back as %q, want %q", secret, totpSeed)
		}
		factors, err := mfatest.Factors(ctx, database, id)
		if err != nil {
			return fmt.Errorf("listing the factors: %w", err)
		}
		totpFactor, found := factorByID(factors, totpID)
		if !found {
			t.Errorf("the TOTP factor %s the command named is not a factor of the account: %+v", totpID, factors)
		} else {
			if totpFactor.Kind != authn.FactorTOTP || totpFactor.Status != authn.FactorActive {
				t.Errorf("the TOTP factor %s reads as %+v, want an active %s factor",
					totpID, totpFactor, authn.FactorTOTP)
			}
			assertActivatedWithin(t, totpFactor, t0, t1, 2*time.Hour)
		}
		smsFactor, found := factorByID(factors, smsID)
		if !found {
			t.Errorf("the SMS factor %s the command named is not a factor of the account: %+v", smsID, factors)
		} else {
			if smsFactor.Kind != authn.FactorSMS || smsFactor.Status != authn.FactorActive {
				t.Errorf("the SMS factor %s reads as %+v, want an active %s factor",
					smsID, smsFactor, authn.FactorSMS)
			}
			assertActivatedWithin(t, smsFactor, t0, t1, time.Minute)
		}
		return nil
	})

	// The same read under another key must fail: a seed that came back anyway
	// would have been written in the clear or under a key nobody configured.
	withDatabase(t, dsn, "another-root-key-0123456789abcdef", func(ctx context.Context, database db.Database) error {
		if _, _, err := mfatest.TOTPSecret(ctx, database, totpID); err == nil {
			t.Error("the TOTP seed reads back under a key that is not the configuration's")
		}
		return nil
	})
}

// TestTheCommandLeavesOutTheFactorItDidNotWrite holds the output's shape from
// the other side: a run that writes only a TOTP factor states no sms key at
// all, rather than writing it empty.
func TestTheCommandLeavesOutTheFactorItDidNotWrite(t *testing.T) {
	source := realConfigPath(t)
	dsn, key, id := prepareService(t, source)

	t0 := time.Now().UTC()
	var stdout, stderr bytes.Buffer
	code := execute([]string{
		"--config", "file:" + source,
		"--account", memberUsername,
		"--totp-secret", totpSeed,
		"--totp-age", "2h",
	}, &stdout, &stderr)
	t1 := time.Now().UTC()
	if code != 0 {
		t.Fatalf("factorseed exited %d: %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("factorseed wrote to stderr: %s", stderr.String())
	}

	written := decodeOutputLine(t, stdout.String())
	if len(written) != 1 {
		t.Fatalf("the output holds %d keys, want exactly totp: %v", len(written), written)
	}
	if value, present := written["sms"]; present {
		t.Errorf("the output holds an sms key %q, and the run wrote no SMS factor", value)
	}
	totpID := written["totp"]
	if totpID == "" {
		t.Fatalf("the output names no TOTP factor: %v", written)
	}

	withDatabase(t, dsn, key, func(ctx context.Context, database db.Database) error {
		factors, err := mfatest.Factors(ctx, database, id)
		if err != nil {
			return fmt.Errorf("listing the factors: %w", err)
		}
		if len(factors) != 1 {
			t.Fatalf("the account holds %d factors, want the one TOTP factor: %+v", len(factors), factors)
		}
		if factors[0].ID != totpID || factors[0].Kind != authn.FactorTOTP {
			t.Errorf("the seeded factor reads as %+v, want the TOTP factor %s", factors[0], totpID)
		}
		assertActivatedWithin(t, factors[0], t0, t1, 2*time.Hour)
		return nil
	})
}

// TestTheCommandRefusesWhatItCannotSeed holds the failure answers that need no
// database: a missing required argument, a configuration written as a bare
// path, one half of the TOTP pair, a run that asks for no factor at all, a
// negative age, and a seed that is not base32. Each case names the fragment its
// own refusal carries, and runs in an empty directory so a refusal that went on
// to open the database would leave a file behind.
func TestTheCommandRefusesWhatItCannotSeed(t *testing.T) {
	source := realConfigPath(t)
	withConfig := func(extra ...string) []string {
		return append([]string{"--config", "file:" + source}, extra...)
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			"no configuration",
			[]string{"--account", memberUsername, "--sms-age", "1m"},
			"-config is required",
		},
		{
			"a bare path as the configuration",
			[]string{"--config", source, "--account", memberUsername, "--sms-age", "1m"},
			"file:<path> locator",
		},
		{
			"a configuration on another host",
			[]string{"--config", "file://otherhost" + source, "--account", memberUsername, "--sms-age", "1m"},
			"names the host",
		},
		{
			"a configuration locator with no path",
			[]string{"--config", "file:", "--account", memberUsername, "--sms-age", "1m"},
			"names no path",
		},
		{
			"no account",
			withConfig("--sms-age", "1m"),
			"-account is required",
		},
		{
			"a secret without an age",
			withConfig("--account", memberUsername, "--totp-secret", totpSeed),
			"-totp-secret and -totp-age are given together or not at all",
		},
		{
			"an age without a secret",
			withConfig("--account", memberUsername, "--totp-age", "2h"),
			"-totp-secret and -totp-age are given together or not at all",
		},
		{
			"no factor",
			withConfig("--account", memberUsername),
			"at least one factor has to be written",
		},
		{
			"a negative totp age",
			withConfig("--account", memberUsername, "--totp-secret", totpSeed, "--totp-age", "-2h"),
			"-totp-age must not be negative",
		},
		{
			"a negative sms age",
			withConfig("--account", memberUsername, "--sms-age", "-2h"),
			"-sms-age must not be negative",
		},
		{
			"a seed that is not base32",
			withConfig("--account", memberUsername, "--totp-secret", "not base32!", "--totp-age", "1h"),
			"-totp-secret is not base32",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			t.Chdir(work)
			var stdout, stderr bytes.Buffer
			if code := execute(tc.args, &stdout, &stderr); code != 1 {
				t.Fatalf("factorseed exited %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("the error does not hold %q: %s", tc.want, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("factorseed wrote %q to stdout, want nothing", stdout.String())
			}
			entries, err := os.ReadDir(work)
			if err != nil {
				t.Fatalf("reading the working directory: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("the refused run left %d entries in the working directory, so it reached the database: %v",
					len(entries), entries)
			}
		})
	}
}

// TestTheCommandRefusesAConfigurationWithoutAnEncryptionKey holds the nested
// shape: the command reads db.sqlite.dsn and db.sqlite.encryption-key from the
// section, and a document that leaves the key out is refused before any
// database is opened.
func TestTheCommandRefusesAConfigurationWithoutAnEncryptionKey(t *testing.T) {
	configDir := t.TempDir()
	path := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(path, []byte("db:\n  sqlite:\n    dsn: \"./var/e2e.db\"\n"), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	work := t.TempDir()
	t.Chdir(work)
	var stdout, stderr bytes.Buffer
	code := execute([]string{
		"--config", "file:" + path,
		"--account", memberUsername,
		"--sms-age", "1m",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("factorseed exited %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "encryption-key") {
		t.Errorf("the error does not name the missing encryption key: %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("factorseed wrote %q to stdout, want nothing", stdout.String())
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatalf("reading the working directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the refused run left %d entries in the working directory, so it reached the database: %v",
			len(entries), entries)
	}
}

// TestTheCommandRefusesAnAccountThatIsNotThere holds the account answer: the
// assembly loads and migrates, and the lookup then reports the name it could
// not find.
func TestTheCommandRefusesAnAccountThatIsNotThere(t *testing.T) {
	source := realConfigPath(t)
	work := t.TempDir()
	dsn := resolveLocator(work, databaseLocatorOf(t, source))
	if err := os.MkdirAll(filepath.Dir(dsn), 0o700); err != nil {
		t.Fatalf("making the database directory: %v", err)
	}
	t.Chdir(work)

	var stdout, stderr bytes.Buffer
	code := execute([]string{
		"--config", "file:" + source,
		"--account", "nobody",
		"--sms-age", "1m",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("factorseed exited %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "nobody") {
		t.Errorf("the error does not name the account: %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("factorseed wrote %q to stdout, want nothing", stdout.String())
	}
}
