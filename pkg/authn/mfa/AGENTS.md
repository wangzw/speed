# pkg/authn/mfa

The multi-factor module of the authentication capability: the register of the
second factors an account holds, the policy that says whether a login needs one,
the orchestration around every call to a factor, and the one place the assurance
of a login is composed. It delivers the `authn.MFA` capability exclusively and
the `authn.AccountLifecycle` hook beside it.

The descriptor declares the static feature `authn.mfa`, present whenever this
module is in force; the frontend gates the multi-factor section on it.

The kinds of factor are not here. A kind is a module of its own — `authn.mfa.totp`,
`authn.mfa.sms`, `authn.mfa.webauthn`, and any third party's — that delivers
`authn.Factor`, one module per kind. This module takes them all with
`core.ResolveAll` in `Init` and imports none of them; it drives them through the
root package's `Factor` interface alone. Registration, back-off, the audit row
and the event are done here, once, for every kind, so a kind that forgot them
does not exist. What a factor does is cryptography and the state of its own.

The login engine reaches factors through `MFA` and never through `Factor`: with
factors registered in the store and no module here, a login would skip the second
factor without a word, so the engine requires `MFA`.

The API is the package's godoc. Read `Module`'s comment before changing the
descriptor, `Require`'s and `Assurance`'s before changing the policy or the
level, and `Remove`'s, `removeFactorAndSettleSessions`'s and `Reset`'s before
changing the order of their steps.

The names the module reports under are a closed set — the `authn.factor.changed`
payload, the `authn.factor.enroll` and `authn.factor.remove` audit actions, and
the `authn.mfa.verifications` instrument with its `factor` and `outcome`
attributes — so a name or an attribute value is a compatibility break for a
dashboard or an audit query written against it.

## Boundaries

- **The register is the authority, and it is not shared.** `authn_factors` is the
  one row per factor an account holds, whatever its kind
  (`register.go`, `migrations`). A kind's own table holds only its secret or
  credential. A read that fails is an outage and never "the account has no
  factors": the direction of a lost register row is to relax verification, so a
  failure to read it must not be taken for an empty one
  (`TestTheListIsAnOutageWhenTheRegisterCannotBeRead`,
  `TestAnUnreadableRegisterAnswersAnOutageAndAsksNoFactor`). A row's zero status
  is pending, and a pending row is never used to verify — a value nobody chose is
  not one to verify with (`TestARowThatIsPendingCannotBeUsedToVerify`,
  `factorRow.record`).
- **The policy only ever tightens.** `Require` walks its policy table top to
  bottom and takes the first row that holds, and no row cancels another: a first
  factor with user verification asks for nothing more; an account with an active
  factor must use one even when the first factor is an IdP that declared
  multi-factor; a mandatory switch and an account with no factor means register;
  an IdP's declaration is believed only with a factor absent and nothing requiring
  one; otherwise one factor completes the login
  (`TestRequireFollowsThePolicyTable`, `Require`). The outcome is decided by the
  requirement account and the offered list holds the proof account's factors:
  they are the same account for an ordinary login, and apart when an impersonating
  identity proves its own factors for the impersonated account
  (`TestRequireDecidesTheOutcomeFromTheRequirementAccount`). A factor whose kind
  has no implementation here is unavailable and not absent, so it is left out of
  the list rather than read as the account having none
  (`TestAKindWithNoImplementationIsRefusedAndNeverCounted`).
- **The assurance is composed in one place and read nowhere else.** `Assurance` is
  a pure function of the first factor's proof and the second factor's pass; the
  gateway backends, tenant admission, `guard.Require` and impersonation read what
  the login session recorded. The second factor's methods must not intersect the
  first factor's, or the level is single. An IdP's declaration is believed only
  when the provider row trusts it and the account has no active factor and neither
  the account nor the deployment requires one — a closed set, each condition
  necessary, and the login engine calls `Assurance` only after `Require` answered
  `MFANone` for such a login or after a second factor passed
  (`TestAssuranceComposesTheLevelFromTheFactorsProven`,
  `TestAnIDPDeclarationNeedsAllThreeConditionsEachOfWhichIsNecessary`). A factor
  registered during a login adds nothing to that login's level
  (`TestAFactorRegisteredDuringALoginAddsNothingToItsStrength`).
