/**
 * One audit record's actor cell, shared by the two audit-record tables
 * (the audit query page and the dashboard's recent activity): the readable
 * identity rather than the raw account identifier.
 *
 * It reads the account's display name through the bound surface and shows
 * it. The account read is gated on authn.account.read, which a page's own
 * audit.read does not imply, so a viewer holding audit.read but not
 * account.read is refused the lookup: the cell then shows the recorded
 * value with a note that the identity could not be resolved, rather than
 * passing the raw id off as a resolved (or system) actor. While the read
 * is pending, or when the actor names no account (a system or import
 * actor), it shows the recorded value instead of a spinner, so the column
 * is never blank. The lookup is keyed by the account id, so a page of rows
 * that share an actor -- and any re-render -- reuses one cached read.
 */

import Typography from '@mui/material/Typography'
import { useConsoleUiTranslation } from './translation.js'
import { useConsoleOperations } from '../operations.js'

export function AuditActorCell({ actor }: { readonly actor: string }) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const account = operations.useAccount({ id: actor })
  const name = account.data?.display_name
  if (name !== undefined && name !== '') {
    return <>{name}</>
  }
  if (account.isError) {
    return (
      <Typography
        component="span"
        variant="body2"
        sx={{ fontStyle: 'italic' }}
        title={t('audit.actorUnresolved')}
        aria-label={`${actor} (${t('audit.actorUnresolved')})`}
      >
        {actor}
      </Typography>
    )
  }
  return <>{actor}</>
}
