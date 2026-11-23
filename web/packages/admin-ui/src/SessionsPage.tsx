/**
 * SessionsPage: the account-session administration page of the console.
 *
 * It lists the sessions of one account -- chosen through the account
 * search picker, which hands the read the account's id, so choosing an
 * account issues the read -- and revokes a single session. The read is
 * GET /api/v1/system/auth/accounts/:id/sessions and the revoke is
 * DELETE /api/v1/system/auth/sessions/:id; both are operations on the console
 * operations bundle, so the page builds no client and writes no query
 * key. The list carries the account's live sessions only: the session
 * module's read excludes revoked, expired and idle-expired rows, so a
 * revoked session does not list. The session's own answer is a snapshot:
 * the assurance level is the view's integer (0 none, 1 single, 2 multi),
 * rendered as a label, and the timestamps render in UTC through Intl. A
 * row the server marks current is the caller's own session; it carries a
 * marker and its confirmation says the revoke signs the caller out too.
 * The revoke reports through the list's one feedback scope, whose region
 * renders directly under the table; choosing another account and the page's
 * refresh action both reset it, so a result never describes a context the
 * page has left. The account picker is held for the whole attempt -- the
 * revoke names the account it was issued for, and a change while it settled
 * would leave its result under another account's list. The row's revoke
 * control keeps focus through the write: it is busy while its own revoke is
 * in flight, and the rows it would race are unavailable for that time. The
 * landed revoke drops the row on the re-read, so focus moves in the same
 * commit to the next row's revoke control, else the previous row's, else the
 * list section's heading -- never to the document body. The page opens with
 * ui-kit's PageHeader.
 */

import { useLayoutEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  DataTable,
  EmptyState,
  ListSkeleton,
  PRIMARY_COLUMN_MAX_WIDTH,
  primaryColumnMaxWidthSx,
  SectionCard,
  useFormatInstant,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { AccountSelector } from './internal/account-selector.js'
import { AdminAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { identifierPrefix } from './internal/identifier-prefix.js'
import { useSectionHeadingFocus } from './internal/section-heading-focus.js'
import { RefreshButton } from '@speed/ui-kit'
import { SelectorRow } from './internal/selector-row.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import {
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useAdminOperations } from './operations.js'
import type { AdminAccount, AdminAccountSession } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'

/**
 * The id of the page's own "choose an account first" text. The disabled
 * refresh is described by it, so the explanation has one copy on the page.
 */
const ACCOUNT_HINT_ID = 'sessions-account-hint'

/** The assurance level's label from its integer (0 none, 1 single, 2 multi). */
function assuranceKey(assurance: number): string {
  if (assurance >= 2) {
    return 'sessions.assurance.multi'
  }
  return assurance === 1 ? 'sessions.assurance.single' : 'sessions.assurance.none'
}

export function SessionsPage() {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()

  // The account the operator picked: choosing one issues its session read.
  const [selectedAccount, setSelectedAccount] = useState<AdminAccount | null>(
    null,
  )
  // The session whose revoke is in flight, so its row reports busy while
  // the other rows report unavailable.
  const [revokingId, setRevokingId] = useState<string | null>(null)
  const account = selectedAccount?.id ?? ''
  const confirm = usePendingConfirm()
  // The list's one write scope: the revoke row action reports here, and the
  // region renders directly under the table. The confirmation names the
  // session lifecycle, and a refusal resolves through the console
  // whitelist like every other scope on the page.
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)

  const sessions = operations.useAccountSessions({
    id: account === '' ? undefined : account,
  })
  const revokeSession = operations.useRevokeSession()

  // Where a landed revoke sends focus. The row leaves the list on the
  // re-read the write invalidates, so the target is resolved against the
  // list the write ran over once that list is replaced.
  const landingAfterRemoval = useRef<{
    readonly id: string
    readonly data: readonly AdminAccountSession[] | undefined
  } | null>(null)
  const previousIds = useRef<readonly string[]>([])
  const revokeButtons = useRef(new Map<string, HTMLButtonElement>())
  const registerRevokeButton = (
    id: string,
    node: HTMLButtonElement | null,
  ): void => {
    if (node === null) {
      revokeButtons.current.delete(id)
    } else {
      revokeButtons.current.set(id, node)
    }
  }
  const { sectionRef: listSectionRef, focusHeading: focusListHeading } =
    useSectionHeadingFocus()

  // A landed revoke leaves the list on the re-read that follows it. The
  // control that started the write is gone with the row, so focus moves to
  // the next row's revoke control, else the previous row's, else the list
  // section's heading, in the same commit that drops the row. Focus is only
  // placed when the removal left it on the document body; a reader who has
  // chosen another resting place keeps it.
  useLayoutEffect(() => {
    const ids = sessions.data?.map((row) => row.id) ?? []
    const landing = landingAfterRemoval.current
    if (landing !== null && sessions.data !== landing.data) {
      landingAfterRemoval.current = null
      const active = document.activeElement
      if (
        (active === null || active === document.body) &&
        !ids.includes(landing.id)
      ) {
        const index = previousIds.current.indexOf(landing.id)
        const targetId = ids[index] ?? ids[index - 1]
        const target =
          targetId === undefined
            ? undefined
            : revokeButtons.current.get(targetId)
        if (target !== undefined) {
          target.focus()
        } else {
          focusListHeading()
        }
      }
    }
    previousIds.current = ids
  }, [sessions.data, focusListHeading])

  // The account is an argument of the revoke: choosing another one clears
  // the slot, because a result from the previous account would read as
  // this list's. The picker is disabled while a revoke is in flight, so a
  // change lands only once the attempt has settled.
  const changeAccount = (next: AdminAccount | null) => {
    setSelectedAccount(next)
    feedback.reset()
  }

  // The refresh action re-reads the one body read and resets the list's
  // scope first: the revoke notice and failure belong to the list as it
  // stood before the re-read.
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('sessions', sessions)],
    feedback.reset,
  )

  const columns: DataTableColumn<AdminAccountSession>[] = [
    {
      id: 'id',
      header: t('sessions.column.id'),
      priority: 'low',
      cellWrap: 'nowrap',
      cellFullValue: (session) => session.id,
      // The prefix is short, but the cap counts the copy control beside
      // it, so the whole identifier cell stays inside the shared budget.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (session) => (
        <Typography variant="body2">
          {identifierPrefix(session.id)}
        </Typography>
      ),
    },
    {
      id: 'kind',
      header: t('sessions.column.kind'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (session) => session.kind,
    },
    {
      id: 'assurance',
      header: t('sessions.column.assurance'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (session) => t(assuranceKey(session.assurance)),
    },
    {
      id: 'created',
      header: t('sessions.column.created'),
      cellWrap: 'nowrap',
      cell: (session) => {
        // The page's primary identity: a session id identifies nothing to
        // a human, so the created time is what the table anchors on. It
        // never hides, and the shared cap keeps it from crowding the
        // actions out of a narrow container. The current-session mark
        // rides here rather than in the low-priority id column, so the
        // revoke-your-own-session warning is visible at every tier.
        const formatted = formatTime(session.created_at)
        return (
          <Box sx={{ display: 'flex', alignItems: 'center', columnGap: 1 }}>
            <Typography
              variant="body2"
              noWrap
              sx={primaryColumnMaxWidthSx}
              title={formatted === '' ? undefined : formatted}
            >
              {formatted}
            </Typography>
            {session.current === true && (
              <Chip
                size="small"
                variant="outlined"
                color="primary"
                label={t('sessions.current')}
                sx={{ flexShrink: 0 }}
              />
            )}
          </Box>
        )
      },
    },
    {
      id: 'lastSeen',
      header: t('sessions.column.lastSeen'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (session) => formatTime(session.last_seen_at),
    },
    {
      id: 'expires',
      header: t('sessions.column.expires'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (session) => formatTime(session.expires_at),
    },
    {
      id: 'state',
      header: t('sessions.column.state'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (session) =>
        session.revoked ? t('sessions.revoked') : t('sessions.active'),
    },
    {
      id: 'actions',
      align: 'right',
      header: t('sessions.column.actions'),
      cellWrap: 'nowrap',
      cell: (session) => {
        const revoking = feedback.pending && revokingId === session.id
        // A row whose write would share this one scope is held back while
        // another row's revoke is in flight.
        const unavailable = feedback.pending && revokingId !== session.id
        return (
          <ActionButton
            ref={(node) => registerRevokeButton(session.id, node)}
            type="button"
            size="small"
            tone="danger"
            aria-label={t('sessions.revokeAriaWithId', { id: session.id })}
            busy={revoking}
            unavailable={unavailable}
            onClick={() =>
              confirm.ask({
                title: t('sessions.revokeTitle'),
                message: t(
                  session.current === true
                    ? 'sessions.revokeMessageCurrent'
                    : 'sessions.revokeMessage',
                  { id: session.id },
                ),
                confirmLabel: t('common.confirm'),
                run: () => {
                  setRevokingId(session.id)
                  void feedback.run(async () => {
                    try {
                      await revokeSession.mutateAsync({
                        accountId: account,
                        sessionId: session.id,
                      })
                      landingAfterRemoval.current = {
                        id: session.id,
                        data: sessions.data,
                      }
                      return t('sessions.revokedNotice')
                    } finally {
                      // The row's own write has settled: a landed revoke
                      // has already dropped the row on the re-read.
                      setRevokingId(null)
                    }
                  })
                },
              })
            }
          >
            {t('sessions.revoke')}
          </ActionButton>
        )
      },
    },
  ]

  const rows = sessions.data
  const pending = sessions.isPending && rows === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('sessions.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('sessions.title') },
        ]}
        sx={{ mb: 2 }}
      />

      <SectionCard sx={{ mb: 3 }}>
        <SelectorRow
          selectorMaxWidth={260}
          trailing={
            <RefreshButton
              busy={pageRefresh.busy}
              disabled={account === ''}
              writePending={feedback.pending}
              describedBy={ACCOUNT_HINT_ID}
              disabledReason={t('sessions.needAccount.description')}
              label={t('common.refresh')}
              onClick={pageRefresh.refresh}
            />
          }
        >
          <AccountSelector
            label={t('sessions.accountLabel')}
            value={selectedAccount}
            onChange={changeAccount}
            disabled={feedback.pending}
            fullWidth
          />
        </SelectorRow>
      </SectionCard>

      {account === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('sessions.needAccount.title')}
          description={
            <span id={ACCOUNT_HINT_ID}>
              {t('sessions.needAccount.description')}
            </span>
          }
        />
      ) : (
        <>
          <SectionCard
            ref={listSectionRef}
            title={t('sessions.section.title')}
            flushContent
          >
            <AdminAsyncSection
              pending={pending}
              payload={rows}
              error={sessions.error}
              refreshing={sessions.isFetching}
              empty={rows !== undefined && rows.length === 0}
              headingLevel="h3"
              loading={
                <ListSkeleton
                  label={t('sessions.loading')}
                  rows={[
                    <Skeleton key="a" variant="text" width="58%" />,
                    <Skeleton key="b" variant="text" width="46%" />,
                  ]}
                />
              }
              errorState={{
                title: t('sessions.error.title'),
                description: t('sessions.error.description'),
                retryLabel: t('sessions.retry'),
                onRetry: () => void sessions.refetch(),
              }}
              emptyState={{
                title: t('sessions.empty.title'),
                description: t('sessions.empty.description'),
              }}
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={columns}
                  rowKey={(session) => session.id}
                />
              )}
            </AdminAsyncSection>
          </SectionCard>
          {/* The revoke scope's region sits directly under the table whose
              row actions write into it. */}
          <Box ref={regionRef}>
            <WriteFeedbackRegion outcome={feedback.outcome} />
          </Box>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
