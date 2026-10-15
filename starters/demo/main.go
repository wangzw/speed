// Command demo is a runnable, copyable skeleton of a speed product. It
// assembles this repository's modules into one program: an HTTP entry point and
// its five listening endpoints, the identity system, tenant resolution and
// admission, the authorization layer, the audit journal, rate limiting,
// settings, jobs, notifications and the object store, a database engine, a
// key-value and an event backend, the logging and telemetry providers, the
// operations surface, and the frontend bundle's host. Copy this directory,
// rename it, and grow a business on it.
//
// It is a starting point rather than an evaluation host: the modules under pkg/
// deliver the seams, and this program shows one way to put them together. Its
// business modules are an empty slot under modules/, and it ships no business
// domain of its own.
//
// What the program names is the platform baseline: every platform capability
// that starts without a credential, an external account or a service outside the
// deployment stack. A capability that needs one of those — a social or
// enterprise identity provider, the SMS family, mail over SMTP, object storage
// on S3, WebAuthn bound to a real origin — is left for a product to add once it
// holds the dependency; the criterion is written down in AGENTS.md beside the
// lists below.
//
// Importing a module's package is what puts it in the available set: each
// registers itself in init, so the import is the whole of the wiring. What each
// run is made of is the command tree below, and a module the tree names takes
// its place in it.
//
// The program runs on five deployment stacks. A stack is one level of the
// command tree: inclusion is inherited down the command path, so the stack node
// names its database engine once and every command under it runs on it. What
// differs between two stacks is the engine, the key-value backend, the event
// backend and the telemetry export backend each of them names — the table below.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/ops"
	"github.com/vislake/speed/pkg/ops/openapi"
	"github.com/vislake/speed/pkg/webui"

	// The platform capabilities the baseline names. Several packages share a
	// name — five register a module under an "admin" package, two under
	// "catalog", two under "account" — so they are aliased to the capability
	// directory they sit in. The alias is what the Include lists below read.
	auditadmin "github.com/vislake/speed/pkg/audit/admin"
	auditjournal "github.com/vislake/speed/pkg/audit/journal"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	authnadmin "github.com/vislake/speed/pkg/authn/admin"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/impersonation"
	"github.com/vislake/speed/pkg/authn/invite"
	"github.com/vislake/speed/pkg/authn/login"
	loginpassword "github.com/vislake/speed/pkg/authn/login/password"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/authn/mfa/totp"
	authnmount "github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/profile"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/authn/sso"
	"github.com/vislake/speed/pkg/authn/static"
	"github.com/vislake/speed/pkg/authn/token"
	authzadmin "github.com/vislake/speed/pkg/authz/admin"
	authzguard "github.com/vislake/speed/pkg/authz/guard"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/http/cors"
	i18naccount "github.com/vislake/speed/pkg/i18n/account"
	i18ncatalog "github.com/vislake/speed/pkg/i18n/catalog"
	i18nhttp "github.com/vislake/speed/pkg/i18n/http"
	jobsdb "github.com/vislake/speed/pkg/jobs/db"
	jobsscheduler "github.com/vislake/speed/pkg/jobs/scheduler"
	notificationinbox "github.com/vislake/speed/pkg/notification/inbox"
	notificationstream "github.com/vislake/speed/pkg/notification/stream"
	objectstorefs "github.com/vislake/speed/pkg/objectstore/fs"
	objectstoremeta "github.com/vislake/speed/pkg/objectstore/metadata"
	"github.com/vislake/speed/pkg/ratelimit"
	"github.com/vislake/speed/pkg/settings"
	settingsadmin "github.com/vislake/speed/pkg/settings/admin"
	tenantadmin "github.com/vislake/speed/pkg/tenant/admin"
	"github.com/vislake/speed/pkg/tenant/admission"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
	"github.com/vislake/speed/pkg/tenant/resolve"

	_ "github.com/vislake/speed/pkg/config/format/yaml"
	_ "github.com/vislake/speed/pkg/config/source/file"

	// The engines and the backends register themselves in init, so importing
	// a package is what puts it in the available set. They are aliased rather
	// than blank-imported because the stack table writes their names: each
	// claims its capability exclusively, and the dependency closure never
	// picks one implementation of an exclusive capability on the host's
	// behalf. Several share a package name.
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	eventnats "github.com/vislake/speed/pkg/event/nats"
	outboxrelay "github.com/vislake/speed/pkg/event/outbox/relay"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	eventredis "github.com/vislake/speed/pkg/event/redis"
	kvmemcached "github.com/vislake/speed/pkg/kv/memcached"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	kvnats "github.com/vislake/speed/pkg/kv/nats"
	kvpostgres "github.com/vislake/speed/pkg/kv/postgres"
	kvredis "github.com/vislake/speed/pkg/kv/redis"

	// The telemetry assembler is an exclusive provider too, and the export
	// backends are not: the assembler names no destination, so the stack says
	// which one its runs export through.
	"github.com/vislake/speed/pkg/observability/otel"
	"github.com/vislake/speed/pkg/observability/otlp"
	"github.com/vislake/speed/pkg/observability/stdout"
)

