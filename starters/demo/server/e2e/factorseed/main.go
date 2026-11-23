// Command factorseed writes multi-factor rows into the database of the service
// under test, for an end-to-end journey that needs a factor older than an
// enrollment could make it.
//
// The impersonation journey needs an operator whose non-SMS factor has been
// active past authn.mfa.sensitive-min-age, whose floor is one hour. A factor
// enrolled through the product is activated now, so the journey cannot reach
// that state by signing in. This command writes the rows through the test
// support package pkg/authn/mfa/mfatest, with the activation instants the
// caller asks for.
//
// The command reads the configuration the service itself runs on, so the
// database it opens and the field-encryption key it seals the TOTP seed under
// are the service's own. --config takes the same locator the service does, a
// file:<path> URI rather than a bare path. It takes the sections of that
// document its own modules need — db.sqlite and authn.account — and hands them
// to the platform's real configuration loader as a minimal document; every
// other section belongs to a module this command does not assemble. The
// database section must be written nested, with db.sqlite.dsn and
// db.sqlite.encryption-key both present; a document that leaves either out is
// refused. A relative database locator therefore resolves against the working
// directory, which is why the command is run from the directory the service
// runs from.
//
// The account is found by the login name the caller gives, through the account
// module's own lookup, so a seeded operator, a username or an email address all
// resolve the way they do at sign-in. Nothing here reads authn_identifiers.
//
// The command belongs to the end-to-end harness only. The harness builds it
// into its own run directory; nothing in the demo program imports it.
package main

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	yamlv3 "go.yaml.in/yaml/v3"

	"github.com/vislake/speed/pkg/authn"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/mfa/mfatest"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/config"
	yamlformat "github.com/vislake/speed/pkg/config/format/yaml"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
)

func main() { os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr)) }

// execute runs the command and turns its outcome into an exit status. A failure
// writes its message to stderr and exits 1; a success leaves stdout carrying
// the one JSON line the caller reads.
func execute(args []string, stdout, stderr io.Writer) int {
	if err := run(context.Background(), args, stdout); err != nil {
		// The writer is the failure sink itself, so a failed write has nowhere
		// to be reported: the exit status is the answer either way.
		_, _ = fmt.Fprintf(stderr, "factorseed: %v\n", err)
		return 1
	}
	return 0
}

// options is what the command line names.
type options struct {
	// configPath is the file the service runs on.
	configPath string
	// account is the account's login name.
	account string

	// totpSecret is the base32 seed of the TOTP factor, and totpAge how long
	// before now it was activated. Both are given together or neither is.
	totpSecret string
	totpAge    time.Duration
	writeTOTP  bool

	// smsAge is how long before now the SMS factor was activated. An absent
	// flag writes no SMS factor.
	smsAge   time.Duration
	writeSMS bool
}

// configScheme is the only locator shape --config accepts, the same one the
// service under test takes. A bare path names no transport and is refused
// rather than guessed at.
const configScheme = "file:"

