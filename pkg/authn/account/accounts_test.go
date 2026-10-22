package account

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/event"
)

var admin = authn.Verification{Via: authn.VerifiedViaAdmin}

func TestCreateWritesAnEnabledAccountFoundByEverySpellingOfItsIdentifiers(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		created, err := h.p.Create(ctx, authn.CreateAccount{
			DisplayName: "Alice A.", Username: "Ａlice", Email: "Alice@Example.COM", EmailVerifiedVia: admin,
			Phone: "+8613812345678", PhoneVerifiedVia: authn.Verification{Via: authn.VerifiedViaSMSCode},
			Attributes: map[string]string{"region": "eu"}, MFARequired: true, Via: "import",
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		if created.Status != authn.StatusActive || created.Kind != authn.KindUser || !created.MFARequired ||
			created.Protected || created.PasswordSet || created.DisplayName != "Alice A." ||
			created.Attributes["region"] != "eu" {
			t.Errorf("the account is %+v", created)
		}
		if !created.ValidAfter.Equal(created.CreatedAt) || !created.CreatedAt.Equal(h.clock.read()) {
			t.Errorf("created %v and valid after %v, want both the clock's %v", created.CreatedAt, created.ValidAfter, h.clock.read())
		}
		if _, err = uuid.Parse(created.ID.String()); err != nil || uuid.MustParse(created.ID.String()).Version() != 7 {
			t.Errorf("the generated identifier %q is not a UUIDv7 text", created.ID)
		}

		got, err := h.p.Get(ctx, created.ID)
		if err != nil || got.ID != created.ID || got.DisplayName != "Alice A." || got.Attributes["region"] != "eu" {
			t.Fatalf("reading back: %+v, %v", got, err)
		}
		for _, c := range []struct {
			kind  authn.IdentifierKind
			value string
		}{
			{authn.IdentifierUsername, "alice"},
			{authn.IdentifierUsername, "ALICE"},
			{authn.IdentifierUsername, "ＡＬＩＣＥ"},
			{authn.IdentifierEmail, "alice@example.com"},
			{authn.IdentifierEmail, " ALICE@example.com "},
			{authn.IdentifierPhone, "+8613812345678"},
		} {
			id, lerr := h.p.Lookup(ctx, c.kind, c.value)
			if lerr != nil || id != created.ID {
				t.Errorf("looking %s %q up gave %q and %v, want %q", c.kind, c.value, id, lerr, created.ID)
			}
		}

		identifiers, err := h.p.Identifiers(ctx, created.ID)
		if err != nil || len(identifiers) != 3 {
			t.Fatalf("the identifiers are %+v, %v", identifiers, err)
		}
		byKind := map[authn.IdentifierKind]authn.Identifier{}
		for _, i := range identifiers {
			byKind[i.Kind] = i
			if !i.VerifiedAt.Equal(h.clock.read()) || i.ID == "" {
				t.Errorf("identifier %+v has no verification time or no identifier", i)
			}
		}
		if byKind[authn.IdentifierUsername].Value != "alice" || byKind[authn.IdentifierEmail].Value != "alice@example.com" ||
			byKind[authn.IdentifierEmail].VerifiedVia != authn.VerifiedViaAdmin ||
			byKind[authn.IdentifierPhone].VerifiedVia != authn.VerifiedViaSMSCode {
			t.Errorf("the identifiers are %+v", byKind)
		}

		events := h.bus.of(authn.EventAccountCreated)
		if len(events) != 1 {
			t.Fatalf("%d created events, want one", len(events))
		}
		var payload authn.AccountCreated
		if err = event.DecodePayload(events[0].Payload, &payload); err != nil ||
			payload != (authn.AccountCreated{AccountID: created.ID.String(), Kind: "user", Via: "import"}) {
			t.Errorf("the created event carries %+v, %v", payload, err)
		}
		if events[0].TenantID != "" {
			t.Errorf("the created event carries the tenant %q", events[0].TenantID)
		}
	})
}

func TestCreateTakesAnImportedIdentifierAndRefusesOnesThatCannotBe(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		created, err := h.p.Create(ctx, authn.CreateAccount{ID: "legacy_42", Username: "legacy42"})
		if err != nil || created.ID != "legacy_42" {
			t.Fatalf("an imported identifier gave %+v, %v", created, err)
		}
		if _, err = h.p.Create(ctx, authn.CreateAccount{ID: "legacy_42", Username: "other"}); !errors.Is(err, authn.ErrAccountExists) {
			t.Errorf("a second account with one identifier gave %v, want ErrAccountExists", err)
		}
		if got := h.count(tableIdentifiers, "kind = ? AND value = ?", "username", "other"); got != 0 {
			t.Errorf("the refused account left %d identifier row(s)", got)
		}
		for _, id := range []authn.AccountID{"static.root", "has space", "ünï", authn.AccountID(strings.Repeat("a", 65))} {
			if _, err = h.p.Create(ctx, authn.CreateAccount{ID: id}); !errors.Is(err, authn.ErrInvalidAccountID) {
				t.Errorf("the identifier %q gave %v, want ErrInvalidAccountID", id, err)
			}
		}
	})
}

