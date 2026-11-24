package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
	"github.com/vislake/speed/pkg/tenant"
)

func requirementOf(m core.Module, token core.Token) (bool, bool) {
	for _, req := range m.Requires {
		if req.Token == token {
			return true, req.Optional
		}
	}
	return false, false
}

// This module delivers notification.Dispatcher, exclusively: one process has
// exactly one outbound pipeline.
func TestTheModuleDeliversTheDispatcherExclusively(t *testing.T) {
	provides := Module().Provides
	if len(provides) != 1 {
		t.Fatalf("Provides = %+v, want exactly the dispatcher", provides)
	}
	if provides[0].Token != core.Token((*notification.Dispatcher)(nil)) {
		t.Errorf("Provides carries %v, want (*notification.Dispatcher)(nil)", provides[0].Token)
	}
	if !provides[0].Exclusive {
		t.Error("the dispatcher is not delivered exclusively")
	}
}

// TestTheDescriptorDeclaresTheFixedDependencies checks the descriptor's
// required and optional dependencies.
func TestTheDescriptorDeclaresTheFixedDependencies(t *testing.T) {
	m := Module()
	required := []core.Token{
		(*jobs.Queue)(nil),
		(*jobs.Handlers)(nil),
		(*db.Database)(nil),
		(*tenant.Catalog)(nil),
		(*notification.Preferences)(nil),
		(*notification.Inbox)(nil),
		(*i18n.Localizer)(nil),
		(*i18n.Resolver)(nil),
		(*authn.Accounts)(nil),
		(*kv.KV)(nil),
		(*budget.Store)(nil),
	}
	for _, token := range required {
		found, optional := requirementOf(m, token)
		if !found {
			t.Errorf("the descriptor does not require %T", token)
			continue
		}
		if optional {
			t.Errorf("the descriptor marks %T optional, and it is required", token)
		}
	}
	optional := []core.Token{
		(*settings.Store)(nil),
		(*observability.MeterProvider)(nil),
		(*log.Logger)(nil),
		(*mail.Sender)(nil),
		(*sms.Registry)(nil),
		(*sms.Sender)(nil),
		(*authn.TenantNameResolver)(nil),
	}
	for _, token := range optional {
		found, isOptional := requirementOf(m, token)
		if !found {
			t.Errorf("the descriptor does not declare %T", token)
			continue
		}
		if !isOptional {
			t.Errorf("the descriptor marks %T required, and its absence is a legal assembly", token)
		}
	}
	if len(m.Requires) != len(required)+len(optional) {
		t.Errorf("the descriptor declares %d requirements, want %d: %+v",
			len(m.Requires), len(required)+len(optional), m.Requires)
	}
}

// TestNotificationDeliveryRequiresNoHumanProof holds the other side of
// the proof-consumption table.
func TestNotificationDeliveryRequiresNoHumanProof(t *testing.T) {
	if found, _ := requirementOf(Module(), core.Token((*authn.HumanVerifier)(nil))); found {
		t.Fatalf("the descriptor declares authn.HumanVerifier, want none: a business message carries no proof")
	}
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")

	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("the dispatch ended with %v", err)
	}
	if err := r.handleLastJob(t, ctx); err != nil {
		t.Fatalf("the delivery ended with %v, want the sms sent without a proof", err)
	}
	if got := r.smsSender.sendCount(); got != 1 {
		t.Errorf("the sms carrier was handed %d messages, want one", got)
	}
}

// The module declares its migrations, catalog, delivery-budget schema and two
// channel features as resources, and reads no configuration.
func TestTheModuleDeclaresItsMigrationsAndCatalog(t *testing.T) {
	var migrations db.Migrations
	var catalog i18n.Catalog
	specs := 0
	schemas := 0
	for _, res := range Module().Resources {
		switch v := res.(type) {
		case db.Migrations:
			migrations = v
		case i18n.Catalog:
			catalog = v
		case settings.Schema:
			schemas++
			if v.Namespace != ModuleName {
				t.Errorf("the settings schema's namespace is %q, want %q", v.Namespace, ModuleName)
			}
			if len(v.Mounts) != 1 {
				t.Errorf("the settings schema has %d mounts, want one", len(v.Mounts))
			}
		case feature.Spec:
			specs++
		default:
			t.Errorf("the module declares an unexpected resource %T", res)
		}
	}
	if migrations.FS == nil {
		t.Error("the descriptor declares no migration set")
	}
	if catalog.FS == nil || catalog.Pattern != "locales/*.json" {
		t.Errorf("the catalog is %+v, want the embedded locales", catalog)
	}
	if specs != 2 {
		t.Errorf("the descriptor declares %d feature specs, want the two channels", specs)
	}
	if schemas != 1 {
		t.Errorf("the descriptor declares %d settings schemas, want the delivery budgets", schemas)
	}
}

// A run takes the module up and registers its handler before the queue starts,
// so every delivery job has a handler to run it.
func TestTheModuleRegistersItsHandler(t *testing.T) {
	var queue *stubQueue
	err := (&host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
		face, resolveErr := core.Resolve[jobs.Handlers](reg)
		if resolveErr != nil {
			return resolveErr
		}
		q, ok := face.(*stubQueue)
		if !ok {
			t.Fatalf("the handlers face is a %T", face)
		}
		queue = q
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	handler, ok := queue.handlerOf(JobType)
	if !ok {
		t.Fatalf("no handler is registered under %q; the registered set is %v", JobType, queue.Registered())
	}
	if handler.Type() != JobType {
		t.Errorf("the handler answers Type %q, want %q", handler.Type(), JobType)
	}
}

// A required seam that is absent fails resolution before New builds anything.
func TestAMissingRequiredSeamFailsResolution(t *testing.T) {
	for name, missing := range map[string]host{
		"no inbox":     {noInbox: true},
		"no localizer": {noLocalizer: true},
	} {
		t.Run(name, func(t *testing.T) {
			if err := missing.drive(t, nil); !errors.Is(err, core.ErrMissingProvider) {
				t.Fatalf("the drive ended with %v, want ErrMissingProvider", err)
			}
		})
	}
}

// The recipient digest is the database's blind index, so a database without a
// root key fails startup with ErrNoEncryptionKey.
func TestStartRefusesWithoutARootKey(t *testing.T) {
	if err := (host{noEncryptionKey: true}).drive(t, nil); !errors.Is(err, authn.ErrNoEncryptionKey) {
		t.Fatalf("the drive ended with %v, want ErrNoEncryptionKey", err)
	}
}
