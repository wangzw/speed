/**
 * NotificationInbox: the notification surface -- the caller's messages
 * with per-message and mark-all read.
 *
 * It reads one page of the caller's messages (GET
 * /notifications/messages) and renders each as a row: title, body and
 * received time, an unread indicator, and -- for an unread row -- a
 * "mark as read" affordance. A header action marks every unread message
 * read (POST /notifications/messages/read-all) and reports the answer's
 * count. Both writes are the generated mutations, and each follows its
 * answer by invalidating the acting tenant's reads (see internal/queries),
 * so the list and the header bell's unread count move together.
 *
 * Both actions report through one write-feedback scope: the header action
 * and every row action share one result slot under the list, so a success
 * and a failure never stand together and the attempt that settles last is
 * the one shown. While one attempt is in flight the control that started
 * it reports busy and every other write control in the scope -- the header
 * action, the other rows' actions, the titles that activate a message and
 * the pager -- stays focusable and unavailable without a reason, so
 * attempts never overlap. Marking one message read confirms with that
 * message's title, not only by flipping the row; marking all confirms the
 * answer's count. A page, group or page-size change is a new context and
 * clears the slot.
 *
 * A landed row action takes its own button out with the row turning read,
 * so focus moves in the same commit to the same button on the next unread
 * row, else the previous one, else the list container. The move happens
 * only when focus was on the removed button (the browser drops it to the
 * body); a reader who moved focus elsewhere keeps it.
 *
 * A host may narrow and shape the surface through optional props: a group
 * filter and a page size (with previous/next paging driven by the
 * response's total), a per-row detail renderer, and an `onOpen` action
 * that turns a row carrying a link into an activatable one -- it marks
 * the message read first and then hands the message to the host, which
 * owns the navigation. The embedded variant drops the page header for a
 * heading at the caller's level, so the surface nests inside another
 * page's outline.
 *
 * The read is one of the four settled states (ui-kit's AsyncSection): a
 * labelled loading placeholder, a retryable error placeholder, an empty
 * placeholder, or the list. The admission layer's bare 404 is split out
 * of the failure branch as the tenant refusal state, which offers no
 * retry. A failed refresh over the already settled list keeps the list
 * on screen and marks it stale above with a retry, rather than replacing
 * it with the error placeholder.
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

export type NotificationInboxProps = {
  /**
   * Called with a message whose row was activated. A row is activatable
   * only when the message carries a link and this is supplied; an unread
   * message is marked read first, and the callback runs once that write
   * lands. The host owns what happens next (usually navigation).
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

/** Whether a message is still unread. The server writes `read_at` as JSON
 * `null` for an unread message (the route's view carries a nullable
 * `*time.Time` with no omitempty), and the generated type models the field
 * as an optional `string | null` -- so a runtime `null` counts as unread
 * alongside an absent or empty value. */
function isUnread(message: NotificationMessage): boolean {
  const readAt: string | null | undefined = message.read_at
  return readAt === undefined || readAt === null || readAt === ''
}

/** The write the inbox scope has in flight, and which control started it.
 * One scope interlocks its writes, so at most one of these stands. A title
 * that opens an unread message starts the same row write as that row's own
 * action, which is what the origin tells apart. */
type PendingWrite =
  | { readonly kind: 'all' }
  | {
      readonly kind: 'row'
      readonly messageId: string
      readonly origin: 'row' | 'title'
    }

/** The id of the row whose mark-read action takes focus when the action of
 * `removedId`'s row is gone from the rendered rows: the next row that still
 * has one, else the previous row, else none (the caller falls back to the
 * list container). */
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

/** The identity of a list context: one group filter and page size. A
 * change of either is a new list, so the page index and the standing
 * feedback of the old one must not survive it. */
function listContextOf(
  group: string | undefined,
  pageSize: number | undefined,
): string {
  return JSON.stringify([group ?? null, pageSize ?? null])
}