func TestCreateRefusesWhatItAdmitsNot(t *testing.T) {
	long := strings.Repeat("x", 300)
	tooMany := map[string]string{}
	for i := range 25 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	for _, c := range []struct {
		name string
		in   authn.CreateAccount
		want error
	}{
		{"a kind that is neither", authn.CreateAccount{Kind: "robot"}, authn.ErrInvalidArgument},
		{"an email address with no way it was verified", authn.CreateAccount{Email: "a@example.com"}, authn.ErrInvalidArgument},
		{"an email address verified in a way that is none", authn.CreateAccount{Email: "a@example.com", EmailVerifiedVia: authn.Verification{Via: "trust-me"}}, authn.ErrInvalidArgument},
		{"a phone number with no way it was verified", authn.CreateAccount{Phone: "+8613812345678"}, authn.ErrInvalidArgument},
		{"an idp proof with no provider", authn.CreateAccount{Email: "a@example.com", EmailVerifiedVia: authn.Verification{Via: "idp:"}}, authn.ErrInvalidArgument},
		{"a username that is not one", authn.CreateAccount{Username: "no"}, authn.ErrInvalidIdentifier},
		{"an email address that is not one", authn.CreateAccount{Email: "nobody", EmailVerifiedVia: admin}, authn.ErrInvalidIdentifier},
		{"a phone number that is not E.164", authn.CreateAccount{Phone: "0138", PhoneVerifiedVia: admin}, authn.ErrInvalidIdentifier},
		{"a display name of 101 characters", authn.CreateAccount{DisplayName: strings.Repeat("x", 101)}, authn.ErrInvalidArgument},
		{"a way the account came about that is none", authn.CreateAccount{Via: "magic"}, authn.ErrInvalidArgument},
		{"too many attributes", authn.CreateAccount{Attributes: tooMany}, authn.ErrInvalidArgument},
		{"an attribute under idp.", authn.CreateAccount{Attributes: map[string]string{"idp.dept": "x"}}, authn.ErrInvalidArgument},
		{"an attribute under authn.", authn.CreateAccount{Attributes: map[string]string{"authn.flag": "x"}}, authn.ErrInvalidArgument},
		{"an attribute key in capitals", authn.CreateAccount{Attributes: map[string]string{"Region": "x"}}, authn.ErrInvalidArgument},
		{"an attribute value of 257 bytes", authn.CreateAccount{Attributes: map[string]string{"k": long[:257]}}, authn.ErrInvalidArgument},
		{"a password and a hash", authn.CreateAccount{Username: "alice1", Password: "correct horse battery", PasswordHash: "$argon2id$x"}, authn.ErrInvalidArgument},
		{"a password on a service account", authn.CreateAccount{Kind: authn.KindService, Username: "svc1", Password: "correct horse battery"}, authn.ErrInvalidArgument},
		{"a password with no login name", authn.CreateAccount{Phone: "+8613812345678", PhoneVerifiedVia: admin, Password: "correct horse battery"}, authn.ErrIdentifierRequired},
		{"a hash with no login name", authn.CreateAccount{PasswordHash: "$argon2id$x"}, authn.ErrIdentifierRequired},
		{"a password shorter than eight", authn.CreateAccount{Username: "alice1", Password: "short"}, authn.ErrPasswordPolicy},
		{"a password that is the username", authn.CreateAccount{Username: "alice-the-user", Password: "ALICE-the-user"}, authn.ErrPasswordPolicy},
		{"a password that is the email address", authn.CreateAccount{Email: "alice@example.com", EmailVerifiedVia: admin, Password: "Alice@Example.com"}, authn.ErrPasswordPolicy},
		{"a password past 128 bytes", authn.CreateAccount{Username: "alice1", Password: strings.Repeat("p", 129)}, authn.ErrPasswordPolicy},
		{"a hash that is not readable", authn.CreateAccount{Username: "alice1", PasswordHash: "$argon2id$v=19$m=999999999,t=1,p=1$AAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA"}, authn.ErrInvalidCredentials},
	} {
		t.Run(c.name, func(t *testing.T) {
			forEachDialect(t, func(t *testing.T, h *harness) {
				_, err := h.p.Create(context.Background(), c.in)
				if !errors.Is(err, c.want) {
					t.Errorf("%s: gave %v, want %v", c.name, err, c.want)
				}
				if n := h.count(tableAccounts, ""); n != 0 {
					t.Errorf("%s: left %d account row(s)", c.name, n)
				}
				if n := h.count(tableIdentifiers, ""); n != 0 {
					t.Errorf("%s: left %d identifier row(s)", c.name, n)
				}
			})
		})
	}
}

func TestAnAccountNobodyEnabledCannotAuthenticate(t *testing.T) {
	if authn.AccountStatus(0) != authn.StatusDisabled || model.AccountStatus(0) != model.StatusDisabled {
		t.Fatal("the zero value of AccountStatus is not disabled")
	}
	forEachDialect(t, func(t *testing.T, h *harness) {
		id := h.user("nobody", "correct horse battery")
		if err := h.handle.Exec("UPDATE authn_accounts SET status = 0 WHERE id = ?", id.String()).Error; err != nil {
			t.Fatalf("disabling by hand: %v", err)
		}
		if _, _, err := h.p.Verify(context.Background(), "nobody", "correct horse battery"); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("a disabled account verified with %v, want ErrInvalidCredentials", err)
		}
		// A row written with no status at all reads as disabled, since the
		// column's default is the zero value.
		now := h.clock.read()
		err := h.handle.Exec("INSERT INTO authn_accounts (id, kind, valid_after, created_at, updated_at) VALUES (?, 'user', ?, ?, ?)",
			"zero_status", now, now, now).Error
		if err != nil {
			t.Fatalf("inserting a row with no status: %v", err)
		}
		got, err := h.p.Get(context.Background(), "zero_status")
		if err != nil || got.Status != authn.StatusDisabled {
			t.Errorf("a row with no status reads %v (%v), want disabled", got.Status, err)
		}
	})
}

func TestTwoCreatesOfOneEmailLeaveOneAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		const callers = 8
		var wg sync.WaitGroup
		errs := make([]error, callers)
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = h.p.Create(context.Background(), authn.CreateAccount{
					Username: fmt.Sprintf("racer%d", i), Email: "same@example.com", EmailVerifiedVia: admin,
				})
			}()
		}
		wg.Wait()
		won := 0
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, authn.ErrContactTaken):
				t.Errorf("a loser got %v, want ErrContactTaken", err)
			}
		}
		if won != 1 {
			t.Errorf("%d creates won, want one", won)
		}
		if n := h.count(tableAccounts, ""); n != 1 {
			t.Errorf("%d account rows, want one: a loser's account row was not rolled back", n)
		}
		if n := h.count(tableIdentifiers, "kind = 'username'"); n != 1 {
			t.Errorf("%d username rows, want one", n)
		}
	})
}