// parse reads the command line and holds it to the calling convention. The two
// TOTP flags are a pair: a secret with no age, or an age with no secret, is
// refused rather than guessed at. A run that names neither factor writes
// nothing, which is a mistake rather than a no-op, so it is refused too. The
// TOTP seed must decode as base32 without padding, since the factor's own
// verification will decode it the same way, and neither age may be negative:
// a factor activated in the future is not the state the caller asked for.
func parse(args []string) (options, error) {
	flags := flag.NewFlagSet("factorseed", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var opts options
	flags.StringVar(&opts.configPath, "config", "",
		"the configuration file the service runs on, as file:<path>; the nested db.sqlite.dsn "+
			"and db.sqlite.encryption-key come from it")
	flags.StringVar(&opts.account, "account", "", "the account's login name")
	flags.StringVar(&opts.totpSecret, "totp-secret", "",
		"the base32 seed of the TOTP factor to write; given together with -totp-age")
	flags.DurationVar(&opts.totpAge, "totp-age", 0,
		"how long before now the TOTP factor was activated, such as 2h; given together with -totp-secret")
	flags.DurationVar(&opts.smsAge, "sms-age", 0,
		"how long before now the SMS factor was activated, such as 1m; omit it to write no SMS factor")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}

	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })

	if strings.TrimSpace(opts.configPath) == "" {
		return options{}, fmt.Errorf("-config is required: name the configuration file the service runs "+
			"on as %s<path>", configScheme)
	}
	name, err := configPathFromLocator(opts.configPath)
	if err != nil {
		return options{}, err
	}
	opts.configPath = name
	if strings.TrimSpace(opts.account) == "" {
		return options{}, errors.New("-account is required: name the account's login name")
	}
	if given["totp-secret"] != given["totp-age"] {
		return options{}, errors.New("-totp-secret and -totp-age are given together or not at all")
	}
	if given["totp-secret"] {
		if opts.totpSecret == "" {
			return options{}, errors.New("-totp-secret is empty, and a TOTP factor without a seed cannot sign in")
		}
		// The factor's own verification decodes the seed without padding and
		// without regard to case, so a seed it could not decode is refused here
		// rather than written and left to fail at sign-in.
		if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).
			DecodeString(strings.ToUpper(opts.totpSecret)); err != nil {
			return options{}, fmt.Errorf("-totp-secret is not base32 without padding: %w", err)
		}
	}
	if given["totp-age"] && opts.totpAge < 0 {
		return options{}, errors.New("-totp-age must not be negative: a factor activated in the future " +
			"is not seasoned")
	}
	if given["sms-age"] && opts.smsAge < 0 {
		return options{}, errors.New("-sms-age must not be negative: a factor activated in the future " +
			"is not seasoned")
	}
	opts.writeTOTP = given["totp-secret"]
	opts.writeSMS = given["sms-age"]
	if !opts.writeTOTP && !opts.writeSMS {
		return options{}, errors.New("name -totp-secret with -totp-age, or -sms-age: " +
			"at least one factor has to be written")
	}
	return opts, nil
}

// configPathFromLocator gives the filesystem path a --config locator designates.
// It reads the locator the way the service's own file transport does, so the
// three shapes a harness writes name one file: file:/etc/app.yaml and
// file:///etc/app.yaml are the absolute shapes, whose host is empty;
// file://localhost/etc/app.yaml is the same with the host RFC 8089 makes equal
// to an empty one; file:app.yaml is the relative shape, which only the opaque
// part of a URI can express. Percent escapes are decoded in both shapes, so a
// space reaches the filesystem as one character. A bare path names no transport,
// and a host other than localhost names a machine this command does not read,
// so both are refused rather than guessed at.
//
// The service settles the same question in pkg/config/source/file's localPath,
// which is unexported and whose package this command cannot reach that way; the
// rules are repeated here rather than the transport being widened, so the two
// read one locator the same way.
func configPathFromLocator(locator string) (string, error) {
	parsed, err := url.Parse(locator)
	if err != nil {
		return "", fmt.Errorf("%w: -config %q does not parse as a URI: %w",
			config.ErrMalformedLocator, locator, err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("%w: -config must be a %s<path> locator, and %q is a bare path that "+
			"names no transport", config.ErrMalformedLocator, configScheme, locator)
	}
	if parsed.Opaque != "" {
		// url.Parse leaves the opaque part encoded while it decodes Path, so
		// the escapes are undone here and the two shapes agree on the name.
		name, err := url.PathUnescape(parsed.Opaque)
		if err != nil {
			return "", fmt.Errorf("%w: -config %q carries a percent escape that does not decode: %w",
				config.ErrMalformedLocator, locator, err)
		}
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("%w: -config is a %s locator with no path after it",
				config.ErrMalformedLocator, configScheme)
		}
		return name, nil
	}
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		return "", fmt.Errorf("%w: -config %q names the host %q, and this command reads the local "+
			"filesystem alone. A local file is written file:<path>, file://<path> or "+
			"file://localhost<path>", config.ErrMalformedLocator, locator, parsed.Host)
	}
	if parsed.Path == "" {
		return "", fmt.Errorf("%w: -config %q names no path, as in file:<path> or file://localhost<path>",
			config.ErrMalformedLocator, locator)
	}
	return parsed.Path, nil
}

