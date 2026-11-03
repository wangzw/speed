/**
 * One account identifier rendered as its readable identity, shared by every
 * console surface that records an account identifier: the audit record's
 * actor (the audit query, the dashboard's recent activity and the audit
 * detail), an impersonation record's actor and target (the record list and
 * the in-force panel of the operator's own session), an invitation's
 * inviter, a membership's account, and an authorization binding's account.
 *
 * It reads the account's display name through the bound surface and shows
 * it. The account read is gated on authn.account.read, which a page's own
 * read permission does not imply, so a viewer refused the lookup sees the
 * recorded value with a note that the identity could not be resolved,
 * rather than the raw identifier passed off as a resolved (or system)
 * account. The read is also gated on the authn.admin feature: a process
 * that does not serve the administration face never issues it, and the
 * recorded value carries the same unresolved note. An answer of 404 is a
 * different fact -- the identifier names no account, deleted or never one --
 * and gets its own note, never the permission or feature one. While the
 * read is pending, or when the identifier names no account, it shows the
 * recorded value instead of a spinner, so the surface is never blank. The
 * lookup is keyed by the account id, so rows sharing an account -- and any
 * re-render -- reuse one cached read.
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
 * identifier does, and `"Ada"` when only the display name does.
 */
const ACCOUNT_REFERENCE_KEYS = {
  name: 'common.accountReference.name',
  identifier: 'common.accountReference.identifier',
  nameWithIdentifier: 'common.accountReference.nameWithIdentifier',
} as const

/** A resolved account identity as a notice names it. */
export type AccountIdentityValue = {
  readonly name: string | undefined
  readonly identifier: string | undefined
}

/** The identity an account view carries, with an empty display name read as none. */
export function accountIdentityValue(
  account: Pick<AdminAccount, 'display_name' | 'identifiers'>,
): AccountIdentityValue {
  return {
    name: account.display_name === '' ? undefined : account.display_name,
    identifier: account.identifiers[0]?.value,
  }
}

/**
 * The reference a notice inserts for `identity`: the display name with its
 * first login identifier when both resolve, either alone otherwise, and
 * `undefined` when neither does. A notice must never fall back to the opaque
 * account id -- an operator cannot match it to a row.
 */
export function accountReference(
  t: AdminTranslate,
  identity: AccountIdentityValue,
): string | undefined {
  if (identity.name !== undefined) {
    return identity.identifier === undefined
      ? t(ACCOUNT_REFERENCE_KEYS.name, { name: identity.name })
      : t(ACCOUNT_REFERENCE_KEYS.nameWithIdentifier, {
          name: identity.name,
          identifier: identity.identifier,
        })
  }
  return identity.identifier === undefined
    ? undefined
    : t(ACCOUNT_REFERENCE_KEYS.identifier, {
        identifier: identity.identifier,
      })
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
 * identity resolved neither a display name nor a login identifier. The
 * opaque account id never enters the copy.
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
 * readable identity without rendering the unit -- a hidden annotation beside the
 * recorded value, or a confirmation that names the account, for one. `name` is
 * the display name when the read answers a non-empty one; `identifier` is the
 * first login identifier's value when the read carries one, so a surface can
 * name an account that has no display name (the view sorts identifiers by kind
 * and value, so the first is the one to name the account by); `refused` says
 * the read failed for a reason other than the account not existing (a
 * permission refusal, a transport failure); and `missing` says the read was
 * answered 404 -- the account was deleted, or the identifier never named an
 * account. `gated` says the read was never issued because the authn.admin
 * feature is absent -- the identity is unresolved for that reason, which is
 * neither a refusal nor a deletion. An absent account issues no read: the
 * caller has no identifier to resolve yet.
 */
export function useAccountIdentity(account: string | undefined): {
  readonly name: string | undefined
  readonly identifier: string | undefined
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
    identifier: read.data?.identifiers[0]?.value,
    refused: read.isError && !isNotFound(read.error),
    missing: isNotFound(read.error),
    gated,
  }
}

/**
 * One account identifier rendered from the identity its own tenant-scoped
 * record carries, for the workspace surface: the member read answers each
 * row's `display_name`, so the identity never needs the platform account
 * read that `AccountIdentity` issues. A name the record carries renders
 * as-is; an absent or empty one -- the read has not settled, was refused, or
 * the record's account has no display name -- renders the recorded
 * identifier with the same unresolved note, because the identifier alone is
 * not a resolved identity.
 */
export function TenantAccountIdentity({
  account,
  name,
}: {
  readonly account: string
  readonly name: string | undefined
}) {
  const { t } = useAdminUiTranslation()
  if (name !== undefined && name !== '') {
    return <>{name}</>
  }
  return (
    <Typography
      component="span"
      variant="body2"
      sx={{ fontStyle: 'italic' }}
      title={t('common.accountUnresolved')}
    >
      {account}
      <Box component="span" sx={visuallyHiddenSx}>
        {` (${t('common.accountUnresolved')})`}
      </Box>
    </Typography>
  )
}

export function AccountIdentity({ account }: { readonly account: string }) {
  const { t } = useAdminUiTranslation()
  const { name, refused, missing, gated } = useAccountIdentity(account)
  if (name !== undefined) {
    return <>{name}</>
  }
  // The identity lookup is not part of this deployment: the recorded value
  // stays, annotated as unresolved -- never as deleted, which is a fact
  // only a read could have answered.
  if (gated) {
    return (
      <Typography
        component="span"
        variant="body2"
        sx={{ fontStyle: 'italic' }}
        title={t('common.accountUnresolved')}
      >
        {account}
        <Box component="span" sx={visuallyHiddenSx}>
          {` (${t('common.accountUnresolved')})`}
        </Box>
      </Typography>
    )
  }
  // A 404 is "this identifier names no account (any more)", which is not a
  // permission problem: the marker says deleted or not-an-account, never
  // that the viewer lacks the account read.
  if (missing) {
    return (
      <Typography
        component="span"
        variant="body2"
        sx={{ fontStyle: 'italic' }}
        title={t('common.accountMissing')}
      >
        {account}
        <Box component="span" sx={visuallyHiddenSx}>
          {` (${t('common.accountMissing')})`}
        </Box>
      </Typography>
    )
  }
  if (refused) {
    return (
      <Typography
        component="span"
        variant="body2"
        sx={{ fontStyle: 'italic' }}
        title={t('common.accountUnresolved')}
      >
        {account}
        <Box component="span" sx={visuallyHiddenSx}>
          {` (${t('common.accountUnresolved')})`}
        </Box>
      </Typography>
    )
  }
  return <>{account}</>
}