// What the version command answers with, and what the OpenAPI document calls
// this program.
const (
	hostName          = "demo"
	hostVersionNumber = "0.1.0"
	hostVersion       = hostName + " " + hostVersionNumber
)

// The long-running commands, by the word the command tree gives them and the
// ready record carries.
const (
	serveCommand   = "serve"
	workerCommand  = "worker"
	migrateCommand = "migrate"
	genCommand     = "gen"
)

// The record a long-running command writes once every module has started and the
// one it writes as it leaves. A caller waits for the ready record rather than
// for a listening socket: the sockets are open before a command's action runs,
// so a wait on a port alone could act on a run whose modules have not all
// started.
const (
	readyMsg   = "ready"
	stoppedMsg = "stopped"
)

// stack is one deployment stack: the database engine, the key-value backend, the
// event backend and the telemetry export backend a set of commands runs on. Each
// field is a module name, and the stack's name says what it contains.
type stack struct {
	name     string
	engine   string
	kv       string
	event    string
	exporter string
}

// stacks is every stack this program runs on. Each is built by the same
// function, so what differs between two of them is this table and nothing else.
var stacks = []stack{
	{
		name:   "sqlite-memory",
		engine: sqlite.ModuleName, kv: kvmemory.ModuleName, event: eventmemory.ModuleName,
		exporter: stdout.ModuleName,
	},
	{
		name:   "postgres",
		engine: dbpostgres.ModuleName, kv: kvpostgres.ModuleName, event: eventpostgres.ModuleName,
		exporter: stdout.ModuleName,
	},
	{
		name:   "postgres-redis-nats",
		engine: dbpostgres.ModuleName, kv: kvredis.ModuleName, event: eventnats.ModuleName,
		exporter: otlp.ModuleName,
	},
	{
		name:   "postgres-nats-redis",
		engine: dbpostgres.ModuleName, kv: kvnats.ModuleName, event: eventredis.ModuleName,
		exporter: stdout.ModuleName,
	},
	{
		name:   "postgres-memcached-nats",
		engine: dbpostgres.ModuleName, kv: kvmemcached.ModuleName, event: eventnats.ModuleName,
		exporter: stdout.ModuleName,
	},
}

// webuiModuleName is the name the frontend host module is registered under. It
// is webui.ModuleName, spelled once here so nothing else writes the literal.
const webuiModuleName = webui.ModuleName

