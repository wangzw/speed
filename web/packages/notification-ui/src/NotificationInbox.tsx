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
 * The read is one of the four settled states (ui-kit's AsyncSection):
 * a labelled loading placeholder, a retryable error placeholder, an empty
 * placeholder, or the list. The page opens with ui-kit's PageHeader, so
 * the heading order never skips.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import ListItemText from '@mui/material/ListItemText'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import type { NotificationMessage } from '@speed/api-sdk'
import {
  AsyncSection,
  InlineSuccess,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
  touchTargetSx,
} from '@speed/ui-kit'
import { formatDateTime } from './internal/datetime.js'
import { InlineError } from './internal/inline-error.js'
import {
  type NotificationInbox as NotificationInboxPage,
  useMarkAllNotificationsRead,
  useMarkNotificationRead,
  useNotificationMessages,
} from './internal/queries.js'
import { useNotificationUiTranslation } from './internal/translation.js'

/** Whether a message is still unread. The server writes `read_at` as JSON
 * `null` for an unread message (the route's view carries a `*time.Time`
 * with no omitempty), though the generated type models the field as an
 * optional string -- so a runtime `null` counts as unread alongside an
 * absent or empty value. */
function isUnread(message: NotificationMessage): boolean {
  const readAt: string | null | undefined = message.read_at
  return readAt === undefined || readAt === null || readAt === ''
}

export function NotificationInbox() {
  const { t, i18n } = useNotificationUiTranslation()
  const messages = useNotificationMessages()
  const markRead = useMarkNotificationRead()
  const markAllRead = useMarkAllNotificationsRead()
  // The last mark-all-read answer's count, or null when none has landed;
  // a fresh attempt clears it so a stale count never lingers.
  const [markedAll, setMarkedAll] = useState<number | null>(null)

  const page = messages.data
  const list = page?.messages ?? []
  const pending = messages.isPending && page === undefined
  const unread = list.filter(isUnread).length

  const writeError = markRead.isError
    ? errorCodeOf(markRead.error)
    : markAllRead.isError
      ? errorCodeOf(markAllRead.error)
      : null

  const runMarkAll = (): void => {
    setMarkedAll(null)
    markAllRead.mutate(undefined, {
      onSuccess: (result) => setMarkedAll(result?.read_count ?? 0),
    })
  }

  return (
    <Box>
      <PageHeader
        title={t('inbox.title')}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            disabled={unread === 0 || markAllRead.isPending}
            onClick={runMarkAll}
            sx={touchTargetSx}
          >
            {markAllRead.isPending
              ? t('inbox.markAllReadPending')
              : t('inbox.markAllRead')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      {/* The read-all confirmation: one standing role="status" region
          (ui-kit's InlineSuccess), mounted for the whole settled phase and
          silent until a write lands -- a region born together with its text
          is unreliable for a screen reader, so the region is there first and
          the count fills it. */}
      <Box sx={{ mb: 2 }}>
        <InlineSuccess
          message={
            markedAll === null ? null : t('inbox.markAllReadDone', { n: markedAll })
          }
        />
      </Box>

      <InlineError code={writeError} />

      {/* The empty placeholder stands under the page's own "Notifications"
          h1, which already names the space: a second heading here ("No
          notifications") would re-enter the outline as a redundant chapter,
          so the placeholder renders headerless as one role="status" region
          (emptyHeadingLevel="none"). */}
      <AsyncSection<NotificationInboxPage>
        pending={pending}
        payload={pending ? undefined : page}
        empty={list.length === 0}
        headingLevel="h2"
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
                markReadDisabled={markRead.isPending}
                onMarkRead={() => markRead.mutate({ messageId: message.id })}
              />
            ))}
          </List>
        )}
      </AsyncSection>
    </Box>
  )
}

/** One message row: title, body, time, state chip and (unread only) the
 * mark-read action. */
function NotificationRow({
  message,
  locale,
  markReadDisabled,
  onMarkRead,
}: {
  readonly message: NotificationMessage
  readonly locale: string
  readonly markReadDisabled: boolean
  readonly onMarkRead: () => void
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
            aria-label={t('inbox.markReadAria', { title: message.title })}
            onClick={onMarkRead}
            sx={touchTargetSx}
          >
            {t('inbox.markRead')}
          </Button>
        ) : undefined
      }
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
            <Typography
              component="span"
              variant="subtitle1"
              sx={{ fontWeight: unread ? 600 : 400 }}
            >
              {message.title}
            </Typography>
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
              {formatDateTime(message.created_at, locale)}
            </Typography>
          </>
        }
      />
    </ListItem>
  )
}
