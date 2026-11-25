import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import Chip from '@mui/material/Chip'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useForm } from 'react-hook-form'
import type { SubmitHandler, UseFormReturn } from 'react-hook-form'
import type { UseMutationResult } from '@tanstack/react-query'
import { useStepUpGate } from '@speed/auth-core'
import {
  ActionButton,
  ConfirmDialog,
  DataTable,
  FormField,
  FormLayout,
  ListSkeleton,
  OneTimeValuePanel,
  PRIMARY_COLUMN_MAX_WIDTH,
  RefreshButton,
  SectionCard,
  SidePanel,
  TablePager,
  errorCodeOf,
  pageRefreshRead,
  primaryColumnMaxWidthSx,
  useFormatInstant,
  useActivationGuard,
  useInertSelectMenu,
  usePageRefresh,
  useWriteFeedback,
} from '@speed/ui-kit'
import type {
  DataTableColumn,
  DataTableColumnPriority,
  WriteFeedback,
} from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import {
  accountIdentityValue,
  accountLabel,
  accountNotice,
  accountUsername,
  identityLabel,
  type AccountIdentityValue,
} from './internal/account-identity.js'
import {
  PendingConfirmDialog,
  usePendingConfirm,
} from './internal/confirm.js'
import type { PendingConfirmState } from './internal/confirm.js'
import { useAdminHasFeature } from './internal/features.js'
import { identifierPrefix } from './internal/identifier-prefix.js'
import { useAdminSession } from './internal/session.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAdminUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { useAdminOperations } from './operations.js'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { AdminPageHeader } from './internal/page-header.js'
import type {
  AdminAccount,
  AdminAccountSecret,
  AdminAccountSecretCreated,
  AdminAccountSession,
  AdminAccountFactor,
  AdminCreateAccountInput,
} from './operations.js'

const ACCOUNT_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

const IDENTIFIER_KINDS = ['username', 'email', 'phone'] as const
type IdentifierKind = (typeof IDENTIFIER_KINDS)[number]

const ACCOUNT_KINDS = ['user', 'service'] as const

const FILTER_STATUSES = ['active', 'disabled', 'deleted'] as const

const ACCOUNTS_PAGE_SIZE = 100

interface OneTimeSecret {
  readonly accountId: string
  readonly accountName: string
  readonly label: string
  readonly secret: string
}

interface AccountDetailFeedback {
  readonly status: WriteFeedback
  readonly mfa: WriteFeedback
  readonly patch: WriteFeedback
  readonly password: WriteFeedback
  readonly addIdentifier: WriteFeedback
  readonly createSecret: WriteFeedback
}

/** Focus on the body or a detached node also counts as caused by the removal. */
function focusFellWithRow(removed: HTMLElement | null): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  return removed !== null && removed.contains(active)
}

function statusLabel(status: string, t: (key: string) => string): string {
  return ACCOUNT_STATUSES.has(status)
    ? t(`accounts.status.${status}`)
    : t('accounts.status.other')
}

function closedSetLabel(
  prefix: string,
  value: string,
  t: (key: string) => string,
  i18n: { exists: (key: string, options?: { ns?: string }) => boolean },
): string {
  const key = `${prefix}.${value}`
  return i18n.exists(key, { ns: ADMIN_UI_NAMESPACE }) ? t(key) : value
}

/** A host-supplied list column inserted before the row-actions column; its
 * priority decides when a narrow container sheds it. */
export type AccountsPageExtraColumn = DataTableColumn<AdminAccount> & {
  readonly priority: DataTableColumnPriority
}

/** One account attribute the host offers as a plain text field. */
export interface AccountsPageAttributeField {
  /** The attribute's key in the account's attribute map. */
  readonly key: string
  /** The field's visible label. */
  readonly label: ReactNode
}

/** Props for AccountsPage: optional host columns and attribute fields. */
export interface AccountsPageProps {
  /** Host columns inserted before the row-actions column. */
  readonly extraColumns?: readonly AccountsPageExtraColumn[]
  /** Host-declared attribute fields replacing the raw-JSON editor; undeclared
   * keys an edited account already carries are preserved. */
  readonly attributeFields?: readonly AccountsPageAttributeField[]
}

/** AccountsPage lists, creates and edits platform accounts and their
 * factors, secrets and sessions. */