func TestReadsReportWhatIsNotThereAsNotFound(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("gone", "")
		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		for _, missing := range []authn.AccountID{id, "never-was", "static.root"} {
			if _, err := h.p.Get(ctx, missing); !errors.Is(err, authn.ErrAccountNotFound) {
				t.Errorf("Get(%q) = %v, want ErrAccountNotFound", missing, err)
			}
			if _, err := h.p.Identifiers(ctx, missing); !errors.Is(err, authn.ErrAccountNotFound) {
				t.Errorf("Identifiers(%q) = %v, want ErrAccountNotFound", missing, err)
			}
			if _, err := h.p.Externals(ctx, missing); !errors.Is(err, authn.ErrAccountNotFound) {
				t.Errorf("Externals(%q) = %v, want ErrAccountNotFound", missing, err)
			}
			if _, err := h.p.ListServiceSecrets(ctx, missing); !errors.Is(err, authn.ErrAccountNotFound) {
				t.Errorf("ListServiceSecrets(%q) = %v, want ErrAccountNotFound", missing, err)
			}
		}
		if _, err := h.p.Get(ctx, "bad id"); !errors.Is(err, authn.ErrInvalidAccountID) {
			t.Errorf("Get of a malformed identifier = %v, want ErrInvalidAccountID", err)
		}
		if _, err := h.p.Lookup(ctx, authn.IdentifierUsername, "gone"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("Lookup of a deleted account's username = %v, want ErrAccountNotFound", err)
		}
		if _, err := h.p.Lookup(ctx, authn.IdentifierEmail, "not an address"); !errors.Is(err, authn.ErrInvalidIdentifier) {
			t.Errorf("Lookup of a malformed address = %v, want ErrInvalidIdentifier", err)
		}
		if _, err := h.p.ByExternal(ctx, "google", "nobody"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("ByExternal of an unbound identity = %v, want ErrAccountNotFound", err)
		}
	})
}

// seedAccounts writes n accounts straight into the table, faster than creating
// each, with identifiers that sort in the order of the number.
func seedAccounts(t *testing.T, h *harness, n int, kind authn.AccountKind) []string {
	t.Helper()
	now := h.clock.read()
	rows := make([]accountRow, 0, n)
	ids := make([]string, 0, n)
	for i := range n {
		id := fmt.Sprintf("seed-%05d", i)
		ids = append(ids, id)
		rows = append(rows, accountRow{
			ID: id, Kind: string(kind), Status: statusActive, Attributes: "{}", ValidAfter: now, CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := h.handle.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatalf("seeding accounts: %v", err)
	}
	return ids
}

func TestListPagesByKeyAndNeverReadsMoreThanItIsAskedFor(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		want := seedAccounts(t, h, 1005, authn.KindUser)

		page, next, err := h.p.List(ctx, authn.AccountQuery{})
		if err != nil || len(page) != authn.DefaultPageLimit || next != page[len(page)-1].ID.String() {
			t.Fatalf("the first page has %d accounts, next %q, %v: want %d", len(page), next, err, authn.DefaultPageLimit)
		}
		page, next, err = h.p.List(ctx, authn.AccountQuery{Limit: 5000})
		if err != nil || len(page) != authn.MaxPageLimit || next == "" {
			t.Fatalf("a limit of 5000 gave %d accounts, next %q, %v: want the ceiling of %d", len(page), next, err, authn.MaxPageLimit)
		}

		var got []string
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > 20 {
				t.Fatal("the cursor never ended")
			}
			batch, nxt, lerr := h.p.List(ctx, authn.AccountQuery{After: cursor, Limit: 400})
			if lerr != nil {
				t.Fatalf("listing: %v", lerr)
			}
			for _, a := range batch {
				got = append(got, a.ID.String())
			}
			if nxt == "" {
				break
			}
			cursor = nxt
		}
		if !slices.Equal(got, want) {
			t.Errorf("the pages hold %d accounts, want the %d in order of identifier, each once", len(got), len(want))
		}

		exact, next, err := h.p.List(ctx, authn.AccountQuery{After: "seed-01000", Limit: 4})
		if err != nil || len(exact) != 4 || next != "" {
			t.Errorf("the last four gave %d accounts, next %q, %v, want four and no cursor", len(exact), next, err)
		}
		rec := h.recordSQL()
		if _, _, err = h.p.List(ctx, authn.AccountQuery{Limit: 3}); err != nil {
			t.Fatalf("listing: %v", err)
		}
		for _, s := range rec.take() {
			if strings.Contains(strings.ToUpper(s), "OFFSET") {
				t.Errorf("a page was read by offset: %s", s)
			}
		}
	})
}

func TestListFiltersAndLeavesTombstonesOutUnlessAsked(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		active := h.user("lively", "correct horse battery")
		disabled := h.user("sleepy", "")
		ended := h.user("ended", "")
		service, err := h.p.Create(ctx, authn.CreateAccount{Kind: authn.KindService, Username: "svc-one"})
		if err != nil {
			t.Fatalf("creating the service account: %v", err)
		}
		if err = h.p.SetStatus(ctx, disabled, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		if err = h.p.Delete(ctx, ended, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}

		ids := func(q authn.AccountQuery) []string {
			t.Helper()
			page, _, lerr := h.p.List(ctx, q)
			if lerr != nil {
				t.Fatalf("listing %+v: %v", q, lerr)
			}
			out := make([]string, 0, len(page))
			for _, a := range page {
				out = append(out, a.ID.String())
			}
			slices.Sort(out)
			return out
		}
		sorted := func(in ...authn.AccountID) []string {
			out := make([]string, 0, len(in))
			for _, id := range in {
				out = append(out, id.String())
			}
			slices.Sort(out)
			return out
		}
		deleted, disabledStatus := authn.StatusDeleted, authn.StatusDisabled
		if got, want := ids(authn.AccountQuery{}), sorted(active, disabled, service.ID); !slices.Equal(got, want) {
			t.Errorf("the default listing is %v, want %v: the tombstone is left out", got, want)
		}
		if got, want := ids(authn.AccountQuery{Status: &deleted}), sorted(ended); !slices.Equal(got, want) {
			t.Errorf("listing the deleted gave %v, want %v", got, want)
		}
		if got, want := ids(authn.AccountQuery{Status: &disabledStatus}), sorted(disabled); !slices.Equal(got, want) {
			t.Errorf("listing the disabled gave %v, want %v", got, want)
		}
		if got, want := ids(authn.AccountQuery{Kind: authn.KindService}), sorted(service.ID); !slices.Equal(got, want) {
			t.Errorf("listing the service accounts gave %v, want %v", got, want)
		}
		page, _, err := h.p.List(ctx, authn.AccountQuery{Kind: authn.KindUser, Limit: 10})
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		for _, a := range page {
			if a.PasswordSet != (a.ID == active) {
				t.Errorf("account %s reads PasswordSet %v in a listing", a.ID, a.PasswordSet)
			}
		}
		for _, bad := range []authn.AccountQuery{
			{Kind: "robot"}, {Status: func() *authn.AccountStatus { s := authn.AccountStatus(9); return &s }()},
		} {
			if _, _, err = h.p.List(ctx, bad); !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("listing %+v gave %v, want ErrInvalidArgument", bad, err)
			}
		}
	})
}

