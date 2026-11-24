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

const (
	memberUsername = "member"
	memberPassword = "member-dev-secret"
	totpSeed       = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
)

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

func encryptionKeyOf(t *testing.T, path string) string {
	t.Helper()
	key, _ := databaseSectionOf(t, path)["encryption-key"].(string)
	if key == "" {
		t.Fatalf("the configuration's %s section names no encryption key", sqlite.ConfigNamespace)
	}
	return key
}

func databaseLocatorOf(t *testing.T, path string) string {
	t.Helper()
	locator, _ := databaseSectionOf(t, path)["dsn"].(string)
	if locator == "" {
		t.Fatalf("the configuration's %s section names no database locator", sqlite.ConfigNamespace)
	}
	return locator
}

func resolveLocator(work, locator string) string {
	if filepath.IsAbs(locator) {
		return locator
	}
	return filepath.Join(work, locator)
}

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

func prepareService(t *testing.T, source string) (dsn, key string, id authn.AccountID) {
	t.Helper()
	key = encryptionKeyOf(t, source)
	locator := databaseLocatorOf(t, source)
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

func seedServiceDatabase(t *testing.T, dsn, key string, migrations []string) authn.AccountID {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q,"encryption-key":%q}},"authn":{"account":{}}}`,
		dsn, key)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), authnaccount.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(authnaccount.Module())
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

func factorByID(factors []authn.FactorRecord, id string) (authn.FactorRecord, bool) {
	for _, factor := range factors {
		if factor.ID == id {
			return factor, true
		}
	}
	return authn.FactorRecord{}, false
}

func assertActivatedWithin(t *testing.T, record authn.FactorRecord, from, to time.Time, age time.Duration) {
	t.Helper()
	earliest := from.Add(-age)
	latest := to.Add(-age)
	if record.ActivatedAt.Before(earliest) || record.ActivatedAt.After(latest) {
		t.Errorf("the %s factor was activated at %s, want between %s and %s: the command has to "+
			"backdate it by %s", record.Kind, record.ActivatedAt, earliest, latest, age)
	}
}

// TestTheConfigurationLocatorShapesNameOneFile holds that file:/path, file:///path and
// file://localhost/path all name the same file.
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

// TestTheConfigurationLocatorDecodesPercentEscapes holds that an escaped and an unescaped spelling of
// a name with a space read one file.
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

// TestTheCommandSeedsFactorsFromTheRealConfiguration runs the command against the end-to-end
// configuration and reads the factors back under the key the configuration states.
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

	withDatabase(t, dsn, "another-root-key-0123456789abcdef", func(ctx context.Context, database db.Database) error {
		if _, _, err := mfatest.TOTPSecret(ctx, database, totpID); err == nil {
			t.Error("the TOTP seed reads back under a key that is not the configuration's")
		}
		return nil
	})
}

// TestTheCommandLeavesOutTheFactorItDidNotWrite holds that a run writing only a TOTP factor states no
// sms key at all.
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

// TestTheCommandRefusesWhatItCannotSeed holds every refusal that needs no database, each naming the
// fragment its own answer carries.
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

// TestTheCommandRefusesAConfigurationWithoutAnEncryptionKey holds that the nested db.sqlite section
// must carry both dsn and encryption-key.
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

// TestTheCommandRefusesAnAccountThatIsNotThere holds that an unknown login name is reported after the
// assembly loads and migrates.
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
