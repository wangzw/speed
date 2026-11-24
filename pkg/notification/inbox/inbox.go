// Package inbox is the in-app inbox module: it holds one account's in-app
// message rows and their listing, unread count and marking-read, and it
// mounts the inbox routes under /api/v1/notifications.
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
	"github.com/vislake/speed/pkg/feature"
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

var inboxSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: inboxFragment,
}

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver the seam the descriptor declares.
var _ notification.Inbox = (*inboxStore)(nil)

// Module is the descriptor a host registers.
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
			inboxSpec,
			operations,
			feature.Spec{Name: "notification.inbox"},
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

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			s, ok := productOf(instance)
			if !ok {
				return nil
			}
			return s.wire(reg)
		},
	}
}

func productOf(instance any) (*inboxStore, bool) {
	s, ok := instance.(*inboxStore)
	return s, ok && s != nil
}

type inboxStore struct {
	handle    *gorm.DB
	bus       event.Bus
	logger    *slog.Logger
	localizer i18n.Localizer
	endpoints []string
}

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

// wire runs in Init, where every instance exists.
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

func (s *inboxStore) diagnose() {
	s.logger.Info(ModuleName+": the inbox routes are mounted", "endpoints", s.endpoints)
}
