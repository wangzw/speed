/**
 * NotificationPreferences: the per-type channel preference matrix.
 *
 * One row per declared notification type (GET /notifications/preferences,
 * whose effective channel set is the stored row when one exists and the
 * type's defaults otherwise), one column per channel the host serves. The
 * in-app column is always present; the optional columns come from
 * NotificationPresenceProvider's `channels` (email, sms), ordered by the
 * platform's closed channel vocabulary (in_app, email, sms -- the set
 * PUT /notifications/preferences/:typeKey/:channel accepts). Without a
 * provider no optional channel is offered: the matrix renders the in-app
 * column alone. Toggling a cell writes that one channel's target state and
 * re-reads the matrix, so the checkboxes always show the server's answer
 * rather than a local guess.
 *
 * A type the taxonomy marks not unsubscribable (from
 * GET /notifications/types) renders its whole row disabled with a note:
 * a write to such a type is refused 409, so the control is offered as
 * unavailable rather than made to fail. When the taxonomy has not loaded
 * yet, the type is treated as unsubscribable-unknown and left enabled,
 * because the server stays the authority.
 *
 * A host may relabel a type and group the rows through `typeLabel` and
 * `groupLabel`; the default type label is the type key, and rows render
 * grouped only when `groupLabel` is supplied.
 *
 * The read is one of the four settled states (ui-kit's AsyncSection), with
 * the admission layer's bare 404 split out as the tenant refusal state and
 * no retry; the section opens with its own h2 so it nests under any page
 * heading.
 */

import { Fragment, useState } from 'react'
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
import { useNotificationPresence } from './NotificationPresence.js'
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

export type NotificationPreferencesProps = {
  /**
   * The visible label of one type row; defaults to the type key. The
   * label is also the type named in each cell's accessible name.
   */
  readonly typeLabel?: (type: {
    readonly key: string
    readonly group: string
  }) => string
  /**
   * The heading of one group of rows. Supplying it turns on grouped
   * rendering (rows grouped in first-seen order); without it the rows
   * render as one ungrouped table.
   */
  readonly groupLabel?: (group: string) => string
}

/** The channel the current value carries (the effective set). */
function hasChannel(preference: NotificationPreference, channel: string): boolean {
  return preference.channels.includes(
    channel as NotificationPreference['channels'][number],
  )
}

/** A group of preference rows, in first-seen order. */
interface PreferenceGroup {
  readonly group: string
  readonly rows: NotificationPreference[]
}

/** Groups rows by their `group` in first-seen order, preserving row order
 * within each group; an empty group value still forms a group. */
function groupRows(
  rows: readonly NotificationPreference[],
): PreferenceGroup[] {
  const groups: PreferenceGroup[] = []
  const byGroup = new Map<string, NotificationPreference[]>()
  for (const preference of rows) {
    const existing = byGroup.get(preference.group)
    if (existing === undefined) {
      const created: NotificationPreference[] = [preference]
      byGroup.set(preference.group, created)
      groups.push({ group: preference.group, rows: created })
    } else {
      existing.push(preference)
    }
  }
  return groups
}

export function NotificationPreferences({
  typeLabel = (type) => type.key,
  groupLabel,
}: NotificationPreferencesProps = {}) {
  const { t } = useNotificationUiTranslation()
  const presence = useNotificationPresence()
  const preferences = useNotificationPreferences()
  const types = useNotificationTypes()
  const setPreference = useSetNotificationPreference()
  const [failureCode, setFailureCode] = useState<string | null>(null)

  const rows = preferences.data ?? []
  const pending = preferences.isPending && preferences.data === undefined
  const unsubscribable = new Map(
    (types.data ?? []).map((type) => [type.key, type.unsubscribable] as const),
  )

  // The in-app column is always offered; an optional channel only when
  // the host's presence facts say the deployment serves it. The order is
  // the closed vocabulary's, never the order the host listed them in.
  const columns = CHANNELS.filter(
    (channel) => channel === 'in_app' || presence.channels.includes(channel),
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

  const renderRow = (preference: NotificationPreference) => {
    const locked = unsubscribable.get(preference.type_key) === false
    const label = typeLabel({
      key: preference.type_key,
      group: preference.group,
    })
    return (
      <TableRow key={preference.type_key}>
        <TableCell>
          <Typography variant="body2">{label}</Typography>
          {locked && (
            <Typography variant="caption" color="text.secondary">
              {t('preferences.unsubscribableNote')}
            </Typography>
          )}
        </TableCell>
        {columns.map((channel) => (
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
                    type: label,
                    channel: t(`preferences.channel.${channel}`),
                  }),
                },
              }}
            />
          </TableCell>
        ))}
      </TableRow>
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
                {columns.map((channel) => (
                  <TableCell key={channel} align="center">
                    {t(`preferences.channel.${channel}`)}
                  </TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>
              {groupLabel === undefined
                ? settled.map(renderRow)
                : groupRows(settled).map((entry) => (
                    <Fragment key={entry.group}>
                      {/* A full-width divider row, not a header cell: it
                          names a section of the single table without
                          claiming to head the columns. */}
                      <TableRow>
                        <TableCell colSpan={columns.length + 1}>
                          <Typography variant="overline">
                            {groupLabel(entry.group)}
                          </Typography>
                        </TableCell>
                      </TableRow>
                      {entry.rows.map(renderRow)}
                    </Fragment>
                  ))}
            </TableBody>
          </Table>
        )}
      </TenantAsyncSection>
    </Box>
  )
}
