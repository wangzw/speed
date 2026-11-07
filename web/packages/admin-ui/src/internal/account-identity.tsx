/**
 * One account identity rendered the way every console surface names an
 * account: its display name with its login username beside it, the username
 * alone when the account has no display name, the display name alone when it
 * has no username, and a neutral "account unavailable" when neither
 * resolves. The account's opaque identifier is the internal key -- a path
 * parameter and a row key -- and never visible text, a copy target, an
 * accessible name or a page title.
 *
 * The unit is shared by every surface that records an account: the audit
 * record's actor (the audit query, the dashboard's recent activity and the
 * audit detail), an impersonation record's actor and target (the record list
 * and the in-force panel of the operator's own session), an invitation's
 * inviter, a membership's account, and an authorization binding's account.
 *
 * `AccountIdentity` reads the account through the bound surface. The account
 * read is gated on authn.account.read, which a page's own read permission
 * does not imply, so a viewer refused the lookup sees the neutral copy with
 * a note that the identity could not be resolved, rather than the raw
 * identifier passed off as a resolved (or system) account. The read is also
 * gated on the authn.admin feature: a process that does not serve the
 * administration face never issues it, and the neutral copy carries the same
 * unresolved note. An answer of 404 is a different fact -- the identifier
 * names no account, deleted or never one -- and gets its own note, never the
 * permission or feature one. While the read is pending it shows the neutral
 * copy instead of a spinner, so the surface is never blank. The lookup is
 * keyed by the account id, so rows sharing an account -- and any re-render --
 * reuse one cached read.
 */

import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { visuallyHiddenSx } from '@speed/ui-kit'
import { useAdminUiTranslation } from './translation.js'
import { isNotFound } from './async-section.js'
import { useAdminHasFeature } from './features.js'
import { useAdminOperations, type AdminAccount } from '../operations.js'

/** The admin-ui namespace's translation function. */
type AdminTranslate = ReturnType<typeof useAdminUiTranslation>['t']

/**
 * The three reference forms a notice inserts for a resolved identity. The
 * confirmation dialogs carry the same forms, so an account is named one way
 * across the console: `"Ada" (ada)` when both resolve, `"ada"` when only the
 * username does, and `"Ada"` when only the display name does. An identity
 * with neither resolves nothing and a notice uses its neutral sentence.
 */
const ACCOUNT_REFERENCE_KEYS = {
  name: 'common.accountReference.name',
  username: 'common.accountReference.identifier',
  nameWithUsername: 'common.accountReference.nameWithIdentifier',
} as const

/** The identifier kind that names an account: the login username. */
const USERNAME_KIND = 'username'

/** The login username among an account's identifiers, when it carries one. */
export function accountUsername(
  identifiers: readonly {
    readonly kind: string
    readonly value: string
  }[],
): string | undefined {
  return identifiers.find((identifier) => identifier.kind === USERNAME_KIND)
    ?.value
}

/**
 * The identity label for one account: `"Ada (ada)"` when both the display
 * name and the username resolve, `"ada"` when only the username does,
 * `"Ada"` when only the display name does, and `undefined` when neither
 * does. A second login identifier -- an email or a phone -- never stands in
 * for the username.
 */
export function identityLabel(
  name: string | undefined,
  username: string | undefined,
): string | undefined {
  const shownName = name === undefined || name === '' ? undefined : name
  const shownUsername =
    username === undefined || username === '' ? undefined : username
  if (shownName !== undefined && shownUsername !== undefined) {
    return `${shownName} (${shownUsername})`
  }
  return shownUsername ?? shownName
}

/** A resolved account identity as a notice names it. */
export type AccountIdentityValue = {
  readonly name: string | undefined
  readonly username: string | undefined
}

/** The identity an account view carries, with an empty display name read as none. */
export function accountIdentityValue(
  account: Pick<AdminAccount, 'display_name' | 'identifiers'>,
): AccountIdentityValue {
  return {
    name: account.display_name === '' ? undefined : account.display_name,
    username: accountUsername(account.identifiers),
  }
}

/**
 * The label a surface shows for `identity`, or the neutral
 * `common.accountUnavailable` copy when it resolves neither a display name
 * nor a username. The opaque account id is never the label.
 */
