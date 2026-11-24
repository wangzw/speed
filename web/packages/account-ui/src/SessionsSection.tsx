/**
 * SessionsSection: the sessions-and-devices surface of the account page, the
 * live sessions with per-session and bulk revocation.
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

const SESSIONS_PER_PAGE = 8

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

/** Null for an absent or malformed value, so a bad row never reaches Intl. */
function parseDate(iso: string | undefined): Date | null {
  if (iso === undefined) {
    return null
  }
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? null : date
}

type PendingWrite =
  | { readonly kind: 'others' }
  | { readonly kind: 'row'; readonly id: string }

/** Whether focus was lost when the initiating control's row left the
 * document; a connected control outside it means the operator moved focus. */
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

/**
 * The host may replace the section's bulk action with its own; the section
 * reads the account's sessions and revokes them singly or all at once.
 */
export type SessionsSectionProps = {
  /** Renders the bulk action -- "sign out other devices" -- in place of the
   * section's own action row, called only while the action would render. */
  readonly renderHeaderAction?: (action: ReactNode) => ReactNode
}

/** The session list with per-session and bulk revocation. */
export function SessionsSection({
  renderHeaderAction,
}: SessionsSectionProps) {
  const { t, i18n } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useSessionsQuery()
  const revokeOneMutation = useAuthnSessionRevokeOne()
  const revokeOthersMutation = useAuthnSessionRevokeOthers()

  const [othersConfirmOpen, setOthersConfirmOpen] = useState(false)
  const feedback = useWriteFeedback()
  const [pendingWrite, setPendingWrite] = useState<PendingWrite | null>(null)
  // Clamped against the page count below, so a shrinking list never leaves
  // the view past the end.
  const [page, setPage] = useState(1)
  // The focus landings when a landed write removes the control that started
  // it. The confirmation's node is remembered too, so the bulk landing waits
  // for this section's dialog rather than for the document to hold none.
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
  // isPending alone tracks "no answer yet" across the in-flight and the
  // parked (offline, fetchStatus 'paused') states, where isLoading is false.
  const pending = isPending && sessions === undefined
  const hasSessions = sessions !== undefined && sessions.length > 0
  // Held for the whole attempt, including the refetch that follows.
  const scopePending = pendingWrite !== null

  const hasRevocableOther =
    sessions !== undefined &&
    sessions.some((session) => session.id != null && session.current !== true)

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
    // the neighbour up from the next page.
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
    // This section's own dialog, not any dialog the host's shell keeps mounted.
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

  // Focus goes to the same action on the row now in the vacated place, else
  // after, else before, else to the list; candidates come from the re-read
  // list at the original position, so a page-edge revoke slides over.
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
      // Settled without removing the row: there is nothing to land from.
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

  // The landing waits for this section's own dialog to leave, never for the
  // document to hold no dialog.
  useLayoutEffect(() => {
    const trigger = bulkRemoval.current
    if (trigger === null) {
      return
    }
    if (trigger.isConnected) {
      // Settled without removing the control: there is nothing to land from.
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
      observer?.disconnect()
      // The confirmation was modal, so surviving focus outside it is a place
      // the operator chose.
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
            {/* role="list" keeps the semantics under WebKit, which strips
                them from a list-style-none ul. */}
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
                    // The settled result would otherwise name another page's device.
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
      {/* Outside the settled content, so it renders while the list is empty
          or a load failed. */}
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
