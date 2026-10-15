// Package inbox is the in-app inbox module: it holds one account's in-app
// message rows and their listing, unread count and marking-read, and it mounts
// the inbox routes under /api/v1/notifications.
//
// A row is written by the delivery pipeline through notification.Inbox.Write —
// the one downstream writer of this module — and the HTTP surface reads the
// rows out and marks them read; it never writes a message row. The title and
// body of a row are rendered before they arrive: rendering, channel choice and
// outbound delivery belong to notification.delivery, and this module neither
// delivers nor pushes.
//
// A row is tenant-scoped: the tenant comes from the context, the recipient is
// an account passed explicitly, and the in_app_messages table embeds the tenant
// convention so the roster module's filter scopes, fills and checks every
// statement this module issues through the model path. read_at changes from
// empty to an instant once and is never rewritten, so a first-read timestamp is
// stable. Write is idempotent on the dedupe key: one key lands one row.
//
// This module registers notification.inbox and delivers notification.Inbox
// exclusively. pkg/notification is the interface package and registers nothing.
package inbox

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that assembles the inbox, and the configuration namespace its input
// items hang from is the same string.
const ModuleName = "notification.inbox"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

// defaultEndpoint is the listening endpoint this module declares its OpenAPI
// fragment for. The endpoint's name belongs to the host's HTTP configuration, and
// a resource is a static declaration written before any configuration is read, so
// the fragment carries this conventional name rather than an endpoint a run
// happens to configure; the routes a run actually mounts follow the endpoints the
// module is told to (notification.inbox.endpoints).
const defaultEndpoint = "api"

// EventInboxCreated is the type name of the event this module publishes after
// every in-app message row is committed. The push module subscribes to it; it
// is a host-visible surface and changing it is a breaking one.
const EventInboxCreated = "notification.inbox.created"

// Created is the payload of the created event: the closed set of fields the
// subscriber reads. The identifier is the one the inbox minted for the row, so
// a subscriber that needs the message content reads the row back by it.
type Created struct {
	// MessageID is the identifier of the row that landed.
	MessageID string `json:"message_id"`
	// TenantID is the tenant the row belongs to.
	TenantID string `json:"tenant_id"`
	// RecipientUserID is the account the row is addressed to.
	RecipientUserID string `json:"recipient_user_id"`
	// TypeKey is the notification type the row was dispatched under.
	TypeKey string `json:"type_key"`
}

//go:embed migrations
var migrationRoot embed.FS

//go:embed openapi/inbox.json
var inboxFragment []byte

// migrationsFS is migrationRoot with the wrapping directory removed, so the
// tree the database module sees has one directory per dialect at its root,
// which is the shape it reads.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// init registers this module with the process registry. Importing the package
// is what puts the inbox in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver the seam the descriptor declares.
var _ notification.Inbox = (*inboxStore)(nil)

// Module is the descriptor a host registers.
//
// The database and the tenant roster are hard requirements: the table lives in
// the delivered database, and every row belongs to a tenant whose roster
// carries it, which is also what installs the statement filter the scoped reads
// and writes go through. The event bus, the logging capability and the HTTP
// entry point are optional; without a bus no created event is published while
// the rows land exactly as they otherwise would, without logging the records
// reach the process default logger, and without an entry point the routes are
// not mounted and the endpoint list is not read. The capability is delivered
// exclusively: one process has exactly one inbox, so a second provider fails
// the startup.
//
// The descriptor also declares this module's own http.Spec, carrying the OpenAPI
// fragment that describes the inbox surface under /api/v1/notifications; the
// operations module merges it into the document a process serves.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*event.Bus)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*speedhttp.Router)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*notification.Inbox)(nil), Exclusive: true}},
		Resources: []any{
			schema(),
			db.Migrations{FS: migrationsFS},
			messageCatalog,
			speedhttp.Spec{Endpoint: defaultEndpoint, Document: inboxFragment},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		// New builds the product and reads no table of its own: this stage runs
		// before the migrations are applied, so on a first start the table is not
		// there yet. The first read of the table is the first call to a method.
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newStore(reg)
		},

		// Init wires the optional surfaces up and mounts the routes. It reads no
		// table: the routes are registered, not served.
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			s, ok := productOf(instance)
			if !ok {
				return nil
			}
			return s.wire(reg)
		},
	}
}

// productOf narrows an instance to this module's product. An instance of another
// shape is one the host supplied for this module, such as a stand-in built for a
// test, and the callbacks leave it alone.
func productOf(instance any) (*inboxStore, bool) {
	s, ok := instance.(*inboxStore)
	return s, ok && s != nil
}

// inboxStore is the module's product and the implementation of
// notification.Inbox. It holds the handle it writes through and the optional
// surfaces it was assembled with.
type inboxStore struct {
	handle    *gorm.DB
	bus       event.Bus
	logger    *slog.Logger
	localizer i18n.Localizer
	endpoints []string
}

// readerOf takes up the configuration reader.
//
// A reader that is not there means the host never supplied one as the instance
// of the config module, which is the host's omission rather than something to
// read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

// optional takes up a capability whose absence is a legal assembly: the zero
// value says it is absent, and any other failure to take it up ends the start,
// because a capability that is there and cannot be taken up is not an absence.
func optional[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

// newStore takes up what the descriptor requires and assembles the product. It
// reads no table: the first read is the first call to a method.
func newStore(reg *core.Registry) (*inboxStore, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	if _, err := core.Resolve[tenant.Catalog](reg); err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, err)
	}
	s := &inboxStore{
		handle:    database.DB(),
		logger:    log.Default(),
		endpoints: cfg.endpoints,
	}
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		s.logger = delivered.Named(ModuleName)
	}
	if delivered, err := core.Resolve[event.Bus](reg); err == nil {
		s.bus = delivered
	}
	return s, nil
}

// wire runs in Init, where every instance exists. It takes up the optional
// message localizer and mounts the routes on the entry point when one is
// present.
//
// The entry point is optional: a command that resolves the inbox and has no
// gateway still gets the capability through Resolve, and its routes are not
// mounted. When the entry point is present the endpoint list is required — a
// list that mounted the routes nowhere would silently drop them — and every
// name in it is looked up before any route is registered, so a name the entry
// point does not declare fails the startup naming that endpoint.
func (s *inboxStore) wire(reg *core.Registry) error {
	if localizer, found, err := optional[i18n.Localizer](reg, "the message localizer"); err != nil {
		return err
	} else if found {
		s.localizer = localizer
	}
	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		// The endpoint list is not read when the route capability is absent.
		return nil
	}
	if len(s.endpoints) == 0 {
		return fmt.Errorf("%s: %w: %s is empty and the HTTP entry point is present, so the routes would "+
			"be mounted on nothing; list the endpoints that should carry them, or leave the entry point "+
			"out of the command", ModuleName, notification.ErrInvalidConfig, endpointsKey)
	}
	if err := validateEndpoints(s.endpoints); err != nil {
		return fmt.Errorf("%s: %w", ModuleName, err)
	}
	if err := s.mount(router); err != nil {
		return err
	}
	s.diagnose()
	return nil
}

// diagnose writes the start diagnostic: the endpoints the inbox routes were
// mounted on. The line is the module's own, so a reader finds what the inbox
// mounted without scanning a chain listing.
func (s *inboxStore) diagnose() {
	s.logger.Info(ModuleName+": the inbox routes are mounted", "endpoints", s.endpoints)
}
