/**
 * SessionsSection: the sessions-and-devices surface of the account page.
 *
 * Renders every live session the authn module holds for the signed-in
 * account (GET /auth/sessions), the session the request's own token
 * belongs to marked as the current one, through the generated list hook.
 * The server's listing carries only live sessions -- a revoked or expired
 * one is gone from the answer, never a greyed row -- so a row is either
 * the current one (no revoke affordance: there is no such route for it)
 * or a live device the account can sign out. Rows show what the server
 * answers, shaped for the eye that has to tell them apart: the readable
 * browser/OS summary the row's user agent parses to ("Chrome · macOS" --
 * never the raw UA string, which would make two sign-ins from one browser
 * read as two identical, truncated walls of text), else the unknown-device
 * label; the client IP, the session kind, and the created/last-seen times.
 *
 * Actions: a row that is not the current one carries a row-end sign-out
 * button that revokes exactly that session (DELETE /auth/sessions/{id}),
 * without a second confirmation -- a signed-out session is low-loss (its
 * owner can simply sign in again) and the row itself is never the current
 * one. The section-top action, "sign out other devices", revokes every
 * other session at once (DELETE /auth/sessions) behind the ui-kit danger
 * ConfirmDialog; a single confirming click, not the heavier two-step arm,
 * because the action is recoverable -- a signed-out device signs in again.
 * The server itself skips the current session; the answer's revoked count
 * is surfaced in a success notice. After every successful revoke the list
 * query is invalidated, so the rows converge on the server's answer (a
 * revoked session drops off the listing).
 *
 * The section takes no props: whose sessions these are, and the right to
 * revoke them, come from the caller's bound client and its access token;
 * the section only renders the list and drives the generated mutations.
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
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
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
  AsyncSection,
  ConfirmDialog,
  errorCodeOf,
  ListSkeleton,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useProfileQuery, useSessionsQuery } from './internal/queries.js'
import { resolveTimeZone } from './internal/time-format.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { summarizeUserAgent } from './internal/ua-summary.js'

/**
 * The session kinds the authn session module records (pkg/authn/session's
 * `kind` values), one bundle key each under sessions.kind. t() is only
 * ever called with a value on this list -- a value outside it (a future
 * kind, a kind a policy adds) renders the generic "other" label, never a
 * raw value.
 */
const KNOWN_SESSION_KINDS = new Set<string>(['browser', 'token', 'service'])

/**
 * How many session rows one page shows. An account can accumulate many
 * live sessions, so the list is paged rather than rendered whole.
 */
const SESSIONS_PER_PAGE = 8

/** The transient outcome of a revoke action, rendered above the list. */
type Notice =
  | { readonly kind: 'others-done'; readonly count: number }
  | { readonly kind: 'revoke-failed'; readonly code: string }
  | null

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