export function NotificationInbox({
  onOpen,
  renderDetail,
  group,
  pageSize,
  variant = 'page',
  headingLevel = 'h2',
}: NotificationInboxProps = {}) {
  const { t } = useNotificationUiTranslation()
  // The page index belongs to the list context it was chosen in: when the
  // host changes the group or page size, the new list is read from its
  // first page during this very render, so no request goes out at the
  // previous page's offset and no follow-up request is needed.
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
  // The read-all action writes the whole inbox, so its gate reads the
  // whole inbox's unread count -- the same query the header bell renders,
  // refreshed by every write's invalidation. Counting the visible page's
  // unread rows would disable the action whenever that page happens to be
  // read through, even though unread messages remain on other pages (or
  // under the current group).
  const unreadTotal = useNotificationUnreadCount().data
  // The inbox is one feedback scope: the header action and every row's
  // action write into the same slot, so only one result stands at a time
  // and the attempt that settles last is the one shown.
  const feedback = useWriteFeedback()
  const resolveError = useNotificationUiErrorText()
  // The mark-read confirmation names the message it changed. A time marker in
  // the title is resolved in the viewer's display zone here too, so the
  // confirmation never repeats a marker the row already resolved.
  const formatInstant = useFormatInstant()
  const resetFeedback = feedback.reset
  // The write in flight and the control that started it. The scope's other
  // write controls stay unavailable without a reason while it runs, so two
  // attempts never overlap and one result never describes another attempt's
  // context.
  const [pendingWrite, setPendingWrite] = useState<PendingWrite | null>(null)
  // The last total a settled page of the current list reported. It keeps the
  // pager mounted while the next page's read is in flight.
  const [pagerTotal, setPagerTotal] = useState(0)
  // The rendered row actions by message id, and the list container: the two
  // landing places when a landed write takes the action that started it out
  // of the document. The removal a write in flight belongs to sits beside
  // them.
  const rowActions = useRef(new Map<string, HTMLButtonElement>())
  const listContainer = useRef<HTMLUListElement | null>(null)
  const pendingRowRemoval = useRef<{ readonly messageId: string } | null>(null)

  // A new filter or page size is a new list: drop the previous list's
  // standing result, which would otherwise describe another context. The
  // context change is detected through a ref so the effect acts on the
  // change alone.
  const previousContext = useRef(listContext)
  useEffect(() => {
    if (previousContext.current === listContext) {
      return
    }
    previousContext.current = listContext
    setPages({ context: listContext, index: 0 })
    setPagerTotal(0)
    setPendingWrite(null)
    // A write started on the old list must not move focus to a landing place
    // that belongs to the new one.
    pendingRowRemoval.current = null
    resetFeedback()
  }, [listContext, resetFeedback])

  const page = messages.data

  // A landed row write turns its row read, so the action that started it
  // leaves the document and the browser drops focus to the body. Focus moves
  // in the same commit to the same action on the next unread row, else on
  // the previous one, else to the list container -- but only when the
  // removal is what dropped it: focus the reader moved elsewhere stays put.
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
  // A page change reads a new window, and until that answer lands there is
  // no page to take a total from. The pager keeps the last settled total for
  // its own list so it stays mounted across the read: unmounting it would
  // take the control the operator just activated out of the document and
  // drop their focus to the page body. A new list context resets it.
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

  // Marking one message read confirms the message it changed; `after` runs
  // only once the write has landed, which is how opening a linked message
  // never navigates on a read that did not reach the server. A row action's
  // own success removes it with the row, so its landing place is recorded
  // before the attempt starts; a title that opens the message keeps its own
  // place because it stays in the document.
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

  // A page change is a context change: the previous page's write result no
  // longer describes what is on screen, so the feedback scope resets with
  // the move.
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

  // "Mark all as read" starts its own write or reports the scope's other one.
  // Its precondition -- something is unread -- is the one state it explains;
  // a write in flight elsewhere in the scope makes it unavailable with no
  // reason, like any other sibling write control.
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
      {/* The empty placeholder stands under the surface's own heading,
          which already names the space: a second heading here ("No
          notifications") would re-enter the outline as a redundant chapter,
          so the placeholder renders headerless as one role="status" region
          (emptyHeadingLevel="none"). */}
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
          // The list is the control group's container: a landed row action
          // that leaves no unread row behind lands focus here. It is named
          // and programmatically focusable, so it is announced as the group
          // the result belongs to.
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

      {/* The scope's one result sits under the controls that write into it:
          the header action and the row actions share this slot. */}
      <WriteFeedbackRegion outcome={feedback.outcome} resolve={resolveError} />
    </>
  )

  return variant === 'embedded' ? (
    <SectionCard
      title={t('inbox.title')}
      actions={markAllAction}
      // The embedded heading continues the host page's outline. SectionCard
      // types its level as the section range (h2-h6); this component keeps
      // accepting every level it always did, so the value passes through.
      headingLevel={headingLevel as SectionCardHeadingLevel}
    >
      {surfaceContent}
    </SectionCard>
  ) : (
    <PageLayout title={t('inbox.title')} actions={markAllAction}>
      {/* The page header owns the h1, so the list's card carries no second
          heading of its own; the list is named by its aria-label. */}
      <SectionCard>{surfaceContent}</SectionCard>
    </PageLayout>
  )
}

/** One message row: title, body, time, state chip and (unread only) the
 * mark-read action. A message carrying a link renders its title as the
 * row's activation control. */
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
  // The title cannot be an ActionButton: it is a text control inside the
  // row's typography. It takes the guard directly, so a swallowed activation
  // never marks the message read or hands it to the host mid-write. A title
  // that starts the row's read reports busy like any other initiating
  // control; a title reached while another write runs is unavailable.
  const activateTitle = useActivationGuard(
    () => {
      onOpen?.()
    },
    { busy: titleBusy, unavailable: titleUnavailable },
  )
  const unread = isUnread(message)
  // The server writes a time segment as a marker naming the parameter; both
  // the visible text and the action's accessible name carry the resolved
  // instant, never the marker.
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
                // Reserve room for the trailing action so a long title
                // never slides under it.
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
                    // The title keeps its own look, but an unavailable one
                    // gives no pointer feedback: the write behind it is what
                    // is running, and the row already reports it.
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
        {/* The detail renders outside ListItemText's secondary typography
            (a <p> by default), so host content may be block-level. */}
        {renderDetail !== undefined && (
          <Box sx={{ mt: 1 }}>{renderDetail(message)}</Box>
        )}
      </Box>
    </ListItem>
  )
}
