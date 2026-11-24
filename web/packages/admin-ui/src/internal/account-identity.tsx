/**
 * Account identity rendering shared by the console: display name and login
 * username, either alone, or the neutral copy -- never the opaque account id.
 */

import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { visuallyHiddenSx } from '@speed/ui-kit'
import { useAdminUiTranslation } from './translation.js'
import { isNotFound } from './async-section.js'
import { useAdminHasFeature } from './features.js'
import { useAdminOperations, type AdminAccount } from '../operations.js'

type AdminTranslate = ReturnType<typeof useAdminUiTranslation>['t']

const ACCOUNT_REFERENCE_KEYS = {
  name: 'common.accountReference.name',
  username: 'common.accountReference.identifier',
  nameWithUsername: 'common.accountReference.nameWithIdentifier',
} as const

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
 * The identity label for one account: `"Ada (ada)"`, `"ada"` or `"Ada"` as the
 * parts resolve, `undefined` when neither does. An email or phone never stands
 * in for the username.
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

/** The label a surface shows for `identity`, or the neutral copy when nothing resolves. */
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
 * The reference a notice inserts for `identity`, or `undefined` when nothing
 * resolves; it never falls back to the opaque account id.
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
 * A success notice that names an account, or its neutral sentence when nothing
 * resolves; the opaque account id never enters the copy.
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
 * The account read's outcome for one identifier: `name`, `username`, `refused`
 * (a failure other than 404), `missing` (404), and `gated` (the authn.admin
 * feature is absent, so no read was issued). An absent identifier issues no read.
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
  // No feature, no read; an absent identifier is equally not a lookup.
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
 * record carries, so it never issues the platform account read.
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
  // A system row carries no actor. It renders the empty placeholder, not the
  // neutral copy: "no actor" and "an account that could not be resolved" are
  // different facts.
  if (account === '') {
    return null
  }
  // A static credential subject is a configured principal, not an account the
  // library holds: it is named as recorded and issues no account read.
  if (isStaticPrincipal(account)) {
    return <>{account}</>
  }
  return <ResolvedAccountIdentity account={account} />
}

function ResolvedAccountIdentity({ account }: { readonly account: string }) {
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
  // permission problem: the answer says deleted or never an account, never
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
 * The neutral identity copy. A note becomes the visible title and a visually
 * hidden annotation, so it reaches assistive technology without visible text.
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
 * The marker an audit object that names an account begins with: an account id
 * has the same opaque shape as a session or invitation id, so the journal
 * states the type.
 */
export const ACCOUNT_OBJECT_PREFIX = 'account:'

/** Whether an audit object names an account. */
export function isAccountObject(object: string): boolean {
  return object.startsWith(ACCOUNT_OBJECT_PREFIX)
}

/**
 * The account id an audit object names, or `undefined` when it names something
 * else. Only the marker decides; an unmarked UUID-shaped value is never sent to
 * the account read.
 */
export function accountObjectId(object: string): string | undefined {
  return isAccountObject(object)
    ? object.slice(ACCOUNT_OBJECT_PREFIX.length)
    : undefined
}

/**
 * The prefix of a static credential subject's identifier (`static.operator`): a
 * configured principal, named as recorded, that issues no account read.
 */
const STATIC_PRINCIPAL_PREFIX = 'static.'

function isStaticPrincipal(value: string): boolean {
  return value.startsWith(STATIC_PRINCIPAL_PREFIX)
}

/** The impersonation actions whose `by` detail names the ending account. */
const IMPERSONATION_BY_ACTIONS: ReadonlySet<string> = new Set([
  'authn.impersonation.end',
  'authn.impersonation.revoke',
])

/** Whether one audit detail value is an account identifier (bare, unmarked by key). */
export function auditDetailNamesAccount(action: string, key: string): boolean {
  if (key === 'account' || key === 'on_behalf_of') {
    return true
  }
  if (key === 'by') {
    return IMPERSONATION_BY_ACTIONS.has(action)
  }
  return false
}

/**
 * One audit record's object: only an object with the account marker is resolved
 * through the account read; every other object renders as recorded.
 */
export function AuditObjectIdentity({ object }: { readonly object: string }) {
  const account = accountObjectId(object)
  if (account === undefined) {
    return <>{object}</>
  }
  return <AuditObjectAccount account={account} />
}

function AuditObjectAccount({ account }: { readonly account: string }) {
  const { t } = useAdminUiTranslation()
  const { name, username, refused, missing, gated } = useAccountIdentity(account)
  const label = identityLabel(name, username)
  if (label !== undefined) {
    return <>{label}</>
  }
  // The marked object names an account the read answered 404 for: the account
  // was deleted, or the identifier never named one. The marker says it is an
  // account, so the recorded identifier is never shown in its place.
  if (missing) {
    return <NeutralIdentity note={t('common.accountMissing')} />
  }
  // No read was issued: the identity is unresolved for that reason, and the
  // object names an account, so the cell keeps the neutral copy.
  if (gated) {
    return <NeutralIdentity note={t('common.accountUnresolved')} />
  }
  // The read was refused: the object names an account, so the cell renders
  // the neutral copy and never the recorded identifier.
  if (refused) {
    return <NeutralIdentity note={t('common.accountUnresolved')} />
  }
  return <NeutralIdentity />
}