// baselineServe and baselineWorker are the platform baseline: the capability set
// a copy of this skeleton carries before it holds any dependency of its own.
//
// The criterion is one sentence: the baseline names every platform capability
// that starts without a credential, an external account or a service outside the
// deployment stack. A capability that needs one of those is left for a product to
// add once it holds the dependency — a social or enterprise identity provider
// (authn.login.google/github/wechat-web/wechat-mp, authn.sso.oidc/saml), the SMS
// family (sms.*, authn.login.sms, authn.mfa.sms, authn.sms), mail over SMTP
// (mail.smtp), object storage on S3 (objectstore.s3), and WebAuthn, whose
// relying party is bound to a real origin (authn.mfa.webauthn). The test of a
// doubtful one is the module's own: if its required configuration items cannot
// be given a value that starts, it does not belong here.
//
// What differs between the two lists is the entry point. serve stands up the
// HTTP entry point and names every module with a route or a middleware; worker
// names the background modules that need neither a router nor a gateway — the
// roster, settings, the job queue and its scheduler, and the outbox relay. The
// stack's engine, key-value backend, event backend and export provider are named
// on the commands themselves, not here, and not on the stack node: migrate runs
// the engine alone (see addStack).
var (
	baselineServe = []string{
		// The entry point and the surfaces it carries.
		speedhttp.ModuleName,
		ops.ModuleName,
		webuiModuleName,
		cors.ModuleName,

		// The identity system: the gateway, its route mounter, the operator
		// password table, the account store, the management routes, the session
		// cookie, the login flow with its password method, the multi-factor
		// register with the TOTP factor, access tokens, impersonation records,
		// invitations, the profile surface, and the enterprise provider register.
		authnmount.ModuleName,
		gate.ModuleName,
		static.ModuleName,
		authnaccount.ModuleName,
		authnadmin.ModuleName,
		session.ModuleName,
		login.ModuleName,
		loginpassword.ModuleName,
		mfa.ModuleName,
		totp.ModuleName,
		token.ModuleName,
		impersonation.ModuleName,
		invite.ModuleName,
		profile.ModuleName,
		sso.ModuleName,

		// Tenancy, the journal, the authorization layer, the limiter and settings.
		resolve.ModuleName,
		admission.ModuleName,
		tenantcatalog.ModuleName,
		tenantadmin.ModuleName,
		auditjournal.ModuleName,
		auditadmin.ModuleName,
		authzpolicy.ModuleName,
		authzguard.ModuleName,
		authzadmin.ModuleName,
		ratelimit.ModuleName,
		settings.ModuleName,
		settingsadmin.ModuleName,

		// The i18n capability: the catalog engine, the entry-point wiring and the
		// account bridge.
		i18ncatalog.ModuleName,
		i18nhttp.ModuleName,
		i18naccount.ModuleName,

		// Jobs, notifications, the object store and the outbox relay. The
		// notification inbox and the live stream are named; the preferences
		// matrix and the outbound delivery pipeline are not, and what keeps them
		// out is the frontend endpoint rather than a credential: the preferences
		// module mounts its routes on every endpoint the entry point declares,
		// the web endpoint carries the frontend host's catch-all route, and two
		// routes that clash on one endpoint fail the startup. The preferences
		// matrix therefore cannot be assembled on this command at all, and the
		// delivery pipeline requires it. See AGENTS.md.
		jobsdb.ModuleName,
		jobsscheduler.ModuleName,
		notificationinbox.ModuleName,
		notificationstream.ModuleName,
		objectstorefs.ModuleName,
		objectstoremeta.ModuleName,
		outboxrelay.ModuleName,
	}

	baselineWorker = []string{
		tenantcatalog.ModuleName,
		settings.ModuleName,
		jobsdb.ModuleName,
		jobsscheduler.ModuleName,
		outboxrelay.ModuleName,
	}
)

//go:embed web/dist
var webBundle embed.FS

// init registers the frontend host module. The bundle it serves is the
// placeholder under web/dist; a real frontend replaces its contents in the
// round that builds it, and the module is unchanged either way — it serves
// whatever the embed.FS carries.
func init() {
	sub, err := fs.Sub(webBundle, "web/dist")
	if err != nil {
		panic("demo: the embedded frontend bundle: " + err.Error())
	}
	core.ProcessRegistry.Register(webui.New(sub))
}

func main() { os.Exit(run()) }

// run builds the command tree, runs it, and turns its outcome into an exit
// status. It is a function of its own so that the deferred stop runs before the
// process leaves: os.Exit skips deferred calls.
//
// The token signing key is put in place before the command tree drives the
// lifecycle, because the token module reads the key its configuration names as a
// module starts and a fresh copy of this skeleton has none yet. What the program
// then runs on is the key already on disk; see ensureSigningKey.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := ensureSigningKey(signingKeyPath); err != nil {
		fmt.Fprintln(os.Stderr, hostName+":", err)
		return 1
	}

	if err := commandTree(core.ProcessRegistry).Execute(ctx); err != nil {
		// A failure has already been written to standard error by the time it
		// arrives here; printing it again would put the same line on the stream
		// twice.
		return 1
	}
	return 0
}

// commandTree writes this program's command line.
//
// The root takes in nothing, so that the program's name on its own prints the
// list of commands and the command that assembles nothing can go on assembling
// nothing. Everything else hangs under a stack.
func commandTree(reg *core.Registry) *config.App {
	app := config.NewApp(reg)
	root := app.Root()

	// version answers with the program's version. It assembles nothing: the
	// answer reads no configuration and needs no module.
	root.Sub("version").Do(core.StagePrepare, printVersion)

	for _, s := range stacks {
		addStack(root, s)
	}
	return app
}