func TestListByIdentifierPrefixReadsARangeAndMatchesNoPattern(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		alice := h.user("alice", "")
		alicia := h.user("alicia", "")
		bob := h.user("bob-b", "")
		names := func(q authn.AccountQuery) []string {
			t.Helper()
			page, _, err := h.p.List(ctx, q)
			if err != nil {
				t.Fatalf("listing %+v: %v", q, err)
			}
			out := make([]string, 0, len(page))
			for _, a := range page {
				out = append(out, a.ID.String())
			}
			slices.Sort(out)
			return out
		}
		both := []string{alice.String(), alicia.String()}
		slices.Sort(both)
		for prefix, want := range map[string][]string{
			"ali": both, "alic": both, "ALIC": both, "alice": {alice.String()}, "alicia": {alicia.String()},
			"bob": {bob.String()}, "zed": {},
		} {
			got := names(authn.AccountQuery{IdentifierKind: authn.IdentifierUsername, IdentifierPrefix: prefix})
			if !slices.Equal(got, want) {
				t.Errorf("the username prefix %q lists %v, want %v", prefix, got, want)
			}
		}
		if got := names(authn.AccountQuery{IdentifierKind: authn.IdentifierEmail, IdentifierPrefix: "bob-b@"}); !slices.Equal(got, []string{bob.String()}) {
			t.Errorf("the email prefix lists %v, want only bob", got)
		}
		// A pattern character is a character: it matches itself and nothing else.
		for _, prefix := range []string{"%%%", "___", "a%c"} {
			if got := names(authn.AccountQuery{IdentifierKind: authn.IdentifierEmail, IdentifierPrefix: prefix}); len(got) != 0 {
				t.Errorf("the prefix %q listed %v: it was read as a pattern", prefix, got)
			}
		}

		var after []string
		cursor := ""
		for range 5 {
			page, next, err := h.p.List(ctx, authn.AccountQuery{
				IdentifierKind: authn.IdentifierUsername, IdentifierPrefix: "ali", After: cursor, Limit: 1,
			})
			if err != nil {
				t.Fatalf("paging by prefix: %v", err)
			}
			for _, a := range page {
				after = append(after, a.ID.String())
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if !slices.Equal(after, both) {
			t.Errorf("paging a prefix one at a time gave %v, want %v", after, both)
		}

		for _, c := range []struct {
			name string
			q    authn.AccountQuery
			want error
		}{
			{"a prefix of two characters", authn.AccountQuery{IdentifierKind: authn.IdentifierUsername, IdentifierPrefix: "al"}, authn.ErrInvalidArgument},
			{"a prefix with no kind", authn.AccountQuery{IdentifierPrefix: "alice"}, authn.ErrInvalidArgument},
			{"a kind with no prefix", authn.AccountQuery{IdentifierKind: authn.IdentifierUsername}, authn.ErrInvalidArgument},
			{"a prefix that no username has", authn.AccountQuery{IdentifierKind: authn.IdentifierUsername, IdentifierPrefix: "a%c"}, authn.ErrInvalidIdentifier},
		} {
			if _, _, err := h.p.List(ctx, c.q); !errors.Is(err, c.want) {
				t.Errorf("%s: gave %v, want %v", c.name, err, c.want)
			}
		}

		rec := h.recordSQL()
		if _, _, err := h.p.List(ctx, authn.AccountQuery{IdentifierKind: authn.IdentifierUsername, IdentifierPrefix: "ali"}); err != nil {
			t.Fatalf("listing: %v", err)
		}
		for _, s := range rec.take() {
			if strings.Contains(strings.ToUpper(s), "LIKE") {
				t.Errorf("a listing by prefix used a pattern match: %s", s)
			}
		}
	})
}

// A listing by a display-name prefix matches the beginning of the stored name,
// as written. The display name is not normalized, so the match is
// case-sensitive; it is a prefix and never a substring; and it has no minimum
// length, so a single character is honored. No index covers the column, so the
// read strips the table by a range comparison and never by a pattern.
func TestListByDisplayNamePrefixMatchesTheStoredName(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		create := func(display, username string) authn.AccountID {
			t.Helper()
			a, err := h.p.Create(ctx, authn.CreateAccount{DisplayName: display, Username: username, Via: "admin"})
			if err != nil {
				t.Fatalf("creating %q: %v", display, err)
			}
			return a.ID
		}
		alice := create("Alice Armstrong", "alice-arm")
		alicia := create("Alicia Brown", "alicia-br")
		bob := create("Bob Carter", "bob-cart")
		nameless := create("", "no-name")

		names := func(prefix string) []string {
			t.Helper()
			page, _, err := h.p.List(ctx, authn.AccountQuery{DisplayNamePrefix: prefix})
			if err != nil {
				t.Fatalf("listing display_name_prefix=%q: %v", prefix, err)
			}
			out := make([]string, 0, len(page))
			for _, a := range page {
				out = append(out, a.ID.String())
			}
			slices.Sort(out)
			return out
		}
		both := []string{alice.String(), alicia.String()}
		slices.Sort(both)
		for prefix, want := range map[string][]string{
			"A":       both, // no minimum length: one character is honored
			"Ali":     both, // a hit spanning two accounts
			"Alic":    both,
			"Alice":   {alice.String()},
			"Bob":     {bob.String()},
			"bob":     {}, // case-sensitive: the name is stored as "Bob"
			"Brown":   {}, // a prefix, not a substring: "Brown" is inside, not at the start
			"mstrong": {},
			"Zed":     {}, // a value no name begins with
		} {
			if got := names(prefix); !slices.Equal(got, want) {
				t.Errorf("the display-name prefix %q lists %v, want %v", prefix, got, want)
			}
		}
		// An empty prefix is no filter: every account is listed.
		all := []string{alice.String(), alicia.String(), bob.String(), nameless.String()}
		slices.Sort(all)
		if got := names(""); !slices.Equal(got, all) {
			t.Errorf("an empty display-name prefix lists %v, want every account %v", got, all)
		}
		// The name is stored as written: a byte prefix of it matches, and the
		// empty-name account is selected only by the empty (no-filter) prefix.
		if got := names("Alice Armstrong"); !slices.Equal(got, []string{alice.String()}) {
			t.Errorf("the full stored name as a prefix lists %v, want only %s", got, alice)
		}

		rec := h.recordSQL()
		if _, _, err := h.p.List(ctx, authn.AccountQuery{DisplayNamePrefix: "Ali"}); err != nil {
			t.Fatalf("listing by display name: %v", err)
		}
		for _, s := range rec.take() {
			if strings.Contains(strings.ToUpper(s), "LIKE") {
				t.Errorf("a listing by display name used a pattern match: %s", s)
			}
		}
	})
}

