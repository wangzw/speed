/**
 * NotificationPreferences: the per-type channel preference matrix.
 *
 * One row per declared notification type (GET /notifications/preferences,
 * whose effective channel set is the stored row when one exists and the
 * type's defaults otherwise), one column per channel the host serves. The
 * in-app column is always present; the optional columns come from
 * NotificationPresenceProvider's `channels` (email, sms), ordered by the
 * platform's closed channel vocabulary (in_app, email, sms -- the set
 * PUT /notifications/preferences/:type_key/:channel accepts). Without a
 * provider no optional channel is offered: the matrix renders the in-app
 * column alone. Toggling a cell writes that one channel's target state and
 * re-reads the matrix, so the checkboxes always show the server's answer
 * rather than a local guess.
 *
 * Every write goes through one `useWriteFeedback` scope whose region
 * renders under the table: a success confirms the type, channel and new
 * state, and a failure resolves in that same region. While a write is in
 * flight the matrix is inoperable, so no second toggle can overlap the
 * first and replace its result: the cell that started the write reports
 * busy and every other cell reports unavailable, all focusable (no cell is
 * natively disabled, so the cell that holds focus keeps it). There is no
 * optimistic update, so a failed cell shows the server's answer again.
 *
 * A type the taxonomy marks not unsubscribable (from
 * GET /notifications/types) renders its checkboxes unavailable with the
 * note as their accessible description: a write to such a type is refused
 * 409, so the control is offered as unavailable rather than made to fail.
 * The checkboxes stay focusable, so a keyboard user still reaches them and
 * hears why they cannot be changed. When the taxonomy has not loaded yet,
 * the type is treated as unsubscribable-unknown and left enabled, because
 * the server stays the authority.
 *
 * A host may relabel a type and group the rows through `typeLabel` and
 * `groupLabel`; the default type label is the type key, and rows render
 * grouped only when `groupLabel` is supplied.
 *
 * The read is one of the four settled states (ui-kit's AsyncSection), with
 * the admission layer's bare 404 split out as the tenant refusal state and
 * no retry; the section opens with its own h2 so it nests under any page
 * heading. A failed first read delivered no matrix and renders the
 * retryable error placeholder, never the empty one, because an empty
 * placeholder would claim there is nothing to configure. A failed refresh
 * over an already settled matrix keeps the matrix and marks it stale above
 * with a retry.
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

/** One matrix cell's inputs. */
interface PreferenceCellProps {
  /** The cell's accessible name: the type label and the channel. */
  readonly ariaLabel: string
  readonly checked: boolean
  /** This cell's own write is in flight. */
  readonly busy: boolean
  /** The precondition is unmet, or a sibling's write is in flight. */
  readonly unavailable: boolean
  /** The id of the note already on the page, for a type that cannot unsubscribe. */
  readonly describedBy?: string
  readonly onToggle: (checked: boolean) => void
}

/**
 * One matrix cell. It is a component of its own because its change handler
 * goes through `useActivationGuard`, which cannot be called from the cell
 * callback. The guard swallows a change while the cell is busy or
 * unavailable; the controlled value therefore stays the server's answer.
 */
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
          // Only the cell that started the write reports busy; a sibling is
          // unavailable for the write in flight without a reason of its
          // own. A type that cannot unsubscribe is unavailable because of
          // its precondition and keeps its note as the reason. None of
          // these is natively disabled, so every cell stays focusable.
          'aria-busy': busy || undefined,
          'aria-disabled': busy || unavailable || undefined,
          'aria-describedby': describedBy,
        },
      }}
    />
  )
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
  const feedback = useWriteFeedback()
  const resolveError = useNotificationUiErrorText()
  // The prefix for each locked row's note id, so a cell's `aria-describedby`
  // points at the note rendered beside its row.
  const noteIdBase = useId()

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

  // The scope admits one write at a time. The cell that started the write
  // in flight reports busy; every other cell reports unavailable.
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
      // The settled content is a table, so it reaches the card's edges and
      // the card's own overflow clips it to the radius. The loading branch
      // and the write-feedback region are not the table, so they carry the
      // content padding themselves.
      flushContent
    >
      <TenantAsyncSection<readonly NotificationPreference[]>
        pending={pending}
        // A failed first read delivered no matrix, so the payload stays
        // undefined and renders the retryable error placeholder; falling
        // back to an empty array would claim there is nothing to configure.
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
            {/* TableContainer owns the horizontal scrolling: without it a
                matrix with every channel overflows a narrow host column. */}
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
            </TableContainer>
            {/* The write result sits under the matrix it describes: the
                card's flush content gives the table its edges, so the
                region carries its own padding. */}
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
