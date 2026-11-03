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
// The program compiles every platform capability it can ever run into the
// default build (see capabilities.go), and a serve run names them all. Which of
// them actually starts is a run-time decision each module states in Prepare
// from its own configuration; the same binary therefore assembles different
// feature sets on different deployments. A capability that needs a credential,
// an external account or a service outside the deployment stack — a social or
// enterprise identity provider, the SMS family, mail over SMTP — is present at
// compile time and simply stands itself down when its configuration is absent.
// The review build adds only test controls and the loopback guard, never a
// product capability.
//
// Importing a module's package is what puts it in the available set: each
// registers itself in init, so the import is the whole of the wiring. The
// available set is also what the merged contract document covers, so a platform
// module with an HTTP fragment is imported even when no run enables it. Naming a
// module for a run and importing it for its contract are two different
// questions; see AGENTS.md.
//
// The program runs on six deployment stacks. A stack is one level of the
// command tree: inclusion is inherited down the command path, so the stack node
// names its database engine once and every command under it runs on it. What
// differs between two stacks is the engine, the key-value backend, the event
// backend and the telemetry export backend each of them names — the table below.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	featureboard "github.com/vislake/speed/pkg/feature/board"
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
	"github.com/vislake/speed/pkg/authn/humanverify"
	"github.com/vislake/speed/pkg/authn/impersonation"
	"github.com/vislake/speed/pkg/authn/invite"
	"github.com/vislake/speed/pkg/authn/login"
	loginpassword "github.com/vislake/speed/pkg/authn/login/password"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/authn/mfa/totp"
	"github.com/vislake/speed/pkg/authn/mfa/webauthn"
	authnmount "github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/profile"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/authn/sso"
	"github.com/vislake/speed/pkg/authn/token"
	authzadmin "github.com/vislake/speed/pkg/authz/admin"
	authzguard "github.com/vislake/speed/pkg/authz/guard"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	authzself "github.com/vislake/speed/pkg/authz/self"
	authztenantadmin "github.com/vislake/speed/pkg/authz/tenantadmin"
	"github.com/vislake/speed/pkg/http/cors"
	i18naccount "github.com/vislake/speed/pkg/i18n/account"
	i18ncatalog "github.com/vislake/speed/pkg/i18n/catalog"
	i18nhttp "github.com/vislake/speed/pkg/i18n/http"
	jobsdb "github.com/vislake/speed/pkg/jobs/db"
	jobsscheduler "github.com/vislake/speed/pkg/jobs/scheduler"
	notificationdelivery "github.com/vislake/speed/pkg/notification/delivery"
	notificationinbox "github.com/vislake/speed/pkg/notification/inbox"
	notificationpreferences "github.com/vislake/speed/pkg/notification/preferences"
	notificationstream "github.com/vislake/speed/pkg/notification/stream"
	objectstorefs "github.com/vislake/speed/pkg/objectstore/fs"
	objectstoremeta "github.com/vislake/speed/pkg/objectstore/metadata"
	"github.com/vislake/speed/pkg/ratelimit"
	"github.com/vislake/speed/pkg/settings"
	settingsadmin "github.com/vislake/speed/pkg/settings/admin"
	settingstenantadmin "github.com/vislake/speed/pkg/settings/tenantadmin"
	tenantadmin "github.com/vislake/speed/pkg/tenant/admin"
	"github.com/vislake/speed/pkg/tenant/admission"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
	"github.com/vislake/speed/pkg/tenant/resolve"
	tenantself "github.com/vislake/speed/pkg/tenant/self"

	// The platform modules whose HTTP fragments belong to the merged contract
	// even though the baseline names neither for a run. The credential
	// criterion keeps sms.admin (an SMS vendor's account) and authn.sso.saml
	// (an enterprise provider's credentials and metadata) out of the baseline,
	// so no baseline Include lists them; their fragments still describe the
	// platform's API surface, and what gen and /openapi.json merge is the
	// fragments of the modules the binary's imports reach, not the modules a
	// run names. The import is what puts them in the available set. No command
	// runs authn.sso.saml; the review build's serve is the only run that names
	// sms.admin. Naming a module for a run and importing it for its contract
	// are two different questions; see AGENTS.md.
	_ "github.com/vislake/speed/pkg/authn/sso/saml"
	_ "github.com/vislake/speed/pkg/sms/admin"

	_ "github.com/vislake/speed/pkg/config/format/json"
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
	seedCommand    = "seed"
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
//
// Two of the rows name the same modules: sqlite and sqlite-memory differ in the
// database their configuration points at and in nothing else. They are still two
// stacks, because that difference is the one a product chooses between — the
// database sqlite names keeps what it is given, and sqlite-memory's is gone with
// the process that opened it, which is what makes one of them able to hold the
// account a run signs in with and the other not.
var stacks = []stack{
	{
		name:   "sqlite",
		engine: sqlite.ModuleName, kv: kvmemory.ModuleName, event: eventmemory.ModuleName,
		exporter: stdout.ModuleName,
	},
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

// baselineServe and baselineWorker are the platform core: the modules a serve
// or worker run always names because they start without a credential, an
// external account or a service outside the deployment stack. The capability
// ceiling is separate and larger — every social and enterprise login method, the
// SMS family, the mail carriers, the verification code sender and the
// notification fixture are compiled into every build and considered by serve
// through capabilityModules (see capabilities.go). A capability outside this
// core stands itself down in Prepare when its configuration is absent, which is
// what lets one binary serve a bare run and a fully configured deployment.
//
// This list is still a compile-time statement of what the program cannot run
// without: the entry point, the identity system, tenancy, the authorization
// layer, the journal, the limiter, settings, jobs, notifications and the object
// store. A capability whose required configuration cannot be given a starting
// value is not here, but it is in capabilityModules; the difference is that its
// absence is a run-time fact reported by the diagnostics rather than a build
// variant.
//
// WebAuthn (authn.mfa.webauthn) is named here because its relying party is bound
// to the origin the deployment is reached at — a value this file's configuration
// carries, not a credential or a service — so it starts with nothing external.
// It is named because the platform's management surface cannot demonstrate a
// step-up on a single factor kind: a step-up asks for a kind the session has not
// proved, TOTP alone leaves none, and the second kind is what lets a copy show
// the step-up, the elevation it grants and the write it unblocks.
//
// The static operator password table (authn.static) is the one capability the
// core criterion admits and this list leaves out. It needs no external service —
// a configuration table alone — but it is the only backend that takes the Basic
// scheme, and a browser opens its native credential prompt for a Basic or
// Digest challenge on any 401. This skeleton's browser-facing traffic is the
// frontend bundle, so a 401 carrying Basic would put a native password box in
// front of a signed-in user the moment a session lapsed, in place of the
// frontend's own end-of-session flow. The session cookie and the bearer token
// carry every caller that reaches the guards here, so a copy that wants a
// Basic-authenticated operator or probe imports this backend and its subject
// table back deliberately.
//
// What differs between the two lists is the entry point. serve stands up the
// HTTP entry point and names every module with a route or a middleware; worker
// names the background modules that need neither a router nor a gateway — the
// roster, settings, the audit journal, the job queue and its scheduler, and the
// outbox relay. A worker carries the audit journal because background paths
// write audit rows; with no router it serves no request, so request capture is
// skipped while statement- and record-level capture continue. The stack's
// engine, key-value backend, event backend and export provider are named on the
// commands themselves, not here, and not on the stack node: migrate runs the
// engine alone (see addStack).
var (
	baselineServe = []string{
		// The entry point and the surfaces it carries.
		speedhttp.ModuleName,
		ops.ModuleName,
		webuiModuleName,
		cors.ModuleName,

		// The identity system: the gateway, its route mounter, the account
		// store, the management routes, the session cookie, the login flow with
		// its password method, the multi-factor register with its two factor
		// kinds — the TOTP authenticator and the WebAuthn relying party — access
		// tokens, impersonation records, invitations, the profile surface, and
		// the enterprise provider register.
		authnmount.ModuleName,
		gate.ModuleName,
		authnaccount.ModuleName,
		authnadmin.ModuleName,
		session.ModuleName,
		login.ModuleName,
		loginpassword.ModuleName,
		mfa.ModuleName,
		totp.ModuleName,
		webauthn.ModuleName,
		token.ModuleName,
		impersonation.ModuleName,
		invite.ModuleName,
		profile.ModuleName,
		sso.ModuleName,
		// The human-verification gate. Its mode is left to the configuration, whose
		// default is the self-hosted proof of work, so the challenge route is
		// mounted. A deployment that wants no gate sets authn.humanverify.mode to
		// off; one that wants Turnstile sets it and supplies the site key, secret
		// and expected hostname.
		humanverify.ModuleName,

		// Tenancy, the journal, the authorization layer, the limiter and settings.
		resolve.ModuleName,
		admission.ModuleName,
		tenantcatalog.ModuleName,
		tenantadmin.ModuleName,
		tenantself.ModuleName,
		auditjournal.ModuleName,
		auditadmin.ModuleName,
		authzpolicy.ModuleName,
		authzguard.ModuleName,
		authzadmin.ModuleName,
		authzself.ModuleName,
		authztenantadmin.ModuleName,
		ratelimit.ModuleName,
		settings.ModuleName,
		settingsadmin.ModuleName,
		settingstenantadmin.ModuleName,

		// The i18n capability: the catalog engine, the entry-point wiring and the
		// account bridge.
		i18ncatalog.ModuleName,
		i18nhttp.ModuleName,
		i18naccount.ModuleName,

		// The feature document. The frontend reads it to find out which of the
		// surfaces it carries code for this process serves, so a run that serves
		// the frontend names this module. It mounts GET /api/v1/features on the endpoint
		// feature.board.endpoint names — the platform defaults document names the account endpoint,
		// which the gateway guards and the listener claims for /api/v1/features. The
		// module also needs the authentication gateway: it fails the start rather
		// than publish the document to anonymous callers.
		featureboard.ModuleName,

		// Jobs, notifications, the object store and the outbox relay. All four
		// notification modules are named: the inbox, the live stream, the
		// preference matrix and the outbound delivery pipeline. Each of them
		// mounts its routes on the endpoints its own configuration names —
		// notification.inbox.endpoints, notification.stream.endpoints,
		// notification.preferences.endpoints — so none of them reaches the web
		// endpoint, whose whole path space the frontend host's catch-all route
		// covers. The delivery pipeline requires the preference matrix, the
		// queue and its registration face, the database, the roster, the message
		// localizer, the account store its destination rule reads, the key-value
		// counters its email budget is counted on and the shared sms ledger, all
		// of which the baseline provides; its mail and sms seams are optional,
		// so a deployment without them starts and simply carries no email or sms
		// channel.
		jobsdb.ModuleName,
		jobsscheduler.ModuleName,
		notificationinbox.ModuleName,
		notificationstream.ModuleName,
		notificationpreferences.ModuleName,
		notificationdelivery.ModuleName,

		// The host's own declaration of the notification surface's access. The
		// notification capability declares no permission point, so its routes
		// carry no gate and the authorization guard would refuse each 403; this
		// module registers them open, which is the host's side of the decision.
		// See notification.go.
		notificationModuleName,

		// The host's own declaration of the authentication capability's
		// tenant-management routes. They mount on the api endpoint and decide
		// each action through the Decider in the path tenant's domain, so this
		// module registers them open to the authorization guard, which would
		// otherwise refuse them 403 as undeclared. See management.go.
		managementModuleName,

		// The host's own personal-tenant policy: off by configuration in the
		// baseline, on in the review assembly. When on it mounts
		// POST /api/v1/workspace/personal on the account endpoint and declares the
		// conditional feature workspace.personal. It requires the kind resolver,
		// which the module beside it delivers. See personal.go.
		personalModuleName,
		personalKindModuleName,

		// The host's own seed guard: a deployment listening off loopback must not
		// run on the compiled demonstration seed, so it refuses at Prepare before
		// any socket is bound. See seed_guard.go.
		seedGuardModuleName,

		// The host's tenancy policy seams: the membership check a
		// platform-administrator binding asks, the write guard protecting system
		// and personal tenants, the membership-removal guard, and the provisioner
		// that opens a tenant with its first tenant-admin. The management surface
		// requires the guard and the provisioner, so a run that names tenant.admin
		// without this module does not start. See tenancy.go.
		tenancyModuleName,
		// The management surface's write guard: it refuses every policy write in a
		// personal tenant's domain. It is named apart from the module above because
		// tenant.WriteGuard and authz.WriteGuard both declare a Denied method, which
		// one Go type cannot carry twice. See tenancy.go.
		policyWriteGuardModuleName,
		// The settings surface's write guard: it refuses every setting write in a
		// personal tenant's domain. It is a module of its own for the same reason,
		// and the tenant settings module requires it. See tenancy.go.
		settingsWriteGuardModuleName,

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
		// Background paths write audit rows: the account-deletion cleanup and
		// the personal tenant's create and delete in the event compensation. A
		// worker without the journal loses them silently. Without an HTTP entry
		// point the run serves no request, so request capture is skipped while
		// statement- and record-level capture continue.
		auditjournal.ModuleName,
	}
)

//go:embed dist
var webBundle embed.FS

// init registers the frontend host module. The bundle it serves is whatever
// dist/ holds, which is the built frontend once `pnpm build` has run in the
// frontend package beside this module and nothing servable before that: a
// checkout that has not built the frontend answers 404 on the endpoint, which
// is the host module's designed answer for a bundle with no entry document.
// The module is the same either way — it serves whatever the embed.FS carries.
func init() {
	sub, err := fs.Sub(webBundle, "dist")
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
// what keeps the six from drifting apart.
//
// The stack node takes in two things, and every command under it inherits both.
// The engine at run level, because every one of those commands runs it. And
// every module's declarations at static level: migrate needs them to build every
// table, and each of the other commands drives past Migrate, so it applies
// migrations too — a command carrying a smaller static inclusion would apply a
// smaller migration set. Putting the declaration inclusion on the stack node
// makes that the same inclusion by construction rather than by keeping six
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
	// The capability ceiling: every capability the program can run is compiled
	// into every build and considered by serve. Whether a capability actually
	// runs is its own judgement in Prepare, read from its configuration; a
	// capability that is absent from this run is reported by the startup
	// diagnostics, not by a different binary. See capabilities.go.
	serve.Include(capabilityModules...)
	// The review build's test controls and loopback guard. The list is empty in
	// a default build, so this line names nothing there; see review.go and
	// review_off.go. The configuration the review build reads is the copy under
	// server/review, never one of testdata/.
	serve.Include(reviewModules...)
	serve.Include(s.kv, s.event, log.ModuleName, otel.ModuleName, s.exporter)
	serve.Do(core.StageServe, untilCancelled(serveCommand))

	// worker runs the background capabilities and no HTTP entry point. It names
	// the modules that need neither a router nor a gateway to start: the roster,
	// settings, the job queue and its scheduler, the outbox relay, the stack's
	// two backends and the telemetry providers. A module whose whole purpose is
	// its routes — the inbox, the outbox's consumers behind a router — belongs to
	// serve, which stands the entry point up; the audit journal is not among them
	// because it skips its request-level layer when the run has no router, and a
	// worker does not need that layer.
	worker := node.Sub(workerCommand)
	worker.Include(baselineWorker...)
	worker.Include(s.kv, s.event, log.ModuleName, otel.ModuleName, s.exporter)
	worker.Do(core.StageServe, untilCancelled(workerCommand))

	// migrate runs the engine the stack node names and nothing else, and reaches
	// every other module through the declarations the node carries.
	node.Sub(migrateCommand).Do(core.StageMigrate, nil)

	// seed writes the accounts the configuration names, the tenants this
	// deployment serves, and the platform administrator role the accounts marked
	// with it hold. It drives to StageStart, which carries it past the migrations
	// and past the wiring, so a copy seeds a database that has never been migrated
	// in one command.
	//
	// It names the policy module because that is where the permission-to-(resource,
	// action) translation is resolved: the grant table holds the pair and not the
	// permission, so writing a grant in a permission's name takes the translation,
	// and this command reads it off the catalog that module delivers. The
	// enforcement module is deliberately left out — it would demand an HTTP entry
	// point, which a command that never serves has no use for.
	//
	// It names the roster because the tenants it registers are rows of that table,
	// and the console's tenant-addressed pages read them from the roster's
	// snapshot: the roster loads in Init, which this command reaches, so the
	// registration runs against a table its own callback has already read.
	//
	// It names its own configuration module so that the items under seed. are
	// judged: a required item binds only for the modules a run takes in at run
	// level, so without this the section could be missing or misspelled and the
	// command would do nothing and say nothing.
	//
	// It names the settings module because the runtime settings the seed
	// section lists are written through that module's store. The keys are the
	// declarations the stack node collects statically, so the store is what
	// refuses a misspelled key and a value outside its domain, in this command.
	//
	// It takes the stack's event backend in because an account the account store
	// writes announces itself on the bus — the creation event is published after
	// the commit and does not go through the outbox. The outbox comes with it
	// because the account store's other events (a status change, a deletion, a
	// membership) are staged into it, and because an account module with a bus
	// and no outbox says so at startup. The key-value backend is left out —
	// nothing this command runs reaches for one.
	//
	// It names the personal-tenant module and the audit journal that module
	// requires, so the account-creation hook runs while this command writes the
	// preset accounts. A deployment that enables the feature then has every
	// seeded account's personal tenant before the first request arrives; without
	// the module, the accounts exist with no space until a browser calls the
	// backfill. The hook honours the feature switch, so a deployment with it off
	// builds no personal tenant here either.
	//
	// Neither module needs an HTTP entry point here: the journal skips its
	// request-level layer and the personal module skips its backfill route when
	// the run has no router, while the account lifecycle hooks keep working.
	// Routes belong to the serve command.
	//
	// It names the seed guard because this is the command that writes the
	// accounts: a deployment listening off loopback must not turn the compiled
	// demonstration account into rows, whatever the serve command would later
	// refuse. The guard's Prepare runs before the seed action. See seed_guard.go.
	seed := node.Sub(seedCommand)
	seed.Include(seedGuardModuleName, seedModuleName, authnaccount.ModuleName, authzpolicy.ModuleName,
		tenantcatalog.ModuleName, settings.ModuleName, outboxrelay.ModuleName, s.event, log.ModuleName,
		tenancyModuleName, auditjournal.ModuleName, personalModuleName, personalKindModuleName)
	seed.Do(core.StageStart, seedDatabase)

	// gen merges the fragments the modules declare into one document and writes
	// it out, and with --features writes the available set's feature
	// declarations to a file beside it. It drives to StagePrepare, so it binds
	// no port and constructs no module; it reaches every fragment and every
	// declaration through the static inclusions the stack node carries. Its own
	// flags reach its command line through the module named here.
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

// The document metadata this program publishes unless the command line names
// its own. They are the host's declaration about itself, so they live here
// rather than in every caller's command line: the regeneration command and the
// case that holds the committed artifact to what gen writes then name nothing
// and still agree.
const (
	defaultContractDescription = "The platform API: every operation the assembled modules declare, " +
		"merged from the OpenAPI fragment each module generates from its own operation declarations. " +
		"Each operation is tagged with the module that declared it, and each names the security " +
		"requirement its route admits."
	defaultContractServer = "http://localhost:8081"
)

// genInput carries the gen command's inputs.
type genInput struct {
	// Output is the file the merged document is written to. Empty writes it to
	// standard output.
	Output string `config:"output"`
	// Features is the file the feature vocabulary is written to. Empty writes
	// no vocabulary.
	Features string `config:"features"`
	// Title is the document's info.title. Empty takes the program's own name.
	Title string `config:"title"`
	// Version is the document's info.version. Empty takes the program's version.
	Version string `config:"version"`
	// Description is the document's info.description. Empty leaves the member
	// out.
	Description string `config:"description"`
	// Servers are the origins the API is reached at, one per flag. Empty leaves
	// the member out.
	Servers []string `config:"servers"`
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
			"features": flag("features", "", "PATH",
				"write the feature vocabulary to this file: every feature.Spec in the available set, "+
					"sorted by name, each with its name, declaring module and whether it is conditional"),
			"title": flag("title", "", "TITLE",
				"the document's info.title; the program's own name by default"),
			"version": flag("version", "", "VERSION",
				"the document's info.version; the program's version by default"),
			"description": flag("description", "", "TEXT",
				"the document's info.description; this program's own default when none is given"),
			"servers": flag("servers", "", "URL",
				"an origin the API is reached at, as one entry of the document's servers "+
					"list; repeat the flag to name more than one; this program's own default "+
					"when none is given"),
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
// writes it to the file the command line names, or to standard output. When
// --features names a file, it also writes the available set's feature
// declarations there, sorted by name.
//
// It reads the declaring set — the fragments of every module reached at
// declaration level, which IncludeAllDeclarations on the stack node makes the
// whole binary's — through the same pure function the operations module serves
// /openapi.json with, so a build writes into an artifact the document a running
// process serves. The feature vocabulary comes from the same declaring set: the
// feature.Spec resources the binary's imports reach, whether or not a command
// names the declaring module for a run. It constructs no module: both reads are
// static declarations, and driving to StagePrepare reaches nothing past the
// stances.
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
	if in.Description == "" {
		in.Description = defaultContractDescription
	}
	if len(in.Servers) == 0 {
		in.Servers = []string{defaultContractServer}
	}
	specs := core.Resources[speedhttp.Spec](reg)
	appOperations, err := applicationOperations(reg, specs)
	if err != nil {
		return err
	}
	document, err := openapi.Merge(specs, openapi.Options{
		Title:       in.Title,
		Version:     in.Version,
		Description: in.Description,
		Servers:     in.Servers,
	})
	if err != nil {
		return err
	}
	if len(appOperations) > 0 {
		if document, err = markOperations(document, appOperations); err != nil {
			return err
		}
	}
	if in.Output == "" {
		if _, err = os.Stdout.Write(document); err != nil {
			return err
		}
	} else {
		// The artifact is written owner-only: it is a build product a consumer
		// reads, and nothing about it needs to be world-readable.
		if err = os.WriteFile(in.Output, document, 0o600); err != nil {
			return err
		}
	}
	if in.Features == "" {
		return nil
	}
	vocabulary, err := featureVocabulary(reg)
	if err != nil {
		return err
	}
	return os.WriteFile(in.Features, vocabulary, 0o600)
}

// The owner vocabulary: every feature declaration and every merged operation
// says whether the platform ships it or the application does. The two writers
// that read it — the feature-name generator and orval's platform configuration
// — select the platform side by it; the application's own generation takes
// both.
const (
	// ownerPlatform is the owner of a declaration made by a module the
	// platform module delivers (package path under the pkg module).
	ownerPlatform = "platform"
	// ownerApplication is the owner of a declaration made by the host's own
	// modules.
	ownerApplication = "application"
	// applicationOperationTag is the tag an application-owned operation
	// carries in the merged document. orval 8.17.0 filters operations by
	// their tags (input.filters.tags), so the tag is the mark the platform
	// generation excludes and the application generation keeps.
	applicationOperationTag = "speed.application"
)

// featureEntry is one declaration in the feature vocabulary: the name, the
// module that declared it, its owner, and whether the module has to post it for
// it to be present in a run.
type featureEntry struct {
	Name        string `json:"name"`
	Module      string `json:"module"`
	Owner       string `json:"owner"`
	Conditional bool   `json:"conditional"`
}

// featureVocabulary serializes the available set's feature declarations, sorted
// by name. The available set is what the binary's imports reach, the same scope
// the merged document covers, so the vocabulary lists a feature of a module no
// command names for a run as well — the frontend's feature-name type is
// generated from it, and a name has to be known before it can be checked
// against a document that may or may not contain it.
func featureVocabulary(reg *core.Registry) ([]byte, error) {
	declarations := core.Resources[feature.Spec](reg)
	entries := make([]featureEntry, 0, len(declarations))
	for _, declaration := range declarations {
		owner, err := moduleOwner(reg, declaration.Module)
		if err != nil {
			return nil, err
		}
		entries = append(entries, featureEntry{
			Name:        declaration.Value.Name,
			Module:      declaration.Module,
			Owner:       owner,
			Conditional: declaration.Value.Conditional,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	body, err := json.Marshal(struct {
		Features []featureEntry `json:"features"`
	}{Features: entries})
	if err != nil {
		return nil, fmt.Errorf("%s: encoding the feature vocabulary: %w", genModuleName, err)
	}
	return body, nil
}

// moduleOwner answers who ships the named module: the platform or the
// application. The criterion is the Go package the module's descriptor lives
// in, not its name: the platform is the module the pkg package belongs to, and
// every module whose code sits under that module path is platform-owned; a
// module in any other module path (the host's own, or another application's)
// is application-owned. The package is read from the first lifecycle callback
// the descriptor carries, because a descriptor is built where its module lives
// and its callbacks are functions in that package. A module with no callback
// has no provenance to read and is refused rather than guessed at.
func moduleOwner(reg *core.Registry, module string) (string, error) {
	descriptor, found := reg.Lookup(module)
	if !found {
		return "", fmt.Errorf("%s: module %q declared a resource but is not registered", genModuleName, module)
	}
	packagePath, ok := modulePackage(descriptor)
	if !ok {
		return "", fmt.Errorf("%s: module %q carries no lifecycle callback, so its owning package "+
			"cannot be read; give the descriptor one or the vocabulary cannot be split by owner",
			genModuleName, module)
	}
	platform := platformModulePath()
	if packagePath == platform || strings.HasPrefix(packagePath, platform+"/") {
		return ownerPlatform, nil
	}
	return ownerApplication, nil
}

// modulePackage reports the import path of the package that declared a module
// descriptor. The callbacks are written in that package (a constructor that
// returns the descriptor usually has them as closures), and
// runtime.FuncForPC reports the symbol with its import path. A package-level
// function (`pkg/observability/stdout.prepare`) and a closure
// (`pkg/audit/admin.Module.func1`) both split the same way: everything up to
// the last slash is the package path, and the segment after it starts with the
// package name followed by a dot.
func modulePackage(descriptor core.Module) (string, bool) {
	callbacks := []any{
		descriptor.Prepare,
		descriptor.New,
		descriptor.Migrate,
		descriptor.Init,
		descriptor.Start,
		descriptor.Serve,
		descriptor.Stop,
		descriptor.Close,
	}
	for _, callback := range callbacks {
		if callback == nil {
			continue
		}
		value := reflect.ValueOf(callback)
		if value.Kind() != reflect.Func || value.IsNil() {
			continue
		}
		fn := runtime.FuncForPC(value.Pointer())
		if fn == nil {
			continue
		}
		name := fn.Name()
		slash := strings.LastIndex(name, "/")
		rest := name[slash+1:]
		dot := strings.Index(rest, ".")
		if dot < 0 {
			continue
		}
		return name[:slash+1] + rest[:dot], true
	}
	return "", false
}

// platformModulePath is the import path of the platform module's root. It is
// taken from the core package at run time rather than written down, so the
// criterion survives a copied starter whose dependency lives at another
// import path: core is the one package every platform module reaches, and its
// package path is the module path plus "/core".
func platformModulePath() string {
	return strings.TrimSuffix(reflect.TypeFor[core.Module]().PkgPath(), "/core")
}

// applicationOperations answers the operations the merged document takes from
// application-owned fragments, keyed as "METHOD path". The fragments are read
// from the declaring modules' own documents rather than from names, so an
// application module's route is marked wherever it appears; a platform
// fragment that declares the same operation is a merge conflict before this is
// reached.
func applicationOperations(reg *core.Registry, specs []core.Resource[speedhttp.Spec]) (map[string]bool, error) {
	operations := map[string]bool{}
	for _, spec := range specs {
		owner, err := moduleOwner(reg, spec.Module)
		if err != nil {
			return nil, err
		}
		if owner != ownerApplication {
			continue
		}
		var fragment struct {
			Paths map[string]map[string]json.RawMessage `json:"paths"`
		}
		if err = json.Unmarshal(spec.Value.Document, &fragment); err != nil {
			return nil, fmt.Errorf("%s: reading the fragment module %q declares: %w",
				genModuleName, spec.Module, err)
		}
		for path, methods := range fragment.Paths {
			absolute := spec.Value.ResolvePath(path)
			for method := range methods {
				operations[strings.ToUpper(method)+" "+absolute] = true
			}
		}
	}
	return operations, nil
}

// markOperations gives the named operations of the merged document the
// application-owner tag in place of the module tag the merge wrote. One
// operation carries one tag: a reader that groups operations by tag would
// otherwise list the operation once per group, and the application's own
// operations belong to the application's group alone.
//
// The module tags it removes leave the document's tag list with them, and the
// owner tag enters it, so the document's groups and its operations' tags stay
// the same vocabulary. The document is read and written with the same field
// order and the same value encodings openapi.Merge uses, so an operation the
// map does not name keeps its exact bytes.
func markOperations(document []byte, operations map[string]bool) ([]byte, error) {
	var merged struct {
		OpenAPI    string                                `json:"openapi"`
		Info       json.RawMessage                       `json:"info"`
		Servers    json.RawMessage                       `json:"servers,omitempty"`
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components map[string]map[string]json.RawMessage `json:"components"`
		Tags       json.RawMessage                       `json:"tags,omitempty"`
		Fragments  []json.RawMessage                     `json:"x-fragments"`
	}
	if err := json.Unmarshal(document, &merged); err != nil {
		return nil, fmt.Errorf("%s: reading the merged document: %w", genModuleName, err)
	}
	replaced := map[string]bool{}
	for path, methods := range merged.Paths {
		for method, raw := range methods {
			if !operations[strings.ToUpper(method)+" "+path] {
				continue
			}
			tagged, module, err := withOwnerTag(raw, applicationOperationTag)
			if err != nil {
				return nil, fmt.Errorf("%s: tagging %s %s: %w", genModuleName,
					strings.ToUpper(method), path, err)
			}
			if module != "" {
				replaced[module] = true
			}
			methods[method] = tagged
		}
	}
	if len(replaced) > 0 {
		tags, err := rewriteTags(merged.Tags, replaced, applicationOperationTag)
		if err != nil {
			return nil, fmt.Errorf("%s: rewriting the tag list: %w", genModuleName, err)
		}
		merged.Tags = tags
	}
	marked, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%s: writing the merged document: %w", genModuleName, err)
	}
	return append(marked, '\n'), nil
}

// withOwnerTag replaces an operation's tags with the owner tag and answers the
// module tag it removed, which is the merge's own: every operation of a
// module declared by one fragment carries that module's name and no other tag.
func withOwnerTag(raw json.RawMessage, tag string) (json.RawMessage, string, error) {
	var operation map[string]json.RawMessage
	if err := json.Unmarshal(raw, &operation); err != nil {
		return nil, "", err
	}
	if operation == nil {
		return nil, "", fmt.Errorf("the operation is not a JSON object")
	}
	var tags []string
	if declared, found := operation["tags"]; found {
		if err := json.Unmarshal(declared, &tags); err != nil {
			return nil, "", fmt.Errorf("the operation's tags are not a string array: %w", err)
		}
	}
	module := ""
	if len(tags) > 0 {
		module = tags[0]
	}
	encoded, err := json.Marshal([]string{tag})
	if err != nil {
		return nil, "", err
	}
	operation["tags"] = encoded
	tagged, err := json.Marshal(operation)
	if err != nil {
		return nil, "", err
	}
	return tagged, module, nil
}

// rewriteTags removes the named groups from the document's tag list and adds
// the owner tag, which the marked operations now carry. The list stays sorted
// by name, as the merge writes it.
func rewriteTags(declared json.RawMessage, dropped map[string]bool, added string) (json.RawMessage, error) {
	var entries []struct {
		Name string `json:"name"`
	}
	if len(declared) > 0 {
		if err := json.Unmarshal(declared, &entries); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(entries)+1)
	seen := map[string]bool{}
	for _, entry := range entries {
		if dropped[entry.Name] || seen[entry.Name] {
			continue
		}
		seen[entry.Name] = true
		names = append(names, entry.Name)
	}
	if !seen[added] {
		names = append(names, added)
	}
	sort.Strings(names)
	tags := make([]struct {
		Name string `json:"name"`
	}, 0, len(names))
	for _, name := range names {
		tags = append(tags, struct {
			Name string `json:"name"`
		}{Name: name})
	}
	return json.Marshal(tags)
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
