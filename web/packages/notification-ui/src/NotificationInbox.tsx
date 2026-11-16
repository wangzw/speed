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
 * the one shown. While one attempt is in flight every write control in the
 * scope -- the header action, every row action and the pager -- disables
 * and reads as busy, so attempts never overlap. Marking one message read
 * confirms with that message's title, not only by flipping the row; marking
 * all confirms the answer's count. A page, group or page-size change is a
 * new context and clears the slot.
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

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import ButtonBase from '@mui/material/ButtonBase'
import Chip from '@mui/material/Chip'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import ListItemText from '@mui/material/ListItemText'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import type { NotificationMessage } from '@speed/api-sdk'
import {
  ListSkeleton,
  PageLayout,
  SectionCard,
  WriteFeedbackRegion,
  useDisplayTimeZone,
  useWriteFeedback,
} from '@speed/ui-kit'
import type {
  EmptyStateHeadingLevel,
  SectionCardHeadingLevel,
} from '@speed/ui-kit'
import { formatDateTime } from './internal/datetime.js'
import { useNotificationUiErrorText } from './internal/error-text.js'
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
  const { t, i18n } = useNotificationUiTranslation()
  const timeZone = useDisplayTimeZone()
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
  const resetFeedback = feedback.reset
  // One write in flight interlocks the scope's writes: the header action,
  // every row's action and the pager disable together, so two attempts
  // never overlap and one result never describes another attempt's
  // context. Every write control reads as busy for the same reason.
  const writePending = feedback.pending

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
    resetFeedback()
  }, [listContext, resetFeedback])

  const page = messages.data
  const list = page?.messages ?? []
  const total = page?.total ?? 0
  const pending = messages.isPending && page === undefined

  const runMarkAll = (): void => {
    void feedback.run(async () => {
      const result = await markAllRead.mutateAsync()
      return t('inbox.markAllReadDone', { n: result?.read_count ?? 0 })
    })
  }

  // Marking one message read confirms the message it changed; `after` runs
  // only once the write has landed, which is how opening a linked message
  // never navigates on a read that did not reach the server.
  const markOneRead = (message: NotificationMessage, after?: () => void): void => {
    void feedback.run(async () => {
      await markRead.mutateAsync({ messageId: message.id })
      after?.()
      return t('inbox.markReadDone', { title: message.title })
    })
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
    markOneRead(message, () => onOpen(message))
  }

  const markAllAction = (
    <Button
      type="button"
      variant="outlined"
      size="small"
      disabled={writePending || (unreadTotal ?? 0) === 0}
      loading={writePending}
      onClick={runMarkAll}
    >
      {t('inbox.markAllRead')}
    </Button>
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
          <List aria-label={t('inbox.title')} sx={{ p: 0 }}>
            {settled.messages.map((message) => (
              <NotificationRow
                key={message.id}
                message={message}
                locale={i18n.language}
                timeZone={timeZone}
                markReadDisabled={writePending}
                onMarkRead={() => markOneRead(message)}
                onOpen={
                  onOpen === undefined || message.link === ''
                    ? undefined
                    : () => openMessage(message)
                }
                renderDetail={renderDetail}
              />
            ))}
          </List>
        )}
      </TenantAsyncSection>

      {paged && (
        <Box
          sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, mt: 2 }}
        >
          <Button
            type="button"
            size="small"
            disabled={previousDisabled || writePending}
            onClick={() => goToPage(pageIndex - 1)}
          >
            {t('inbox.previous')}
          </Button>
          <Button
            type="button"
            size="small"
            disabled={nextDisabled || writePending}
            onClick={() => goToPage(pageIndex + 1)}
          >
            {t('inbox.next')}
          </Button>
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
  locale,
  timeZone,
  markReadDisabled,
  onMarkRead,
  onOpen,
  renderDetail,
}: {
  readonly message: NotificationMessage
  readonly locale: string
  readonly timeZone: string | undefined
  readonly markReadDisabled: boolean
  readonly onMarkRead: () => void
  readonly onOpen: (() => void) | undefined
  readonly renderDetail: ((message: NotificationMessage) => ReactNode) | undefined
}) {
  const { t } = useNotificationUiTranslation()
  const unread = isUnread(message)
  return (
    <ListItem
      divider
      alignItems="flex-start"
      sx={{ px: 0 }}
      secondaryAction={
        unread ? (
          <Button
            type="button"
            size="small"
            disabled={markReadDisabled}
            loading={markReadDisabled}
            aria-label={t('inbox.markReadAria', { title: message.title })}
            onClick={onMarkRead}
          >
            {t('inbox.markRead')}
          </Button>
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
                  {message.title}
                </Typography>
              ) : (
                <ButtonBase
                  disabled={markReadDisabled}
                  onClick={onOpen}
                  sx={{ textAlign: 'start', borderRadius: 1, p: 0 }}
                >
                  <Typography
                    component="span"
                    variant="subtitle1"
                    sx={{ fontWeight: unread ? 600 : 400 }}
                  >
                    {message.title}
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
              {message.body !== '' && (
                <Typography
                  component="span"
                  variant="body2"
                  color="text.secondary"
                  sx={{ display: 'block' }}
                >
                  {message.body}
                </Typography>
              )}
              <Typography
                component="span"
                variant="caption"
                color="text.secondary"
                sx={{ display: 'block', mt: 0.5 }}
              >
                {formatDateTime(message.created_at, locale, timeZone)}
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