export function accountLabel(
  t: AdminTranslate,
  identity: AccountIdentityValue,
): string {
  return (
    identityLabel(identity.name, identity.username) ??
    t('common.accountUnavailable')
  )
}

/**
 * The reference a notice inserts for `identity`: the display name with its
 * login username when both resolve, either alone otherwise, and `undefined`
 * when neither does. A notice must never fall back to the opaque account id
 * -- an operator cannot match it to a row.
 */
export function accountReference(
  t: AdminTranslate,
  identity: AccountIdentityValue,
): string | undefined {
  const name = identity.name === '' ? undefined : identity.name
  const username = identity.username === '' ? undefined : identity.username
  if (name !== undefined) {
    return username === undefined
      ? t(ACCOUNT_REFERENCE_KEYS.name, { name })
      : t(ACCOUNT_REFERENCE_KEYS.nameWithUsername, {
          name,
          identifier: username,
        })
  }
  return username === undefined
    ? undefined
    : t(ACCOUNT_REFERENCE_KEYS.username, { identifier: username })
}

/** The two bundle keys of a success notice that names an account. */
export type AccountNoticeKeys = {
  /** The sentence carrying the account reference in `{{account}}`. */
  readonly named: string
  /** The sentence naming no account, for an identity that resolved nothing. */
  readonly unnamed: string
}

/**
 * A success notice that names an account: the named sentence with the
 * account reference in `{{account}}`, or the neutral sentence when the
 * identity resolved neither a display name nor a username. The opaque
 * account id never enters the copy.
 */
export function accountNotice(
  t: AdminTranslate,
  keys: AccountNoticeKeys,
  identity: AccountIdentityValue,
  values: Record<string, string> = {},
): string {
  const reference = accountReference(t, identity)
  return reference === undefined
    ? t(keys.unnamed, values)
    : t(keys.named, { ...values, account: reference })
}

/**
 * The account read's outcome for one identifier, for surfaces that need the
 * readable identity without rendering the unit -- a hidden annotation beside
 * the recorded value, or a confirmation that names the account, for one.
 * `name` is the display name when the read answers a non-empty one;
 * `username` is the login username among the identifiers the read carries,
 * so an account with no username resolves none -- an email or a phone never
 * stands in for it; `refused` says the read failed for a reason other than
 * the account not existing (a permission refusal, a transport failure); and
 * `missing` says the read was answered 404 -- the account was deleted, or
 * the identifier never named an account. `gated` says the read was never
 * issued because the authn.admin feature is absent -- the identity is
 * unresolved for that reason, which is neither a refusal nor a deletion. An
 * absent account issues no read: the caller has no identifier to resolve
 * yet.
 */
export function useAccountIdentity(account: string | undefined): {
  readonly name: string | undefined
  readonly username: string | undefined
  readonly refused: boolean
  readonly missing: boolean
  readonly gated: boolean
} {
  const hasFeature = useAdminHasFeature()
  const operations = useAdminOperations()
  // No feature, no read: the account administration face (and the identity
  // lookup it carries) does not exist in this process. An absent identifier
  // is equally not a lookup: there is nothing to resolve.
  const gated = !hasFeature('authn.admin')
  const read = operations.useAccount({
    id: gated || account === undefined || account === '' ? undefined : account,
  })
  const recorded = read.data?.display_name
  return {
    name: recorded !== undefined && recorded !== '' ? recorded : undefined,
    username: accountUsername(read.data?.identifiers ?? []),
    refused: read.isError && !isNotFound(read.error),
    missing: isNotFound(read.error),
    gated,
  }
}

/**
 * One account identifier rendered from the identity its own tenant-scoped
 * record carries, for the organization surface: the member read answers each
 * row's `display_name` and `username`, so the identity never needs the
 * platform account read that `AccountIdentity` issues. The four branches are
 * the same as everywhere else, and an absent pair renders the neutral copy
 * -- never the recorded identifier.
 */
export function TenantAccountIdentity({
  name,
  username,
}: {
  readonly name: string | undefined
  readonly username: string | undefined
}) {
  const { t } = useAdminUiTranslation()
  const label = identityLabel(name, username)
  return <>{label ?? t('common.accountUnavailable')}</>
}

