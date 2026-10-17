/**
 * One account identifier rendered as its readable identity, shared by every
 * console surface that records an account identifier: the audit record's
 * actor (the audit query, the dashboard's recent activity and the audit
 * detail), an invitation's inviter and a membership's account.
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

export function AccountIdentity({ account }: { readonly account: string }) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const read = operations.useAccount({ id: account })
  const name = read.data?.display_name
  if (name !== undefined && name !== '') {
    return <>{name}</>
  }
  if (read.isError) {
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