// The bound that closes a display-name range raises the prefix's last character,
// not its last byte: a prefix that ends in a multi-byte character must yield a
// bound that is itself valid UTF-8, or the range reads a byte sequence the
// database rejects and the answer is an outage rather than a shorter page. A
// prefix that is every character the highest rune has no bound above it.
func TestDisplayNameBoundRaisesTheLastCharacter(t *testing.T) {
	const max = "\U0010FFFF"
	for _, c := range []struct {
		in    string
		want  string
		bound bool
	}{
		{"", "", false}, // no prefix names no name
		{"A", "B", true},
		{"Ali", "Alj", true},
		{"alice", "alicf", true},
		{"Á", "Â", true},               // a two-byte character is raised as a character
		{"Alicia Á", "Alicia Â", true}, // and the raise keeps the leading ASCII as it is
		{"\uD7FF", "\uE000", true},     // the character below the surrogate range is raised past it, not into it
		{"A\uD7FF", "A\uE000", true},   // the surrogate range is skipped wherever the character sits
		{max, "", false},               // the highest rune alone bounds nothing above it
		{max + max, "", false},
		{"A" + max, "B", true}, // a lower character is carried to and raised
		{"Ali" + max, "Alj", true},
	} {
		got, bounded := displayNameBound(c.in)
		if got != c.want || bounded != c.bound {
			t.Errorf("displayNameBound(%q) = %q, %v, want %q, %v", c.in, got, bounded, c.want, c.bound)
		}
	}
}

// A bound is computed on the read seam directly, not only behind the route's
// shape check, and every bound it returns must be valid UTF-8: a byte sequence
// the database rejects would turn a shorter page into an outage. The bytes that
// begin no rune may fall anywhere in the prefix, not only at its end, so the
// cases below put them in the middle, at the tail and alone alike.
func TestDisplayNameBoundIsAlwaysValidUTF8(t *testing.T) {
	for _, in := range []string{
		"\xbf",         // a lone continuation byte
		"\xff",         // a byte that is no rune and cannot be raised
		"\xff\xfe",     // two of them
		"A\xbf",        // ASCII then a stray continuation byte
		"caf\xc3",      // a truncated two-byte character at the end
		"\xe4\xb8",     // a truncated three-byte character
		"\xf4\x8f\xbf", // a truncated four-byte character
		"a\x80b\xc0",   // a byte that begins no rune in the middle and at the end
		"\xbfA",        // the offending byte is not the last one
		"\x80xy",       // a stray continuation byte before a valid tail
		"a\x80bc",      // it sits between valid characters
		"\xffZ",        // a byte that is no rune before a valid tail
	} {
		if utf8.ValidString(in) {
			t.Fatalf("the case %q is valid UTF-8; it does not exercise the branch", in)
		}
		got, _ := displayNameBound(in)
		if !utf8.ValidString(got) {
			t.Errorf("displayNameBound(%q) = %q, which is not valid UTF-8", in, got)
		}
	}
}

// A display-name prefix whose every character is the highest rune has no bound
// above it, and the names it begins are exactly those at or above it. The read
// must return those rows rather than the empty set a degenerate upper bound — the
// empty string — would close it to. This is the whole point of the branch: the
// names are silently dropped otherwise, with no error to see.
func TestListByAnUppermostDisplayNamePrefixReadsItsNames(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const max = "\U0010FFFF"
		create := func(display, username string) authn.AccountID {
			t.Helper()
			a, err := h.p.Create(ctx, authn.CreateAccount{DisplayName: display, Username: username, Via: "admin"})
			if err != nil {
				t.Fatalf("creating %q: %v", display, err)
			}
			return a.ID
		}
		upper := create(max+"Zoe", "zoe-upper")
		create("Alice", "alice-plain")
		create("Zoe", "zoe-plain")

		page, _, err := h.p.List(ctx, authn.AccountQuery{DisplayNamePrefix: max})
		if err != nil {
			t.Fatalf("listing display_name_prefix=%q: %v", max, err)
		}
		got := make([]string, 0, len(page))
		for _, a := range page {
			got = append(got, a.ID.String())
		}
		if !slices.Equal(got, []string{upper.String()}) {
			t.Errorf("the uppermost prefix listed %v, want only %s", got, upper)
		}
	})
}

// A prefix that is not valid UTF-8 is text the column never holds, so the read
// seam returns no name for it rather than handing the database a byte sequence it
// rejects. The management route refuses such a prefix as a shape error before it
// reaches here.
func TestListByANonUTF8DisplayNamePrefixReadsNothing(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		if _, err := h.p.Create(ctx, authn.CreateAccount{DisplayName: "€uro", Username: "euro", Via: "admin"}); err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		page, next, err := h.p.List(ctx, authn.AccountQuery{DisplayNamePrefix: "\xbf"})
		if err != nil || len(page) != 0 || next != "" {
			t.Errorf("a non-UTF-8 prefix read %d account(s), next %q, %v, want none", len(page), next, err)
		}
	})
}