- **`Enroll` is the only entry for registration, and it activates the row last.**
  It counts the account's factors of the kind against `max-per-kind` and writes a
  pending row, then calls the factor; the row becomes active only on the factor's
  last step, and a row a sweep, a reset or a removal got to first is not brought
  back. The first step stores the optional label carried in the input map under
  `label`: trimmed, at most 64 characters with no control character, stored as
  given, and empty when none is submitted because no name is generated. A label
  outside that domain is refused before any row is written, and a label on a later
  step changes nothing (`Enroll`, `PendingFactor`, `finishEnroll`,
  `normalizeFactorLabel`,
  `TestEnrollRefusesPastTheLimitPerKindAndCountsOnlyWhatIsNotDisabled`,
  `TestARegistrationActivatesTheRowOnTheLastStepOnly`,
  `TestARegistrationStoresTheSubmittedLabelAndGeneratesNothing`,
  `TestARegistrationRefusesALabelOutsideItsDomain`,
  `TestAnActivationThatLostTheRaceIsRefused`).
- **`Verify` and `Challenge` throttle first and call the factor only if the
  back-off lets them.** The scope is `ScopeFactor` and the key is the factor's
  identifier. A back-off that cannot be read does not let an attempt through
  (`TestAFactorInBackOffIsNotAskedAndNothingIsCounted`,
  `TestAFactorWhoseBackOffCannotBeReadIsNotAsked`). A failure is counted and a
  pass clears the key; a replay is counted as a failure and reported as a replay,
  a refusal whose cause is `authn.ErrFactorFlagged` disables that factor's row and
  publishes `authn.factor.changed` with `flagged` — the row is taken out of the
  account's factors, so a cloned credential does not stay usable — and an outage
  of the factor is neither counted nor answered as a refusal
  (`TestVerifyThrottlesBeforeAskingAndCountsTheOutcome`,
  `TestAReplayIsCountedAsAFailureAndReportedAsAReplay`,
  `TestAFlaggedFactorIsDisabledAndAnnounced`,
  `TestAnOutageOfTheFactorIsNotCountedAsAGuess`). A factor answer a call may not
  give is the factor's fault and ends as its outage, never a pass or a refusal
  (`TestAnAnswerAFactorMayNotGiveIsAFaultOfTheFactor`). When a failure begins a
  back-off the module publishes `authn.throttle.engaged` and counts it, once
  (`TestTheFailureThatStartsABackOffIsAnnouncedOnceAndCounted`).
- **A removal cleans the kind's data and never takes the account below its
  floor; ending the other sessions and rotating the requesting one is the route's
  work.** `Remove` calls `Factor.Remove` to clean the kind's data and then deletes
  the row, and it acts on no session: the capability promises nothing about
  sessions, so a host that calls it directly leaves them alone. The user-facing
  DELETE route reads the factor's status, calls `Remove`, and only after a
  successful removal of an active factor settles the account's sessions under the
  `self` class: the other sessions are ended keeping the one that asked, and then
  that session is rotated and its new token written as the answer's cookie, so the
  device that removed the factor stays signed in on a new token while the others
  are turned away. The other sessions are ended first, and the rotation runs only
  when that succeeded: a new token the client could not receive in a failed answer
  would sign the device out while leaving the failure unfixed. A pending or
  disabled factor changes nothing about the account's strength, so removing one
  ends no session and rotates none, and the last active factor of an account that
  requires one is `ErrLastFactor`, refused before any session is touched. The
  session store is therefore a failure point of that route: a revocation or
  rotation failure after the row is gone answers 503, and repeating the request
  finishes what is left, because a name that is no longer there reads as a removal
  that has already happened and settles the sessions all the same. The price is
  explicit: a DELETE naming a factor that never existed ends the caller's other
  sessions and rotates the caller's own as well. A module with no session store
  removes the factor and logs a warning that the sessions stay valid
  (`TestRemoveHasTheFactorCleanItsDataBeforeTheRowGoes`,
  `TestRemoveEndsNoSessionItself`,
  `TestRemovingAFactorEndsTheAccountsOtherSessionsAndKeepsTheCurrentOne`,
  `TestRemovingAnActiveFactorRotatesTheRequestingSessionsToken`,
  `TestDeletingAFactorThatIsNotActiveEndsNoSession`,
  `TestADeletionWhoseSessionRevocationFailsReportsItAndARetryFinishes`,
  `TestADeletionWhoseRotationFailsReportsItAndARetryRotates`,
  `TestDeletingAFactorWithoutASessionModuleRemovesItAndSaysSo`,
  `TestDeletingTheLastFactorOfAnAccountThatRequiresOneIsAConflict`).
