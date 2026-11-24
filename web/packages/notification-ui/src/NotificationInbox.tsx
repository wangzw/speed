/**
 * The notification surface: the caller's messages with per-message and
 * mark-all read. Optional props narrow the list by group, page it, append
 * a row detail, or render it embedded.
 */

import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import ButtonBase from '@mui/material/ButtonBase'
import Chip from '@mui/material/Chip'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import ListItemText from '@mui/material/ListItemText'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import type { NotificationMessage } from '@speed/api-sdk'
import {
  ActionButton,
  ListSkeleton,
  PageLayout,
  SectionCard,
  WriteFeedbackRegion,
  useActivationGuard,
  useFormatInstant,
  useWriteFeedback,
} from '@speed/ui-kit'
import type {
  EmptyStateHeadingLevel,
  SectionCardHeadingLevel,
} from '@speed/ui-kit'
import { useNotificationUiErrorText } from './internal/error-text.js'
import { resolveMessageText } from './internal/message-text.js'
import { TenantAsyncSection } from './internal/tenant-section.js'
import {
  type NotificationInbox as NotificationInboxPage,
  useMarkAllNotificationsRead,
  useMarkNotificationRead,
  useNotificationMessages,
  useNotificationUnreadCount,
} from './internal/queries.js'
import { useNotificationUiTranslation } from './internal/translation.js'

/** Props narrowing the inbox: a group filter, page size, per-row detail
 * renderer, and activation of linked rows. */
export type NotificationInboxProps = {
  /**
   * Called with an activated row's message. A row activates only with a
   * link and this supplied; an unread message is marked read first, then
   * the callback runs. The host owns what follows.
   */
  readonly onOpen?: (message: NotificationMessage) => void
  /** Extra content appended to each row, below its body and time. */
  readonly renderDetail?: (message: NotificationMessage) => ReactNode
  /** Restrict the list to one group; absent or empty means every group. */
  readonly group?: string
  /** Page size. Supplying it turns on previous/next paging, with the
   * response's total deciding where the next page ends. */
  readonly pageSize?: number
  /** 'page' (default) opens with the page header; 'embedded' renders a
   * heading at `headingLevel` instead, omitting the page header. */
  readonly variant?: 'page' | 'embedded'
  /** The embedded heading's element; defaults to 'h2'. */
  readonly headingLevel?: EmptyStateHeadingLevel
}

function isUnread(message: NotificationMessage): boolean {
  const readAt: string | null | undefined = message.read_at
  return readAt === undefined || readAt === null || readAt === ''
}

type PendingWrite =
  | { readonly kind: 'all' }
  | {
      readonly kind: 'row'
      readonly messageId: string
      readonly origin: 'row' | 'title'
    }

function rowLandingId(
  rows: readonly NotificationMessage[],
  removedId: string,
): string | null {
  const index = rows.findIndex((message) => message.id === removedId)
  if (index === -1) {
    return null
  }
  for (let next = index + 1; next < rows.length; next += 1) {
    const candidate = rows[next]
    if (candidate !== undefined && isUnread(candidate)) {
      return candidate.id
    }
  }
  for (let previous = index - 1; previous >= 0; previous -= 1) {
    const candidate = rows[previous]
    if (candidate !== undefined && isUnread(candidate)) {
      return candidate.id
    }
  }
  return null
}

function listContextOf(
  group: string | undefined,
  pageSize: number | undefined,
): string {
  return JSON.stringify([group ?? null, pageSize ?? null])
}

/** Renders one page of the caller's messages with per-message and
 * mark-all read. */
