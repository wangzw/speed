# pkg/authn/sms

The SMS module of the authentication capability: the whole state machine of a
verification code, whatever its purpose. It writes the challenge row, spends the
budgets, counts the attempts, consumes the code and hands the sending to a
carrier through the sending seam. It delivers `authn.SMSCodes` exclusively and
the `authn.AccountLifecycle` hook beside it.

A purpose is one of the closed set `login`, `mfa`, `enroll` and `reset`, the
last being the anonymous password reset's proof that its caller holds a number.
The state of a challenge is one table whatever the purpose, so the code is held
once and its consumers — the SMS login method, the SMS factor, the phone routes
of the login engine and its password reset route — hold none of it. Only `login`
and `reset` send without an account: a reset row fills no account and neither
purpose counts against the per-account budget. The module takes the top-level `pkg/sms`
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
  marked suppressed, spends the number, source and account budgets, and enqueues
  nothing; the answer is the same shape, and the difference in what is enqueued
  is off the answer. A suppressed row never passes `Verify`, even
  for the exact code it was written for (`Send`, `Verify`,
  `TestASuppressedSendTakesTheSamePathAndQueuesNothing`,
  `TestARegisteredAndASuppressedNumberTakeTheSameCountingPath`,
  `TestVerifyNeverPassesASuppressedRow`). The phone, source and account budgets
  are across purposes and include suppressed, consumed and voided rows. Every
  budget leaves out a refunded row: a message the carrier certainly did not take
  gives its spend back, and apart from a refund only the sweep takes a row out of
  a window (`TestARefundedRowCountsAgainstNoBudget`,
  `TestASuppressedRowKeepsItsCountingRule`). The deployment-wide budget counts
  only unsuppressed rows, so anonymous requests for numbers nobody registered
  cannot exhaust it; the other dimensions keep counting suppressed rows, because
  a refusal that arrived at a different count for an unregistered number would be
  a phone-enumeration oracle (`TestTheGlobalBudgetCountsOnlyRealSends`,
  `TestASuppressedSendStillSpendsThePhoneBudget`). The global budgets are set per
  purpose — `global-hourly-login`, `-mfa`, `-enroll` and `-reset` — so one
  purpose's exhaustion never refuses another's. The number, source and resend
  dimensions are shared across purposes; the account dimension counts only `mfa`
  and `enroll`
  (`TestTheResetPurposeHasItsOwnGlobalBudget`,
  `TestTheLoginBudgetDoesNotRefuseAReset`,
  `TestTheResetPurposeSharesTheNumberSourceAndResendBudgets`,
  `TestAResetSendFillsNoAccountAndSkipsTheAccountBudget`).
- **The budget is a hard cap, read under the send's own lock order.** `Send` reads
  every limit before it opens the write transaction, then the transaction takes
  transaction-scoped advisory locks in the fixed order global purpose → number →
  source → account, re-reads every count, and writes the row only under those
  locks; a refusal rolls the transaction back and writes nothing (`Send`,
  `checkBudgets`, `register.insert`, `register.lock`,
  `TestTheBudgetIsCheckedUnderTheSendsOwnLocks`,
  `TestConcurrentSendsDoNotOverrunTheBudget`). The keys are
  `authn.sms:lock:global:<purpose>`, `authn.sms:lock:phone:<phone_mac>`,
  `authn.sms:lock:source:<source_mac>` and
  `authn.sms:lock:account:<account_id>`, hashed by `hashtextextended`; the login
  purpose has no account and takes no account lock, and a send with no source
  takes no source lock. SQLite takes no advisory lock because it serializes
  writers itself.
- **The source dimension counts a request's address only as a digest, and only
  when there is one.** `SendCode.Source` is the request's client address. A
  non-empty source is normalised through `netip.ParseAddr` and hashed through
  `db.Database.BlindIndex`; the row stores `source_mac` and the budget counts it across
  purposes for an hour and a rolling day, against `per-source-hourly` and
  `per-source-daily`. The plaintext address is never written and never logged
  (`sourceMAC`, `checkBudgets`, `countSourceSince`,
  `TestThePerSourceHourlyBudgetThrottles`,
  `TestTheSourceAddressIsNormalisedBeforeCounting`,
  `TestTheSourceIsKeptOnlyAsADigest`). An empty source is fail-open: no source
  lock, no source budget and a NULL `source_mac`, because putting every
  origin-less send under one key would make the dimension fail-closed on a
  deployment that cannot resolve an address
  (`TestAnEmptySourceIsFailOpen`).
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
  message the user resends. A registry that will not resolve, and a queue that
  fills between the pre-check and the enqueue, certainly sent nothing, so both
  refund the row exactly as the carrier's certain failures do. An error from the
  carrier that is none of the four sentinels is read as uncertain, never as a
  delivery (`deliverOne`, `send.go`).
  `sms.ErrUnroutable` is a definite failure recorded under its own `unroutable`
  outcome: the message is not defective, but this deployment cannot carry it to
  that destination, so it is never read as an unknown outcome and never retried.
  A definite failure — `ErrThrottled`, `ErrRejected` or `ErrUnroutable` — also
  refunds the challenge row: the row is marked `refunded_at` and spent in the
  same statement, so no budget counts the send and the undelivered code cannot
  pass `Verify`. An uncertain outcome, and an error outside the closed set, stay
  counted (`refundChallenge`, `register.refund`,
  `TestACertainCarrierFailureRefundsTheSend`,
  `TestARegistryRefusalRefundsTheSend`,
  `TestADropAfterTheRowIsWrittenRefundsTheSend`,
  `TestAnUncertainCarrierFailureStaysCounted`).