export function SessionsSection() {
  const { t, i18n } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, refetch } = useSessionsQuery()
  const revokeOneMutation = useAuthnSessionRevokeOne()
  const revokeOthersMutation = useAuthnSessionRevokeOthers()

  const [othersConfirmOpen, setOthersConfirmOpen] = useState(false)
  const [notice, setNotice] = useState<Notice>(null)
  const [revokingId, setRevokingId] = useState<string | null>(null)
  // The 1-based page of the session list. It is clamped against the page
  // count on render below, so a revoke that shrinks the list can never
  // leave the view on a page past the end.
  const [page, setPage] = useState(1)

  // The display-timezone chain (stored profile preference -> device ->
  // UTC): the rows render in the account's chosen zone, not whatever
  // machine happens to run the client. The formatter always receives an
  // explicit timeZone -- resolveTimeZone has validated it, so the
  // constructor cannot throw on a stale stored value.
  const { data: profile } = useProfileQuery()
  const formatTime = useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language, {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: resolveTimeZone(profile?.time_zone),
      }),
    [i18n.language, profile?.time_zone],
  )

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
  const busy = revokeOneMutation.isPending || revokeOthersMutation.isPending

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

  async function handleRevokeSession(sessionId: string): Promise<void> {
    if (busy || sessionId === revokingId) {
      return
    }
    setNotice(null)
    setRevokingId(sessionId)
    try {
      await revokeOneMutation.mutateAsync({ id: sessionId })
      await queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      })
    } catch (error) {
      setNotice({ kind: 'revoke-failed', code: errorCodeOf(error) })
    } finally {
      setRevokingId(null)
    }
  }

  async function handleRevokeOthers(): Promise<void> {
    setNotice(null)
    try {
      const result = (await revokeOthersMutation.mutateAsync()) as
        | { readonly revoked?: number }
        | undefined
      setOthersConfirmOpen(false)
      setNotice({
        kind: 'others-done',
        count: result?.revoked ?? 0,
      })
      await queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      })
    } catch (error) {
      setOthersConfirmOpen(false)
      setNotice({ kind: 'revoke-failed', code: errorCodeOf(error) })
    }
  }

  const showHeader = pending || hasSessions

  return (
    <Box>
      {showHeader && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'baseline',
            justifyContent: 'space-between',
            gap: 2,
            mb: 2,
          }}
        >
          <Typography variant="h5" component="h2">
            {t('sessions.title')}
          </Typography>
          {hasSessions && hasRevocableOther && (
            <Button
              variant="outlined"
              color="error"
              disabled={busy}
              onClick={() => setOthersConfirmOpen(true)}
            >
              {t('sessions.revokeOthers.label')}
            </Button>
          )}
        </Box>
      )}

      <AsyncSection
        pending={pending}
        payload={sessions}
        empty={sessions !== undefined && sessions.length === 0}
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
            {notice?.kind === 'revoke-failed' && (
              <InlineError code={notice.code} />
            )}
            {/* The revoke-others success live region. It must never mount
                together with its text: a role="status" region announces
                only content changes that follow its own insertion, so a
                success notice born in the same commit as the region would
                be silent. The region therefore stands here for the whole
                list phase -- mounted empty and visually silent (clipped,
                never display:none) while there is nothing to announce --
                and the notice commit fills the text into a region the
                screen reader already knows, which is what makes the
                announcement fire. The clip and the notice never coexist:
                the same node shows the full success banner exactly while
                it carries the text. */}
            <Alert
              severity="success"
              role="status"
              sx={
                notice?.kind === 'others-done'
                  ? { width: '100%', mb: 1.5 }
                  : visuallyHiddenSx
              }
            >
              {notice?.kind === 'others-done'
                ? t('sessions.revokeOthers.done', { count: notice.count })
                : ''}
            </Alert>
            {/* The rows are one real list: a screen-reader user hears each
                session as one item of a numbered set with a boundary
                between rows, never a flat div stack. The notices above are
                page-level messages and stay outside the list. role="list"
                keeps the list semantics under WebKit, which strips them
                from a list-style-none ul. */}
            <Box component="ul" role="list" sx={{ m: 0, p: 0, listStyle: 'none' }}>
              {pageSessions.map((session, index) => {
                const id = session.id ?? null
                const current = session.current === true
                const revocable = id !== null && !current
                // Line 1 carries the readable summary the user agent
                // parses to ("Chrome · macOS"), never the raw UA -- the
                // raw string is the defect this surface exists to spare
                // its reader -- else the unknown-device label.
                const agent =
                  session.user_agent != null && session.user_agent !== ''
                    ? session.user_agent
                    : null
                const summary =
                  agent !== null ? summarizeUserAgent(agent) : null
                const deviceLabel = summary ?? t('sessions.deviceUnknown')
                const kind =
                  session.kind != null && session.kind !== ''
                    ? session.kind
                    : null
                const kindLabel =
                  kind !== null
                    ? KNOWN_SESSION_KINDS.has(kind)
                      ? t(`sessions.kind.${kind}`)
                      : t('sessions.kind.other')
                    : null
                const created = parseDate(session.created_at)
                const lastSeen = parseDate(session.last_seen_at)
                const showLastSeen =
                  lastSeen !== null &&
                  session.last_seen_at !== session.created_at
                const hasMeta =
                  session.client_ip != null || created !== null || showLastSeen
                return (
                  <Box
                    component="li"
                    key={id ?? String(index)}
                    sx={{
                      py: 1.5,
                      minWidth: 0,
                      ...(index > 0
                        ? { borderTop: '1px solid', borderColor: 'divider' }
                        : {}),
                    }}
                  >
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 1.5,
                      }}
                    >
                      <Typography
                        variant="body1"
                        noWrap
                        sx={{ minWidth: 0, fontWeight: 500 }}
                      >
                        {deviceLabel}
                      </Typography>
                      <Box
                        sx={{
                          marginLeft: 'auto',
                          flexShrink: 0,
                          display: 'flex',
                          alignItems: 'center',
                        }}
                      >
                        {current && (
                          <Chip
                            size="small"
                            color="primary"
                            variant="outlined"
                            label={t('sessions.current')}
                          />
                        )}
                        {revocable && (
                          <IconButton
                            aria-label={t('sessions.revokeAriaWithDevice', {
                              device: deviceLabel,
                              // The label is an aria-label, not HTML: the
                              // device string must reach the accessibility
                              // tree verbatim (i18next's default value
                              // escaping would embed a literal `&#x2F;`
                              // for a free-text device label's slashes).
                              interpolation: { escapeValue: false },
                            })}
                            size="small"
                            disabled={busy || revokingId === id}
                            onClick={() => {
                              if (id !== null) {
                                void handleRevokeSession(id)
                              }
                            }}
                            sx={{ color: 'text.secondary' }}
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
                        )}
                      </Box>
                    </Box>

                    {hasMeta && (
                      <Box
                        sx={{
                          display: 'flex',
                          flexWrap: 'wrap',
                          columnGap: 2.5,
                          rowGap: 0.5,
                          mt: 0.5,
                        }}
                      >
                        {kindLabel !== null && (
                          <Typography variant="body2" color="text.secondary">
                            {kindLabel}
                          </Typography>
                        )}
                        {session.client_ip != null && (
                          <Typography variant="body2" color="text.secondary">
                            {session.client_ip}
                          </Typography>
                        )}
                        {created !== null && (
                          <Typography variant="body2" color="text.secondary">
                            {t('sessions.signedIn', {
                              time: formatTime.format(created),
                            })}
                          </Typography>
                        )}
                        {showLastSeen && lastSeen !== null && (
                          <Typography variant="body2" color="text.secondary">
                            {t('sessions.lastSeen', {
                              time: formatTime.format(lastSeen),
                            })}
                          </Typography>
                        )}
                      </Box>
                    )}
                  </Box>
                )
              })}
            </Box>
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
                  disabled={currentPage <= 1}
                  onClick={() => setPage(currentPage - 1)}
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
                  disabled={currentPage >= pageCount}
                  onClick={() => setPage(currentPage + 1)}
                >
                  {t('sessions.nextPage')}
                </Button>
              </Box>
            )}
          </Box>
          )
        }}
      </AsyncSection>

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
    </Box>
  )
}
