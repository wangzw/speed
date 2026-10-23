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
import { useConsoleUiTranslation } from './translation.js'
import { isNotFound } from './async-section.js'
import { useConsoleHasFeature } from './features.js'
import { useConsoleOperations } from '../operations.js'

/**
 * The account read's outcome for one identifier, for surfaces that need the
 * readable name without rendering the unit -- a hidden annotation beside the
 * recorded value, for one. `name` is the display name when the read answers a
 * non-empty one; `refused` says the read failed for a reason other than the
 * account not existing (a permission refusal, a transport failure); and
 * `missing` says the read was answered 404 -- the account was deleted, or
 * the identifier never named an account. `gated` says the read was never
 * issued because the authn.admin feature is absent -- the identity is
 * unresolved for that reason, which is neither a refusal nor a deletion.
 */
export function useAccountIdentity(account: string): {
  readonly name: string | undefined
  readonly refused: boolean
  readonly missing: boolean
  readonly gated: boolean
} {
  const hasFeature = useConsoleHasFeature()
  const operations = useConsoleOperations()
  // No feature, no read: the account administration face (and the identity
  // lookup it carries) does not exist in this process.
  const gated = !hasFeature('authn.admin')
  const read = operations.useAccount({ id: gated ? undefined : account })
  const recorded = read.data?.display_name
  return {
    name: recorded !== undefined && recorded !== '' ? recorded : undefined,
    refused: read.isError && !isNotFound(read.error),
    missing: isNotFound(read.error),
    gated,
  }
}

export function AccountIdentity({ account }: { readonly account: string }) {
  const { t } = useConsoleUiTranslation()
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