// seededFactors is the one line the command writes on success. A kind the run
// did not write is left out rather than written empty.
type seededFactors struct {
	TOTP string `json:"totp,omitempty"`
	SMS  string `json:"sms,omitempty"`
}

// run reads the configuration, assembles the modules the command needs and
// writes the requested factors. The output line is written only after the drive
// has finished, so a caller that sees it knows the rows are committed.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	opts, err := parse(args)
	if err != nil {
		return err
	}
	document, err := minimalConfig(opts.configPath,
		[]string{sqlite.ConfigNamespace, authnaccount.ConfigNamespace})
	if err != nil {
		return err
	}
	// The loader reads a locator, not bytes, so the minimal document goes to a
	// file of its own. It carries the same database locator as the service's
	// document, so the handle it opens is the service's database.
	dir, err := os.MkdirTemp("", "factorseed-config-")
	if err != nil {
		return fmt.Errorf("making a directory for the assembled configuration: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "config.yaml")
	if err = os.WriteFile(path, document, 0o600); err != nil {
		return fmt.Errorf("writing the assembled configuration: %w", err)
	}

	reg, err := assemble(ctx, path)
	if err != nil {
		return err
	}

	var written seededFactors
	err = reg.Drive(ctx, core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, authnaccount.ModuleName, provisioningtest.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			return seed(ctx, reg, opts, &written)
		},
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(written)
}

// seed resolves the account and writes the factors the options ask for. It runs
// while the assembly is up: StageInit has applied the migrations and every
// module is wired, and the database handle is released when Drive returns.
func seed(ctx context.Context, reg *core.Registry, opts options, written *seededFactors) error {
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return fmt.Errorf("taking up the database: %w", err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return fmt.Errorf("taking up the account reads: %w", err)
	}
	kind, name, err := authnaccount.LoginName(opts.account)
	if err != nil {
		return fmt.Errorf("reading the account name %q: %w", opts.account, err)
	}
	id, err := accounts.Lookup(ctx, kind, name)
	if errors.Is(err, authn.ErrAccountNotFound) {
		return fmt.Errorf("no account signs in as %q; seed the account before seeding its factors", opts.account)
	}
	if err != nil {
		return fmt.Errorf("looking up the account %q: %w", opts.account, err)
	}

	now := time.Now().UTC()
	if opts.writeTOTP {
		written.TOTP, err = mfatest.SeedTOTPFactor(ctx, database, id,
			now.Add(-opts.totpAge), opts.totpSecret)
		if err != nil {
			return err
		}
	}
	if opts.writeSMS {
		written.SMS, err = mfatest.SeedActiveFactor(ctx, database, id, authn.FactorSMS,
			now.Add(-opts.smsAge))
		if err != nil {
			return err
		}
	}
	return nil
}

// assemble registers the modules the command drives and loads the configuration
// from path. The database engine and the account store are the two products it
// needs; the provisioning stand-in fills the seam the account module requires,
// and nothing in the command writes the provisioning data the real seam would.
//
// The formats and the source are registered beside the config module because a
// fresh registry inherits no process-level registrations, and the loader selects
// its transports from what the registry carries.
func assemble(ctx context.Context, path string) (*core.Registry, error) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(yamlformat.Module())
	reg.Register(filesource.Module())
	reg.Register(sqlite.Module())
	reg.Register(authnaccount.Module())
	reg.Register(provisioningtest.Module())
	locator := (&url.URL{Scheme: "file", Path: path}).String()
	reader, err := config.Load(ctx, reg, []string{"--config", locator})
	if err != nil {
		return nil, fmt.Errorf("loading the configuration: %w", err)
	}
	reg.Supply(reader, config.ModuleName)
	return reg, nil
}