func TestAccountFieldsAreWrittenOnTheAccountThatIsThere(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("fields", "")
		if err := h.p.SetDisplayName(ctx, id, strings.Repeat("名", 100)); err != nil {
			t.Fatalf("a display name of 100 characters of three bytes each gave %v", err)
		}
		if err := h.p.SetDisplayName(ctx, id, strings.Repeat("名", 101)); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("a display name of 101 characters gave %v, want ErrInvalidArgument", err)
		}
		if err := h.p.SetDisplayName(ctx, id, "bad\xffname"); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("a display name that is not text gave %v, want ErrInvalidArgument", err)
		}
		if err := h.p.SetProtected(ctx, id, true); err != nil {
			t.Fatalf("protecting: %v", err)
		}
		if err := h.p.SetMFARequired(ctx, id, true); err != nil {
			t.Fatalf("requiring a factor: %v", err)
		}
		if err := h.p.SetAttributes(ctx, id, map[string]string{"tier": "gold", "region": "eu"}); err != nil {
			t.Fatalf("setting attributes: %v", err)
		}
		if err := h.p.SetAttributes(ctx, id, map[string]string{"tier": "silver"}); err != nil {
			t.Fatalf("replacing attributes: %v", err)
		}
		got, err := h.p.Get(ctx, id)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if got.DisplayName != strings.Repeat("名", 100) || !got.Protected || !got.MFARequired ||
			!maps.Equal(got.Attributes, map[string]string{"tier": "silver"}) {
			t.Errorf("the account reads %+v", got)
		}
		if err = h.p.SetAttributes(ctx, id, nil); err != nil {
			t.Fatalf("clearing the attributes: %v", err)
		}
		if got, _ = h.p.Get(ctx, id); len(got.Attributes) != 0 {
			t.Errorf("cleared attributes read %v", got.Attributes)
		}
		if err = h.p.SetProtected(ctx, id, false); err != nil {
			t.Fatalf("unprotecting: %v", err)
		}
		if got, _ = h.p.Get(ctx, id); got.Protected {
			t.Error("the account is still protected")
		}

		if err = h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		for name, call := range map[string]func() error{
			"SetDisplayName": func() error { return h.p.SetDisplayName(ctx, id, "x") },
			"SetAttributes":  func() error { return h.p.SetAttributes(ctx, id, nil) },
			"SetProtected":   func() error { return h.p.SetProtected(ctx, id, true) },
			"SetMFARequired": func() error { return h.p.SetMFARequired(ctx, id, true) },
			"on nobody":      func() error { return h.p.SetProtected(ctx, "nobody-at-all", true) },
		} {
			if err := call(); !errors.Is(err, authn.ErrAccountNotFound) {
				t.Errorf("%s on a tombstone gave %v, want ErrAccountNotFound", name, err)
			}
		}
		if got := h.count(tableAccounts, "id = ? AND (display_name <> '' OR protected OR mfa_required)", id.String()); got != 0 {
			t.Errorf("a write after the deletion put personal data back on the tombstone")
		}
	})
}

func TestAttributeSetsAreHeldToTheBoundsOfTheModel(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("attrs", "")
		full := map[string]string{}
		for i := range model.AccountAttributesMax {
			full[fmt.Sprintf("k%02d", i)] = "v"
		}
		if err := h.p.SetAttributes(ctx, id, full); err != nil {
			t.Fatalf("24 attributes gave %v", err)
		}
		over := maps.Clone(full)
		over["k24"] = "v"
		bytesOver := map[string]string{}
		for i := range 9 {
			bytesOver[fmt.Sprintf("big%d", i)] = strings.Repeat("v", model.AccountAttributeValueMax)
		}
		for name, attrs := range map[string]map[string]string{
			"25 attributes":               over,
			"a reserved idp prefix":       {"idp.dept": "x"},
			"a reserved authn prefix":     {"authn.flag": "x"},
			"a key in capitals":           {"Tier": "x"},
			"an empty key":                {"": "x"},
			"a key of 65 bytes":           {strings.Repeat("k", 65): "x"},
			"a value of 257 bytes":        {"k": strings.Repeat("v", 257)},
			"a value with a control byte": {"k": "a\x01b"},
			"a value that is not text":    {"k": "a\xffb"},
			"keys and values over 2048 B": bytesOver,
		} {
			if err := h.p.SetAttributes(ctx, id, attrs); !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("%s gave %v, want ErrInvalidArgument", name, err)
			}
		}
		got, err := h.p.Get(ctx, id)
		if err != nil || len(got.Attributes) != model.AccountAttributesMax {
			t.Errorf("a refused set changed the stored one: %d attributes, %v", len(got.Attributes), err)
		}
	})
}

