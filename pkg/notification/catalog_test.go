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

// TestTheInboxAndPreferencesCatalogsMergeUnderOneEngine holds that the inbox
// and preferences catalogs merge into one i18n index.
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
	if errors.Is(err, i18ncatalog.ErrDuplicateKey) {
		t.Fatalf("the engine refused the two modules' catalogs as a collision: %v", err)
	}
	if err != nil {
		t.Fatalf("driving the inbox, the preferences module and the engine to prepare failed: %v", err)
	}
}