export function NotificationInbox({
  onOpen,
  renderDetail,
  group,
  pageSize,
  variant = 'page',
  headingLevel = 'h2',
}: NotificationInboxProps = {}) {
  const { t } = useNotificationUiTranslation()
  const listContext = listContextOf(group, pageSize)
  const [pages, setPages] = useState<{
    readonly context: string
    readonly index: number
  }>({ context: listContext, index: 0 })
  const pageIndex = pages.context === listContext ? pages.index : 0
  const messages = useNotificationMessages({
    group,
    limit: pageSize,
    offset: pageSize === undefined ? undefined : pageIndex * pageSize,
  })
  const markRead = useMarkNotificationRead()
  const markAllRead = useMarkAllNotificationsRead()
  const unreadTotal = useNotificationUnreadCount().data
  const feedback = useWriteFeedback()
  const resolveError = useNotificationUiErrorText()
  const formatInstant = useFormatInstant()
  const resetFeedback = feedback.reset
  const [pendingWrite, setPendingWrite] = useState<PendingWrite | null>(null)
  // The pager keeps the last settled total so it stays mounted while the
  // next page's read is in flight; unmounting it would drop the operator's
  // focus to the body. A new list context resets it.
  const [pagerTotal, setPagerTotal] = useState(0)
  const rowActions = useRef(new Map<string, HTMLButtonElement>())
  const listContainer = useRef<HTMLUListElement | null>(null)
  const pendingRowRemoval = useRef<{ readonly messageId: string } | null>(null)

  const previousContext = useRef(listContext)
  useEffect(() => {
    if (previousContext.current === listContext) {
      return
    }
    previousContext.current = listContext
    setPages({ context: listContext, index: 0 })
    setPagerTotal(0)
    setPendingWrite(null)
    pendingRowRemoval.current = null
    resetFeedback()
  }, [listContext, resetFeedback])

  const page = messages.data

  useLayoutEffect(() => {
    const pending = pendingRowRemoval.current
    if (pending === null || rowActions.current.has(pending.messageId)) {
      return
    }
    pendingRowRemoval.current = null
    const active = document.activeElement
    if (active !== null && active !== document.body) {
      return
    }
    const rows = page?.messages ?? []
    const landing = rowLandingId(rows, pending.messageId)
    const target =
      (landing === null ? undefined : rowActions.current.get(landing)) ??
      listContainer.current
    target?.focus()
  }, [page])
  const list = page?.messages ?? []
  useEffect(() => {
    if (page !== undefined) {
      setPagerTotal(page.total)
    }
  }, [page])
  const total = page?.total ?? pagerTotal
  const pending = messages.isPending && page === undefined

  const runMarkAll = (): void => {
    setPendingWrite({ kind: 'all' })
    void feedback
      .run(async () => {
        const result = await markAllRead.mutateAsync()
        return t('inbox.markAllReadDone', { n: result?.read_count ?? 0 })
      })
      .finally(() => setPendingWrite(null))
  }

  const markOneRead = (
    message: NotificationMessage,
    options: {
      readonly origin: 'row' | 'title'
      readonly after?: () => void
    } = { origin: 'row' },
  ): void => {
    setPendingWrite({
      kind: 'row',
      messageId: message.id,
      origin: options.origin,
    })
    if (options.origin === 'row') {
      pendingRowRemoval.current = { messageId: message.id }
    }
    void feedback
      .run(async () => {
        try {
          await markRead.mutateAsync({ messageId: message.id })
        } catch (error) {
          pendingRowRemoval.current = null
          throw error
        }
        options.after?.()
        return t('inbox.markReadDone', {
          title: resolveMessageText(message.title, message.params, formatInstant),
        })
      })
      .finally(() => setPendingWrite(null))
  }

  const goToPage = (next: number): void => {
    resetFeedback()
    setPages({ context: listContext, index: next })
  }

  const openMessage = (message: NotificationMessage): void => {
    if (onOpen === undefined) {
      return
    }
    if (!isUnread(message)) {
      onOpen(message)
      return
    }
    markOneRead(message, { origin: 'title', after: () => onOpen(message) })
  }

  const markAllInitiated = pendingWrite?.kind === 'all'
  const markAllBlocked = (unreadTotal ?? 0) === 0
  const markAllAction = (
    <ActionButton
      type="button"
      size="small"
      busy={markAllInitiated}
      unavailable={!markAllInitiated && (feedback.pending || markAllBlocked)}
      unavailableReason={
        markAllBlocked ? t('inbox.markAllReadUnavailable') : undefined
      }
      onClick={runMarkAll}
    >
      {t('inbox.markAllRead')}
    </ActionButton>
  )

  const paged = pageSize !== undefined && pageSize > 0 && total > pageSize
  const previousDisabled = pageIndex === 0
  const nextDisabled = (pageIndex + 1) * (pageSize ?? 0) >= total

  const surfaceContent = (
    <>
      <TenantAsyncSection<NotificationInboxPage>
        pending={pending}
        payload={pending ? undefined : page}
        error={messages.error}
        refreshing={messages.isFetching}
        empty={list.length === 0}
        headingLevel={variant === 'embedded' ? headingLevel : 'h2'}
        emptyHeadingLevel="none"
        loading={
          <ListSkeleton
            label={t('inbox.loading')}
            rows={[
              <Skeleton key="a" variant="text" width="52%" />,
              <Skeleton key="b" variant="text" width="38%" />,
              <Skeleton key="c" variant="text" width="46%" />,
            ]}
          />
        }
        errorState={{
          title: t('inbox.error.title'),
          description: t('inbox.error.description'),
          retryLabel: t('inbox.retry'),
          onRetry: () => void messages.refetch(),
        }}
        emptyState={{
          title: t('inbox.empty.title'),
          description: t('inbox.empty.description'),
        }}
      >
        {(settled) => (
          <List
            ref={listContainer}
            tabIndex={-1}
            aria-label={t('inbox.title')}
            sx={{ p: 0 }}
          >
            {settled.messages.map((message) => {
              const rowWrite =
                pendingWrite?.kind === 'row' &&
                pendingWrite.messageId === message.id
                  ? pendingWrite
                  : null
              return (
                <NotificationRow
                  key={message.id}
                  message={message}
                  markReadBusy={rowWrite?.origin === 'row'}
                  markReadUnavailable={
                    feedback.pending && rowWrite?.origin !== 'row'
                  }
                  markReadRef={(element) => {
                    if (element === null) {
                      rowActions.current.delete(message.id)
                    } else {
                      rowActions.current.set(message.id, element)
                    }
                  }}
                  titleBusy={rowWrite?.origin === 'title'}
                  titleUnavailable={
                    feedback.pending && rowWrite?.origin !== 'title'
                  }
                  onMarkRead={() => markOneRead(message)}
                  onOpen={
                    onOpen === undefined || message.link === ''
                      ? undefined
                      : () => openMessage(message)
                  }
                  renderDetail={renderDetail}
                />
              )
            })}
          </List>
        )}
      </TenantAsyncSection>

      {paged && (
        <Box
          sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, mt: 2 }}
        >
          <ActionButton
            type="button"
            size="small"
            unavailable={previousDisabled || feedback.pending}
            onClick={() => goToPage(pageIndex - 1)}
          >
            {t('inbox.previous')}
          </ActionButton>
          <ActionButton
            type="button"
            size="small"
            unavailable={nextDisabled || feedback.pending}
            onClick={() => goToPage(pageIndex + 1)}
          >
            {t('inbox.next')}
          </ActionButton>
        </Box>
      )}

      <WriteFeedbackRegion outcome={feedback.outcome} resolve={resolveError} />
    </>
  )

  return variant === 'embedded' ? (
    <SectionCard
      title={t('inbox.title')}
      actions={markAllAction}
      headingLevel={headingLevel as SectionCardHeadingLevel}
    >
      {surfaceContent}
    </SectionCard>
  ) : (
    <PageLayout title={t('inbox.title')} actions={markAllAction}>
      <SectionCard>{surfaceContent}</SectionCard>
    </PageLayout>
  )
}