// addStack writes one stack's subtree. Every stack goes through here, which is
// what keeps the five from drifting apart.
//
// The stack node takes in two things, and every command under it inherits both.
// The engine at run level, because every one of those commands runs it. And
// every module's declarations at static level: migrate needs them to build every
// table, and each of the other commands drives past Migrate, so it applies
// migrations too — a command carrying a smaller static inclusion would apply a
// smaller migration set. Putting the declaration inclusion on the stack node
// makes that the same inclusion by construction rather than by keeping five
// copies alike. It is also what lets gen read every declared fragment without
// naming a single module.
//
// The key-value, event and export backends are not on the stack node, and that
// is the migrate command's doing: inclusion is inherited, so a backend named on
// the stack node would run in migrate as well, and the command that applies
// migrations runs the engine alone — anything more puts that module's input
// items and its reachability into a pre-deployment job that has no use for
// them. Each backend is named instead on the commands that run a module
// requiring it, from the same stack value.
func addStack(root *config.Command, s stack) {
	node := root.Sub(s.name)
	node.Include(s.engine)
	node.IncludeAllDeclarations()

	// serve runs the whole lifecycle: the entry point and its listening
	// endpoints, the operations surface, the frontend bundle's host, and the
	// platform baseline below — the identity system, tenancy, the authorization
	// layer, the journal, the limiter, settings, jobs, notifications and the
	// object store — together with the stack's two backends and the logging,
	// telemetry and export providers.
	serve := node.Sub(serveCommand)
	serve.Include(baselineServe...)
	serve.Include(s.kv, s.event, log.ModuleName, otel.ModuleName, s.exporter)
	serve.Do(core.StageServe, untilCancelled(serveCommand))

	// worker runs the background capabilities and no HTTP entry point. It names
	// the modules that need neither a router nor a gateway to start: the roster,
	// settings, the job queue and its scheduler, the outbox relay, the stack's
	// two backends and the telemetry providers. A module whose routes or request
	// lists are read wherever it runs — the journal, the inbox, the outbox's
	// consumers behind a router — belongs to serve, which stands the entry point
	// up; a worker on this process-wide configuration would fail their
	// resolution for want of one.
	worker := node.Sub(workerCommand)
	worker.Include(baselineWorker...)
	worker.Include(s.kv, s.event, log.ModuleName, otel.ModuleName, s.exporter)
	worker.Do(core.StageServe, untilCancelled(workerCommand))

	// migrate runs the engine the stack node names and nothing else, and reaches
	// every other module through the declarations the node carries.
	node.Sub(migrateCommand).Do(core.StageMigrate, nil)

	// gen merges the fragments the modules declare into one document and writes
	// it out. It drives to StagePrepare, so it binds no port and constructs no
	// module; it reaches every fragment through the declarations the stack node
	// carries. Its own flags reach its command line through the module named
	// here.
	gen := node.Sub(genCommand)
	gen.Include(genModuleName)
	gen.Do(core.StagePrepare, genDocument)

	for _, g := range commandGroups {
		group := node.Sub(g.word)
		group.Include(groupPlatform...)
		for _, c := range g.commands {
			group.Sub(c.use).Do(c.depth, c.action)
		}
	}
}

// untilCancelled writes the ready record once every module has started, then
// does nothing until the run is asked to stop. A non-nil action at the serving
// depth replaces the drive's own blocking, so this is what keeps the process up.
func untilCancelled(command string) config.Action {
	return func(ctx context.Context, _ *core.Registry) error {
		announceReady(command)
		<-ctx.Done()
		announceStopped(command)
		return nil
	}
}

// announceReady writes the record a caller waits for, once every module of the
// command has started. It goes through the process's default logger, which
// writes standard output whatever destinations the configuration names, so the
// record is where a supervisor reading the child's output finds it.
func announceReady(command string) {
	log.Default().Info(readyMsg, "command", command)
}

// announceStopped writes the record that says the command's run is over and its
// modules are about to be released.
func announceStopped(command string) {
	log.Default().Info(stoppedMsg, "command", command)
}

// printVersion answers with the version. It goes to standard output directly:
// it is what the caller asked for, not a record of what the program is doing.
func printVersion(_ context.Context, _ *core.Registry) error {
	fmt.Println(hostVersion)
	return nil
}

// genModuleName is the name the gen command's flag module is registered under.
const genModuleName = "gen"

// genNamespace is where the gen command's input items are mounted. They take
// their value from the command line alone, so nothing under it may appear in a
// configuration file or the environment.
const genNamespace = "gen"

