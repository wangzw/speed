/**
 * SessionsSection: the sessions-and-devices surface of the account page.
 *
 * Renders every live session the authn module holds for the signed-in
 * account (GET /api/v1/auth/sessions), the session the request's own token
 * belongs to marked as the current one, through the generated list hook.
 * The server's listing carries only live sessions -- a revoked or expired
 * one is gone from the answer, never a greyed row -- so a row is either
 * the current one (no revoke affordance: there is no such route for it)
 * or a live device the account can sign out. Rows show what the server
 * answers, shaped for the eye that has to tell them apart: the readable
 * browser/OS summary the row's user agent parses to ("Chrome · macOS" --
 * never the raw UA string, which would make two sign-ins from one browser
 * read as two identical, truncated walls of text), else the unknown-device
 * label; the client IP, and the last-seen time (else the created one).
 *
 * Actions: a row that is not the current one carries a row-end sign-out
 * button that revokes exactly that session (DELETE /api/v1/auth/sessions/{id}),
 * without a second confirmation -- a signed-out session is low-loss (its
 * owner can simply sign in again) and the row itself is never the current
 * one. The section-top action, "sign out other devices", revokes every
 * other session at once (DELETE /api/v1/auth/sessions) behind the ui-kit danger
 * ConfirmDialog; a single confirming click, not the heavier two-step arm,
 * because the action is recoverable -- a signed-out device signs in again.
 * The server itself skips the current session; the answer's revoked count
 * is surfaced in a success notice. After every successful revoke the list
 * query is invalidated, so the rows converge on the server's answer (a
 * revoked session drops off the listing).
 *
 * The section's one optional prop is a render slot for its bulk action:
 * `renderHeaderAction` receives the "sign out other devices" control and
 * returns the node rendered in its place, so a host that owns the
 * surrounding block can lift the action into the block title's action
 * area -- typically by returning a portal into a header slot of its own.
 * Without it the section renders the action itself above the list, on its
 * own row under a plain surface. Whose sessions these are, and the right
 * to revoke them, come from the caller's bound client and its access
 * token; the section only renders the list and drives the generated
 * mutations.
 * An unresolved load -- the first load in flight, or parked by
 * react-query's default networkMode 'online' while the device is offline
 * -- keeps the loading branch, header included. Only a settled query
 * leaves it: an answer listing zero sessions hides the header and
 * renders the ui-kit EmptyState empty variant, while a load that failed
 * -- and a successful answer that omits the optional sessions key, as
 * unreadable as an error and never grounds for a "no sessions" claim --
 * render the error variant with a retry button. In every settled state
 * the EmptyState title stands in for the hidden h2 header at its own
 * level, so the heading order never skips a level and the no-sessions
 * text is only ever asserted for an answer that genuinely listed none.
 */

import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { formatInstant, isSupportedTimeZone, resolveTimeZone } from '@speed/i18n'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import CircularProgress from '@mui/material/CircularProgress'
import IconButton from '@mui/material/IconButton'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnSessionListQueryKey,
  useAuthnSessionRevokeOne,
  useAuthnSessionRevokeOthers,
} from '@speed/api-sdk'
import {
  ConfirmDialog,
  ListSkeleton,
  SectionCard,
  useDisplayTimeZone,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useProfileQuery, useSessionsQuery } from './internal/queries.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { summarizeUserAgent } from './internal/ua-summary.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/**
 * How many session rows one page shows. An account can accumulate many
 * live sessions, so the list is paged rather than rendered whole.
 */
const SESSIONS_PER_PAGE = 8

/** The row-end sign-out glyph, hand-drawn on the packages' 24-grid
 * pattern: an arrow leaving a door, stroke currentColor, no fill. */
function SignOutIcon({ size = 20 }: { readonly size?: number }) {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M9.5 21H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h2.5" />
      <path d="m15.5 17.5 4.5-4.5-4.5-4.5" />
      <path d="M20 13H9" />
    </svg>
  )
}

/** Parses a server timestamp; answers null for an absent or malformed
 * value so a bad row can never reach Intl and throw. */
function parseDate(iso: string | undefined): Date | null {
  if (iso === undefined) {
    return null
  }
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? null : date
}

/** The pending-state placeholder: rows shaped like the content rows, read
 * aloud as one loading announcement, never a fake heading. */
