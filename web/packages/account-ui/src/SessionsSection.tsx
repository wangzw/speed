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

import { useLayoutEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { formatInstant } from '@speed/i18n'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnSessionListQueryKey,
  useAuthnSessionRevokeOne,
  useAuthnSessionRevokeOthers,
} from '@speed/api-sdk'
import {
  ActionButton,
  ConfirmDialog,
  ListSkeleton,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useSessionsQuery } from './internal/queries.js'
import { useAccountTimeZone } from './internal/time-zone.js'
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

/** Which control started the section's one write in flight. The control
 * itself reports busy; every other write control in the scope is
 * unavailable, so two attempts never overlap. */
type PendingWrite =
  | { readonly kind: 'others' }
  | { readonly kind: 'row'; readonly id: string }

/**
 * Whether focus was lost when the initiating control's row left the
 * document. A landed write that removes its own row hands focus to a
 * neighbour, but only if the operator had not moved focus elsewhere while
 * the write ran: the browser drops focus to the body (or leaves it on a
 * detached node inside the removed row) in that case, while a connected
 * control outside the row means the operator chose that place.
 */
function focusFellWith(removed: HTMLElement | null): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  return removed !== null && removed.contains(active)
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
  // The write in flight and the control that started it. The initiating
  // control reports busy; the scope's other write controls stay
  // unavailable while it runs.
  const [pendingWrite, setPendingWrite] = useState<PendingWrite | null>(null)
  // The 1-based page of the session list. It is clamped against the page
  // count on render below, so a revoke that shrinks the list can never
  // leave the view on a page past the end.
  const [page, setPage] = useState(1)
  // The landing places when a landed write takes the control that started
  // it out of the document: the row controls by session id, the list, and
  // the section's named container for when the list itself goes away. The
  // write's own removal record sits beside them; the confirmation's node is
  // remembered too, so the bulk landing waits for this section's dialog
  // rather than for the document to hold none.
  const revokeButtons = useRef(new Map<string, HTMLButtonElement>())
  const listRegion = useRef<HTMLElement | null>(null)
  const sectionRegion = useRef<HTMLElement | null>(null)
  const bulkButton = useRef<HTMLButtonElement | null>(null)
  const confirmDialog = useRef<HTMLDivElement | null>(null)
  const bulkDialog = useRef<HTMLDivElement | null>(null)
  const rowRemoval = useRef<{
    readonly id: string
    readonly row: HTMLElement | null
    readonly index: number
  } | null>(null)
  const bulkRemoval = useRef<HTMLButtonElement | null>(null)

  const timeZone = useAccountTimeZone()
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
  // The scope is held for the whole attempt, including the refetch that
  // follows the mutation, so no control changes context under a pending
  // scope. The control that started the write reports busy; every other
  // write control is unavailable.
  const scopePending = pendingWrite !== null

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
  const pageStart = (currentPage - 1) * SESSIONS_PER_PAGE
  const pageSessions = (sessions ?? []).slice(
    pageStart,
    pageStart + SESSIONS_PER_PAGE,
  )

  function handleRevokeSession(
    sessionId: string,
    deviceLabel: string,
  ): Promise<void> {
    if (pendingWrite !== null) {
      return Promise.resolve()
    }
    const control = revokeButtons.current.get(sessionId) ?? null
    // The whole read, not the page in view: a revoke at a page edge brings
    // the next page's first row up into the vacated place, so the neighbour
    // may not be on the page the operator started from. The original
    // position is recorded with it; the landing resolves it against the
    // re-read page.
    const order = (sessions ?? [])
      .filter((entry) => entry.id != null && entry.current !== true)
      .map((entry) => entry.id as string)
    rowRemoval.current = {
      id: sessionId,
      row: control?.closest('li') ?? null,
      index: order.indexOf(sessionId),
    }
    setPendingWrite({ kind: 'row', id: sessionId })
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
      } catch (error) {
        rowRemoval.current = null
        throw error
      } finally {
        setPendingWrite(null)
      }
    })
  }

  function handleRevokeOthers(): Promise<void> {
    bulkRemoval.current = bulkButton.current
    // This section's own confirmation, not any dialog the document holds:
    // the host's shell can keep a drawer mounted with role="dialog" for the
    // whole page's life.
    bulkDialog.current = confirmDialog.current
    setPendingWrite({ kind: 'others' })
    return feedback.run(async () => {
      let result:
        | { readonly revoked?: number }
        | undefined
      try {
        result = (await revokeOthersMutation.mutateAsync()) as
          | { readonly revoked?: number }
          | undefined
      } catch (error) {
        bulkRemoval.current = null
        bulkDialog.current = null
        setOthersConfirmOpen(false)
        throw error
      }
      setOthersConfirmOpen(false)
      await queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      })
      return t('sessions.revokeOthers.done', { count: result?.revoked ?? 0 })
    }).finally(() => {
      setPendingWrite(null)
    })
  }

  // A landed row revoke takes its row out of the document. Focus goes to
  // the same action on the row that now occupies the vacated place, else
  // after it, else before it, else to the list (or the section's named
  // container when the list goes away) -- in the commit that removes the
  // row, and only when the removal is what took focus; an operator who
  // moved elsewhere keeps that place. The candidates come from the re-read
  // list at the original position, so a revoke at a page edge hands over to
  // the row that slid up from the next page, and one that empties the last
  // page hands over to the previous page's last row; only controls the
  // current page renders are connected, so the rest are skipped.
  useLayoutEffect(() => {
    const target = rowRemoval.current
    if (target === null) {
      return
    }
    const remaining = new Set(
      (sessions ?? [])
        .filter((entry) => entry.id != null)
        .map((entry) => entry.id as string),
    )
    if (remaining.has(target.id)) {
      // Settled without removing the row (a refusal, or an answer that still
      // carries it): there is nothing to land from.
      if (pendingWrite === null) {
        rowRemoval.current = null
      }
      return
    }
    rowRemoval.current = null
    if (!focusFellWith(target.row)) {
      return
    }
    const reread = (sessions ?? [])
      .filter((entry) => entry.id != null && entry.current !== true)
      .map((entry) => entry.id as string)
    const index = Math.max(0, Math.min(target.index, reread.length))
    const candidates = [
      ...reread.slice(index),
      ...reread.slice(0, index).reverse(),
    ]
    for (const id of candidates) {
      const button = revokeButtons.current.get(id)
      if (button !== undefined && button.isConnected) {
        button.focus()
        return
      }
    }
    ;(listRegion.current ?? sectionRegion.current)?.focus()
  }, [sessions, pendingWrite])

  // A landed bulk revoke can take the control that opened the confirmation
  // out of the document: no other session is left to sign out, so the
  // section stops rendering it. Focus falls to the list under the same
  // focus test. The confirmation's focus trap owns focus until it has
  // animated out and unmounted, so the landing waits for this section's own
  // dialog node to leave -- never for the document to hold no dialog, since
  // the host's shell may keep a drawer mounted with role="dialog".
  useLayoutEffect(() => {
    const trigger = bulkRemoval.current
    if (trigger === null) {
      return
    }
    if (trigger.isConnected) {
      // Settled without removing the control (an answer that still lists
      // another session): there is nothing to land from.
      if (pendingWrite === null) {
        bulkRemoval.current = null
        bulkDialog.current = null
      }
      return
    }
    let observer: MutationObserver | null = null
    const land = (): void => {
      if (bulkRemoval.current !== trigger || trigger.isConnected) {
        return
      }
      const dialog = bulkDialog.current
      if (dialog !== null && dialog.isConnected) {
        return
      }
      bulkRemoval.current = null
      bulkDialog.current = null
      // The landing is decided in this pass, so the observer has no later
      // mutation left to wait for.
      observer?.disconnect()
      // The confirmation was modal, so the only places focus can be while
      // it closes are its own controls and the trigger; focus anywhere that
      // survives is a place the operator chose.
      const active = document.activeElement
      const activeSurvives =
        active instanceof HTMLElement &&
        active.isConnected &&
        active !== document.body
      if (activeSurvives) {
        return
      }
      ;(listRegion.current ?? sectionRegion.current)?.focus()
    }
    land()
    if (bulkRemoval.current !== trigger) {
      return
    }
    observer = new MutationObserver(land)
    observer.observe(document.body, { childList: true })
    return () => {
      observer?.disconnect()
    }
  }, [sessions, pendingWrite])

  const showHeader = pending || hasSessions

  // The bulk action, rendered in place when no host takes it over. Handed to
  // the host's slot when one is supplied: the host places it (a portal into
  // its block header) and the section's own row is left out.
  const bulkAction =
    hasSessions && hasRevocableOther ? (
      <ActionButton
        ref={bulkButton}
        size="small"
        tone="danger"
        busy={pendingWrite?.kind === 'others'}
        unavailable={pendingWrite !== null && pendingWrite.kind !== 'others'}
        onClick={() => setOthersConfirmOpen(true)}
      >
        {t('sessions.revokeOthers.label')}
      </ActionButton>
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
        {/* The section's control group: the landing place when a landed
            write takes the list itself away. Named and programmatically
            focusable, so the reader is told which group the result belongs
            to instead of being dropped on the document body. */}
        <Box
          ref={sectionRegion}
          tabIndex={-1}
          role="group"
          aria-label={t('sessions.title')}
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
        {() => {
          return (
          <Box>
            {/* The rows are one real list: a screen-reader user hears each
                session as one item of a numbered set with a boundary
                between rows, never a flat div stack. The result region
                below is page-level and stays outside the list. role="list"
                keeps the list semantics under WebKit, which strips them
                from a list-style-none ul. */}
            <Box
              component="ul"
              role="list"
              ref={listRegion}
              tabIndex={-1}
              aria-label={t('sessions.title')}
              sx={{
                listStyle: 'none',
                m: 0,
                p: 0,
                display: 'flex',
                flexDirection: 'column',
                gap: 2,
              }}
            >
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
                const address = session.client_ip ?? ''
                const time = activity === null ? '' : formatTime(activity)
                const detail =
                  address !== '' && time !== ''
                    ? t('sessions.rowDetail', {
                        address,
                        time,
                        interpolation: { escapeValue: false },
                      })
                    : time !== ''
                      ? t('sessions.rowActivity', {
                          time,
                          interpolation: { escapeValue: false },
                        })
                      : address
                return (
                  <Box
                    component="li"
                    key={id ?? String(index)}
                    sx={{ display: 'flex', alignItems: 'flex-start', gap: 2 }}
                  >
                    <Box sx={{ flex: '1 1 auto', minWidth: 0 }}>
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
                          sx={{ fontWeight: 600, overflowWrap: 'anywhere' }}
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
                      {detail === '' ? null : (
                        <Typography
                          variant="body2"
                          color="text.secondary"
                          sx={{ mt: 0.25, overflowWrap: 'anywhere' }}
                        >
                          {detail}
                        </Typography>
                      )}
                    </Box>
                    {revocable ? (
                      <ActionButton
                        ref={(node) => {
                          if (id === null) {
                            return
                          }
                          if (node === null) {
                            revokeButtons.current.delete(id)
                          } else {
                            revokeButtons.current.set(id, node)
                          }
                        }}
                        size="small"
                        tone="danger"
                        aria-label={t('sessions.revokeAriaWithDevice', {
                          device: deviceLabel,
                          interpolation: { escapeValue: false },
                        })}
                        busy={
                          pendingWrite?.kind === 'row' &&
                          pendingWrite.id === id
                        }
                        unavailable={
                          pendingWrite !== null &&
                          !(
                            pendingWrite.kind === 'row' &&
                            pendingWrite.id === id
                          )
                        }
                        onClick={() => {
                          if (id !== null) {
                            void handleRevokeSession(id, deviceLabel)
                          }
                        }}
                        startIcon={<SignOutIcon size={16} />}
                        sx={{ flex: '0 0 auto' }}
                      >
                        {t('sessions.revokeRowLabel')}
                      </ActionButton>
                    ) : null}
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
                <ActionButton
                  size="small"
                  unavailable={scopePending || currentPage <= 1}
                  onClick={() => {
                    // Paging changes what the list describes: the settled
                    // result would otherwise name a device on another page.
                    feedback.reset()
                    setPage(currentPage - 1)
                  }}
                >
                  {t('sessions.prevPage')}
                </ActionButton>
                <Typography variant="body2" color="text.secondary">
                  {t('sessions.pageIndicator', {
                    page: currentPage,
                    total: pageCount,
                  })}
                </Typography>
                <ActionButton
                  size="small"
                  unavailable={scopePending || currentPage >= pageCount}
                  onClick={() => {
                    feedback.reset()
                    setPage(currentPage + 1)
                  }}
                >
                  {t('sessions.nextPage')}
                </ActionButton>
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
        </Box>
      </SectionCard>

      <ConfirmDialog
        ref={confirmDialog}
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
