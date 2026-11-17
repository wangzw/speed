# pkg/authn/mfa/webauthn

The WebAuthn factor of the authentication capability and, in the same module, the
passkey login method. It delivers `authn.Factor` for the kind `webauthn` and
`authn.LoginMethod` under the name `passkey`, both without claiming either
capability, so the factor kinds stand side by side and the login engine finds the
method among the others.

As a factor it proves a second factor with an authenticator-bound credential: it
registers a credential, verifies an assertion against it, and keeps the
credential's own state — its counter, its backup flags and whether it is a
passkey. As a login method it proves a first factor the same way, through a
client-side discoverable credential the user picks, which needs no password and
counts as two factors when the assertion verifies the user.

The orchestration is not here. Registration, the failure back-off, the audit row
and the event belong to `authn.mfa`, which drives this factor through the root
package's `Factor` interface and drives this method through `LoginMethod`. What
this module does is speak the protocol and keep its own two tables.

The API is the package's godoc. Read `factor.Enroll`'s comment before changing the
two steps of a registration, and `factor.Verify`'s before changing the counter
write-back.

The three input items are `authn.mfa.webauthn.rp-id` (defaulting to the host of
the gateway's public address), `rp-name` and `origins`.

## Boundaries

- **No type of the WebAuthn library leaves this package.** The library is at
  version 0.x and its types change between releases, so a caller tied to one of
  them would be tied to the version this module pins. The library is named only
  inside this package; the factor and the login method speak `authn.Factor`,
  `authn.Step` and `authn.LoginMethod`
  (`TestNoLibraryTypeReachesTheExportSurface`, `TestTheExportSurfaceIsPinned` —
  the exported surface is `ModuleName`, `ConfigNamespace` and `Module`). An upgrade
  reads the library's migration notes and drains the ceremonies in flight, because
  the serialized session data changes shape with the version.
- **The kind is `webauthn` and the method name is `passkey`, both delivered
  without claiming either.** The factor kinds stand side by side, so neither
  provision is exclusive; the module requires the database, the multi-factor module
  and the account reads, and a command that takes this module without them fails at
  resolution (`webauthn.go` `Module`,
  `TestTheDescriptorNamesWhatItDeliversAndRequires`).
- **The user handle carries nothing of the person.** It is 32 random bytes in
  base64url, drawn once per account and stored in `authn_webauthn_users`; it is
  what a discoverable credential returns at assertion time for the relying party to
  find the account by, so a client sees it. The account's name for the
  authenticator is the username identifier, or the account identifier when it has
  none (`register.go` `ensureHandle`, `factor.go` `buildUser`, `nameOf`).
- **The public key is not a secret, and it is kept unencrypted.** Its integrity
  matters more than its confidentiality, so the column ships plain; the seed-like
  secrets of the capability live in the TOTP module, not here
  (`migrations/*/0001_authn_webauthn.sql`).
- **Two registration profiles differ in the authenticator selection.** A passkey
  asks for a discoverable credential and requires user verification, and can be a
  first factor or a second one; a security key asks for neither and is a second
  factor only. The attestation preference is none, and the account's existing
  credentials are excluded so one authenticator is not registered twice under one
  account (`ceremony.go` `beginRegistration`, `TestTheTwoProfilesDifferInTheOptions`,
  `TestRegistrationExcludesExistingCredentials`).
- **The ceremony data is the library's own bytes, kept in the flow record.** The
  engine holds the private bytes between steps and consumes them once; the factor
  never keeps them. Only bytes this module wrote are handed back to the library,
  because its decoder trusts a length prefix and a tampered value could make it
  allocate far more than the data is worth (`ceremony.go` `marshalCeremony`,
  `parseCeremony`). The library's login and registration timeouts are enforced, so
  a ceremony that has aged out is refused (`ceremony.go` `newRelyingParty`,
  `TestAnExpiredCeremonyIsRefused`).
- **The relying party id is checked against the public address at startup.** A
  credential is bound to the relying party id, so one that does not cover the
  host — equal to it or a parent domain of it — would let an assertion collected
  at the site complete a ceremony begun elsewhere. The public suffix table is not
  consulted, which is the residual risk accepted here (`config.go` `resolve`,
  `withinHost`, `TestTheRelyingPartyIDMustCoverTheHost`,
  `TestPrepareRefusesARelyingPartyIDOutsideTheHost`).
- **An assertion that holds advances the counter by a conditional write.** The
  write is `UPDATE authn_webauthn_credentials SET sign_count = ?, clone_warning = ?,
  user_verified = ?, backup_state = ? WHERE factor_id = ? AND sign_count <= ?`:
  one row affected is a pass, zero is `ErrReplay`. Two attempts that present one
  assertion both read a lower stored value, and the write lets exactly one through
  (`factor.go` `settleAssertion`, `register.go` `writeBack`,
  `TestWriteBackWinsOnlyAtOrAboveTheStoredCounter`, `TestASecondPresentationOfAnAssertionIsRefused`).
  A counter that moves backwards is not a replay but a clone: the library reports
  it, the credential is flagged, and the assertion is refused
  (`factor.go` `settleAssertion`, `register.go` `flagClone`,
  `TestACloneWarningRefusesAndFlagsTheFactor`). A credential whose counter never
  moves — a synchronizable passkey reports 0 every time — is not flagged, because
  the library warns only when the presented count is not above the stored one and
  the two are not both zero (`TestACredentialWhoseCounterStaysZeroKeepsPassing`).
- **A flagged credential starts nothing.** A factor whose clone flag is set refuses
  a challenge and a verification before the library is asked, so it is unusable
  until it is removed; the factor row's own status is the multi-factor module's,
  which owns that row (`factor.go` `Challenge`, `Verify`).
- **A passkey login resolves the account from the handle the client returns.** An
  unknown handle, an unknown credential and a signature that does not check read
  alike: no account, no proof, both in the log and on the wire
  (`method.go` `Answer`, `TestAPasskeyWithAnUnknownHandleIsRefused`,
  `TestAPasskeyWithATamperedSignatureIsRefused`). The method serves a login and a
  step-up, which are what a passkey proves, and refuses a link and an enroll
  (`method.go` `servesPurpose`, `TestTheMethodServesLoginAndStepUpOnly`).
- **Remove is idempotent and drops the handle with the last credential.** It
  deletes the credential row and, once the account holds none, the account's user
  handle, which is what a handle is for; the factor row that names the factor
  belongs to the multi-factor module and is not touched here
  (`factor.go` `Remove`, `TestRemoveDropsTheCredentialAndWithItTheHandle`).
- **Start probes the two tables.** A missing table ends the startup with
  `ErrNotMigrated`; the multi-factor module having taken this kind in is warned
  about when it has not, because a credential registered here would otherwise never
  be offered (`webauthn.go` `start`, `register.go` `probe`,
  `TestStartRefusesWhenATableIsMissing`).

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

CI runs the segments of `make check` as parallel jobs. On this module alone:

```
go -C pkg build ./authn/mfa/webauthn/...
go -C pkg vet ./authn/mfa/webauthn/...
go -C pkg test ./authn/mfa/webauthn/...
go -C pkg test -tags=integration ./authn/mfa/webauthn/...
go -C pkg test -race ./authn/mfa/webauthn/...
env -C pkg golangci-lint run ./authn/mfa/webauthn/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

`helpers_test.go` builds the factor over a real database handle
(`pkg/internal/dbtest`) and a virtual authenticator — one ES256 key, one
credential id, and the response bytes a client would produce — so the cases drive
the real protocol end to end. `factor_test.go` and `method_test.go` cover the
factor's ceremonies and the passkey login over that rig, on SQLite always and on
PostgreSQL in an integration run; `register_test.go` covers the counter
write-back, the clone flag and the startup probe. `config_test.go` covers the
relying-party defaults and the id check. `assembly_test.go` drives a real
registry through the descriptor and the real multi-factor module with stand-in
modules for the exclusive capabilities, which is what holds the descriptor, the
pending-row handoff and the registration-and-verification path together; the
configuration reader is supplied to the registry, because only a host-supplied
instance is reachable in `Prepare`.
`surface_test.go` type-checks the package from source and holds the export
surface.
