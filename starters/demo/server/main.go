// Command demo is a runnable, copyable skeleton of a speed product: one program
// that assembles this repository's modules on six deployment stacks. Copy this
// directory, rename it, and grow a business on it.
package main

import (
	"context"
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
	tenantownership "github.com/vislake/speed/pkg/tenant/ownership"
	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
	tenantprofile "github.com/vislake/speed/pkg/tenant/profile"
	"github.com/vislake/speed/pkg/tenant/resolve"
	tenantself "github.com/vislake/speed/pkg/tenant/self"
	tenantselfcreate "github.com/vislake/speed/pkg/tenant/selfcreate"

	// Imported for the merged contract: no baseline Include names sms.admin, and
	// its fragment is part of the platform contract the imports reach.
	_ "github.com/vislake/speed/pkg/sms/admin"

	_ "github.com/vislake/speed/pkg/config/format/json"
	_ "github.com/vislake/speed/pkg/config/format/yaml"
	_ "github.com/vislake/speed/pkg/config/source/file"

	// The engines and the backends register themselves in init, so importing
	// a package is what puts it in the available set. They are aliased rather
	// than blank-imported because the stack table writes their names: each
	// claims its capability exclusively, and the dependency closure never
	// picks one implementation of an exclusive capability on the host's
	// behalf.
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

	"github.com/vislake/speed/pkg/observability/otel"
	"github.com/vislake/speed/pkg/observability/otlp"
	"github.com/vislake/speed/pkg/observability/stdout"
)

const (
	hostName          = "demo"
	hostVersionNumber = "0.1.0"
	hostVersion       = hostName + " " + hostVersionNumber
)

const (
	serveCommand   = "serve"
	workerCommand  = "worker"
	migrateCommand = "migrate"
	seedCommand    = "seed"
	genCommand     = "gen"
)

const (
	readyMsg   = "ready"
	stoppedMsg = "stopped"
)

type stack struct {
	name     string
	engine   string
	kv       string
	event    string
	exporter string
}

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

const webuiModuleName = webui.ModuleName

var (
	baselineServe = []string{
		speedhttp.ModuleName,
		ops.ModuleName,
		webuiModuleName,
		cors.ModuleName,

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
		humanverify.ModuleName,

		resolve.ModuleName,
		admission.ModuleName,
		tenantcatalog.ModuleName,
		tenantadmin.ModuleName,
		tenantownership.ModuleName,
		tenantself.ModuleName,
		tenantprofile.ModuleName,
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

		i18ncatalog.ModuleName,
		i18nhttp.ModuleName,
		i18naccount.ModuleName,

		featureboard.ModuleName,

		jobsdb.ModuleName,
		jobsscheduler.ModuleName,
		notificationinbox.ModuleName,
		notificationstream.ModuleName,
		notificationpreferences.ModuleName,
		notificationdelivery.ModuleName,

		notificationModuleName,

		managementModuleName,

		tenantpersonal.ModuleName,

		tenantselfcreate.ModuleName,

		seedGuardModuleName,

		tenancyModuleName,
		policyWriteGuardModuleName,
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
		auditjournal.ModuleName,
	}
)

func init() {
	sub, err := fs.Sub(webBundle, webBundleRoot)
	if err != nil {
		panic("demo: the embedded frontend bundle: " + err.Error())
	}
	core.ProcessRegistry.Register(webui.New(sub))
}

func main() { os.Exit(run()) }

// run builds the command tree, runs it, and turns its outcome into an exit
// status. It is a function of its own so the deferred stop runs before the
// process leaves: os.Exit skips deferred calls.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := ensureSigningKey(signingKeyPath); err != nil {
		fmt.Fprintln(os.Stderr, hostName+":", err)
		return 1
	}

	if err := commandTree(core.ProcessRegistry).Execute(ctx); err != nil {
		return 1
	}
	return 0
}

func commandTree(reg *core.Registry) *config.App {
	app := config.NewApp(reg)
	root := app.Root()

	root.Sub("version").Do(core.StagePrepare, printVersion)

	for _, s := range stacks {
		addStack(root, s)
	}
	return app
}