// minimalConfig reads the file at path and answers a document holding only the
// named namespaces, with their values as the source wrote them. The platform's
// loader refuses a key no carried module declares, so handing it the service's
// whole document would fail on every section this command does not assemble;
// the extracted document is the smallest one the modules here can load.
//
// A missing namespace is left out rather than written as an empty section: the
// account module's items all have defaults, so its section may be absent. The
// database's section may not, because the engine has no locator of its own and
// the command would otherwise fail far from the cause. That section is read in
// its nested form alone: db.sqlite.dsn and db.sqlite.encryption-key must both be
// there, and a document that names neither, or states either somewhere else, is
// refused with the keys it is missing.
func minimalConfig(path string, namespaces []string) ([]byte, error) {
	//nolint:gosec // G304: the path is the operator's -config, which is the file this command exists to read.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the configuration %s: %w", path, err)
	}
	document := map[string]any{}
	decoder := yamlv3.NewDecoder(bytes.NewReader(raw))
	if err = decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("parsing the configuration %s: %w", path, err)
	}
	var extra any
	switch err = decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, fmt.Errorf("parsing the configuration %s: %w", path, err)
	default:
		return nil, fmt.Errorf("the configuration %s holds more than one document; a config source is one", path)
	}

	minimal := map[string]any{}
	for _, namespace := range namespaces {
		segments := strings.Split(namespace, ".")
		value, found := lookup(document, segments)
		if !found {
			continue
		}
		set(minimal, segments, value)
	}
	if _, found := lookup(minimal, strings.Split(sqlite.ConfigNamespace, ".")); !found {
		return nil, fmt.Errorf("the configuration %s names no %s section, and the database locator "+
			"and encryption key come from it; write it nested as %s.dsn and %s.encryption-key",
			path, sqlite.ConfigNamespace, sqlite.ConfigNamespace, sqlite.ConfigNamespace)
	}
	if err = requireNested(minimal, path, sqlite.ConfigNamespace, "dsn"); err != nil {
		return nil, err
	}
	if err = requireNested(minimal, path, sqlite.ConfigNamespace, "encryption-key"); err != nil {
		return nil, err
	}
	encoded, err := yamlv3.Marshal(minimal)
	if err != nil {
		return nil, fmt.Errorf("writing the assembled configuration: %w", err)
	}
	return encoded, nil
}

// requireNested holds one key of the database section to its nested place. A
// value that is missing, empty, or written outside the section is the same
// failure: the command names the key it needs and where the section states it.
func requireNested(document map[string]any, path, namespace, key string) error {
	segments := append(strings.Split(namespace, "."), key)
	value, found := lookup(document, segments)
	if !found {
		return fmt.Errorf("the configuration %s names no %s.%s; the database section must be written "+
			"nested, with both dsn and encryption-key inside %s", path, namespace, key, namespace)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return fmt.Errorf("the configuration %s has an empty %s.%s; the database section must be "+
			"written nested, with both dsn and encryption-key inside %s", path, namespace, key, namespace)
	}
	return nil
}

// lookup follows path through nested mappings and answers the value it reaches.
func lookup(document map[string]any, path []string) (any, bool) {
	var current any = document
	for _, segment := range path {
		section, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = section[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// set writes value at path in document, creating the mappings on the way.
func set(document map[string]any, path []string, value any) {
	section := document
	for _, segment := range path[:len(path)-1] {
		next, ok := section[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			section[segment] = next
		}
		section = next
	}
	section[path[len(path)-1]] = value
}