func TestIdentifiersAreAddedAndRemovedAndAPhoneIsReplaced(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("holder", "")
		other := h.user("rival", "")
		smsCode := authn.Verification{Via: authn.VerifiedViaSMSCode}

		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierPhone, "+8613800000001", smsCode); err != nil {
			t.Fatalf("adding a phone number: %v", err)
		}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierPhone, "+8613800000002", smsCode); err != nil {
			t.Fatalf("replacing the phone number: %v", err)
		}
		if _, err := h.p.Lookup(ctx, authn.IdentifierPhone, "+8613800000001"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("the replaced number is still found: %v", err)
		}
		if got, err := h.p.Lookup(ctx, authn.IdentifierPhone, "+8613800000002"); err != nil || got != id {
			t.Errorf("the new number is found at %q, %v", got, err)
		}
		if n := h.count(tableIdentifiers, "account_id = ? AND kind = 'phone'", id.String()); n != 1 {
			t.Errorf("the account has %d phone rows, want one", n)
		}
		changes := h.bus.of(authn.EventIdentifierChanged)
		var seen []string
		for _, e := range changes {
			var p authn.IdentifierChanged
			if err := event.DecodePayload(e.Payload, &p); err != nil || p.AccountID != id.String() || p.Kind != "phone" {
				continue
			}
			seen = append(seen, p.Change)
		}
		if !slices.Equal(seen, []string{"added", "removed", "added"}) {
			t.Errorf("the phone events are %v, want added, then the replacement as removed and added", seen)
		}

		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierPhone, "+8613800000002", admin); err != nil {
			t.Errorf("the same number again gave %v, want no change", err)
		}
		held, _ := h.p.Identifiers(ctx, id)
		for _, i := range held {
			if i.Kind == authn.IdentifierPhone && i.VerifiedVia != authn.VerifiedViaAdmin {
				t.Errorf("the same number again left its proof %q", i.VerifiedVia)
			}
		}
		if got := len(h.bus.of(authn.EventIdentifierChanged)); got != len(changes) {
			t.Errorf("the same number again published %d more event(s)", got-len(changes))
		}

		if err := h.p.AddIdentifier(ctx, other, authn.IdentifierPhone, "+8613800000002", smsCode); !errors.Is(err, authn.ErrAccountExists) {
			t.Errorf("another account's number gave %v, want ErrAccountExists", err)
		}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "second@example.com", admin); err != nil {
			t.Errorf("a second email address gave %v: an account may have any number", err)
		}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierUsername, "holder2", admin); !errors.Is(err, authn.ErrAccountExists) {
			t.Errorf("a second username gave %v, want ErrAccountExists: an account has one", err)
		}
		if err := h.p.AddIdentifier(ctx, other, authn.IdentifierEmail, "SECOND@example.com", admin); !errors.Is(err, authn.ErrContactTaken) {
			t.Errorf("an email address another account holds gave %v, want ErrContactTaken", err)
		}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "third@example.com", authn.Verification{}); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("an identifier with no proof gave %v, want ErrInvalidArgument", err)
		}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "bad address", admin); !errors.Is(err, authn.ErrInvalidIdentifier) {
			t.Errorf("a malformed address gave %v, want ErrInvalidIdentifier", err)
		}
		if err := h.p.AddIdentifier(ctx, "nobody-at-all", authn.IdentifierEmail, "x@example.com", admin); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("an identifier on nobody gave %v, want ErrAccountNotFound", err)
		}

		held, _ = h.p.Identifiers(ctx, id)
		var emailID, foreignID string
		for _, i := range held {
			if i.Kind == authn.IdentifierEmail && i.Value == "second@example.com" {
				emailID = i.ID
			}
		}
		theirs, _ := h.p.Identifiers(ctx, other)
		foreignID = theirs[0].ID
		if err := h.p.RemoveIdentifier(ctx, id, foreignID); err != nil {
			t.Errorf("removing an identifier the account does not have gave %v, want nil", err)
		}
		if got := h.count(tableIdentifiers, "id = ?", foreignID); got != 1 {
			t.Error("removing by the identifier of another account's row removed it")
		}
		if err := h.p.RemoveIdentifier(ctx, id, emailID); err != nil {
			t.Fatalf("removing: %v", err)
		}
		if _, err := h.p.Lookup(ctx, authn.IdentifierEmail, "second@example.com"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("a removed address is still found: %v", err)
		}
		if err := h.p.RemoveIdentifier(ctx, id, emailID); err != nil {
			t.Errorf("removing twice gave %v, want nil", err)
		}
		if err := h.p.RemoveIdentifier(ctx, "nobody-at-all", emailID); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("removing from nobody gave %v, want ErrAccountNotFound", err)
		}
	})
}

func TestExternalIdentitiesAreBoundByProviderAndSubjectAndConflictAcrossAccounts(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		first := h.user("first", "")
		second := h.user("second", "")
		link := authn.ExternalLink{Provider: "google", Subject: "sub-1", Email: "p@example.com", EmailVerified: true}

		if err := h.p.LinkExternal(ctx, first, link); err != nil {
			t.Fatalf("linking: %v", err)
		}
		if got, err := h.p.ByExternal(ctx, "google", "sub-1"); err != nil || got != first {
			t.Errorf("ByExternal = %q, %v, want %q", got, err, first)
		}
		if _, err := h.p.ByExternal(ctx, "github", "sub-1"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("another provider's identity with the same subject is found: %v", err)
		}
		if err := h.p.LinkExternal(ctx, second, link); !errors.Is(err, authn.ErrBindingConflict) {
			t.Errorf("another account's identity gave %v, want ErrBindingConflict", err)
		}
		link.Email, link.EmailVerified = "changed@example.com", false
		if err := h.p.LinkExternal(ctx, first, link); err != nil {
			t.Errorf("linking again gave %v, want the same binding", err)
		}
		bound, err := h.p.Externals(ctx, first)
		if err != nil || len(bound) != 1 || bound[0].Provider != "google" || bound[0].Subject != "sub-1" ||
			bound[0].Email != "changed@example.com" || bound[0].EmailVerified || bound[0].ID == "" ||
			!bound[0].LastLoginAt.IsZero() || !bound[0].CreatedAt.Equal(h.clock.read()) {
			t.Errorf("the bindings are %+v, %v", bound, err)
		}
		// The email of a binding is a display field: it never reaches the
		// identifiers, and so no lookup finds the account by it.
		if _, err = h.p.Lookup(ctx, authn.IdentifierEmail, "changed@example.com"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("the email of a binding is an identifier: %v", err)
		}
		for _, bad := range []authn.ExternalLink{
			{Subject: "s"},
			{Provider: "google"},
			{Provider: strings.Repeat("p", 129), Subject: "s"},
			{Provider: "g", Subject: strings.Repeat("s", 513)},
			{Provider: "g\n", Subject: "s"},
		} {
			if err = h.p.LinkExternal(ctx, first, bad); !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("a binding %+v gave %v, want ErrInvalidArgument", bad, err)
			}
		}
		if err = h.p.LinkExternal(ctx, "nobody-at-all", authn.ExternalLink{Provider: "g", Subject: "s"}); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("a binding on nobody gave %v, want ErrAccountNotFound", err)
		}
	})
}

func TestTwoAccountsLinkingOneExternalIdentityLeaveOneBinding(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		const callers = 6
		accounts := make([]authn.AccountID, callers)
		for i := range callers {
			accounts[i] = h.user(fmt.Sprintf("linker%d", i), "")
		}
		var wg sync.WaitGroup
		errs := make([]error, callers)
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = h.p.LinkExternal(context.Background(), accounts[i], authn.ExternalLink{Provider: "github", Subject: "42"})
			}()
		}
		wg.Wait()
		won := 0
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, authn.ErrBindingConflict):
				t.Errorf("a loser got %v, want ErrBindingConflict", err)
			}
		}
		if won != 1 {
			t.Errorf("%d links won, want one", won)
		}
		if n := h.count(tableExternals, "provider = 'github' AND subject = '42'"); n != 1 {
			t.Errorf("%d bindings of the identity, want one", n)
		}
	})
}