export function AccountsPage({
  extraColumns,
  attributeFields,
}: AccountsPageProps = {}) {
  const { t, i18n } = useAdminUiTranslation()
  const formatTime = useFormatInstant()
  const operations = useAdminOperations()
  const confirm = usePendingConfirm()
  const session = useAdminSession()
  const hasFeature = useAdminHasFeature()
  const factorsPresent = hasFeature('authn.admin.factors')
  const secretsPresent = hasFeature('authn.admin.secrets')
  const sessionsPresent = hasFeature('authn.admin.sessions')
  const gate = useStepUpGate<() => void>(
    (retry) => retry(),
    (error) => session !== null && session.isStepUpShortfall(error),
  )

  const identifierKindMenu = useInertSelectMenu()
  const statusFilterMenu = useInertSelectMenu()
  const accountKindFilterMenu = useInertSelectMenu()
  const [identifierDraft, setIdentifierDraft] = useState('')
  const [identifier, setIdentifier] = useState('')
  const [kindDraft, setKindDraft] = useState<IdentifierKind>('username')
  const [identifierKind, setIdentifierKind] = useState<IdentifierKind>('username')
  const [displayNameDraft, setDisplayNameDraft] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [statusDraft, setStatusDraft] = useState('')
  const [status, setStatus] = useState('')
  const [accountKindDraft, setAccountKindDraft] = useState('')
  const [accountKind, setAccountKind] = useState('')
  const [cursors, setCursors] = useState<readonly string[]>([''])
  const [page, setPage] = useState(0)
  const [selectedId, setSelectedId] = useState('')
  const [detailOpen, setDetailOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [identifierToRemove, setIdentifierToRemove] = useState('')
  // The one-time secret is page state, not scope state: a scope reset must
  // not drop its only copy, and the display outlives the panel content.
  const [oneTimeSecret, setOneTimeSecret] = useState<OneTimeSecret | null>(null)
  // A discard-confirmed close unmounts the panel's own close control with it,
  // so the page restores focus to the opener once the panel has exited.
  const createTrigger = useRef<HTMLButtonElement | null>(null)
  const detailTrigger = useRef<HTMLButtonElement | null>(null)
  const [discardSecretOpen, setDiscardSecretOpen] = useState(false)
  // Written after `onExited`: the page behind an open modal is aria-hidden,
  // so a notice written then would never be announced.
  const createSuccess = useRef<string | null>(null)
  const deleteButtons = useRef(new Map<string, HTMLButtonElement>())
  // The deleting row with its order and element: they name the neighbour that
  // takes focus, and tell the removal's focus loss from the operator moving it.
  const deleteFocus = useRef<{
    readonly id: string
    readonly order: readonly string[]
    readonly row: HTMLElement | null
  } | null>(null)
  const [deletingAccountId, setDeletingAccountId] = useState<string | null>(
    null,
  )
  const listRegion = useRef<HTMLDivElement | null>(null)

  const sessionButtons = useRef(new Map<string, HTMLButtonElement>())
  const [revokingSessionId, setRevokingSessionId] = useState<string | null>(
    null,
  )
  const sessionRevokeFocus = useRef<{
    readonly id: string
    readonly order: readonly string[]
    readonly row: HTMLElement | null
  } | null>(null)
  const sessionsHeading = useRef<HTMLHeadingElement | null>(null)
  const [revokingSecretId, setRevokingSecretId] = useState<string | null>(null)

  const listWrite = useWriteFeedback()
  const detailStatusWrite = useWriteFeedback()
  const detailMfaWrite = useWriteFeedback()
  const detailPatchWrite = useWriteFeedback()
  const detailPasswordWrite = useWriteFeedback()
  const detailAddIdentifierWrite = useWriteFeedback()
  const detailCreateSecretWrite = useWriteFeedback()
  const detailActionWrite = useWriteFeedback()
  const detailSecretWrite = useWriteFeedback()
  const detailSessionWrite = useWriteFeedback()
  const detailIdentifierRemoveWrite = useWriteFeedback()

  const cursor = cursors[page] ?? ''
  const accountParams = useMemo(
    () => ({
      ...(identifier === ''
        ? {}
        : { identifier_prefix: identifier, identifier_kind: identifierKind }),
      ...(displayName === '' ? {} : { display_name_prefix: displayName }),
      ...(status === '' ? {} : { status }),
      ...(accountKind === '' ? {} : { kind: accountKind }),
      ...(cursor === '' ? {} : { after: cursor }),
      limit: ACCOUNTS_PAGE_SIZE,
    }),
    [identifier, identifierKind, displayName, status, accountKind, cursor],
  )
  const accounts = operations.useAccounts(accountParams)
  // Keyed by the applied filters so a filter change drops the recorded
  // direction; the cursor and page size stay out of the key.
  const pagerScope = JSON.stringify(accountParams, (field, value) =>
    field === 'after' || field === 'limit' ? undefined : value,
  )

  function changePage(nextPage: number): void {
    if (listWrite.pending) {
      return
    }
    deleteFocus.current = null
    if (nextPage === page - 1 && page > 0) {
      listWrite.reset()
      setPage(nextPage)
      return
    }
    if (nextPage !== page + 1) {
      return
    }
    const nextCursor = accounts.data?.next_cursor ?? ''
    if (nextCursor === '') {
      return
    }
    listWrite.reset()
    setCursors((previous) => {
      const grown = [...previous]
      grown[page + 1] = nextCursor
      return grown
    })
    setPage(nextPage)
  }
  const account = operations.useAccount({
    id: selectedId === '' ? undefined : selectedId,
  })
  const selectedRow = accounts.data?.accounts.find((row) => row.id === selectedId)
  const selectedIdentity: AccountIdentityValue =
    account.data !== undefined
      ? accountIdentityValue(account.data)
      : selectedRow !== undefined
        ? accountIdentityValue(selectedRow)
        : { name: undefined, username: undefined }
  const sessions = operations.useAccountSessions({
    id: selectedId === '' || !sessionsPresent ? undefined : selectedId,
  })
  const sessionsIncludeCurrent =
    sessions.data?.some((row) => row.current === true) ?? false

  const deleteAccount = operations.useDeleteAccount()
  const resetMFA = operations.useResetMFA()
  const revokeSessions = operations.useRevokeAccountSessions()
  const revokeSecret = operations.useRevokeSecret()
  const revokeSession = operations.useRevokeSession()
  const removeIdentifier = operations.useRemoveIdentifier()

  const pageRefresh = usePageRefresh(
    [pageRefreshRead('accounts', accounts)],
    () => {
      deleteFocus.current = null
      listWrite.reset()
    },
  )

  const detailFeedback: AccountDetailFeedback = {
    status: detailStatusWrite,
    mfa: detailMfaWrite,
    patch: detailPatchWrite,
    password: detailPasswordWrite,
    addIdentifier: detailAddIdentifierWrite,
    createSecret: detailCreateSecretWrite,
  }

  const detailBusy =
    detailStatusWrite.pending ||
    detailMfaWrite.pending ||
    detailPatchWrite.pending ||
    detailPasswordWrite.pending ||
    detailAddIdentifierWrite.pending ||
    detailCreateSecretWrite.pending ||
    detailActionWrite.pending ||
    detailSecretWrite.pending ||
    detailSessionWrite.pending ||
    detailIdentifierRemoveWrite.pending

  function openDetail(id: string): void {
    setSelectedId(id)
    setDetailOpen(true)
  }

  function requestDetailClose(): void {
    if (oneTimeSecret !== null) {
      setDiscardSecretOpen(true)
      return
    }
    setDetailOpen(false)
  }

  function detailExited(): void {
    setSelectedId('')
    setIdentifierToRemove('')
    detailStatusWrite.reset()
    detailMfaWrite.reset()
    detailPatchWrite.reset()
    detailPasswordWrite.reset()
    detailAddIdentifierWrite.reset()
    detailCreateSecretWrite.reset()
    detailActionWrite.reset()
    detailSecretWrite.reset()
    detailSessionWrite.reset()
    detailIdentifierRemoveWrite.reset()
    sessionRevokeFocus.current = null
    setRevokingSessionId(null)
    setRevokingSecretId(null)
    const trigger = detailTrigger.current
    detailTrigger.current = null
    if (trigger !== null && trigger.isConnected) {
      trigger.focus()
    }
  }

  function accountCreated(created: AdminAccount): void {
    createSuccess.current = accountNotice(
      t,
      { named: 'accounts.created', unnamed: 'accounts.createdUnnamed' },
      accountIdentityValue(created),
    )
    setCreateOpen(false)
  }

  function createPanelExited(): void {
    const message = createSuccess.current
    createSuccess.current = null
    if (message !== null) {
      listWrite.succeed(message)
    }
    const trigger = createTrigger.current
    createTrigger.current = null
    if (trigger !== null && trigger.isConnected) {
      trigger.focus()
    }
  }

  function deleteOne(row: AdminAccount): Promise<void> {
    // The order at the attempt's start names the neighbour to focus; the next
    // render's order, already missing the row, cannot. Only the activating
    // control reports busy; its siblings are held without a reason.
    setDeletingAccountId(row.id)
    deleteFocus.current = {
      id: row.id,
      order: (accounts.data?.accounts ?? []).map((entry) => entry.id),
      row: deleteButtons.current.get(row.id)?.closest('tr') ?? null,
    }
    return listWrite.run(async () => {
      try {
        await deleteAccount.mutateAsync({ id: row.id })
      } catch (error) {
        deleteFocus.current = null
        if (gate.intercept(error, () => void deleteOne(row))) {
          return null
        }
        throw error
      }
      if (selectedId === row.id) {
        setSelectedId('')
      }
      return accountNotice(
        t,
        { named: 'accounts.deleted', unnamed: 'accounts.deletedUnnamed' },
        accountIdentityValue(row),
      )
    })
  }

  function deleteMessageFor(row: AdminAccount): string {
    const username = accountUsername(row.identifiers)
    if (row.display_name !== '') {
      return username === undefined
        ? t('accounts.deleteMessage', { name: row.display_name })
        : t('accounts.deleteMessageWithIdentifier', {
            name: row.display_name,
            identifier: username,
          })
    }
    return username === undefined
      ? t('accounts.deleteMessageUnnamed')
      : t('accounts.deleteMessageByIdentifier', { identifier: username })
  }

  function resetAllFactors(): Promise<void> {
    return detailActionWrite.run(async () => {
      try {
        await resetMFA.mutateAsync({ id: selectedId })
      } catch (error) {
        if (gate.intercept(error, () => void resetAllFactors())) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        { named: 'accounts.mfaReset', unnamed: 'accounts.mfaResetUnnamed' },
        selectedIdentity,
      )
    })
  }

  function revokeAllSessions(): Promise<void> {
    return detailActionWrite.run(async () => {
      try {
        await revokeSessions.mutateAsync({ id: selectedId })
      } catch (error) {
        if (gate.intercept(error, () => void revokeAllSessions())) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        {
          named: 'accounts.sessionsRevoked',
          unnamed: 'accounts.sessionsRevokedUnnamed',
        },
        selectedIdentity,
      )
    })
  }

  function removeOne(identifier: string): Promise<void> {
    return detailIdentifierRemoveWrite.run(async () => {
      try {
        await removeIdentifier.mutateAsync({ id: selectedId, identifier })
      } catch (error) {
        if (gate.intercept(error, () => void removeOne(identifier))) {
          return null
        }
        throw error
      }
      setIdentifierToRemove('')
      return accountNotice(
        t,
        {
          named: 'accounts.identifierRemoved',
          unnamed: 'accounts.identifierRemovedUnnamed',
        },
        selectedIdentity,
        { loginIdentifier: identifier },
      )
    })
  }

  function revokeOneSecret(row: AdminAccountSecret): Promise<void> {
    setRevokingSecretId(row.id)
    return detailSecretWrite.run(async () => {
      try {
        await revokeSecret.mutateAsync({ id: selectedId, secret: row.id })
      } catch (error) {
        if (gate.intercept(error, () => void revokeOneSecret(row))) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        {
          named: 'accounts.secretRevoked',
          unnamed: 'accounts.secretRevokedUnnamed',
        },
        selectedIdentity,
        { label: row.label },
      )
    })
  }

  function revokeOneSession(row: AdminAccountSession): Promise<void> {
    setRevokingSessionId(row.id)
    sessionRevokeFocus.current = {
      id: row.id,
      order: (sessions.data ?? []).map((entry) => entry.id),
      row: sessionButtons.current.get(row.id)?.closest('tr') ?? null,
    }
    return detailSessionWrite.run(async () => {
      try {
        await revokeSession.mutateAsync({
          accountId: selectedId,
          sessionId: row.id,
        })
      } catch (error) {
        sessionRevokeFocus.current = null
        if (gate.intercept(error, () => void revokeOneSession(row))) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        {
          named: 'accounts.sessionRevoked',
          unnamed: 'accounts.sessionRevokedUnnamed',
        },
        selectedIdentity,
        { session: row.id },
      )
    })
  }

  const columns: DataTableColumn<AdminAccount>[] = [
    {
      id: 'kind',
      header: t('accounts.column.kind'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (row) =>
        closedSetLabel('accounts.create.kind', row.kind, t, i18n),
    },
    {
      id: 'identity',
      header: t('accounts.column.identity'),
      cellWrap: 'nowrap',
      cellFullValue: (row) =>
        identityLabel(row.display_name, accountUsername(row.identifiers)),
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (row) => (
        <Typography variant="body2" noWrap>
          {identityLabel(row.display_name, accountUsername(row.identifiers)) ??
            t('common.accountUnavailable')}
        </Typography>
      ),
    },
    {
      id: 'status',
      header: t('accounts.column.status'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (row) => statusLabel(row.status, t),
    },
    {
      id: 'mfa',
      header: t('accounts.column.mfa'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (row) =>
        row.mfa_required ? t('accounts.mfaYes') : t('accounts.mfaNo'),
    },
    ...(extraColumns ?? []),
    {
      id: 'actions',
      align: 'right',
      header: t('accounts.column.actions'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <Box sx={{ display: 'flex', columnGap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
            aria-label={t('accounts.manageAria', {
              name: accountLabel(t, accountIdentityValue(row)),
            })}
            aria-haspopup="dialog"
            onClick={(event) => {
              detailTrigger.current = event.currentTarget
              openDetail(row.id)
            }}
          >
            {t('accounts.manage')}
          </Button>
          <ActionButton
            ref={(element) => {
              if (element === null) {
                deleteButtons.current.delete(row.id)
              } else {
                deleteButtons.current.set(row.id, element)
              }
            }}
            type="button"
            size="small"
            tone="danger"
            aria-label={t('accounts.deleteAria', {
              name: accountLabel(t, accountIdentityValue(row)),
            })}
            busy={listWrite.pending && deletingAccountId === row.id}
            unavailable={listWrite.pending && deletingAccountId !== row.id}
            onClick={() =>
              confirm.ask({
                title: t('accounts.deleteTitle'),
                message: deleteMessageFor(row),
                confirmLabel: t('common.confirm'),
                doubleConfirm: true,
                run: () => void deleteOne(row),
              })
            }
          >
            {t('accounts.delete')}
          </ActionButton>
        </Box>
      ),
    },
  ]

  const sessionColumns: DataTableColumn<AdminAccountSession>[] = [
    {
      id: 'id',
      header: t('accounts.sessions.column.id'),
      priority: 'low',
      cellWrap: 'nowrap',
      cellFullValue: (row) => row.id,
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (row) => (
        <Typography variant="body2">{identifierPrefix(row.id)}</Typography>
      ),
    },
    {
      id: 'created',
      header: t('accounts.sessions.column.created'),
      cellWrap: 'nowrap',
      cell: (row) => {
        const formatted = formatTime(row.created_at)
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
            {row.current === true && (
              <Chip
                size="small"
                variant="outlined"
                color="primary"
                label={t('accounts.sessions.current')}
                sx={{ flexShrink: 0 }}
              />
            )}
          </Box>
        )
      },
    },
    {
      id: 'state',
      header: t('accounts.sessions.column.state'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (row) =>
        row.revoked ? t('accounts.sessions.revoked') : t('accounts.sessions.active'),
    },
    {
      id: 'actions',
      align: 'right',
      header: t('accounts.sessions.column.actions'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <ActionButton
          ref={(element) => {
            if (element === null) {
              sessionButtons.current.delete(row.id)
            } else {
              sessionButtons.current.set(row.id, element)
            }
          }}
          type="button"
          size="small"
          tone="danger"
          aria-label={t('accounts.sessions.revokeAria', { id: row.id })}
          busy={
            detailSessionWrite.pending && revokingSessionId === row.id
          }
          unavailable={
            detailSessionWrite.pending && revokingSessionId !== row.id
          }
          onClick={() =>
            confirm.ask({
              title: t('accounts.sessions.revokeTitle'),
              message: t(
                row.current === true
                  ? 'accounts.sessions.revokeMessageCurrent'
                  : 'accounts.sessions.revokeMessage',
                { id: row.id },
              ),
              confirmLabel: t('common.confirm'),
              run: () => void revokeOneSession(row),
            })
          }
        >
          {t('accounts.sessions.revoke')}
        </ActionButton>
      ),
    },
  ]

  const rows = accounts.data?.accounts
  // Hand focus to the adjacent row's control, else the list container, in the
  // commit that removes the row — only when the removal took it, not when the
  // operator moved it during the write.
  useLayoutEffect(() => {
    const target = deleteFocus.current
    if (target === null) {
      return
    }
    const remaining = new Set((rows ?? []).map((row) => row.id))
    if (remaining.has(target.id)) {
      return
    }
    deleteFocus.current = null
    if (!focusFellWithRow(target.row)) {
      return
    }
    const index = target.order.indexOf(target.id)
    const candidates =
      index === -1
        ? [...target.order]
        : [
            ...target.order.slice(index + 1),
            ...target.order.slice(0, index).reverse(),
          ]
    for (const id of candidates) {
      if (!remaining.has(id)) {
        continue
      }
      const button = deleteButtons.current.get(id)
      if (button !== undefined) {
        button.focus()
        return
      }
    }
    listRegion.current?.focus()
  }, [rows])
  // Focus goes to the next row's revoke control, else the previous one, else
  // the section heading; a place the operator chose during the write stays.
  useLayoutEffect(() => {
    const target = sessionRevokeFocus.current
    if (target === null) {
      return
    }
    const list = sessions.data
    if (list === undefined) {
      return
    }
    const remaining = new Set(list.map((row) => row.id))
    if (remaining.has(target.id)) {
      return
    }
    sessionRevokeFocus.current = null
    if (!focusFellWithRow(target.row)) {
      return
    }
    const index = target.order.indexOf(target.id)
    const candidates =
      index === -1
        ? [...target.order]
        : [
            ...target.order.slice(index + 1),
            ...target.order.slice(0, index).reverse(),
          ]
    for (const id of candidates) {
      if (!remaining.has(id)) {
        continue
      }
      const button = sessionButtons.current.get(id)
      if (button !== undefined) {
        button.focus()
        return
      }
    }
    sessionsHeading.current?.focus()
  }, [sessions.data])
  useEffect(() => {
    if (!listWrite.pending) {
      setDeletingAccountId(null)
    }
  }, [listWrite.pending])
  useEffect(() => {
    if (!detailSessionWrite.pending) {
      setRevokingSessionId(null)
    }
  }, [detailSessionWrite.pending])
  useEffect(() => {
    if (!detailSecretWrite.pending) {
      setRevokingSecretId(null)
    }
  }, [detailSecretWrite.pending])
  const pending = accounts.isPending && rows === undefined
  const hasNextPage =
    accounts.data !== undefined && accounts.data.next_cursor !== ''

  return (
    <Box>
      <AdminPageHeader
        title={t('accounts.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('accounts.title') },
        ]}
        actions={
          <Box sx={{ display: 'flex', gap: 1 }}>
            <RefreshButton
              busy={pageRefresh.busy}
              writePending={listWrite.pending}
              label={t('common.refresh')}
              onClick={pageRefresh.refresh}
            />
            <Button
              type="button"
              variant="contained"
              size="small"
              aria-haspopup="dialog"
              onClick={(event) => {
                createTrigger.current = event.currentTarget
                setCreateOpen(true)
              }}
            >
              {t('accounts.create.title')}
            </Button>
          </Box>
        }
        sx={{ mb: 2 }}
      />

      <AccountCreatePanel
        open={createOpen}
        attributeFields={attributeFields}
        onClose={() => setCreateOpen(false)}
        onCreated={accountCreated}
        onExited={createPanelExited}
      />

      <SectionCard sx={{ mb: 3 }}>
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'flex-end',
            columnGap: 2,
            rowGap: 1,
          }}
        >
          <TextField
            select
            slotProps={{ select: identifierKindMenu }}
            size="small"
            value={kindDraft}
            onChange={(event) => setKindDraft(event.target.value as IdentifierKind)}
            label={t('accounts.identifierKindLabel')}
            sx={{ minWidth: 140 }}
          >
            {IDENTIFIER_KINDS.map((kind) => (
              <MenuItem key={kind} value={kind}>
                {t(`accounts.identifierKind.${kind}`)}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            size="small"
            value={identifierDraft}
            onChange={(event) => setIdentifierDraft(event.target.value)}
            label={t('accounts.identifierLabel')}
          />
          <TextField
            size="small"
            value={displayNameDraft}
            onChange={(event) => setDisplayNameDraft(event.target.value)}
            label={t('accounts.displayNamePrefixLabel')}
          />
          <TextField
            select
            slotProps={{ select: statusFilterMenu }}
            size="small"
            value={statusDraft}
            onChange={(event) => setStatusDraft(event.target.value)}
            label={t('accounts.filter.status')}
            sx={{ minWidth: 140 }}
          >
            <MenuItem value="">{t('accounts.filter.all')}</MenuItem>
            {FILTER_STATUSES.map((value) => (
              <MenuItem key={value} value={value}>
                {t(`accounts.status.${value}`)}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            select
            slotProps={{ select: accountKindFilterMenu }}
            size="small"
            value={accountKindDraft}
            onChange={(event) => setAccountKindDraft(event.target.value)}
            label={t('accounts.filter.kind')}
            sx={{ minWidth: 140 }}
          >
            <MenuItem value="">{t('accounts.filter.all')}</MenuItem>
            {ACCOUNT_KINDS.map((value) => (
              <MenuItem key={value} value={value}>
                {t(`accounts.create.kind.${value}`)}
              </MenuItem>
            ))}
          </TextField>
          <ActionButton
            type="button"
            emphasis="primary"
            unavailable={listWrite.pending}
            onClick={() => {
              listWrite.reset()
              deleteFocus.current = null
              setIdentifier(identifierDraft.trim())
              setIdentifierKind(kindDraft)
              setDisplayName(displayNameDraft.trim())
              setStatus(statusDraft)
              setAccountKind(accountKindDraft)
              setCursors([''])
              setPage(0)
            }}
          >
            {t('accounts.search')}
          </ActionButton>
        </Box>
      </SectionCard>

      <SectionCard flushContent>
        <Box
          ref={listRegion}
          tabIndex={-1}
          role="group"
          aria-label={t('accounts.listRegion')}
        >
          <AdminAsyncSection
            pending={pending}
            payload={rows}
            error={accounts.error}
            refreshing={accounts.isFetching}
            empty={
              rows !== undefined && rows.length === 0 && page === 0
            }
            headingLevel="h2"
            loading={
              <ListSkeleton
                label={t('accounts.loading')}
                rows={[
                  <Skeleton key="a" variant="text" width="58%" />,
                  <Skeleton key="b" variant="text" width="46%" />,
                ]}
              />
            }
            errorState={{
              title: t('accounts.error.title'),
              description: t('accounts.error.description'),
              retryLabel: t('accounts.retry'),
              onRetry: () => void accounts.refetch(),
            }}
            emptyState={{
              title: t('accounts.empty.title'),
              description: t('accounts.empty.description'),
            }}
          >
            {(settled) => (
              <DataTable
                rows={settled}
                columns={columns}
                rowKey={(row) => row.id}
                pinLastColumn
                rowVerticalAlign="top"
                emptyTitle={t('accounts.empty.title')}
                emptyDescription={t('accounts.empty.description')}
                emptyHeadingLevel="h2"
              />
            )}
          </AdminAsyncSection>
        </Box>
      </SectionCard>

      {/* The footer sits outside the async branches so the control that
          started the page read survives a pending, failed or empty page. */}
      <Box sx={{ mt: 2 }}>
        <TablePager
          key={pagerScope}
          count={-1}
          page={page}
          rowsPerPage={ACCOUNTS_PAGE_SIZE}
          rowsPerPageOptions={[ACCOUNTS_PAGE_SIZE]}
          onPageChange={(nextPage) => changePage(nextPage)}
          onRowsPerPageChange={() => undefined}
          hasNext={hasNextPage}
          unavailable={listWrite.pending}
          reading={accounts.isFetching}
        />
      </Box>

      <WriteFeedbackRegion outcome={listWrite.outcome} sx={{ mb: 2 }} />

      <SidePanel
        open={detailOpen}
        size="wide"
        title={accountLabel(t, selectedIdentity)}
        busy={detailBusy}
        onClose={requestDetailClose}
        onExited={detailExited}
      >
        {selectedId !== '' && (
          <>
            <AdminAsyncSection
              pending={account.isPending && account.data === undefined}
              payload={account.data}
              error={account.error}
              refreshing={account.isFetching}
              empty={false}
              headingLevel="h3"
              loading={
                <ListSkeleton
                  label={t('accounts.detail.loading')}
                  rows={[
                    <Skeleton key="a" variant="text" width="42%" />,
                    <Skeleton key="b" variant="text" width="34%" />,
                  ]}
                />
              }
              errorState={{
                title: t('accounts.detail.error.title'),
                description: t('accounts.detail.error.description'),
                retryLabel: t('accounts.detail.retry'),
                onRetry: () => void account.refetch(),
              }}
              notFoundState={{
                title: t('detail.notFound.title'),
                description: t('detail.notFound.description'),
                retryLabel: t('accounts.detail.retry'),
                onRetry: () => void account.refetch(),
              }}
            >
              {(settled) => (
                <Box sx={{ mb: 2 }}>
                  <Typography variant="body2">
                    {t('accounts.detail.displayNameLine', {
                      value:
                        settled.display_name === ''
                          ? t('common.emptyValue')
                          : settled.display_name,
                    })}
                  </Typography>
                  {accountUsername(settled.identifiers) !== undefined && (
                    <Typography variant="body2">
                      {t('accounts.detail.usernameLine', {
                        value: accountUsername(settled.identifiers) ?? '',
                      })}
                    </Typography>
                  )}
                  <Typography variant="body2">
                    {t('accounts.detail.statusLine', {
                      value: statusLabel(settled.status, t),
                    })}
                  </Typography>
                  <Typography variant="body2">
                    {t('accounts.detail.kindLine', {
                      value: closedSetLabel(
                        'accounts.create.kind',
                        settled.kind,
                        t,
                        i18n,
                      ),
                    })}
                  </Typography>
                </Box>
              )}
            </AdminAsyncSection>

            {(factorsPresent || sessionsPresent) && (
              <>
                <Box
                  sx={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    columnGap: 1,
                    rowGap: 1,
                  }}
                >
                  {factorsPresent && (
                    <ActionButton
                      type="button"
                      busy={detailActionWrite.pending}
                      onClick={() =>
                        confirm.ask({
                          title: t('accounts.resetMFATitle'),
                          message: t('accounts.resetMFAMessage'),
                          confirmLabel: t('common.confirm'),
                          run: () => void resetAllFactors(),
                        })
                      }
                    >
                      {t('accounts.resetMFA')}
                    </ActionButton>
                  )}
                  {sessionsPresent && (
                    <ActionButton
                      type="button"
                      tone="danger"
                      busy={detailActionWrite.pending}
                      onClick={() =>
                        confirm.ask({
                          title: t('accounts.revokeSessionsTitle'),
                          message: t(
                            sessionsIncludeCurrent
                              ? 'accounts.revokeSessionsMessageCurrent'
                              : 'accounts.revokeSessionsMessage',
                          ),
                          confirmLabel: t('common.confirm'),
                          run: () => void revokeAllSessions(),
                        })
                      }
                    >
                      {t('accounts.revokeSessions')}
                    </ActionButton>
                  )}
                </Box>
                <WriteFeedbackRegion
                  outcome={detailActionWrite.outcome}
                  sx={{ mb: 2 }}
                />
              </>
            )}

            {account.data !== undefined && (
              <AccountDetailWrites
                key={selectedId}
                account={account.data}
                feedback={detailFeedback}
                attributeFields={attributeFields}
                oneTimeSecret={oneTimeSecret}
                onSecretCreated={setOneTimeSecret}
                onCloseSecret={() => setOneTimeSecret(null)}
              />
            )}

            {factorsPresent && <AccountFactorsSection accountId={selectedId} />}

            {secretsPresent && (
              <AccountSecretsSection
                accountId={selectedId}
                feedback={detailSecretWrite}
                revokingSecretId={revokingSecretId}
                confirm={confirm}
                onRevoke={revokeOneSecret}
              />
            )}

            {sessionsPresent && (
              <>
                <Typography
                  ref={sessionsHeading}
                  component="h3"
                  variant="subtitle1"
                  // A revoked last row focuses this heading: focusable, but
                  // outside the tab order.
                  tabIndex={-1}
                  sx={{ mt: 2, mb: 1 }}
                >
                  {t('accounts.sessions.title')}
                </Typography>
                <AdminAsyncSection
                  pending={sessions.isPending && sessions.data === undefined}
                  payload={sessions.data}
                  error={sessions.error}
                  refreshing={sessions.isFetching}
                  empty={sessions.data !== undefined && sessions.data.length === 0}
                  headingLevel="h3"
                  loading={
                    <ListSkeleton
                      label={t('accounts.sessions.loading')}
                      rows={[<Skeleton key="a" variant="text" width="48%" />]}
                    />
                  }
                  errorState={{
                    title: t('accounts.sessions.error.title'),
                    description: t('accounts.sessions.error.description'),
                    retryLabel: t('accounts.sessions.retry'),
                    onRetry: () => void sessions.refetch(),
                  }}
                  emptyState={{
                    title: t('accounts.sessions.empty.title'),
                    description: t('accounts.sessions.empty.description'),
                  }}
                >
                  {(settled) => (
                    <DataTable
                      rows={settled}
                      columns={sessionColumns}
                      rowKey={(row) => row.id}
                    />
                  )}
                </AdminAsyncSection>
                <WriteFeedbackRegion
                  outcome={detailSessionWrite.outcome}
                  sx={{ mb: 2 }}
                />
              </>
            )}

            <Box
              sx={{
                mt: 2,
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'flex-end',
                columnGap: 2,
                rowGap: 1,
              }}
            >
              <TextField
                size="small"
                value={identifierToRemove}
                onChange={(event) => setIdentifierToRemove(event.target.value)}
                label={t('accounts.removeIdentifier.label')}
              />
              <ActionButton
                type="button"
                tone="danger"
                busy={detailIdentifierRemoveWrite.pending}
                unavailable={identifierToRemove.trim() === ''}
                unavailableReason={t('accounts.removeIdentifier.unavailable')}
                onClick={() =>
                  confirm.ask({
                    title: t('accounts.removeIdentifier.confirmTitle'),
                    message: t('accounts.removeIdentifier.confirmMessage', {
                      identifier: identifierToRemove,
                    }),
                    confirmLabel: t('common.confirm'),
                    run: () => void removeOne(identifierToRemove),
                  })
                }
              >
                {t('accounts.removeIdentifier.submit')}
              </ActionButton>
            </Box>
            <WriteFeedbackRegion
              outcome={detailIdentifierRemoveWrite.outcome}
              sx={{ mb: 2 }}
            />
          </>
        )}
      </SidePanel>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />

      <ConfirmDialog
        open={discardSecretOpen}
        title={t('accounts.createSecret.discardTitle')}
        message={t('accounts.createSecret.discardMessage')}
        confirmLabel={t('accounts.createSecret.discardConfirm')}
        cancelLabel={t('common.cancel')}
        onConfirm={() => {
          setDiscardSecretOpen(false)
          setOneTimeSecret(null)
          setDetailOpen(false)
        }}
        onCancel={() => setDiscardSecretOpen(false)}
      />

      {session !== null && (
        <StepUpChallenge
          open={gate.open}
          reason={gate.reason}
          session={session}
          onSuccess={gate.succeed}
          onCancel={gate.cancel}
        />
      )}
    </Box>
  )
}

function AccountFactorsSection({ accountId }: { readonly accountId: string }) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const factors = operations.useAccountFactors({ id: accountId })
  const columns: DataTableColumn<AdminAccountFactor>[] = [
    {
      id: 'kind',
      header: t('accounts.factors.column.kind'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (row) =>
        closedSetLabel('accounts.factors.kind', row.kind, t, i18n),
    },
    {
      id: 'label',
      header: t('accounts.factors.column.label'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <Typography
          variant="body2"
          noWrap
          sx={primaryColumnMaxWidthSx}
          title={row.label}
        >
          {row.label}
        </Typography>
      ),
    },
    {
      id: 'status',
      header: t('accounts.factors.column.status'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (row) =>
        closedSetLabel('accounts.factors.status', row.status, t, i18n),
    },
  ]
  return (
    <>
      <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
        {t('accounts.factors.title')}
      </Typography>
      <AdminAsyncSection
        pending={factors.isPending && factors.data === undefined}
        payload={factors.data}
        error={factors.error}
        refreshing={factors.isFetching}
        empty={factors.data !== undefined && factors.data.length === 0}
        headingLevel="h3"
        loading={
          <ListSkeleton
            label={t('accounts.factors.loading')}
            rows={[<Skeleton key="a" variant="text" width="48%" />]}
          />
        }
        errorState={{
          title: t('accounts.factors.error.title'),
          description: t('accounts.factors.error.description'),
          retryLabel: t('accounts.factors.retry'),
          onRetry: () => void factors.refetch(),
        }}
        emptyState={{
          title: t('accounts.factors.empty.title'),
          description: t('accounts.factors.empty.description'),
        }}
      >
        {(settled) => (
          <DataTable
            rows={settled}
            columns={columns}
            rowKey={(row) => `${row.kind}:${row.label}`}
          />
        )}
      </AdminAsyncSection>
    </>
  )
}

function AccountSecretsSection({
  accountId,
  feedback,
  revokingSecretId,
  confirm,
  onRevoke,
}: {
  readonly accountId: string
  readonly feedback: WriteFeedback
  readonly revokingSecretId: string | null
  readonly confirm: PendingConfirmState
  readonly onRevoke: (row: AdminAccountSecret) => Promise<void>
}) {
  const { t } = useAdminUiTranslation()
  const formatTime = useFormatInstant()
  const operations = useAdminOperations()
  const secrets = operations.useAccountSecrets({ id: accountId })
  const columns: DataTableColumn<AdminAccountSecret>[] = [
    {
      id: 'label',
      header: t('accounts.secrets.column.label'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <Typography
          variant="body2"
          noWrap
          sx={primaryColumnMaxWidthSx}
          title={row.label}
        >
          {row.label}
        </Typography>
      ),
    },
    {
      id: 'expires',
      header: t('accounts.secrets.column.expires'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (row) =>
        row.expires_at === null
          ? t('accounts.secrets.neverExpires')
          : formatTime(row.expires_at),
    },
    {
      id: 'state',
      header: t('accounts.secrets.column.state'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (row) =>
        row.revoked ? t('accounts.secrets.revoked') : t('accounts.secrets.active'),
    },
    {
      id: 'actions',
      align: 'right',
      header: t('accounts.secrets.column.actions'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <ActionButton
          type="button"
          size="small"
          tone="danger"
          aria-label={t('accounts.secrets.revokeAria', { label: row.label })}
          busy={feedback.pending && revokingSecretId === row.id}
          unavailable={
            row.revoked ||
            (feedback.pending && revokingSecretId !== row.id)
          }
          unavailableReason={
            row.revoked ? t('accounts.secrets.revokeUnavailable') : undefined
          }
          onClick={() =>
            confirm.ask({
              title: t('accounts.secrets.revokeTitle'),
              message: t('accounts.secrets.revokeMessage', { label: row.label }),
              confirmLabel: t('common.confirm'),
              run: () => void onRevoke(row),
            })
          }
        >
          {t('accounts.secrets.revoke')}
        </ActionButton>
      ),
    },
  ]
  return (
    <>
      <Typography component="h3" variant="subtitle1" sx={{ mt: 2, mb: 1 }}>
        {t('accounts.secrets.title')}
      </Typography>
      <AdminAsyncSection
        pending={secrets.isPending && secrets.data === undefined}
        payload={secrets.data}
        error={secrets.error}
        refreshing={secrets.isFetching}
        empty={secrets.data !== undefined && secrets.data.length === 0}
        headingLevel="h3"
        loading={
          <ListSkeleton
            label={t('accounts.secrets.loading')}
            rows={[<Skeleton key="a" variant="text" width="48%" />]}
          />
        }
        errorState={{
          title: t('accounts.secrets.error.title'),
          description: t('accounts.secrets.error.description'),
          retryLabel: t('accounts.secrets.retry'),
          onRetry: () => void secrets.refetch(),
        }}
        emptyState={{
          title: t('accounts.secrets.empty.title'),
          description: t('accounts.secrets.empty.description'),
        }}
      >
        {(settled) => (
          <DataTable rows={settled} columns={columns} rowKey={(row) => row.id} />
        )}
      </AdminAsyncSection>
      <WriteFeedbackRegion outcome={feedback.outcome} sx={{ mb: 2 }} />
    </>
  )
}

function parseStringMap(text: string): Record<string, string> | null {
  try {
    const parsed: unknown = JSON.parse(text)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    const out: Record<string, string> = {}
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value !== 'string') {
        return null
      }
      out[key] = value
    }
    return out
  } catch {
    return null
  }
}

function attributeValueOf(text: string, key: string): string {
  const parsed = text.trim() === '' ? null : parseStringMap(text)
  return parsed?.[key] ?? ''
}

function withAttributeValue(text: string, key: string, value: string): string {
  const parsed = text.trim() === '' ? {} : (parseStringMap(text) ?? {})
  const next = { ...parsed }
  if (value === '') {
    delete next[key]
  } else {
    next[key] = value
  }
  return JSON.stringify(next)
}

type VerifiedVia = '' | 'admin' | 'import-unverified'

type CreateAccountFields = {
  kind: 'user' | 'service'
  displayName: string
  username: string
  email: string
  emailVia: VerifiedVia
  phone: string
  phoneVia: VerifiedVia
  password: string
  mfaRequired: boolean
  attributes: string
}

const CREATE_ACCOUNT_DEFAULTS: CreateAccountFields = {
  kind: 'user',
  displayName: '',
  username: '',
  email: '',
  emailVia: '',
  phone: '',
  phoneVia: '',
  password: '',
  mfaRequired: false,
  attributes: '',
}

type CreateAccountMutation = UseMutationResult<
  AdminAccount,
  unknown,
  AdminCreateAccountInput
>

function createFailureKey(
  kind: CreateAccountFields['kind'],
  code: string,
): string | null {
  if (kind !== 'user') {
    return null
  }
  switch (code) {
    case 'authn.username_taken':
      return 'accounts.create.usernameTaken'
    case 'authn.username_reserved':
      return 'accounts.create.usernameReserved'
    case 'authn.username_invalid':
      return 'accounts.create.usernameInvalid'
    default:
      return null
  }
}

function AccountCreateForm({
  form,
  feedback,
  createAccount,
  attributeFields,
  onCreated,
}: {
  readonly form: UseFormReturn<CreateAccountFields>
  readonly feedback: WriteFeedback
  readonly createAccount: CreateAccountMutation
  readonly attributeFields?: readonly AccountsPageAttributeField[]
  readonly onCreated: (created: AdminAccount) => void
}) {
  const { t } = useAdminUiTranslation()
  const usernameInput = useRef<HTMLInputElement | null>(null)
  const kindMenu = useInertSelectMenu()
  const emailViaMenu = useInertSelectMenu()
  const phoneViaMenu = useInertSelectMenu()

  // An array, not a fragment: MUI's Select walks its children to build the
  // options and warns when one of them is a Fragment.
  const viaOptions = [
    <MenuItem key="none" value="">
      {t('accounts.create.via.none')}
    </MenuItem>,
    <MenuItem key="admin" value="admin">
      {t('accounts.create.via.admin')}
    </MenuItem>,
    <MenuItem key="import-unverified" value="import-unverified">
      {t('accounts.create.via.importUnverified')}
    </MenuItem>,
  ]

  const watched = form.watch()
  const namesAnAccount =
    watched.displayName.trim() !== '' &&
    (watched.kind === 'service' || watched.username.trim() !== '')
  const submitUnavailableKey =
    watched.displayName.trim() === ''
      ? 'accounts.create.submitUnavailable'
      : watched.kind !== 'service' && watched.username.trim() === ''
        ? 'accounts.create.usernameUnavailable'
        : undefined

  const onSubmit: SubmitHandler<CreateAccountFields> = (values) => {
    const username = values.username.trim()
    const phone = values.phone.trim()
    if (
      values.displayName.trim() === '' ||
      (values.kind !== 'service' && username === '')
    ) {
      feedback.reset()
      form.clearErrors('username')
      return
    }
    const parsed =
      values.attributes.trim() === '' ? null : parseStringMap(values.attributes)
    void feedback.run(async () => {
      form.clearErrors('username')
      let created: AdminAccount
      try {
        created = await createAccount.mutateAsync({
          kind: values.kind,
          ...(values.displayName === '' ? {} : { display_name: values.displayName }),
          ...(username === '' ? {} : { username }),
          ...(values.email === '' ? {} : { email: values.email }),
          ...(values.emailVia === '' ? {} : { email_verified_via: values.emailVia }),
          ...(phone === '' ? {} : { phone }),
          ...(values.phoneVia === '' ? {} : { phone_verified_via: values.phoneVia }),
          ...(values.password === '' ? {} : { password: values.password }),
          ...(parsed === null ? {} : { attributes: parsed }),
          ...(values.mfaRequired ? { mfa_required: true } : {}),
        })
      } catch (error) {
        const key = createFailureKey(values.kind, errorCodeOf(error))
        if (key === null) {
          throw error
        }
        form.setError('username', { type: 'server', message: t(key) })
        usernameInput.current?.focus()
        return null
      }
      form.reset()
      onCreated(created)
      return null
    })
  }

  const guardedSubmit = useActivationGuard(onSubmit, {
    busy: feedback.pending,
    unavailable: !namesAnAccount,
  })

  return (
    <>
      <FormLayout
        form={form}
        onSubmit={guardedSubmit}
        columns={2}
        maxWidth={false}
        actions={
          <ActionButton
            type="submit"
            emphasis="primary"
            busy={feedback.pending}
            unavailable={!namesAnAccount}
            unavailableReason={
              submitUnavailableKey === undefined
                ? undefined
                : t(submitUnavailableKey)
            }
          >
            {t('accounts.create.submit')}
          </ActionButton>
        }
      >
        <FormField
          name="kind"
          render={({ field }) => (
            <TextField
              {...field}
              select
              slotProps={{ select: kindMenu }}
              size="small"
              fullWidth
              label={t('accounts.create.kindLabel')}
            >
              <MenuItem value="user">{t('accounts.create.kind.user')}</MenuItem>
              <MenuItem value="service">{t('accounts.create.kind.service')}</MenuItem>
            </TextField>
          )}
        />
        <FormField
          name="displayName"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              label={t('accounts.create.displayNameLabel')}
            />
          )}
        />
        <FormField
          name="username"
          render={({ field, invalid, errorText }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              inputRef={usernameInput}
              label={t('accounts.create.usernameLabel')}
              error={invalid}
              helperText={errorText ?? undefined}
              slotProps={{ htmlInput: { 'aria-required': true } }}
            />
          )}
        />
        <FormField
          name="email"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              label={t('accounts.create.emailLabel')}
            />
          )}
        />
        <FormField
          name="emailVia"
          render={({ field }) => (
            <TextField
              {...field}
              select
              slotProps={{ select: emailViaMenu }}
              size="small"
              fullWidth
              label={t('accounts.create.emailViaLabel')}
            >
              {viaOptions}
            </TextField>
          )}
        />
        <FormField
          name="phone"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              label={t('accounts.create.phoneLabel')}
            />
          )}
        />
        <FormField
          name="phoneVia"
          render={({ field }) => (
            <TextField
              {...field}
              select
              slotProps={{ select: phoneViaMenu }}
              size="small"
              fullWidth
              label={t('accounts.create.phoneViaLabel')}
            >
              {viaOptions}
            </TextField>
          )}
        />
        <FormField
          name="password"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              type="password"
              label={t('accounts.create.passwordLabel')}
            />
          )}
        />
        {attributeFields === undefined ? (
          <FormField
            name="attributes"
            rules={{
              validate: (value) =>
                value.trim() === '' ||
                parseStringMap(value) !== null ||
                t('accounts.create.attributesHelper'),
            }}
            render={({ field, invalid, errorText }) => (
              <TextField
                {...field}
                size="small"
                fullWidth
                multiline
                minRows={2}
                label={t('accounts.create.attributesLabel')}
                error={invalid}
                helperText={errorText ?? t('accounts.create.attributesHelper')}
              />
            )}
          />
        ) : (
          attributeFields.map((declared) => (
            <FormField
              key={declared.key}
              name="attributes"
              render={({ field }) => (
                <TextField
                  size="small"
                  fullWidth
                  label={declared.label}
                  value={attributeValueOf(
                    typeof field.value === 'string' ? field.value : '',
                    declared.key,
                  )}
                  onChange={(event) =>
                    field.onChange(
                      withAttributeValue(
                        typeof field.value === 'string' ? field.value : '',
                        declared.key,
                        event.target.value,
                      ),
                    )
                  }
                  onBlur={field.onBlur}
                  name={field.name}
                />
              )}
            />
          ))
        )}
        <FormField
          name="mfaRequired"
          render={({ field }) => (
            <FormControlLabel
              control={
                <Checkbox
                  checked={field.value}
                  onChange={(event) => field.onChange(event.target.checked)}
                  onBlur={field.onBlur}
                  name={field.name}
                />
              }
              label={t('accounts.create.mfaLabel')}
            />
          )}
        />
      </FormLayout>
      <WriteFeedbackRegion outcome={feedback.outcome} />
    </>
  )
}

function AccountCreatePanel({
  open,
  attributeFields,
  onClose,
  onCreated,
  onExited,
}: {
  readonly open: boolean
  readonly attributeFields?: readonly AccountsPageAttributeField[]
  readonly onClose: () => void
  readonly onCreated: (created: AdminAccount) => void
  readonly onExited: () => void
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const createAccount = operations.useCreateAccount()
  const feedback = useWriteFeedback()
  const form = useForm<CreateAccountFields>({
    defaultValues: CREATE_ACCOUNT_DEFAULTS,
  })
  // Reading `isDirty` during render subscribes the form state to dirty
  // tracking, so the close guard below judges the current draft rather than
  // a value that was never computed.
  const dirty = form.formState.isDirty
  const [discardOpen, setDiscardOpen] = useState(false)

  useEffect(() => {
    if (!open) {
      return
    }
    form.reset()
    feedback.reset()
    setDiscardOpen(false)
  }, [open, form, feedback.reset])

  function requestClose(): void {
    if (feedback.pending) {
      return
    }
    if (dirty) {
      setDiscardOpen(true)
      return
    }
    onClose()
  }

  return (
    <>
      <SidePanel
        open={open}
        title={t('accounts.create.title')}
        size="wide"
        busy={feedback.pending}
        onClose={requestClose}
        onExited={() => {
          form.reset()
          feedback.reset()
          onExited()
        }}
      >
        <AccountCreateForm
          form={form}
          feedback={feedback}
          createAccount={createAccount}
          attributeFields={attributeFields}
          onCreated={onCreated}
        />
      </SidePanel>
      <ConfirmDialog
        open={discardOpen}
        title={t('accounts.create.discardTitle')}
        message={t('accounts.create.discardMessage')}
        confirmLabel={t('accounts.create.discardConfirm')}
        cancelLabel={t('common.cancel')}
        onConfirm={() => {
          setDiscardOpen(false)
          onClose()
        }}
        onCancel={() => setDiscardOpen(false)}
      />
    </>
  )
}

function OneTimeSecretNotice({
  notice,
  onClose,
}: {
  readonly notice: OneTimeSecret
  readonly onClose: () => void
}) {
  const { t } = useAdminUiTranslation()
  return (
    <Box sx={{ mt: 1 }}>
      <OneTimeValuePanel
        message={t('accounts.createSecret.handedOver', {
          label: notice.label,
        })}
        value={notice.secret}
        copyLabel={t('accounts.createSecret.copy')}
        closeLabel={t('accounts.createSecret.close')}
        closeAriaLabel={t('accounts.createSecret.closeAria', {
          name: notice.accountName,
          label: notice.label,
        })}
        onClose={onClose}
      />
    </Box>
  )
}

function AccountDetailWrites({
  account,
  feedback,
  attributeFields,
  oneTimeSecret,
  onSecretCreated,
  onCloseSecret,
}: {
  readonly account: AdminAccount
  readonly feedback: AccountDetailFeedback
  readonly attributeFields?: readonly AccountsPageAttributeField[]
  readonly oneTimeSecret: OneTimeSecret | null
  readonly onSecretCreated: (notice: OneTimeSecret) => void
  readonly onCloseSecret: () => void
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const hasFeature = useAdminHasFeature()
  const factorsPresent = hasFeature('authn.admin.factors')
  const secretsPresent = hasFeature('authn.admin.secrets')
  const session = useAdminSession()
  const gate = useStepUpGate<() => void>(
    (retry) => retry(),
    (error) => session !== null && session.isStepUpShortfall(error),
  )
  const patchAccount = operations.usePatchAccount()
  const statusMenu = useInertSelectMenu()
  const mfaMenu = useInertSelectMenu()
  const protectedMenu = useInertSelectMenu()
  const identifierKindMenu = useInertSelectMenu()
  const setStatus = operations.useSetAccountStatus()
  const setMfaRequired = operations.useSetMFARequired()
  const resetPassword = operations.useResetPassword()
  const addIdentifier = operations.useAddIdentifier()
  const createSecret = operations.useCreateSecret()

  const id = account.id
  const identity = accountIdentityValue(account)
  const currentStatus: 'active' | 'disabled' =
    account.status === 'disabled' ? 'disabled' : 'active'
  const currentAttributes =
    account.attributes === undefined ? '' : JSON.stringify(account.attributes)
  const currentProtected: 'true' | 'false' = account.protected ? 'true' : 'false'

  const [statusDraft, setStatusDraft] = useState<'active' | 'disabled'>(currentStatus)
  const [mfaDraft, setMfaDraft] = useState<'true' | 'false'>(
    account.mfa_required ? 'true' : 'false',
  )
  const [displayName, setDisplayName] = useState(account.display_name)
  const [attributes, setAttributes] = useState(currentAttributes)
  const [protectedDraft, setProtectedDraft] = useState<'unchanged' | 'true' | 'false'>(
    currentProtected,
  )
  const [tempPassword, setTempPassword] = useState('')
  const [identifierKind, setIdentifierKind] = useState<'username' | 'email'>('username')
  const [identifierValue, setIdentifierValue] = useState('')
  const [secretLabel, setSecretLabel] = useState('')
  const [secretExpires, setSecretExpires] = useState('')
  const attributesFieldId = useId()
  const attributesHelperId = `${attributesFieldId}-helper-text`

  const attributesInvalid =
    attributeFields === undefined &&
    attributes.trim() !== '' &&
    parseStringMap(attributes) === null
  const statusUnchanged = statusDraft === currentStatus
  const mfaUnchanged = (mfaDraft === 'true') === account.mfa_required
  const protectedUnchanged =
    protectedDraft === 'unchanged' || protectedDraft === currentProtected
  const patchUnchanged =
    displayName === account.display_name &&
    attributes === currentAttributes &&
    protectedUnchanged


  function writeStatus(): void {
    void feedback.status.run(async () => {
      try {
        await setStatus.mutateAsync({ id, status: statusDraft })
      } catch (error) {
        if (gate.intercept(error, writeStatus)) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        { named: 'accounts.statusSaved', unnamed: 'accounts.statusSavedUnnamed' },
        identity,
      )
    })
  }

  function writeMfa(): void {
    void feedback.mfa.run(async () => {
      try {
        await setMfaRequired.mutateAsync({ id, required: mfaDraft === 'true' })
      } catch (error) {
        if (gate.intercept(error, writeMfa)) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        { named: 'accounts.mfaSaved', unnamed: 'accounts.mfaSavedUnnamed' },
        identity,
      )
    })
  }

  function writePatch(): void {
    const parsed = attributes.trim() === '' ? null : parseStringMap(attributes)
    void feedback.patch.run(async () => {
      try {
        await patchAccount.mutateAsync({
          id,
          ...(displayName === account.display_name ? {} : { display_name: displayName }),
          ...(attributes === currentAttributes || parsed === null
            ? {}
            : { attributes: parsed }),
          ...(protectedUnchanged ? {} : { protected: protectedDraft === 'true' }),
        })
      } catch (error) {
        if (gate.intercept(error, writePatch)) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        { named: 'accounts.patchSaved', unnamed: 'accounts.patchSavedUnnamed' },
        identity,
      )
    })
  }

  function writePassword(): void {
    void feedback.password.run(async () => {
      try {
        await resetPassword.mutateAsync({ id, password: tempPassword })
      } catch (error) {
        if (gate.intercept(error, writePassword)) {
          return null
        }
        throw error
      }
      setTempPassword('')
      return accountNotice(
        t,
        {
          named: 'accounts.passwordReset',
          unnamed: 'accounts.passwordResetUnnamed',
        },
        identity,
      )
    })
  }

  function addOne(): void {
    void feedback.addIdentifier.run(async () => {
      try {
        await addIdentifier.mutateAsync({
          id,
          kind: identifierKind,
          value: identifierValue,
        })
      } catch (error) {
        if (gate.intercept(error, addOne)) {
          return null
        }
        throw error
      }
      setIdentifierValue('')
      return accountNotice(
        t,
        {
          named: 'accounts.identifierAdded',
          unnamed: 'accounts.identifierAddedUnnamed',
        },
        identity,
        { loginIdentifier: identifierValue },
      )
    })
  }

  function createOne(): void {
    void feedback.createSecret.run(async () => {
      let created: AdminAccountSecretCreated
      try {
        created = await createSecret.mutateAsync({
          id,
          label: secretLabel,
          ...(secretExpires === '' ? {} : { expires_at: secretExpires }),
        })
      } catch (error) {
        if (gate.intercept(error, createOne)) {
          return null
        }
        throw error
      }
      setSecretLabel('')
      setSecretExpires('')
      onSecretCreated({
        accountId: id,
        accountName: accountLabel(t, identity),
        label: created.label,
        secret: created.secret,
      })
      return accountNotice(
        t,
        {
          named: 'accounts.secretCreated',
          unnamed: 'accounts.secretCreatedUnnamed',
        },
        identity,
        { label: created.label },
      )
    })
  }

  const rowSx = {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'flex-end',
    columnGap: 2,
    rowGap: 1,
    mb: 1,
  } as const

  return (
    <>
      <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
        {t('accounts.writes.title')}
      </Typography>

      <Box sx={rowSx}>
        <TextField
          select
          slotProps={{ select: statusMenu }}
          size="small"
          value={statusDraft}
          onChange={(event) =>
            setStatusDraft(event.target.value as 'active' | 'disabled')
          }
          label={t('accounts.status.label')}
          sx={{ minWidth: 160 }}
        >
          <MenuItem value="active">{t('accounts.status.active')}</MenuItem>
          <MenuItem value="disabled">{t('accounts.status.disabled')}</MenuItem>
        </TextField>
        <ActionButton
          type="button"
          busy={feedback.status.pending}
          unavailable={statusUnchanged}
          unavailableReason={t('accounts.status.unchanged')}
          onClick={() => writeStatus()}
        >
          {t('accounts.status.submit')}
        </ActionButton>
      </Box>
      <WriteFeedbackRegion outcome={feedback.status.outcome} />

      {factorsPresent && (
        <>
          <Box sx={{ ...rowSx, mt: 1 }}>
            <TextField
              select
              slotProps={{ select: mfaMenu }}
              size="small"
              value={mfaDraft}
              onChange={(event) =>
                setMfaDraft(event.target.value as 'true' | 'false')
              }
              label={t('accounts.mfa.label')}
              sx={{ minWidth: 160 }}
            >
              <MenuItem value="true">{t('accounts.mfa.required')}</MenuItem>
              <MenuItem value="false">{t('accounts.mfa.notRequired')}</MenuItem>
            </TextField>
            <ActionButton
              type="button"
              busy={feedback.mfa.pending}
              unavailable={mfaUnchanged}
              unavailableReason={t('accounts.mfa.unchanged')}
              onClick={() => writeMfa()}
            >
              {t('accounts.mfa.submit')}
            </ActionButton>
          </Box>
          <WriteFeedbackRegion outcome={feedback.mfa.outcome} />
        </>
      )}

      <Box sx={{ ...rowSx, mt: 1 }}>
        <TextField
          size="small"
          value={displayName}
          onChange={(event) => setDisplayName(event.target.value)}
          label={t('accounts.patch.displayNameLabel')}
        />
        {attributeFields === undefined ? (
          <TextField
            id={attributesFieldId}
            size="small"
            value={attributes}
            onChange={(event) => setAttributes(event.target.value)}
            label={t('accounts.patch.attributesLabel')}
            error={attributesInvalid}
            helperText={t('accounts.patch.attributesHelper')}
            multiline
            minRows={1}
            sx={{ minWidth: 240 }}
          />
        ) : (
          attributeFields.map((declared) => (
            <TextField
              key={declared.key}
              size="small"
              value={attributeValueOf(attributes, declared.key)}
              onChange={(event) =>
                setAttributes(
                  withAttributeValue(
                    attributes,
                    declared.key,
                    event.target.value,
                  ),
                )
              }
              label={declared.label}
              sx={{ minWidth: 200 }}
            />
          ))
        )}
        <TextField
          select
          slotProps={{ select: protectedMenu }}
          size="small"
          value={protectedDraft}
          onChange={(event) =>
            setProtectedDraft(event.target.value as 'unchanged' | 'true' | 'false')
          }
          label={t('accounts.patch.protectedLabel')}
          sx={{ minWidth: 180 }}
        >
          <MenuItem value="unchanged">
            {t('accounts.patch.protected.unchanged')}
          </MenuItem>
          <MenuItem value="true">{t('accounts.patch.protected.true')}</MenuItem>
          <MenuItem value="false">{t('accounts.patch.protected.false')}</MenuItem>
        </TextField>
        <ActionButton
          type="button"
          busy={feedback.patch.pending}
          unavailable={attributesInvalid || patchUnchanged}
          {...(attributesInvalid
            ? { unavailableDescribedBy: attributesHelperId }
            : { unavailableReason: t('accounts.patch.unchanged') })}
          onClick={() => writePatch()}
        >
          {t('accounts.patch.submit')}
        </ActionButton>
      </Box>
      <WriteFeedbackRegion outcome={feedback.patch.outcome} />

      {factorsPresent && (
        <>
          <Box sx={{ ...rowSx, mt: 1 }}>
            <TextField
              size="small"
              type="password"
              value={tempPassword}
              onChange={(event) => setTempPassword(event.target.value)}
              label={t('accounts.password.label')}
            />
            <ActionButton
              type="button"
              busy={feedback.password.pending}
              unavailable={tempPassword === ''}
              unavailableReason={t('accounts.password.unavailable')}
              onClick={() => writePassword()}
            >
              {t('accounts.password.submit')}
            </ActionButton>
          </Box>
          <WriteFeedbackRegion outcome={feedback.password.outcome} />
        </>
      )}

      <Box sx={{ ...rowSx, mt: 1 }}>
        <TextField
          select
          slotProps={{ select: identifierKindMenu }}
          size="small"
          value={identifierKind}
          onChange={(event) =>
            setIdentifierKind(event.target.value as 'username' | 'email')
          }
          label={t('accounts.addIdentifier.kindLabel')}
          sx={{ minWidth: 140 }}
        >
          <MenuItem value="username">
            {t('accounts.identifierKind.username')}
          </MenuItem>
          <MenuItem value="email">{t('accounts.identifierKind.email')}</MenuItem>
        </TextField>
        <TextField
          size="small"
          value={identifierValue}
          onChange={(event) => setIdentifierValue(event.target.value)}
          label={t('accounts.addIdentifier.valueLabel')}
        />
        <ActionButton
          type="button"
          busy={feedback.addIdentifier.pending}
          unavailable={identifierValue === ''}
          unavailableReason={t('accounts.addIdentifier.unavailable')}
          onClick={() => addOne()}
        >
          {t('accounts.addIdentifier.submit')}
        </ActionButton>
      </Box>
      <WriteFeedbackRegion outcome={feedback.addIdentifier.outcome} />

      {secretsPresent && (
        <>
          <Box sx={{ ...rowSx, mt: 1 }}>
            <TextField
              size="small"
              value={secretLabel}
              onChange={(event) => setSecretLabel(event.target.value)}
              label={t('accounts.createSecret.labelLabel')}
            />
            <TextField
              size="small"
              value={secretExpires}
              onChange={(event) => setSecretExpires(event.target.value)}
              label={t('accounts.createSecret.expiresLabel')}
              helperText={t('accounts.createSecret.expiresHelper')}
            />
            <ActionButton
              type="button"
              busy={feedback.createSecret.pending}
              unavailable={secretLabel === ''}
              unavailableReason={t('accounts.createSecret.unavailable')}
              onClick={() => createOne()}
            >
              {t('accounts.createSecret.submit')}
            </ActionButton>
          </Box>
          <WriteFeedbackRegion
            outcome={feedback.createSecret.outcome}
            sx={{ mb: 2 }}
          />
          {oneTimeSecret !== null && oneTimeSecret.accountId === id && (
            <OneTimeSecretNotice notice={oneTimeSecret} onClose={onCloseSecret} />
          )}
        </>
      )}

      {session !== null && (
        <StepUpChallenge
          open={gate.open}
          reason={gate.reason}
          session={session}
          onSuccess={gate.succeed}
          onCancel={gate.cancel}
        />
      )}
    </>
  )
}
