/**
 * NotificationPreferences: the per-type channel preference matrix.
 *
 * One row per declared notification type (GET /notifications/preferences,
 * whose effective channel set is the stored row when one exists and the
 * type's defaults otherwise), one column per channel of the platform's
 * closed channel vocabulary (in_app, email, sms -- the set
 * PUT /notifications/preferences/:typeKey/:channel accepts). Toggling a
 * cell writes that one channel's target state and re-reads the matrix,
 * so the checkboxes always show the server's answer rather than a local
 * guess.
 *
 * A type the taxonomy marks not unsubscribable (from
 * GET /notifications/types) renders its whole row disabled with a note:
 * a write to such a type is refused 409, so the control is offered as
 * unavailable rather than made to fail. When the taxonomy has not loaded
 * yet, the type is treated as unsubscribable-unknown and left enabled,
 * because the server stays the authority.
 *
 * The read is one of the four settled states (ui-kit's AsyncSection), with
 * the admission layer's bare 404 split out as the tenant refusal state and
 * no retry; the section opens with its own h2 so it nests under any page
 * heading.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Checkbox from '@mui/material/Checkbox'
import Skeleton from '@mui/material/Skeleton'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import Typography from '@mui/material/Typography'
import type { NotificationPreference } from '@speed/api-sdk'
import { errorCodeOf } from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { TenantAsyncSection } from './internal/tenant-section.js'
import {
  useNotificationPreferences,
  useNotificationTypes,
  useSetNotificationPreference,
} from './internal/queries.js'
import { useNotificationUiTranslation } from './internal/translation.js'

/** The platform's closed channel vocabulary, in display order. */
const CHANNELS = ['in_app', 'email', 'sms'] as const

/** The channel the current value carries (the effective set). */
function hasChannel(preference: NotificationPreference, channel: string): boolean {
  return preference.channels.includes(
    channel as NotificationPreference['channels'][number],
  )
}

export function NotificationPreferences() {
  const { t } = useNotificationUiTranslation()
  const preferences = useNotificationPreferences()
  const types = useNotificationTypes()
  const setPreference = useSetNotificationPreference()
  const [failureCode, setFailureCode] = useState<string | null>(null)

  const rows = preferences.data ?? []
  const pending = preferences.isPending && preferences.data === undefined
  const unsubscribable = new Map(
    (types.data ?? []).map((type) => [type.key, type.unsubscribable] as const),
  )

  const toggle = (
    preference: NotificationPreference,
    channel: (typeof CHANNELS)[number],
    enabled: boolean,
  ): void => {
    setFailureCode(null)
    setPreference.mutate(
      { typeKey: preference.type_key, channel, data: { enabled } },
      { onError: (error) => setFailureCode(errorCodeOf(error)) },
    )
  }

  return (
    <Box>
      <Typography variant="h5" component="h2" sx={{ mb: 1 }}>
        {t('preferences.title')}
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        {t('preferences.description')}
      </Typography>

      <InlineError code={failureCode} />

      <TenantAsyncSection<readonly NotificationPreference[]>
        pending={pending}
        payload={pending ? undefined : rows}
        error={preferences.error}
        empty={rows.length === 0}
        headingLevel="h3"
        loading={
          <Box role="status" aria-busy="true" aria-label={t('preferences.loading')}>
            <Skeleton variant="text" width="36%" />
            <Skeleton variant="text" width="54%" />
          </Box>
        }
        errorState={{
          title: t('preferences.error.title'),
          description: t('preferences.error.description'),
          retryLabel: t('preferences.retry'),
          onRetry: () => void preferences.refetch(),
        }}
        emptyState={{
          title: t('preferences.empty.title'),
          description: t('preferences.empty.description'),
        }}
      >
        {(settled) => (
          <Table aria-label={t('preferences.title')} size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('preferences.column.type')}</TableCell>
                {CHANNELS.map((channel) => (
                  <TableCell key={channel} align="center">
                    {t(`preferences.channel.${channel}`)}
                  </TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>
              {settled.map((preference) => {
                const locked = unsubscribable.get(preference.type_key) === false
                return (
                  <TableRow key={preference.type_key}>
                    <TableCell>
                      <Typography variant="body2">
                        {preference.type_key}
                      </Typography>
                      {locked && (
                        <Typography variant="caption" color="text.secondary">
                          {t('preferences.unsubscribableNote')}
                        </Typography>
                      )}
                    </TableCell>
                    {CHANNELS.map((channel) => (
                      <TableCell key={channel} align="center">
                        <Checkbox
                          checked={hasChannel(preference, channel)}
                          disabled={locked || setPreference.isPending}
                          onChange={(event) =>
                            toggle(preference, channel, event.target.checked)
                          }
                          slotProps={{
                            input: {
                              'aria-label': t('preferences.channelToggleAria', {
                                type: preference.type_key,
                                channel: t(`preferences.channel.${channel}`),
                              }),
                            },
                          }}
                        />
                      </TableCell>
                    ))}
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </TenantAsyncSection>
    </Box>
  )
}
