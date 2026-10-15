package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/tenant"
)

// requirementOf reports whether the descriptor declares a token, and whether the
// declaration is optional.
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

// The descriptor declares the dependencies the design fixes: the queue, the
// handler registration face, the database, the tenant roster, the preferences
// seam, the inbox seam and the localizer are required; the mail sender and the
// two sms seams are optional, one per channel that sends outside this process.
// It declares nothing else — not an entry point, which this module mounts no
// route on, and not an event bus, which it publishes nothing on.
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
		(*mail.Sender)(nil),
		(*sms.Registry)(nil),
		(*sms.Sender)(nil),
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

// The module declares its migrations and its message catalog as resources, and
// reads no configuration: it declares no schema and no namespace of its own.
func TestTheModuleDeclaresItsMigrationsAndCatalog(t *testing.T) {
	var migrations db.Migrations
	var catalog i18n.Catalog
	for _, res := range Module().Resources {
		switch v := res.(type) {
		case db.Migrations:
			migrations = v
		case i18n.Catalog:
			catalog = v
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
