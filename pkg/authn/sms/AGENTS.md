# pkg/authn/sms

The SMS module of the authentication capability: the whole state machine of a
verification code, whatever its purpose. It writes the challenge row, spends the
budgets, counts the attempts, consumes the code and hands the sending to a
carrier through the sending seam. It delivers `authn.SMSCodes` exclusively and
the `authn.AccountLifecycle` hook beside it.

A purpose is one of the closed set `login`, `mfa` and `enroll`. The state of a
challenge is one table whatever the purpose, so the code is held once and its
consumers — the SMS login method, the SMS factor and the phone routes of the
login engine — hold none of it. The module takes the top-level `pkg/sms`
capability's `Sender` and `Registry` as requirements, each delivered exclusively
by its provider: a command tree names one of each, and two at once fail in
resolution. The registry holds the templates in force and decides which one a
message goes out under; a carrier module (one of `sms.aliyun`, `sms.tencent`, or
a third party's, under `pkg/sms`) constructs, calls and classifies, and is handed
only what the registry resolved.

The API is the package's godoc. Read `Send`'s and `Verify`'s comments before
changing either.

The names the module reports under are a closed set — the `authn.sms.sends`
instrument and its `outcome` set, the `authn.audit.write-failures` counter, and
the `settings.changed` subscription — so a name or an attribute value is a
compatibility break for a dashboard written against it.

## Boundaries

- **The plaintext code never leaves the module.** It is written to no table, no
  log, no event and no metric, and lives only in the queued message and the call
  to the carrier. The row holds `code_mac`, the keyed digest of the challenge
  identifier and the code, and `phone_mac`, the keyed digest of the number
  (`TestNoPlaintextCodeReachesTheTable`, `TestTheCodeAndTheRootKeyNeverReachTheLog`,
  `TestNoMetricAttributeCarriesASecret`). Every digest goes through
  `db.Database.BlindIndex`; `Start` proves the root key is there and ends the
  startup with `ErrNoEncryptionKey` without one, rather than writing an unkeyed
  digest anybody could reverse (`checkEncryptionKey`,
  `TestStartRefusesWithoutARootKey`).
- **A suppressed row and a real one go one path.** Deliver false writes a row
  marked suppressed, spends every budget, and enqueues nothing; the answer is the
  same shape, and the only difference — a message queued or not — is off the
  answer. A suppressed row never passes `Verify`, even for the exact code it was
  written for (`Send`, `Verify`, `TestASuppressedSendTakesTheSamePathAndQueuesNothing`,
  `TestARegisteredAndASuppressedNumberTakeTheSameCountingPath`,
  `TestVerifyNeverPassesASuppressedRow`). The phone and account budgets are across
  purposes and include suppressed, consumed and voided rows; only the sweep takes
  a row out of a window.
- **The check counts before it compares.** `Verify` runs one conditional update
  first — the row must be there, unconsumed, unexpired and below its attempt
  limit — and only then compares, so a concurrent submitter cannot get past the
  limit. The pass is a second conditional update on `consumed_at`, so of two
  correct submits exactly one wins. Every case that does not pass is
  `ErrInvalidCredentials` with no distinction, a missing, expired, consumed or
  suppressed row included, and each does one comparison (`Verify`,
  `TestVerifyAcceptsTheRightCodeOnce`, `TestVerifyRefusesAfterTheAttemptLimit`,
  `TestConcurrentSubmitsOfOneCorrectCodeHaveOneWinner`).
- **The failure direction is closed.** A database that cannot be read is
  `ErrUnavailable` in `Send` and `Verify`; a carrier that cannot send, and a
  registry that will not resolve a message, are a `Warn`, a metric and a dropped
  message the user resends. An error from the carrier that is none of the three
  sentinels is read as uncertain, never as a delivery (`deliverOne`, `send.go`).
- **The worker pool is bounded and its outcomes are a closed set.** `Send`
  resolves the message against the registry, writes the row and returns without
  waiting for the carrier; the pool sends the resolved message off the request
  path and records one of the nine outcomes. `Stop` drains the queue
  within its own bound (`pool`, `TestTheWorkersDrainQueuedMessagesOffTheRequestPath`,
  `TestStopDrainsTheQueueWithinItsBound`, `TestEveryOutcomeIsRecorded`). The one
  `dropped` path that is reached after the row is written — the queue filling
  between the pre-check and the enqueue — is not driven by any case: the gap
  spans a database write and cannot be held open deterministically without a test
  seam, so it stays covered by inspection, not by a case
  (`TestAQuedMessageIsDroppedWhenTheQueueIsFull` and `TestANormalEnqueueIsNotRecordedAsADrop`
  pin the pre-check path on either side of it).
- **The sweep keys on the window, not on the state.** It removes rows past the
  longest counting window plus a margin, so no budget inside a window is
  under-counted; it never removes a row by its being spent or expired alone
  (`Sweep`, `TestTheSweepRemovesOnlyRowsPastTheWindow`).
- **The account cleanup erases mfa and enroll rows, and the compensation repeats
  it, deduplicated by `Event.ID`.** A login row carries no account and goes with
  the sweep; the cleanup is idempotent (`AccountDeleted`, `TestAccountDeleted...`,
  `TestTheDeletionEventCompensatesAndIsDeduplicatedByItsID`).
- **A setting moved toward more lenient is visible after the fact, never
  blocked.** The subscription to `settings.changed` writes a `Warn` and a
  best-effort audit row naming the key and the direction and no value
  (`relax.go`, `TestARelaxingChangeWritesAWarnAndAnAuditRow`).
- **The module knows no tenant and no carrier.** No package here imports
  `pkg/tenant`; the carrier is taken only as `pkg/sms`'s `Sender`, and no
  carrier subpackage is imported (`Module`).
- **The templates and the languages are the registry's, held to its declaration
  in `New`.** The top-level `pkg/sms` capability carries the first-class template
  declaration and does not know what a consumer needs; this module maps each
  purpose to a template key (`authn.code.login`, `authn.code.mfa`,
  `authn.code.enroll`), and checks against `Registry.Declared()` after the
  registry is resolved in `newProduct` that every template is present, carries
  exactly the `code` and `ttl_minutes` parameters, has at least one language, and
  covers the configured default language. The parameter names are compared for
  equality, not containment. A mismatch — a missing template, a stray parameter,
  or a template without the default language — stops the startup with
  `ErrInvalidConfig` naming it, ahead of any code. `Prepare` runs before `New` and
  has no instance, so the check lives in `New` (`templates.go`, `newProduct`).
- **The set of languages is derived, and a requested language is resolved, never
  refused.** The languages a message may be sent in are the ones the template its
  purpose maps to declares, taken from the registry's declaration, not a set
  enumerated in code; two templates of one registry may declare different sets. A
  language a caller names is matched by primary subtag against that template's set
  — `en-GB` goes out under the template's `en-US` — and an empty request, or one
  that template's set does not match, resolves to the configured default. The
  default is the shared `i18n.default-locale` item, read from the i18n
  capability's namespace so one statement of the default serves both capabilities;
  an assembly with no i18n engine keeps `en-US` (`readDefaultLocale`,
  `resolveLocale`, `templateLocales`).
- **The configuration sizes the pool; the settings tune the state machine.** The
  worker count and the queue size are read before a database is reachable;
  everything a request reads is a setting with a declared default. An absent
  settings store is a legal assembly whose allowed table is empty, so no code is
  sent (`config.go`, `settings.go`, `settings_test.go`).

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this module alone:

```
go -C pkg build ./authn/sms/...
go -C pkg vet ./authn/sms/...
go -C pkg test ./authn/sms/...
go -C pkg test -race ./authn/sms/...
env -C pkg golangci-lint run ./authn/sms/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

The PostgreSQL leg of every dialect case needs docker and is skipped without it;
the SQLite leg asks for nothing. `helpers_test.go` builds the product by hand over
a real database handle (`pkg/internal/dbtest`) with a root key configured and
every collaborator a case drives — the carrier, the bus, the audit sink, the
settings store, the clock and the metric reader — each a stub whose answers the
case chooses. `assembly_test.go` drives a real registry through the descriptor
with stand-in modules for the database and the carrier, which is what holds each
callback's stage. `dialect_test.go` runs the native SQL and the window counting
on both engines under a clock that does not read in UTC.