- **Every call to the carrier writes one line, and the line carries no secret.**
  An accepted send writes an `Info` line with `outcome=sent`, the carrier's
  `provider_id` and the call's `duration_ms`; a refused one keeps its `Warn`
  line, which now carries the outcome, the cause's own text, `duration_ms` and
  the `provider_id` when the carrier answered with one. A send stopped before
  the carrier is reached — a registry that will not resolve the message — keeps
  its `Warn` with the outcome and the cause and has no duration, because no call
  was made. No line carries the number, the code or the root key (`deliverOne`,
  `infoSent`, `warnNotSent`, `TestEachCarrierCallWritesOneLineWithoutSecrets`).
- **The worker pool is bounded and its outcomes are a closed set.** `Send`
  resolves the message against the registry, writes the row and returns without
  waiting for the carrier; the pool sends the resolved message off the request
  path, records one of the ten outcomes and refunds a certain failure's row.
  `Stop` drains the queue within its own bound
  (`pool`, `TestTheWorkersDrainQueuedMessagesOffTheRequestPath`,
  `TestStopDrainsTheQueueWithinItsBound`, `TestEveryOutcomeIsRecorded`). The
  `dropped` path reached after the row is written — the queue filling between the
  pre-check and the enqueue — refunds the row, as a refusing registry does:
  nothing reached the carrier, so neither spends the budget. A case holds that
  window open through the registry's resolving hook
  (`TestADropAfterTheRowIsWrittenRefundsTheSend`,
  `TestARegistryRefusalRefundsTheSend`); the pre-check path either side of it is
  pinned by `TestAQuedMessageIsDroppedWhenTheQueueIsFull` and
  `TestANormalEnqueueIsNotRecordedAsADrop`.
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
  `authn.code.enroll`, `authn.code.reset`), and checks against
  `Registry.Declared()` after the registry is resolved in `newProduct` that every
  required template is present, carries exactly the `code` and `ttl_minutes`
  parameters, has at least one language, and covers the configured default
  language. The parameter names are compared for equality, not containment. A
  template's language set is its effective one — the languages it has a vendor
  code for and the languages it has a body for, together — so the same templates
  serve a carrier that sends by code and one that sends by rendered body. The
  body text is a configuration value, not something this module writes: it
  carries no carrier-specific copy. A mismatch — a missing required template, a
  stray parameter, or a template without the default language — stops the startup
  with `ErrInvalidConfig` naming it, ahead of any code. `Prepare` runs before
  `New` and has no instance, so the check lives in `New` (`templates.go`,
  `newProduct`).
- **The reset template is optional, and its presence is the reset channel's
  availability.** `authn.code.reset` is held to the same shape as the required
  templates when the carrier declares it; declaring a malformed one stops the
  startup. Declaring none keeps the startup and every other purpose working and
  leaves the reset purpose unsupported
  (`TestTheResetTemplateIsOptional`,
  `TestACarrierThatDeclaresAResetTemplateItCannotCarryIsRefused`,
  `TestADeclaredResetTemplateThatCannotBeCarriedStopsTheNewStage`). The product
  publishes the optional `authn.PurposeSupport`, whose `SupportsPurpose` answers
  true for `login`, `mfa` and `enroll` always and for `reset` exactly when that
  template was declared; it changes no `SMSCodes` method
  (`TestTheResetTemplateIsOptional`, `TestAResetSendCarriesTheResetTemplate`).
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

The PostgreSQL leg of every dialect case carries the `integration` tag and needs
the repository's compose stack; a default run compiles none of it, and an
integration run fails without it. The SQLite leg asks for nothing. `helpers_test.go` builds the product by hand over
a real database handle (`pkg/internal/dbtest`) with a root key configured and
every collaborator a case drives — the carrier, the bus, the audit sink, the
settings store, the clock and the metric reader — each a stub whose answers the
case chooses. `assembly_test.go` drives a real registry through the descriptor
with stand-in modules for the database and the carrier, which is what holds each
callback's stage. `dialect_test.go` runs the native SQL and the window counting
on both engines under a clock that does not read in UTC.