function SessionListSkeleton({ label }: { readonly label: string }) {
  const widths = [
    { primary: '46%', secondary: '62%', tertiary: '38%' },
    { primary: '58%', secondary: '44%', tertiary: '52%' },
    { primary: '38%', secondary: '68%', tertiary: '30%' },
  ]
  return (
    <ListSkeleton
      label={label}
      rows={widths.map((width) => (
        <>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
            }}
          >
            <Skeleton variant="text" width={width.primary} />
            <Skeleton variant="circular" width={20} height={20} />
          </Box>
          <Skeleton variant="text" width={width.secondary} />
          <Skeleton variant="text" width={width.tertiary} />
        </>
      ))}
    />
  )
}

export type SessionsSectionProps = {
  /**
   * Renders the section's bulk action -- "sign out other devices" -- in
   * place of the section's own action row. The section calls it with the
   * control and renders the returned node; a host that owns the
   * surrounding block returns a portal into its block header, lifting the
   * action out of the list. It is called only while the action would
   * render at all (a settled list holding another live session). Without
   * it, the action keeps the section's own placement above the list.
   */
  readonly renderHeaderAction?: (action: ReactNode) => ReactNode
}

export function SessionsSection({
  renderHeaderAction,
}: SessionsSectionProps) {
  const { t, i18n } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useSessionsQuery()
  const revokeOneMutation = useAuthnSessionRevokeOne()
  const revokeOthersMutation = useAuthnSessionRevokeOthers()

  const [othersConfirmOpen, setOthersConfirmOpen] = useState(false)
  // One scope for the section: the row revoke and the bulk revoke share the
  // result position below the list, so a success and a failure never stand
  // together. Each success names what changed -- the signed-out session or
  // the count of other devices.
  const feedback = useWriteFeedback()
  const [revokingId, setRevokingId] = useState<string | null>(null)
  // The 1-based page of the session list. It is clamped against the page
  // count on render below, so a revoke that shrinks the list can never
  // leave the view on a page past the end.
  const [page, setPage] = useState(1)

  // The display-timezone chain: the account's stored preference wins, then
  // the host's display-timezone context (which replaces only the device
  // tier), then the device zone, then UTC. The rows render in the
  // account's chosen zone, not whatever machine happens to run the
  // client. Every candidate is validated through the shared i18n helper,
  // so the formatter never receives a zone the engine refuses.
  const { data: profile } = useProfileQuery()
  const hostTimeZone = useDisplayTimeZone()
  const timeZone = useMemo(() => {
    const stored = profile?.time_zone?.trim() ?? ''
    if (stored !== '' && isSupportedTimeZone(stored)) {
      return stored
    }
    if (
      hostTimeZone !== undefined &&
      hostTimeZone !== '' &&
      isSupportedTimeZone(hostTimeZone)
    ) {
      return hostTimeZone
    }
    return resolveTimeZone(undefined)
  }, [profile?.time_zone, hostTimeZone])
  const formatTime = (value: string | number | Date): string =>
    formatInstant(value, i18n.language, { timeZone })

  const sessions = data?.sessions
  // The answer is unresolved whenever there is no data yet: the first
  // load in flight, or parked by react-query's default networkMode
  // 'online' while the device is offline -- such a fetch sits at
  // fetchStatus 'paused', where isFetching (and isLoading, its
  // isPending-and-isFetching conjunction) is false and isError stays
  // false too, so a pending test derived from isLoading would miss
  // every branch and read an unresolved load as an empty answer.
  // isPending alone tracks "no answer yet" across the in-flight and the
  // parked states.
  const pending = isPending && sessions === undefined
  const hasSessions = sessions !== undefined && sessions.length > 0
  // Disabled through the whole attempt, including the refetch that follows
  // the mutation, so no control changes context under a pending scope.
  const busy =
    revokeOneMutation.isPending ||
    revokeOthersMutation.isPending ||
    feedback.pending

  // The server lists only live sessions, so every non-current row is a
  // live device the section-top bulk action exists to sign out.
  const hasRevocableOther =
    sessions !== undefined &&
    sessions.some((session) => session.id != null && session.current !== true)

  // The page the list renders, clamped so a shrinking list (a revoke drops
  // a row) never leaves the view past the last page.
  const total = sessions?.length ?? 0
  const pageCount = Math.max(1, Math.ceil(total / SESSIONS_PER_PAGE))
  const currentPage = Math.min(page, pageCount)

  function handleRevokeSession(
    sessionId: string,
    deviceLabel: string,
  ): Promise<void> {
    if (busy || sessionId === revokingId) {
      return Promise.resolve()
    }
    setRevokingId(sessionId)
    return feedback.run(async () => {
      try {
        await revokeOneMutation.mutateAsync({ id: sessionId })
        await queryClient.invalidateQueries({
          queryKey: getAuthnSessionListQueryKey(),
        })
        // The confirmation names the session that was signed out.
        return t('sessions.revokedOne', {
          device: deviceLabel,
          interpolation: { escapeValue: false },
        })
      } finally {
        setRevokingId(null)
      }
    })
  }

  function handleRevokeOthers(): Promise<void> {
    return feedback.run(async () => {
      let result:
        | { readonly revoked?: number }
        | undefined
      try {
        result = (await revokeOthersMutation.mutateAsync()) as
          | { readonly revoked?: number }
          | undefined
      } catch (error) {
        setOthersConfirmOpen(false)
        throw error
      }
      setOthersConfirmOpen(false)
      await queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      })
      return t('sessions.revokeOthers.done', { count: result?.revoked ?? 0 })
    })
  }

  const showHeader = pending || hasSessions

  // The bulk action, rendered in place when no host takes it over. Handed to
  // the host's slot when one is supplied: the host places it (a portal into
  // its block header) and the section's own row is left out.
  const bulkAction =
    hasSessions && hasRevocableOther ? (
      <Button
        variant="outlined"
        color="error"
        size="small"
        disabled={busy}
        onClick={() => setOthersConfirmOpen(true)}
      >
        {t('sessions.revokeOthers.label')}
      </Button>
    ) : undefined
  const headerAction =
    renderHeaderAction !== undefined && bulkAction !== undefined
      ? renderHeaderAction(bulkAction)
      : undefined

  return (
    <>
      {headerAction}
      <SectionCard
        title={showHeader ? t('sessions.title') : undefined}
        actions={renderHeaderAction === undefined ? bulkAction : undefined}
      >
        <AccountAsyncSection
        pending={pending}
        payload={sessions}
        empty={sessions !== undefined && sessions.length === 0}
        error={error}
        refreshing={isFetching}
        loading={<SessionListSkeleton label={t('sessions.loading')} />}
        errorState={{
          title: t('sessions.error.title'),
          description: t('sessions.error.description'),
          retryLabel: t('sessions.retry'),
          onRetry: () => void refetch(),
        }}
        emptyState={{
          title: t('sessions.empty.title'),
          description: t('sessions.empty.description'),
        }}
      >
        {(settledSessions) => {
          const start = (currentPage - 1) * SESSIONS_PER_PAGE
          const pageSessions = settledSessions.slice(
            start,
            start + SESSIONS_PER_PAGE,
          )
          return (
          <Box>
            {/* The rows are one real list: a screen-reader user hears each
                session as one item of a numbered set with a boundary
                between rows, never a flat div stack. The result region
                below is page-level and stays outside the list. role="list"
                keeps the list semantics under WebKit, which strips them
                from a list-style-none ul. */}
            <Table
              size="small"
              sx={{
                // Fixed columns keep the four rails steady: the first column
                // shares the block's left edge, the last ends at its right
                // edge, and no label wraps to a second line.
                width: '100%',
                tableLayout: 'fixed',
                '& th, & td': {
                  padding: '8px 0',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                },
                '& th + th, & td + td': { paddingLeft: '16px' },
              }}
            >
              <TableHead>
                <TableRow>
                  <TableCell sx={{ width: '35%' }}>
                    {t('sessions.table.device')}
                  </TableCell>
                  <TableCell sx={{ width: '20%' }}>
                    {t('sessions.table.address')}
                  </TableCell>
                  <TableCell sx={{ width: '30%' }}>
                    {t('sessions.table.activity')}
                  </TableCell>
                  <TableCell
                    align="right"
                    sx={{
                      width: '15%',
                      // The row action is a compact IconButton, whose own
                      // inner inset holds its glyph 5px off the cell's
                      // right edge. Matching that inset here ends the
                      // header text on the glyph's right edge rather than
                      // on the button's box, so the label sits on the
                      // same rail as the action it names. The selector
                      // outranks the table's shared `th, td` padding rule,
                      // which a bare property would lose against.
                      '&.MuiTableCell-root': { paddingRight: '5px' },
                    }}
                  >
                    {t('sessions.table.action')}
                  </TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {pageSessions.map((session, index) => {
                  const id = session.id ?? null
                  const current = session.current === true
                  const revocable = id !== null && !current
                  const agent =
                    session.user_agent != null && session.user_agent !== ''
                      ? session.user_agent
                      : null
                  const summary =
                    agent !== null ? summarizeUserAgent(agent) : null
                  const deviceLabel = summary ?? t('sessions.deviceUnknown')
                  const created = parseDate(session.created_at)
                  const lastSeen = parseDate(session.last_seen_at)
                  const activity = lastSeen ?? created
                  return (
                    <TableRow key={id ?? String(index)}>
                      <TableCell>
                        <Box
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: 1,
                            flexWrap: 'wrap',
                          }}
                        >
                          <Typography
                            variant="body1"
                            noWrap
                            sx={{ fontWeight: 600 }}
                          >
                            {deviceLabel}
                          </Typography>
                          {current ? (
                            <Chip
                              size="small"
                              color="primary"
                              variant="outlined"
                              label={t('sessions.current')}
                            />
                          ) : null}
                        </Box>
                      </TableCell>
                      <TableCell>
                        <Typography variant="body2" color="text.secondary">
                          {session.client_ip ?? ''}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Typography variant="body2" color="text.secondary">
                          {activity === null ? '' : formatTime(activity)}
                        </Typography>
                      </TableCell>
                      <TableCell align="right">
                        {revocable ? (
                          <IconButton
                            aria-label={t('sessions.revokeAriaWithDevice', {
                              device: deviceLabel,
                              interpolation: { escapeValue: false },
                            })}
                            size="small"
                            disabled={busy || revokingId === id}
                            onClick={() => {
                              if (id !== null) {
                                void handleRevokeSession(id, deviceLabel)
                              }
                            }}
                            sx={{ color: 'error.main' }}
                          >
                            {revokingId === id ? (
                              <CircularProgress
                                size={18}
                                thickness={5}
                                aria-hidden="true"
                              />
                            ) : (
                              <SignOutIcon />
                            )}
                          </IconButton>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
            {pageCount > 1 && (
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'flex-end',
                  columnGap: 2,
                  mt: 2,
                }}
              >
                <Button
                  size="small"
                  variant="outlined"
                  disabled={busy || currentPage <= 1}
                  onClick={() => {
                    // Paging changes what the list describes: the settled
                    // result would otherwise name a device on another page.
                    feedback.reset()
                    setPage(currentPage - 1)
                  }}
                >
                  {t('sessions.prevPage')}
                </Button>
                <Typography variant="body2" color="text.secondary">
                  {t('sessions.pageIndicator', {
                    page: currentPage,
                    total: pageCount,
                  })}
                </Typography>
                <Button
                  size="small"
                  variant="outlined"
                  disabled={busy || currentPage >= pageCount}
                  onClick={() => {
                    feedback.reset()
                    setPage(currentPage + 1)
                  }}
                >
                  {t('sessions.nextPage')}
                </Button>
              </Box>
            )}
          </Box>
          )
        }}
      </AccountAsyncSection>
      {/* The section's one result position, below the list and its pager whose
          row and section actions write into it: a row revoke and the bulk
          revoke share it, so a success and a failure never stand together.
          It stays outside the settled content, so it is still rendered while
          the list is empty or a load failed. */}
      <WriteFeedbackRegion outcome={feedback.outcome} sx={{ mt: 2 }} />
      </SectionCard>

      <ConfirmDialog
        open={othersConfirmOpen}
        title={t('sessions.revokeOthers.confirmTitle')}
        message={t('sessions.revokeOthers.confirmMessage')}
        variant="danger"
        confirmLabel={t('sessions.revokeOthers.confirmLabel')}
        confirmLoading={revokeOthersMutation.isPending}
        onCancel={() => setOthersConfirmOpen(false)}
        onConfirm={() => void handleRevokeOthers()}
      />
    </>
  )
}