function NotificationRow({
  message,
  markReadBusy,
  markReadUnavailable,
  markReadRef,
  titleBusy,
  titleUnavailable,
  onMarkRead,
  onOpen,
  renderDetail,
}: {
  readonly message: NotificationMessage
  readonly markReadBusy: boolean
  readonly markReadUnavailable: boolean
  readonly markReadRef: (element: HTMLButtonElement | null) => void
  readonly titleBusy: boolean
  readonly titleUnavailable: boolean
  readonly onMarkRead: () => void
  readonly onOpen: (() => void) | undefined
  readonly renderDetail: ((message: NotificationMessage) => ReactNode) | undefined
}) {
  const { t } = useNotificationUiTranslation()
  const formatInstant = useFormatInstant()
  const activateTitle = useActivationGuard(
    () => {
      onOpen?.()
    },
    { busy: titleBusy, unavailable: titleUnavailable },
  )
  const unread = isUnread(message)
  const title = resolveMessageText(message.title, message.params, formatInstant)
  const body = resolveMessageText(message.body, message.params, formatInstant)
  return (
    <ListItem
      divider
      alignItems="flex-start"
      sx={{ px: 0 }}
      secondaryAction={
        unread ? (
          <ActionButton
            ref={markReadRef}
            type="button"
            size="small"
            busy={markReadBusy}
            unavailable={markReadUnavailable}
            aria-label={t('inbox.markReadAria', { title })}
            onClick={onMarkRead}
          >
            {t('inbox.markRead')}
          </ActionButton>
        ) : undefined
      }
    >
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          width: '100%',
          minWidth: 0,
        }}
      >
        <ListItemText
          primary={
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: 1,
                pr: unread ? 12 : 0,
              }}
            >
              {onOpen === undefined ? (
                <Typography
                  component="span"
                  variant="subtitle1"
                  sx={{ fontWeight: unread ? 600 : 400 }}
                >
                  {title}
                </Typography>
              ) : (
                <ButtonBase
                  aria-busy={titleBusy ? true : undefined}
                  aria-disabled={
                    titleBusy || titleUnavailable ? true : undefined
                  }
                  onClick={activateTitle}
                  sx={{
                    textAlign: 'start',
                    borderRadius: 1,
                    p: 0,
                    '&[aria-disabled="true"]': {
                      cursor: 'default',
                      '&:hover': { backgroundColor: 'transparent' },
                    },
                  }}
                >
                  <Typography
                    component="span"
                    variant="subtitle1"
                    sx={{ fontWeight: unread ? 600 : 400 }}
                  >
                    {title}
                  </Typography>
                </ButtonBase>
              )}
              <Chip
                size="small"
                color={unread ? 'primary' : 'default'}
                label={unread ? t('inbox.unread') : t('inbox.read')}
              />
            </Box>
          }
          secondary={
            <>
              {body !== '' && (
                <Typography
                  component="span"
                  variant="body2"
                  color="text.secondary"
                  sx={{ display: 'block' }}
                >
                  {body}
                </Typography>
              )}
              <Typography
                component="span"
                variant="caption"
                color="text.secondary"
                sx={{ display: 'block', mt: 0.5 }}
              >
                {formatInstant(message.created_at) || message.created_at}
              </Typography>
            </>
          }
        />
        {renderDetail !== undefined && (
          <Box sx={{ mt: 1 }}>{renderDetail(message)}</Box>
        )}
      </Box>
    </ListItem>
  )
}