func addStack(root *config.Command, s stack) {
	node := root.Sub(s.name)
	node.Include(s.engine)
	node.IncludeAllDeclarations()

	serve := node.Sub(serveCommand)
	serve.Include(baselineServe...)
	serve.Include(capabilityModules...)
	serve.Include(s.kv, s.event, log.ModuleName, otel.ModuleName, s.exporter)
	serve.Do(core.StageServe, untilCancelled(serveCommand))

	worker := node.Sub(workerCommand)
	worker.Include(baselineWorker...)
	worker.Include(s.kv, s.event, log.ModuleName, otel.ModuleName, s.exporter)
	worker.Do(core.StageServe, untilCancelled(workerCommand))

	node.Sub(migrateCommand).Do(core.StageMigrate, nil)

	seed := node.Sub(seedCommand)
	seed.Include(seedGuardModuleName, seedModuleName, authnaccount.ModuleName, authzpolicy.ModuleName,
		tenantcatalog.ModuleName, settings.ModuleName, outboxrelay.ModuleName, s.event, log.ModuleName,
		tenancyModuleName, auditjournal.ModuleName, tenantpersonal.ModuleName)
	seed.Do(core.StageStart, seedDatabase)

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

// untilCancelled writes the ready record, then blocks until the run is asked to
// stop.
func untilCancelled(command string) config.Action {
	return func(ctx context.Context, _ *core.Registry) error {
		announceReady(command)
		<-ctx.Done()
		announceStopped(command)
		return nil
	}
}

// announceReady writes the record a caller waits for, through the process's
// default logger, which writes standard output.
func announceReady(command string) {
	log.Default().Info(readyMsg, "command", command)
}

func announceStopped(command string) {
	log.Default().Info(stoppedMsg, "command", command)
}

func printVersion(_ context.Context, _ *core.Registry) error {
	fmt.Println(hostVersion)
	return nil
}

const genModuleName = "gen"

const genNamespace = "gen"

const (
	defaultContractDescription = "The platform API: every operation the assembled modules declare, " +
		"merged from the OpenAPI fragment each module generates from its own operation declarations. " +
		"Each operation is tagged with the module that declared it, and each names the security " +
		"requirement its route admits."
	defaultContractServer = "http://localhost:8081"
)

type genInput struct {
	Output      string   `config:"output"`
	Features    string   `config:"features"`
	Title       string   `config:"title"`
	Version     string   `config:"version"`
	Description string   `config:"description"`
	Servers     []string `config:"servers"`
}

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

func init() { core.ProcessRegistry.Register(genModule()) }

func genModule() core.Module {
	return core.Module{
		Name:      genModuleName,
		Resources: []any{genSchema()},
	}
}

// genDocument merges every declared OpenAPI fragment into one document and
// writes it to the named file or to standard output, plus the feature
// vocabulary when --features names a file.
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

const (
	ownerPlatform = "platform"

	ownerApplication = "application"

	// orval filters operations by their tags, so this tag marks an
	// application-owned operation for the platform generation to exclude.
	applicationOperationTag = "speed.application"
)

type featureEntry struct {
	Name        string `json:"name"`
	Module      string `json:"module"`
	Owner       string `json:"owner"`
	Conditional bool   `json:"conditional"`
}

// featureVocabulary serializes the available set's feature declarations, sorted
// by name.
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

// moduleOwner answers who ships the named module, decided by the Go package the
// descriptor's first lifecycle callback lives in: a module under the platform's
// module path is platform-owned, any other is application-owned. A module with
// no callback is refused rather than guessed at.
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

// modulePackage reads the import path from a descriptor's first non-nil
// callback's symbol: everything up to the last slash is the package path, and
// the segment after it starts with the package name followed by a dot.
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

// platformModulePath is the platform module's import path, read from the core
// package at run time so the criterion survives a copied starter whose
// dependency lives at another import path.
func platformModulePath() string {
	return strings.TrimSuffix(reflect.TypeFor[core.Module]().PkgPath(), "/core")
}

// applicationOperations answers the merged document's application-owned
// operations, keyed as "METHOD path".
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

// markOperations replaces the module tag of each named operation with the
// application-owner tag and moves the document's tag list to match. One
// operation carries one tag, so a tag-grouped reader lists it once.
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

// withOwnerTag replaces an operation's tags with tag and answers the module tag
// it removed.
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

// rewriteTags removes the named groups from the document's tag list, adds the
// owner tag, and keeps the list sorted by name.
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

type cliCommand struct {
	use    string
	depth  core.Stage
	action config.Action
}

type commandGroup struct {
	word     string
	commands []cliCommand
}

var groupPlatform = []string{log.ModuleName}

var commandGroups = []commandGroup{exampleGroup}

var exampleGroup = commandGroup{word: "example", commands: []cliCommand{
	{use: "read", depth: core.StageInit, action: placeholderAction("example read")},
	{use: "write", depth: core.StageStart, action: placeholderAction("example write")},
}}

func placeholderAction(command string) config.Action {
	return func(_ context.Context, _ *core.Registry) error {
		fmt.Printf("%s is a placeholder: replace its action in main.go with this product's own work\n", command)
		return nil
	}
}
