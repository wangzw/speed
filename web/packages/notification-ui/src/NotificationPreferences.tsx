/**
 * The per-type channel preference matrix: one row per notification type,
 * one column per served channel. Unsubscribable types are unavailable.
 */

import { Fragment, useId, type ChangeEvent } from 'react'
import Box from '@mui/material/Box'
import Checkbox from '@mui/material/Checkbox'
import Skeleton from '@mui/material/Skeleton'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableContainer from '@mui/material/TableContainer'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import Typography from '@mui/material/Typography'
import type { NotificationPreference } from '@speed/api-sdk'
import {
  SectionCard,
  WriteFeedbackRegion,
  useActivationGuard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { useNotificationPresence } from './NotificationPresence.js'
import { useNotificationUiErrorText } from './internal/error-text.js'
import { TenantAsyncSection } from './internal/tenant-section.js'
import {
  useNotificationPreferences,
  useNotificationTypes,
  useSetNotificationPreference,
} from './internal/queries.js'
import { useNotificationUiTranslation } from './internal/translation.js'

const CHANNELS = ['in_app', 'email', 'sms'] as const

/** Props shaping the preference matrix: a per-type label and an optional
 * group heading that enables grouped rendering. */
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

function hasChannel(preference: NotificationPreference, channel: string): boolean {
  return preference.channels.includes(
    channel as NotificationPreference['channels'][number],
  )
}

interface PreferenceGroup {
  readonly group: string
  readonly rows: NotificationPreference[]
}

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

interface PreferenceCellProps {
  readonly ariaLabel: string
  readonly checked: boolean
  readonly busy: boolean
  readonly unavailable: boolean
  readonly describedBy?: string
  readonly onToggle: (checked: boolean) => void
}

function PreferenceCell({
  ariaLabel,
  checked,
  busy,
  unavailable,
  describedBy,
  onToggle,
}: PreferenceCellProps) {
  const guardedToggle = useActivationGuard(
    (event: ChangeEvent<HTMLInputElement>) => {
      onToggle(event.target.checked)
    },
    { busy, unavailable },
  )
  return (
    <Checkbox
      checked={checked}
      onChange={guardedToggle}
      slotProps={{
        input: {
          'aria-label': ariaLabel,
          'aria-busy': busy || undefined,
          'aria-disabled': busy || unavailable || undefined,
          'aria-describedby': describedBy,
        },
      }}
    />
  )
}

/** Renders the preference matrix, one row per type and one column per
 * served channel. */
export function NotificationPreferences({
  typeLabel = (type) => type.key,
  groupLabel,
}: NotificationPreferencesProps = {}) {
  const { t } = useNotificationUiTranslation()
  const presence = useNotificationPresence()
  const preferences = useNotificationPreferences()
  const types = useNotificationTypes()
  const setPreference = useSetNotificationPreference()
  const feedback = useWriteFeedback()
  const resolveError = useNotificationUiErrorText()
  const noteIdBase = useId()

  const rows = preferences.data ?? []
  const pending = preferences.isPending && preferences.data === undefined
  const unsubscribable = new Map(
    (types.data ?? []).map((type) => [type.key, type.unsubscribable] as const),
  )

  const columns = CHANNELS.filter(
    (channel) => channel === 'in_app' || presence.channels.includes(channel),
  )

  const writePending = feedback.pending
  const pendingTarget = writePending ? setPreference.variables : undefined

  const toggle = (
    preference: NotificationPreference,
    label: string,
    channel: (typeof CHANNELS)[number],
    enabled: boolean,
  ): void => {
    const typeKey = preference.type_key
    void feedback.run(async () => {
      await setPreference.mutateAsync({
        typeKey,
        channel,
        data: { enabled },
      })
      return t('preferences.saved', {
        type: label,
        channel: t(`preferences.channel.${channel}`),
        state: t(
          enabled
            ? 'preferences.state.enabled'
            : 'preferences.state.disabled',
        ),
      })
    })
  }

  const renderRow = (preference: NotificationPreference) => {
    const locked = unsubscribable.get(preference.type_key) === false
    const label = typeLabel({
      key: preference.type_key,
      group: preference.group,
    })
    const noteId = locked
      ? `${noteIdBase}-${encodeURIComponent(preference.type_key)}`
      : undefined
    return (
      <TableRow key={preference.type_key}>
        <TableCell>
          <Typography variant="body2">{label}</Typography>
          {locked && (
            <Typography id={noteId} variant="caption" color="text.secondary">
              {t('preferences.unsubscribableNote')}
            </Typography>
          )}
        </TableCell>
        {columns.map((channel) => {
          const initiating =
            pendingTarget !== undefined &&
            pendingTarget.typeKey === preference.type_key &&
            pendingTarget.channel === channel
          return (
            <TableCell key={channel} align="center">
              <PreferenceCell
                checked={hasChannel(preference, channel)}
                busy={writePending && initiating}
                unavailable={locked || (writePending && !initiating)}
                describedBy={noteId}
                ariaLabel={t('preferences.channelToggleAria', {
                  type: label,
                  channel: t(`preferences.channel.${channel}`),
                })}
                onToggle={(checked) =>
                  toggle(preference, label, channel, checked)
                }
              />
            </TableCell>
          )
        })}
      </TableRow>
    )
  }

  return (
    <SectionCard
      title={t('preferences.title')}
      description={t('preferences.description')}
      flushContent
    >
      <TenantAsyncSection<readonly NotificationPreference[]>
        pending={pending}
        payload={pending ? undefined : preferences.data}
        error={preferences.error}
        refreshing={preferences.isFetching}
        empty={rows.length === 0}
        headingLevel="h3"
        loading={
          <Box
            role="status"
            aria-busy="true"
            aria-label={t('preferences.loading')}
            sx={{ px: 3, py: 3 }}
          >
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
          <Fragment>
            <TableContainer>
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
            </TableContainer>
            <WriteFeedbackRegion
              outcome={feedback.outcome}
              resolve={resolveError}
              sx={{ px: 3, py: 2 }}
            />
          </Fragment>
        )}
      </TenantAsyncSection>
    </SectionCard>
  )
}