- **`Reset` runs its four steps in order and can be repeated.** The flag is set,
  the factors are disabled in one transaction, the sessions are revoked when a
  session module is present, and the event is published after the commit; a
  failure at any step ends the call with the account stricter and not looser, and
  every step is a no-op when it has nothing to do
  (`TestResetRunsItsStepsInOrder`,
  `TestResetThatFailsAtAStepLeavesTheAccountStricterAndCanBeRepeated`,
  `TestResetStopsAtADisableThatFailsBeforeAnySessionIsRevoked`). The
  authorization and the strength of whoever asked are the administration
  handler's, not this module's.
- **`Seasoned` reads the register and no clock but the module's.** It is true when
  the account has an active factor that is not SMS and has been active for
  `sensitive-min-age`; a factor registered during the session that asks does not
  count yet (`Seasoned`, `TestSeasonedNeedsANonSMSActiveFactorOldEnough`).
- **The account lifecycle hook erases, the compensation event repeats it, and
  the compensation is deduplicated by `Event.ID`.** `AccountDeleted` has every
  factor clean its data and removes the rows, returns every failure together, and
  a repeated call finishes the work; `AccountDisabled` leaves the factors alone.
  The `authn.account.deleted` subscription does the same cleanup and is idempotent
  (`TestAccountDeletedHasEveryFactorCleanItsDataAndRemovesTheRows`,
  `TestACleanupThatFailsIsReturnedAndTheRepeatFinishesIt`,
  `TestADisabledAccountKeepsItsFactors`,
  `TestTheDeletionEventCompensatesAndIsDeduplicatedByItsID`).
- **The routes are the ones below, all guarded and all impersonable; a deletion
  asks for a fresh local multi-factor verified here and a rename asks for
  nothing beyond the login session.** `GET B/factors` lists the caller's own
  factors and nothing of a kind's own; `DELETE B/factors/:id` acts on the
  effective account's factors, requires `FreshLocalMulti(recent-window)` from
  the caller's own proof — an IdP's declared multi-factor does not satisfy it —
  and reads a factor that is not the effective account's as one already gone;
  `PATCH B/factors/:id` writes the label of one of the effective account's
  factors, answers the updated row, and answers 404 for another account's
  identifier, the same as one that is not there. A label outside its domain is
  400 and leaves the stored one as it is; the rename writes no audit row and
  publishes no event
  (`TestTheRoutesAreTheThreeTheDesignNamesAndAllAreGuarded`,
  `TestDeletingAFactorNeedsAFreshMultiFactorAuthenticationVerifiedHere`,
  `TestAnImpersonatingIdentityRemovesTheEffectiveAccountsFactor`,
  `TestTheRenameWritesOnlyTheLabelAndAnswersTheRow`,
  `TestRenamingAnotherAccountsFactorReadsAsNotFound`,
  `TestTheRenameRefusesALabelOutsideItsDomain`,
  `TestTheListIsTheCallersOwnAndCarriesNothingSecret`).
- **The failure direction is closed: a dependency that cannot be read is an
  outage, never a refusal.** The database and the back-off store being unreadable
  answer 503 and check nothing; a factor that is unavailable answers 503 and is
  never merged with a refusal (`TestAnUnreadableRegisterAnswersAnOutageAndAsksNoFactor`,
  `TestDeletingIsAnOutageWhenTheFactorCannotCleanUp`). The mandatory switch
  `global-required` that cannot be read is a refusal, and the adjustment settings
  that cannot be read fall to their declared defaults
  (`TestRequireRefusesWhenTheMandatorySwitchCannotBeRead`).
- **The settings are these five, each with its domain and its failure
  direction.** `global-required` is a mandatory switch and reads as a refusal when
  it cannot be read; `recent-window`, `max-per-kind`, `pending-ttl` and
  `sensitive-min-age` are adjustments that fall to their declared defaults and are
  reported once per distinct fault (`settingsSchema`, `reader`).
- **Time is UTC, and the sweep is bounded.** Every time written or bound is
  converted to UTC, because SQLite compares a time as text in the zone it carries
  (`TestComparisonsAreRightWhenTheClockReadsInAnotherZone`,
  `TestTheRegisterConvertsATimeItIsGivenToUTC`). The sweep deletes only rows that
  are pending and past `pending-ttl`, conditionally, so it never deletes a
  registration that is finishing, and `Stop` waits at most five seconds
  (`TestTheSweepDeletesOnlyPendingRowsPastTheirTime`,
  `TestTheSweepAndARegistrationThatIsFinishingDoNotDeleteEachOthersWork`,
  `TestStopEndsTheSweepWithinItsBound`).
