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
 * account. While the read is pending, or when the identifier names no
 * account (a system or import actor), it shows the recorded value instead
 * of a spinner, so the surface is never blank. The lookup is keyed by the
 * account id, so rows sharing an account -- and any re-render -- reuse one
 * cached read.
 */

import Typography from '@mui/material/Typography'
import { useConsoleUiTranslation } from './translation.js'
import { useConsoleOperations } from '../operations.js'

/**
 * The account read's outcome for one identifier, for surfaces that need the
 * readable name without rendering the unit -- an aria label that names the
 * account, for one. `name` is the display name when the read answers a
 * non-empty one; `refused` says the read failed, so the recorded identifier
 * is not passed off as resolved.
 */
export function useAccountIdentity(account: string): {
  readonly name: string | undefined
  readonly refused: boolean
} {
  const operations = useConsoleOperations()
  const read = operations.useAccount({ id: account })
  const recorded = read.data?.display_name
  return {
    name: recorded !== undefined && recorded !== '' ? recorded : undefined,
    refused: read.isError,
  }
}

export function AccountIdentity({ account }: { readonly account: string }) {
  const { t } = useConsoleUiTranslation()
  const { name, refused } = useAccountIdentity(account)
  if (name !== undefined) {
    return <>{name}</>
  }
  if (refused) {
    return (
      <Typography
        component="span"
        variant="body2"
        sx={{ fontStyle: 'italic' }}
        title={t('common.accountUnresolved')}
        aria-label={`${account} (${t('common.accountUnresolved')})`}
      >
        {account}
      </Typography>
    )
  }
  return <>{account}</>
}
