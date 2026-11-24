/**
 * SessionsPage lists one account's live sessions, chosen through the account
 * search picker, and revokes a single session. A revoked session does not list
 * on the following read; a row the server marks current is the caller's own.
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

// Shared by the empty-state text and the disabled refresh's described-by.
const ACCOUNT_HINT_ID = 'sessions-account-hint'

function assuranceKey(assurance: number): string {
  if (assurance >= 2) {
    return 'sessions.assurance.multi'
  }
  return assurance === 1 ? 'sessions.assurance.single' : 'sessions.assurance.none'
}

/**
 * SessionsPage lists one account's live sessions and revokes a single session.
 */
export function SessionsPage() {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()

  const [selectedAccount, setSelectedAccount] = useState<AdminAccount | null>(
    null,
  )
  const [revokingId, setRevokingId] = useState<string | null>(null)
  const account = selectedAccount?.id ?? ''
  const confirm = usePendingConfirm()
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)

  const sessions = operations.useAccountSessions({
    id: account === '' ? undefined : account,
  })
  const revokeSession = operations.useRevokeSession()

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

  // A landed revoke drops its row: focus moves to the next row's revoke control,
  // else the previous row's, else the list heading, unless the reader has moved
  // focus elsewhere.
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

  const changeAccount = (next: AdminAccount | null) => {
    setSelectedAccount(next)
    feedback.reset()
  }

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
      // The cap counts the copy control beside the prefix.
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
          <Box ref={regionRef}>
            <WriteFeedbackRegion outcome={feedback.outcome} />
          </Box>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