- **The module imports no factor subpackage and no package of the tenant tree.**
  Factors are taken only as the root package's `Factor`. The admission layer's
  participation is read as a module name string, and its absence writes one Warn
  at `Start`, because a tenant's requirement of multi-factor is enforced by nobody
  in that assembly (`admissionModule`, `TestStartWarnsWhenTheAdmissionLayerTakesNoPart`).
- **The descriptor declares the required capabilities and the one optional one.**
  The database, the route mounter, the account reads and writes and the back-off
  are required and each delivered exclusively by another module, so the closure
  does not pull them in; the settings store is optional; the factor capability is
  taken with `ResolveAll` in `Init` and never written into `Requires`
  (`TestTheDescriptorNamesWhatItDeliversAndRequires`). Two factors of one kind are
  `ErrFactorConflict`, a kind outside the set is `ErrInvalidClaim`, and a command
  that requires a factor of every account and takes in no factor module is
  `ErrNoFactor` before anything runs — as is one whose register holds active
  factors of a kind no module here delivers
  (`TestTwoFactorsOfOneKindFailTheDrive`,
  `TestAFactorClaimingAKindOutsideTheSetFailsTheDrive`,
  `TestNoFactorWithTheMandatorySwitchOnFailsTheDrive`,
  `TestStartRefusesWhenAccountsHoldAKindNoModuleDelivers`).
- **A table module ends the startup when its table is missing.** `Start` probes the
  register and answers `ErrNotMigrated` when it cannot be read, naming the
  migration command (`TestStartRefusesWhenTheRegisterTableIsMissing`).
- **The callbacks act on this module's own product and on nothing else.**
  `productOf` recognises the one type this package builds; an instance a host
  supplied for the module is left alone (`productOf`).
- **A setting moved toward more lenient is visible after the fact, never
  blocked.** The subscription to `settings.changed` writes a `Warn` and a
  best-effort `authn.settings.relaxed` audit row naming the key and the
  direction and no value; the settings it watches are `global-required` in the
  `to-false` direction, `recent-window` in `increase` and `sensitive-min-age` in
  `decrease` (`relax.go`, `TestARelaxingChangeWritesAWarnAndAnAuditRow`).

## Review-only fixture seams

`review_seed.go` compiles only under the `review` tag and exists for the demo
starter's review assembly, which seeds an operator's second factors so the
impersonation journey is walkable. It adds no product behaviour:

- `ReviewSeedActiveFactor` writes one active factor row through the register's
  own `insertPending` and `activate` statements. `Enroll` activates at the
  clock's now; the fixture chooses a backdated instant instead, so the sender of
  an impersonation can hold a seasoned factor without an hour of waiting. It is
  a review-only fixture, named as one. `ReviewRemoveFactor`, `ReviewFactors` and
  `ReviewSeasoned` run the register's own remove, list and seasoned statements.
- Nothing here writes a kind's own data. A TOTP seed is written by
  `totp.ReviewSeedFactor` through that module's encrypted seed register.

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
go -C pkg build ./authn/mfa/...
go -C pkg vet ./authn/mfa/...
go -C pkg test ./authn/mfa/...
go -C pkg test -tags=integration ./authn/mfa/...
go -C pkg test -race ./authn/mfa/...
env -C pkg golangci-lint run ./authn/mfa/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

Only the SQLite leg of a dialect case belongs to a default run: the PostgreSQL
leg needs the `integration` tag and the repository's compose stack, and fails
rather than skips when nothing answers there. `helpers_test.go` builds the
product by hand over a real database handle (`pkg/internal/dbtest`) with every
collaborator a case drives — the account reads and writes, the back-off, the
sessions, the bus, the audit sink, the settings store, the clock and the metric
reader — each a stub whose answers the case chooses and whose calls it counts.
`assembly_test.go`
drives a real registry through the descriptor with stand-in modules for the
exclusive capabilities, which is what holds the step each callback runs at and
what the registry's own delivery check asserts. `dialect_test.go` runs the
comparisons on every engine the build runs on, under a clock that does not read
in UTC.