func TestAnExternalIdentityIsUnlinkedUnlessItIsTheLastWayIn(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		bind := func(id authn.AccountID, provider string) string {
			t.Helper()
			if err := h.p.LinkExternal(ctx, id, authn.ExternalLink{Provider: provider, Subject: "s-" + id.String()}); err != nil {
				t.Fatalf("linking %s: %v", provider, err)
			}
			bound, err := h.p.Externals(ctx, id)
			if err != nil {
				t.Fatalf("listing: %v", err)
			}
			for _, b := range bound {
				if b.Provider == provider {
					return b.ID
				}
			}
			t.Fatalf("no binding of %s", provider)
			return ""
		}

		bare, err := h.p.Create(ctx, authn.CreateAccount{Username: "bare-one"})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		only := bind(bare.ID, "google")
		if err = h.p.UnlinkExternal(ctx, bare.ID, only); !errors.Is(err, authn.ErrLastLoginMethod) {
			t.Errorf("unlinking the only way in gave %v, want ErrLastLoginMethod", err)
		}
		if n := h.count(tableExternals, "account_id = ?", bare.ID.String()); n != 1 {
			t.Errorf("a refused unlink left %d bindings, want one", n)
		}

		withPassword := h.user("has-password", "correct horse battery")
		if err = h.p.UnlinkExternal(ctx, withPassword, bind(withPassword, "google")); err != nil {
			t.Errorf("unlinking with a password to fall back on gave %v", err)
		}
		withPhone, err := h.p.Create(ctx, authn.CreateAccount{Username: "has-phone", Phone: "+8613800000009", PhoneVerifiedVia: admin})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		if err = h.p.UnlinkExternal(ctx, withPhone.ID, bind(withPhone.ID, "github")); err != nil {
			t.Errorf("unlinking with a phone number to fall back on gave %v", err)
		}
		two, err := h.p.Create(ctx, authn.CreateAccount{Username: "has-two"})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		google, github := bind(two.ID, "google"), bind(two.ID, "github")
		if err = h.p.UnlinkExternal(ctx, two.ID, google); err != nil {
			t.Errorf("unlinking with another binding to fall back on gave %v", err)
		}
		if err = h.p.UnlinkExternal(ctx, two.ID, github); !errors.Is(err, authn.ErrLastLoginMethod) {
			t.Errorf("unlinking what is now the last gave %v, want ErrLastLoginMethod", err)
		}
		if err = h.p.UnlinkExternal(ctx, two.ID, google); err != nil {
			t.Errorf("unlinking what is already gone gave %v, want nil", err)
		}
		if err = h.p.UnlinkExternal(ctx, "nobody-at-all", google); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("unlinking from nobody gave %v, want ErrAccountNotFound", err)
		}
	})
}

func TestTwoUnlinksOfAnAccountsTwoBindingsLeaveOne(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		account, err := h.p.Create(ctx, authn.CreateAccount{Username: "racing-pair"})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		for _, provider := range []string{"google", "github"} {
			if err = h.p.LinkExternal(ctx, account.ID, authn.ExternalLink{Provider: provider, Subject: "s"}); err != nil {
				t.Fatalf("linking: %v", err)
			}
		}
		bound, _ := h.p.Externals(ctx, account.ID)
		var wg sync.WaitGroup
		errs := make([]error, len(bound))
		for i, b := range bound {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = h.p.UnlinkExternal(ctx, account.ID, b.ID)
			}()
		}
		wg.Wait()
		refused := 0
		for _, err := range errs {
			if errors.Is(err, authn.ErrLastLoginMethod) {
				refused++
			} else if err != nil {
				t.Errorf("an unlink gave %v", err)
			}
		}
		if n := h.count(tableExternals, "account_id = ?", account.ID.String()); n != 1 || refused != 1 {
			t.Errorf("%d binding(s) left and %d refusal(s), want one of each: the two checks did not exclude each other", n, refused)
		}
	})
}

func TestTouchWritesTheTimeOfTheLastLogin(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("toucher", "")
		at := h.clock.read().Add(90 * time.Minute)
		if err := h.p.Touch(ctx, id, at); err != nil {
			t.Fatalf("touching: %v", err)
		}
		var row accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil || row.LastLoginAt == nil || !row.LastLoginAt.Equal(at) {
			t.Errorf("the last login is %v (%v), want %v", row.LastLoginAt, err, at)
		}
		if err := h.p.Touch(ctx, id, time.Time{}); err != nil {
			t.Fatalf("touching with no time: %v", err)
		}
		if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil || row.LastLoginAt == nil || !row.LastLoginAt.Equal(h.clock.read()) {
			t.Errorf("a touch with no time wrote %v, want the clock's %v", row.LastLoginAt, h.clock.read())
		}
		if err := h.p.Touch(ctx, "nobody-at-all", at); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("touching nobody gave %v, want ErrAccountNotFound", err)
		}
		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		if err := h.p.Touch(ctx, id, at); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("touching a tombstone gave %v, want ErrAccountNotFound", err)
		}
		// A fresh row: a field the query reads as NULL is not written back onto
		// a value the previous read left in the same struct.
		var tomb accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&tomb).Error; err != nil || tomb.LastLoginAt != nil {
			t.Errorf("the tombstone has a last login %v (%v)", tomb.LastLoginAt, err)
		}
	})
}

func TestTouchLeavesNoAuditRowOfItsOwn(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		id := h.user("quiet-toucher", "")
		rec := h.recordSQL()
		if err := h.p.Touch(context.Background(), id, time.Time{}); err != nil {
			t.Fatalf("touching: %v", err)
		}
		statements := rec.take()
		if len(statements) != 1 || !strings.HasPrefix(statements[0], "UPDATE "+tableAccounts) {
			t.Errorf("a touch ran %v, want one raw UPDATE", statements)
		}
	})
}

// plain admits text of exactly its bound and refuses one byte past it, which is
// the inclusive upper bound each of its call sites passes.
func TestPlainAdmitsExactlyTheBound(t *testing.T) {
	for _, c := range []struct {
		what string
		max  int
	}{
		{"a service-secret label", labelMax},
		{"a provider", providerMax},
		{"a subject", subjectMax},
		{"a membership source", sourceMax},
		{"an external email", emailMaxBytes},
	} {
		if !plain(strings.Repeat("x", c.max), c.max) {
			t.Errorf("%s of exactly %d bytes was refused, want it admitted", c.what, c.max)
		}
		if plain(strings.Repeat("x", c.max+1), c.max) {
			t.Errorf("%s of %d bytes was admitted, want it refused", c.what, c.max+1)
		}
	}
}
