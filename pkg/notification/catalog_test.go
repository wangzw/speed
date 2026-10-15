package notification_test

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	i18ncatalog "github.com/vislake/speed/pkg/i18n/catalog"
	"github.com/vislake/speed/pkg/notification/inbox"
	"github.com/vislake/speed/pkg/notification/preferences"
)

// TestTheInboxAndPreferencesCatalogsMergeUnderOneEngine holds that the inbox and
// the preferences module hand the i18n engine catalogs it can merge into one
// index. The engine keys a message by one declaration across every module: a key
// two modules both declare is refused as a collision and stops the startup, so
// two modules that write one key cannot be assembled together — and the delivery
// module, which requires both, cannot be assembled at all.
//
// The observation is the assembly of the real engine over both modules' own
// embedded locales/*.json, driven to the stage where the catalog is validated.
// Each module renders its own refusals, so each declares its own messages: a key
// is namespaced by the module that owns it, and this case is what holds the two
// key sets disjoint.
func TestTheInboxAndPreferencesCatalogsMergeUnderOneEngine(t *testing.T) {
	inboxModule, preferencesModule := inbox.Module(), preferences.Module()
	// Both modules read their own section of one configuration document, and so
	// does the engine; the document itself names nothing, because every item all
	// three carry a default for.
	reader := authntest.ConfigReaderAll(t, `{}`, inboxModule, preferencesModule, i18ncatalog.Module())

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(i18ncatalog.Module())
	reg.Register(inboxModule)
	reg.Register(preferencesModule)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: []string{i18ncatalog.ModuleName, inbox.ModuleName, preferences.ModuleName},
	})
	// The collision is the failure this case exists to catch, and it is named
	// apart from every other one the drive can end with, so a case that broke the
	// assembly some other way is not read as the collision being gone.
	if errors.Is(err, i18ncatalog.ErrDuplicateKey) {
		t.Fatalf("the engine refused the two modules' catalogs as a collision: %v", err)
	}
	if err != nil {
		t.Fatalf("driving the inbox, the preferences module and the engine to prepare failed: %v", err)
	}
}
