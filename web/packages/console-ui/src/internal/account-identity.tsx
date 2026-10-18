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
 * account. An answer of 404 is a different fact -- the identifier names no
 * account, deleted or never one -- and gets its own note, never the
 * permission one. While the read is pending, or when the identifier names no
 * account, it shows the recorded value instead of a spinner, so the surface
 * is never blank. The lookup is keyed by the account id, so rows sharing an
 * account -- and any re-render -- reuse one cached read.
 */

import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { primaryColumnMaxWidthSx } from '@speed/ui-kit'
import { useConsoleUiTranslation } from './translation.js'
import { isNotFound } from './async-section.js'
import { useConsoleOperations } from '../operations.js'

/**
 * The account read's outcome for one identifier, for surfaces that need the
 * readable name without rendering the unit -- an aria label that names the
 * account, for one. `name` is the display name when the read answers a
 * non-empty one; `refused` says the read failed for a reason other than the
 * account not existing (a permission refusal, a transport failure), and
 * `missing` says the read was answered 404 -- the account was deleted, or
 * the identifier never named an account.
 */
export function useAccountIdentity(account: string): {
  readonly name: string | undefined
  readonly refused: boolean
  readonly missing: boolean
} {
  const operations = useConsoleOperations()
  const read = operations.useAccount({ id: account })
  const recorded = read.data?.display_name
  return {
    name: recorded !== undefined && recorded !== '' ? recorded : undefined,
    refused: read.isError && !isNotFound(read.error),
    missing: isNotFound(read.error),
  }
}

export function AccountIdentity({ account }: { readonly account: string }) {
  const { t } = useConsoleUiTranslation()
  const { name, refused, missing } = useAccountIdentity(account)
  if (name !== undefined) {
    return <>{name}</>
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
        aria-label={`${account} (${t('common.accountMissing')})`}
      >
        {account}
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
        aria-label={`${account} (${t('common.accountUnresolved')})`}
      >
        {account}
      </Typography>
    )
  }
  return <>{account}</>
}

/**
 * `AccountIdentity` inside the shared primary-column width cap: the
 * identity truncates with an ellipsis instead of widening a table that
 * would otherwise overflow its container. The complete value stays on the
 * cell's title and copy control (the column's `cellFullValue`).
 */
export function CappedAccountIdentity({
  account,
}: {
  readonly account: string
}) {
  return (
    <Box
      component="span"
      sx={{
        ...primaryColumnMaxWidthSx,
        display: 'inline-block',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
        verticalAlign: 'bottom',
      }}
    >
      <AccountIdentity account={account} />
    </Box>
  )
}