// genInput carries the gen command's inputs.
type genInput struct {
	// Output is the file the merged document is written to. Empty writes it to
	// standard output.
	Output string `config:"output"`
	// Title is the document's info.title. Empty takes the program's own name.
	Title string `config:"title"`
	// Version is the document's info.version. Empty takes the program's version.
	Version string `config:"version"`
}

// genSchema declares the gen command's input items as command-line flags.
func genSchema() config.Schema {
	flag := func(name, short, placeholder, description string) config.Item {
		return config.Item{
			Origins:     config.OriginFlag,
			FlagName:    name,
			FlagShort:   short,
			Placeholder: placeholder,
			Description: description,
		}
	}
	return config.Schema{
		Namespace: genNamespace,
		Mounts:    []config.Mount{{Value: &genInput{}}},
		Items: map[string]config.Item{
			"output": flag("output", "o", "PATH",
				"write the merged OpenAPI document to this file; with none it goes to standard output"),
			"title": flag("title", "", "TITLE",
				"the document's info.title; the program's own name by default"),
			"version": flag("version", "", "VERSION",
				"the document's info.version; the program's version by default"),
		},
	}
}

// init registers the gen command's flag module. It constructs nothing: it
// carries the command-line items and its only purpose is to put them on the gen
// command's line.
func init() { core.ProcessRegistry.Register(genModule()) }

func genModule() core.Module {
	return core.Module{
		Name:      genModuleName,
		Resources: []any{genSchema()},
	}
}

// genDocument merges every declared OpenAPI fragment into one document and
// writes it to the file the command line names, or to standard output.
//
// It reads the declaring set — the fragments of every module reached at
// declaration level, which IncludeAllDeclarations on the stack node makes the
// whole binary's — through the same pure function the operations module serves
// /openapi.json with, so a build writes into an artifact the document a running
// process serves. It constructs no module: the merge reads static declarations,
// and driving to StagePrepare reaches nothing past the stances.
func genDocument(_ context.Context, reg *core.Registry) error {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return err
	}
	var in genInput
	if err = reader.Decode(genNamespace, &in); err != nil {
		return err
	}
	if in.Title == "" {
		in.Title = hostName
	}
	if in.Version == "" {
		in.Version = hostVersionNumber
	}
	document, err := openapi.Merge(core.Resources[speedhttp.Spec](reg),
		openapi.Options{Title: in.Title, Version: in.Version})
	if err != nil {
		return err
	}
	if in.Output == "" {
		_, err = os.Stdout.Write(document)
		return err
	}
	// The artifact is written owner-only: it is a build product a consumer
	// reads, and nothing about it needs to be world-readable.
	return os.WriteFile(in.Output, document, 0o600)
}

// cliCommand is one leaf of a command group: the word, how far it drives the
// lifecycle, and what it does there.
type cliCommand struct {
	use    string
	depth  core.Stage
	action config.Action
}

// commandGroup is a word under every stack whose leaves are one-shot commands.
// The group node takes in the platform the group's actions use, and each leaf
// inherits it.
type commandGroup struct {
	word     string
	commands []cliCommand
}

// groupPlatform is what every command group takes in besides the modules it
// inherits from the stack node. A group's actions here read and write nothing
// yet, so the logging capability is the whole of it; a group whose actions use
// a database, a store or a bus names those modules beside it.
var groupPlatform = []string{log.ModuleName}

// commandGroups is every command group. This skeleton ships one placeholder: a
// group is the node a product's one-shot operations hang from, and the leaves
// below are where a business fills in its own actions and depth.
var commandGroups = []commandGroup{exampleGroup}

// exampleGroup is the placeholder group. Its leaves declare only how far they
// drive — a read stops at Init, where a store's snapshot is loaded, and a write
// goes to Start, where a recorder accepts a row — and their actions are
// placeholders a product replaces.
var exampleGroup = commandGroup{word: "example", commands: []cliCommand{
	{use: "read", depth: core.StageInit, action: placeholderAction("example read")},
	{use: "write", depth: core.StageStart, action: placeholderAction("example write")},
}}

// placeholderAction is what a placeholder leaf does: it says so, on standard
// output, so that running the group before a business has filled it in answers
// rather than doing nothing silently.
func placeholderAction(command string) config.Action {
	return func(_ context.Context, _ *core.Registry) error {
		fmt.Printf("%s is a placeholder: replace its action in main.go with this product's own work\n", command)
		return nil
	}
}