export function AccountIdentity({ account }: { readonly account: string }) {
  const { t } = useAdminUiTranslation()
  const { name, username, refused, missing, gated } = useAccountIdentity(account)
  const label = identityLabel(name, username)
  if (label !== undefined) {
    return <>{label}</>
  }
  // The identity lookup is not part of this deployment: the neutral copy
  // stays, annotated as unresolved -- never as deleted, which is a fact only
  // a read could have answered.
  if (gated) {
    return <NeutralIdentity note={t('common.accountUnresolved')} />
  }
  // A 404 is "this identifier names no account (any more)", which is not a
  // permission problem: the marker says deleted or not-an-account, never
  // that the viewer lacks the account read.
  if (missing) {
    return <NeutralIdentity note={t('common.accountMissing')} />
  }
  if (refused) {
    return <NeutralIdentity note={t('common.accountUnresolved')} />
  }
  return <NeutralIdentity />
}

/**
 * The neutral identity copy. A note, when there is one, is the visible title
 * and a visually hidden annotation, so the marker reaches assistive
 * technology without becoming visible text beside the neutral sentence.
 */
function NeutralIdentity({ note }: { readonly note?: string }) {
  const { t } = useAdminUiTranslation()
  const label = t('common.accountUnavailable')
  if (note === undefined) {
    return <>{label}</>
  }
  return (
    <Typography
      component="span"
      variant="body2"
      sx={{ fontStyle: 'italic' }}
      title={note}
    >
      {label}
      <Box component="span" sx={visuallyHiddenSx}>
        {` (${note})`}
      </Box>
    </Typography>
  )
}

/**
 * The shape an account identifier always has: the account module creates
 * every account id as a UUID. The shape makes a value a candidate, not a
 * fact -- a session id has the same shape -- so the account read decides;
 * the shape only keeps a tenant alias, a role name or a permission code from
 * being sent to that read.
 */
const ACCOUNT_IDENTIFIER_SHAPE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** Whether a value has the shape an account identifier always has. */
export function isAccountIdentifierShaped(value: string): boolean {
  return ACCOUNT_IDENTIFIER_SHAPE.test(value)
}

/**
 * The impersonation actions whose `by` detail is the account that ended or
 * revoked the grant. The factor events write `by` as the actor class (self,
 * admin, system), which is not an account and renders as recorded.
 */
const IMPERSONATION_BY_ACTIONS: ReadonlySet<string> = new Set([
  'authn.impersonation.end',
  'authn.impersonation.revoke',
])

/**
 * Whether one audit detail value is an account identifier. The journal writes
 * `on_behalf_of` only from an impersonating identity, so it is always an
 * account id. `by` is one only on an impersonation end or revoke; the factor
 * events carry a class there instead.
 */
export function auditDetailNamesAccount(action: string, key: string): boolean {
  if (key === 'on_behalf_of') {
    return true
  }
  if (key === 'by') {
    return IMPERSONATION_BY_ACTIONS.has(action)
  }
  return false
}

/**
 * One audit record's object. A value shaped like an account identifier is
 * resolved through the account read, and the read's answer decides: an
 * account renders its identity, a value the read answers 404 for is not an
 * account (a session id has the same shape) and renders as recorded. Every
 * other value -- a tenant alias, a role name, a permission code -- is not an
 * account identifier by shape and renders as recorded without a read. A
 * refusal and an absent admin face both keep the neutral copy and never the
 * recorded identifier: the value may name an account, and neither state can
 * prove it does not.
 */
export function AuditObjectIdentity({ object }: { readonly object: string }) {
  if (!isAccountIdentifierShaped(object)) {
    return <>{object}</>
  }
  return <AuditObjectAccount account={object} />
}

function AuditObjectAccount({ account }: { readonly account: string }) {
  const { t } = useAdminUiTranslation()
  const { name, username, refused, missing, gated } = useAccountIdentity(account)
  const label = identityLabel(name, username)
  if (label !== undefined) {
    return <>{label}</>
  }
  // The read answered "no such account", so this value does not name one.
  if (missing) {
    return <>{account}</>
  }
  // No read was issued: the identity is unresolved for that reason, and the
  // value may name an account, so the cell keeps the neutral copy.
  if (gated) {
    return <NeutralIdentity note={t('common.accountUnresolved')} />
  }
  // The read was refused: the value may name an account, so the cell renders
  // the neutral copy and never the recorded identifier.
  if (refused) {
    return <NeutralIdentity note={t('common.accountUnresolved')} />
  }
  return <NeutralIdentity />
}
